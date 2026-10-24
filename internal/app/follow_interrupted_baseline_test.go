package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestFollowInterruptedThreadBaselineKeepsBackfillLimit(t *testing.T) {
	for _, limit := range []int{0, 1} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			origin := newFollowApp(t, "origin", true, nil)
			followSend(t, origin.Handler(), "root", "public", protocol.TargetKindRoom, "")
			for i := 0; i < 3; i++ {
				followSend(t, origin.Handler(), fmt.Sprintf("reply_%d", i), "public", protocol.TargetKindThread, "root")
			}
			var failed atomic.Bool
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/rooms/public/threads" && failed.CompareAndSwap(false, true) {
					http.Error(w, "interrupted initial thread discovery", http.StatusServiceUnavailable)
					return
				}
				origin.Handler().ServeHTTP(w, r)
			}))
			t.Cleanup(remote.Close)
			n := limit
			follower := newFollowApp(t, "follower", false, []protocol.Follow{{ID: "news", RemoteBaseURL: remote.URL, Backfill: &n, PollIntervalSeconds: 3600}})
			followWait(t, func() bool { return followStatus(t, follower.Handler()).Status == "live" })
			if !failed.Load() {
				t.Fatal("thread discovery failure was not injected")
			}
			response := followRequest(follower.Handler(), http.MethodGet, "/v1/rooms/news.public/threads", "operator", nil)
			var threads protocol.ThreadPage
			if err := json.Unmarshal(response.Body.Bytes(), &threads); err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, thread := range threads.Threads {
				response = followRequest(follower.Handler(), http.MethodGet, "/v1/threads/"+thread.ID+"/messages", "operator", nil)
				var page protocol.MessagePage
				if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
					t.Fatal(err)
				}
				count += len(page.Messages)
			}
			if count != limit {
				t.Fatalf("interrupted initial sync imported%d old thread replies, want%d", count, limit)
			}
		})
	}
}
