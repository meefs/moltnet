package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestAppResumesPairingDeliveryAfterApplyingRoomPolicy(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		name := "federated"
		if !allowed {
			name = "federation removed"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sender.db")
			storage, err := store.NewSQLiteStore(path)
			if err != nil {
				t.Fatal(err)
			}
			room := protocol.Room{ID: "research", NetworkID: "origin", Members: []string{"writer"}, Federation: &protocol.RoomFederation{Mode: protocol.RoomFederationAll}, CreatedAt: time.Now().UTC()}
			if err := storage.CreateRoom(room); err != nil {
				t.Fatal(err)
			}
			message := protocol.Message{ID: "pending-at-startup", NetworkID: "origin", Origin: protocol.MessageOrigin{NetworkID: "origin", MessageID: "pending-at-startup"}, Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "research"}, From: protocol.Actor{Type: "agent", ID: "writer", NetworkID: "origin"}, Parts: []protocol.Part{{Kind: "text", Text: "retained"}}, CreatedAt: time.Now().UTC()}
			if _, err := storage.AppendMessageWithPairingDeliveriesContext(context.Background(), message, nil, []string{"peer"}); err != nil {
				t.Fatal(err)
			}
			if err := storage.Close(); err != nil {
				t.Fatal(err)
			}
			var posts atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/network" {
					_ = json.NewEncoder(w).Encode(protocol.Network{ID: "peer", Protocols: protocol.NetworkProtocols{HTTP: []string{protocol.HTTPProtocolV1}, Pair: []string{protocol.PairProtocolV1}}, Capabilities: protocol.NetworkCapabilities{DirectMessages: true, MessagePagination: "cursor"}})
					return
				}
				if r.URL.Path != "/v1/messages" {
					http.NotFound(w, r)
					return
				}
				posts.Add(1)
				w.WriteHeader(http.StatusAccepted)
				_ = json.NewEncoder(w).Encode(protocol.MessageAccepted{Accepted: true, MessageID: message.ID})
			}))
			defer peer.Close()
			federation := protocol.DefaultRoomFederation()
			if allowed {
				federation = &protocol.RoomFederation{Mode: protocol.RoomFederationAll}
			}
			instance, err := New(Config{NetworkID: "origin", Auth: authn.Config{Mode: authn.ModeNone}, Storage: StorageConfig{Kind: storageKindSQLite, SQLite: SQLiteStorageConfig{Path: path}}, Pairings: []protocol.Pairing{{ID: "peer", RemoteNetworkID: "peer", RemoteBaseURL: peer.URL}}, Rooms: []RoomConfig{{ID: "research", Members: []string{"writer"}, Federation: federation}}})
			if err != nil {
				t.Fatal(err)
			}
			defer instance.Close()
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				response := httptest.NewRecorder()
				instance.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/pairings", nil))
				var page protocol.PairingPage
				if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
					t.Fatal(err)
				}
				if len(page.Pairings) == 1 && page.Pairings[0].Delivery != nil {
					state := page.Pairings[0].Delivery
					if allowed && state.Pending == 0 && posts.Load() == 1 {
						return
					}
					if !allowed && state.Pending == 1 && state.LastError != "" {
						if posts.Load() != 0 {
							t.Fatal("delivery escaped the newly applied room policy")
						}
						return
					}
				}
				time.Sleep(5 * time.Millisecond)
			}
			t.Fatalf("startup delivery did not reach expected state; relayed posts=%d", posts.Load())
		})
	}
}
