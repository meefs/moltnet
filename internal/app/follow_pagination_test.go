package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestFollowBackfillTraversesHistoryPages(t *testing.T) {
	origin := newFollowApp(t, "origin", true, nil)
	for i := 0; i < 211; i++ {
		followSend(t, origin.Handler(), fmt.Sprintf("history_%03d", i), "public", protocol.TargetKindRoom, "")
	}
	remote := httptest.NewServer(origin.Handler())
	t.Cleanup(remote.Close)
	n := 207
	follower := newFollowApp(t, "follower", false, []protocol.Follow{{ID: "news", RemoteBaseURL: remote.URL, Backfill: &n}})
	followWait(t, func() bool {
		status := followStatus(t, follower.Handler())
		return status.Status == "live" && len(status.Rooms) == 1 && status.Rooms[0].MessageCount == 207
	})
	// Independently enumerate local history rather than trusting status counters.
	seen := map[string]bool{}
	before := ""
	for {
		path := "/v1/rooms/news.public/messages?limit=73"
		if before != "" {
			path += "&before=" + url.QueryEscape(before)
		}
		response := followRequest(follower.Handler(), http.MethodGet, path, "operator", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("history: %s", response.Body.String())
		}
		var page protocol.MessagePage
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		for _, message := range page.Messages {
			if seen[message.Origin.MessageID] {
				t.Fatalf("duplicate: %s", message.Origin.MessageID)
			}
			seen[message.Origin.MessageID] = true
		}
		if !page.Page.HasMore {
			break
		}
		if page.Page.NextBefore == "" || page.Page.NextBefore == before {
			t.Fatal("pagination did not advance")
		}
		before = page.Page.NextBefore
	}
	if len(seen) != 207 || seen["history_003"] || !seen["history_004"] || !seen["history_210"] {
		t.Fatalf("wrong backfill: %d messages, first wanted=%v last=%v", len(seen), seen["history_004"], seen["history_210"])
	}
}

func TestFollowZeroBackfillStillReceivesLiveMessages(t *testing.T) {
	origin := newFollowApp(t, "origin", true, nil)
	followSend(t, origin.Handler(), "old", "public", protocol.TargetKindRoom, "")
	remote := httptest.NewServer(origin.Handler())
	t.Cleanup(remote.Close)
	zero := 0
	follower := newFollowApp(t, "follower", false, []protocol.Follow{{ID: "news", RemoteBaseURL: remote.URL, Backfill: &zero, PollIntervalSeconds: 3600}})
	followWait(t, func() bool { return followStatus(t, follower.Handler()).Status == "live" })
	if count := followMessageCount(follower.Handler(), "news.public"); count != 0 {
		t.Fatalf("zero backfill imported %d messages", count)
	}
	followSend(t, origin.Handler(), "new", "public", protocol.TargetKindRoom, "")
	followWait(t, func() bool { return followMessageCount(follower.Handler(), "news.public") == 1 })
	page := followMessages(t, follower.Handler(), "news.public")
	if page.Messages[0].Origin.MessageID != "new" {
		t.Fatalf("wrong live message: %+v", page.Messages)
	}
}

func TestFollowZeroBackfillImportsNewThreadReply(t *testing.T) {
	origin := newFollowApp(t, "origin", true, nil)
	followSend(t, origin.Handler(), "root", "public", protocol.TargetKindRoom, "")
	remote := httptest.NewServer(origin.Handler())
	t.Cleanup(remote.Close)
	zero := 0
	follower := newFollowApp(t, "follower", false, []protocol.Follow{{ID: "news", RemoteBaseURL: remote.URL, Backfill: &zero, PollIntervalSeconds: 3600}})
	followWait(t, func() bool { return followStatus(t, follower.Handler()).Status == "live" })
	followSend(t, origin.Handler(), "reply", "public", protocol.TargetKindThread, "root")
	followWait(t, func() bool {
		response := followRequest(follower.Handler(), http.MethodGet, "/v1/rooms/news.public/threads", "operator", nil)
		var page protocol.ThreadPage
		_ = json.Unmarshal(response.Body.Bytes(), &page)
		if len(page.Threads) != 1 {
			return false
		}
		response = followRequest(follower.Handler(), http.MethodGet, "/v1/threads/"+page.Threads[0].ID+"/messages", "operator", nil)
		var messages protocol.MessagePage
		_ = json.Unmarshal(response.Body.Bytes(), &messages)
		return len(messages.Messages) == 1 && messages.Messages[0].Origin.MessageID == "reply"
	})
}

func TestFollowZeroBackfillClosesSSEHandshakeGap(t *testing.T) {
	origin := newFollowApp(t, "origin", true, nil)
	followSend(t, origin.Handler(), "before-follow", "public", protocol.TargetKindRoom, "")
	var injected atomic.Bool
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/events/stream" && injected.CompareAndSwap(false, true) {
			// This message lands after the baseline history read and before the
			// server subscribes the SSE caller. Only the history gap-fill can see it.
			followSend(t, origin.Handler(), "during-handshake", "public", protocol.TargetKindRoom, "")
		}
		origin.Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(remote.Close)
	zero := 0
	follower := newFollowApp(t, "follower", false, []protocol.Follow{{ID: "news", RemoteBaseURL: remote.URL, Backfill: &zero, PollIntervalSeconds: 3600}})
	followWait(t, func() bool { return followMessageCount(follower.Handler(), "news.public") == 1 })
	page := followMessages(t, follower.Handler(), "news.public")
	if !injected.Load() || page.Messages[0].Origin.MessageID != "during-handshake" {
		t.Fatalf("handshake gap lost: %+v", page)
	}
}

func TestFollowNewlyPublicRoomUsesConfiguredBackfill(t *testing.T) {
	origin := newFollowApp(t, "origin", true, nil)
	remote := httptest.NewServer(origin.Handler())
	t.Cleanup(remote.Close)
	n := 2
	follower := newFollowApp(t, "follower", false, []protocol.Follow{{ID: "news", RemoteBaseURL: remote.URL, Backfill: &n, PollIntervalSeconds: 3600}})
	followWait(t, func() bool { return followStatus(t, follower.Handler()).Status == "live" })
	room := protocol.CreateRoomRequest{ID: "late", Visibility: protocol.RoomVisibilityPrivate, WritePolicy: protocol.RoomWritePolicyOperators, Federation: protocol.DefaultRoomFederation()}
	response := followRequest(origin.Handler(), http.MethodPost, "/v1/rooms", "operator", room)
	if response.Code != http.StatusCreated {
		t.Fatalf("create private room: %s", response.Body.String())
	}
	for i := 0; i < 5; i++ {
		followSend(t, origin.Handler(), fmt.Sprintf("late_%d", i), "late", protocol.TargetKindRoom, "")
	}
	room.Visibility = protocol.RoomVisibilityPublic
	response = followRequest(origin.Handler(), http.MethodPost, "/v1/apply", "operator", protocol.ApplyConfigRequest{NetworkID: "origin", Rooms: []protocol.CreateRoomRequest{room}})
	if response.Code != http.StatusOK {
		t.Fatalf("make room public: %s", response.Body.String())
	}
	followSend(t, origin.Handler(), "late_live", "late", protocol.TargetKindRoom, "")
	followWait(t, func() bool { return followMessageCount(follower.Handler(), "news.late") == 2 })
	page := followMessages(t, follower.Handler(), "news.late")
	if page.Messages[0].Origin.MessageID != "late_4" || page.Messages[1].Origin.MessageID != "late_live" {
		t.Fatalf("new room backfill: %+v", page.Messages)
	}
}
