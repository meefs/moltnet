package follow

import (
	"testing"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestDuplicateClaimWarningOnlyForUnpinnedFollows(t *testing.T) {
	m := New(nil)
	for _, id := range []string{"one", "two", "pinned"} {
		status := protocol.FollowStatus{ID: id, ClaimedNetworkID: "good", Status: "live"}
		if id == "pinned" {
			status.RemoteNetworkID = "good"
		}
		m.workers[id] = &worker{status: status}
	}
	for _, s := range m.Status() {
		if (s.Warning != "") != (s.ID != "pinned") || s.LastError != "" || s.Status != "live" {
			t.Fatalf("warning changed health: %+v", s)
		}
	}
	m.workers["two"].status.ClaimedNetworkID = "other"
	for _, s := range m.Status() {
		if s.Warning != "" {
			t.Fatalf("stale warning: %+v", s)
		}
	}
}

func TestHistoryCheckpointIdentityIgnoresDiscoveryClaim(t *testing.T) {
	w := worker{config: protocol.Follow{ID: "news", RemoteBaseURL: "https://origin.example"}}
	if w.historyKey("good", "public", "") != w.historyKey("evil", "public", "") {
		t.Fatal("discovery claim determines checkpoint identity")
	}
}
