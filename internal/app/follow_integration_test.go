package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// These tests use the actual app, transport, public-read authorization, SSE,
// follow workers, and durable SQLite store; no remote protocol is mocked.
func TestFollowTwoServersBackfillLiveReconnectAndRestart(t *testing.T) {
	origin := newFollowApp(t, "origin", true, nil)
	gate := &followOriginGate{handler: origin.Handler(), streams: make(map[int]context.CancelFunc)}
	remote := httptest.NewServer(gate)
	t.Cleanup(remote.Close)
	for i := 0; i < 5; i++ {
		followSend(t, origin.Handler(), fmt.Sprintf("old_%d", i), "public", protocol.TargetKindRoom, "")
	}
	n := 3
	config := followTestConfig("follower", false)
	config.Storage = StorageConfig{Kind: storageKindSQLite, SQLite: SQLiteStorageConfig{Path: filepath.Join(t.TempDir(), "follow.db")}}
	config.Follows = []protocol.Follow{{ID: "news", RemoteBaseURL: remote.URL, RemoteNetworkID: "origin", Rooms: []string{"public"}, Backfill: &n, PollIntervalSeconds: 3600}}
	follower, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	// The handler variable changes only after the previous worker is fully closed.
	server := httptest.NewServer(follower.Handler())
	defer server.Close()
	defer func() { _ = follower.Close() }()
	followWait(t, func() bool { return followMessageCount(follower.Handler(), "news.public") == 3 })
	followWait(t, func() bool { return followStatus(t, follower.Handler()).Status == "live" })
	roomResponse := followRequest(follower.Handler(), http.MethodGet, "/v1/rooms/news.public", "operator", nil)
	var room protocol.Room
	if err := json.Unmarshal(roomResponse.Body.Bytes(), &room); err != nil {
		t.Fatal(err)
	}
	if room.Follow == nil || room.Follow.ID != "news" || room.Visibility != "private" {
		t.Fatalf("mirror metadata: %s", roomResponse.Body.String())
	}
	// Anonymous origin discovery must not turn private mirrors into public data.
	if r := followRequest(follower.Handler(), http.MethodGet, "/v1/rooms/news.public", "", nil); r.Code == http.StatusOK {
		t.Fatal("anonymous caller read private mirror")
	}
	followSend(t, origin.Handler(), "live_one", "public", protocol.TargetKindRoom, "")
	followWait(t, func() bool { return followMessageCount(follower.Handler(), "news.public") == 4 })
	// Public thread history and its parent references must survive mirroring.
	followSend(t, origin.Handler(), "reply_one", "public", protocol.TargetKindThread, "live_one")
	followWait(t, func() bool {
		r := followRequest(follower.Handler(), http.MethodGet, "/v1/rooms/news.public/threads", "operator", nil)
		var p protocol.ThreadPage
		_ = json.Unmarshal(r.Body.Bytes(), &p)
		if len(p.Threads) != 1 {
			return false
		}
		r = followRequest(follower.Handler(), http.MethodGet, "/v1/threads/"+p.Threads[0].ID+"/messages", "operator", nil)
		var messages protocol.MessagePage
		_ = json.Unmarshal(r.Body.Bytes(), &messages)
		return len(messages.Messages) == 1 && messages.Messages[0].Origin.MessageID == "reply_one"
	})
	gate.disconnect()
	for i := 0; i < 4; i++ {
		followSend(t, origin.Handler(), fmt.Sprintf("gap_%d", i), "public", protocol.TargetKindRoom, "")
	}
	gate.reconnect()
	followWait(t, func() bool { return followMessageCount(follower.Handler(), "news.public") == 8 })
	// Reloading an unchanged follow must resume, not replay the initial backfill.
	follows := config.Follows
	applied := followRequest(follower.Handler(), http.MethodPost, "/v1/apply", "operator", protocol.ApplyConfigRequest{NetworkID: "follower", Follows: &follows})
	if applied.Code != http.StatusOK {
		t.Fatalf("apply: %s", applied.Body.String())
	}
	followWait(t, func() bool { return followStatus(t, follower.Handler()).Status == "live" })
	if err := follower.Close(); err != nil {
		t.Fatal(err)
	}
	followSend(t, origin.Handler(), "while_stopped", "public", protocol.TargetKindRoom, "")
	follower, err = New(config)
	if err != nil {
		t.Fatal(err)
	}
	followWait(t, func() bool { return followMessageCount(follower.Handler(), "news.public") == 9 })
	page := followMessages(t, follower.Handler(), "news.public")
	seen := map[string]bool{}
	for _, message := range page.Messages {
		if message.Origin.NetworkID != "origin" || message.Origin.ReceivedVia != "follow:news" {
			t.Fatalf("lost provenance: %+v", message)
		}
		if seen[message.Origin.MessageID] {
			t.Fatalf("duplicate origin id: %s", message.Origin.MessageID)
		}
		seen[message.Origin.MessageID] = true
	}
	if seen["old_0"] || seen["old_1"] {
		t.Fatalf("backfill limit ignored: %v", seen)
	}
}

func TestFollowTwoServersRejectIncompatibleOrigins(t *testing.T) {
	for _, tc := range []struct {
		name   string
		public bool
		pin    string
	}{{"identity mismatch", true, "other"}, {"not public", false, "origin"}} {
		t.Run(tc.name, func(t *testing.T) {
			origin := newFollowApp(t, "origin", tc.public, nil)
			remote := httptest.NewServer(origin.Handler())
			t.Cleanup(remote.Close)
			follower := newFollowApp(t, "follower", false, []protocol.Follow{{ID: "news", RemoteBaseURL: remote.URL, RemoteNetworkID: tc.pin}})
			followWait(t, func() bool { return followStatus(t, follower.Handler()).Status == "incompatible" })
			status := followStatus(t, follower.Handler())
			if status.LastError == "" {
				t.Fatal("incompatibility lacks diagnostics")
			}
			if followMessageCount(follower.Handler(), "news.public") > 0 {
				t.Fatal("incompatible follow imported messages")
			}
		})
	}
}

func TestFollowStatusAuthorizationAndMirrorWrites(t *testing.T) {
	origin := newFollowApp(t, "origin", true, nil)
	remote := httptest.NewServer(origin.Handler())
	t.Cleanup(remote.Close)
	follower := newFollowApp(t, "follower", false, []protocol.Follow{{ID: "news", RemoteBaseURL: remote.URL, Rooms: []string{"public"}}})
	followWait(t, func() bool { return followStatus(t, follower.Handler()).Status == "live" })
	for _, token := range []string{"operator", "observer"} {
		if r := followRequest(follower.Handler(), http.MethodGet, "/v1/follows", token, nil); r.Code != http.StatusOK {
			t.Fatalf("%s status access: %d %s", token, r.Code, r.Body.String())
		}
	}
	for _, token := range []string{"", "agent", "pair"} {
		if r := followRequest(follower.Handler(), http.MethodGet, "/v1/follows", token, nil); r.Code == http.StatusOK {
			t.Fatalf("%s may read follows", token)
		}
	}
	for _, token := range []string{"agent", "operator", "pair"} {
		req := protocol.SendMessageRequest{ID: "forged_" + token, Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "news.public"}, From: protocol.Actor{Type: "agent", ID: "writer", NetworkID: "follower"}, Parts: []protocol.Part{{Kind: "text", Text: "must fail"}}}
		if token == "pair" {
			req.From.NetworkID = "peer"
			req.Origin = protocol.MessageOrigin{NetworkID: "peer", MessageID: req.ID}
		}
		r := followRequest(follower.Handler(), http.MethodPost, "/v1/messages", token, req)
		if r.Code != http.StatusForbidden || !strings.Contains(strings.ToLower(r.Body.String()), "read-only") {
			t.Fatalf("%s mirror write: %d %s", token, r.Code, r.Body.String())
		}
	}
}

func followTestConfig(id string, public bool) Config {
	return Config{NetworkID: id, NetworkName: id, Version: "test", AllowHumanIngress: true,
		Storage: StorageConfig{Kind: storageKindMemory},
		Auth: authn.Config{Mode: authn.ModeBearer, PublicRead: public, Tokens: []authn.TokenConfig{
			{ID: "operator", Value: "operator", Scopes: []authn.Scope{authn.ScopeAdmin, authn.ScopeObserve, authn.ScopeWrite}},
			{ID: "observer", Value: "observer", Scopes: []authn.Scope{authn.ScopeObserve}},
			{ID: "writer", Value: "agent", Scopes: []authn.Scope{authn.ScopeWrite}},
			{ID: "peer", Value: "pair", Network: "peer", Scopes: []authn.Scope{authn.ScopePair}},
		}},
		Pairings: []protocol.Pairing{{ID: "peer", RemoteNetworkID: "peer"}},
		Rooms:    []RoomConfig{{ID: "public", Visibility: protocol.RoomVisibilityPublic, WritePolicy: protocol.RoomWritePolicyOperators, Federation: protocol.DefaultRoomFederation()}},
	}
}
func newFollowApp(t *testing.T, id string, public bool, follows []protocol.Follow) *App {
	t.Helper()
	config := followTestConfig(id, public)
	config.Follows = follows
	instance, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = instance.Close() })
	return instance
}
func followRequest(handler http.Handler, method, path, token string, payload any) *httptest.ResponseRecorder {
	var body bytes.Buffer
	if payload != nil {
		_ = json.NewEncoder(&body).Encode(payload)
	}
	request := httptest.NewRequest(method, path, &body)
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
func followSend(t *testing.T, handler http.Handler, id, room, kind, thread string) {
	t.Helper()
	target := protocol.Target{Kind: kind, RoomID: room}
	if kind == protocol.TargetKindThread {
		target.ThreadID = thread
		target.ParentMessageID = thread
	}
	r := followRequest(handler, http.MethodPost, "/v1/messages", "operator", protocol.SendMessageRequest{ID: id, Target: target, From: protocol.Actor{Type: "human", ID: "operator"}, Parts: []protocol.Part{{Kind: "text", Text: id}}})
	if r.Code != http.StatusAccepted {
		t.Fatalf("send %s: %d %s", id, r.Code, r.Body.String())
	}
}
func followMessageCount(handler http.Handler, room string) int {
	r := followRequest(handler, http.MethodGet, "/v1/rooms/"+room+"/messages?limit=100", "operator", nil)
	if r.Code != http.StatusOK {
		return -1
	}
	var p protocol.MessagePage
	_ = json.Unmarshal(r.Body.Bytes(), &p)
	return len(p.Messages)
}
func followMessages(t *testing.T, handler http.Handler, room string) protocol.MessagePage {
	t.Helper()
	r := followRequest(handler, http.MethodGet, "/v1/rooms/"+room+"/messages?limit=100", "operator", nil)
	var p protocol.MessagePage
	if r.Code != http.StatusOK {
		t.Fatalf("messages: %s", r.Body.String())
	}
	if err := json.Unmarshal(r.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}
func followStatus(t *testing.T, handler http.Handler) protocol.FollowStatus {
	t.Helper()
	r := followRequest(handler, http.MethodGet, "/v1/follows", "operator", nil)
	var p protocol.FollowPage
	if err := json.Unmarshal(r.Body.Bytes(), &p); err != nil {
		t.Fatalf("status: %d %s", r.Code, r.Body.String())
	}
	if len(p.Follows) != 1 {
		t.Fatalf("status: %+v", p)
	}
	return p.Follows[0]
}
func followWait(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("follow did not reach expected state within 12 seconds")
}

type followOriginGate struct {
	handler http.Handler
	mu      sync.Mutex
	down    bool
	next    int
	streams map[int]context.CancelFunc
}

func (g *followOriginGate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	if g.down {
		g.mu.Unlock()
		http.Error(w, "origin temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.URL.Path != "/v1/events/stream" {
		g.mu.Unlock()
		g.handler.ServeHTTP(w, r)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	id := g.next
	g.next++
	g.streams[id] = cancel
	g.mu.Unlock()
	defer func() { cancel(); g.mu.Lock(); delete(g.streams, id); g.mu.Unlock() }()
	g.handler.ServeHTTP(w, r.WithContext(ctx))
}
func (g *followOriginGate) disconnect() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.down = true
	for _, cancel := range g.streams {
		cancel()
	}
}
func (g *followOriginGate) reconnect() { g.mu.Lock(); defer g.mu.Unlock(); g.down = false }
