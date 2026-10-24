package app

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestFollowImportedMessagesNeverReachPairing(t *testing.T) {
	origin := newFollowApp(t, "origin", true, nil)
	followSend(t, origin.Handler(), "backfilled", "public", protocol.TargetKindRoom, "")
	remote := httptest.NewServer(origin.Handler())
	t.Cleanup(remote.Close)
	// The third real server acts as the follower's configured paired network.
	peer := newFollowApp(t, "peer", true, nil)
	var relays atomic.Int64
	paired := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/messages" {
			relays.Add(1)
		}
		peer.Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(paired.Close)
	config := followTestConfig("follower", false)
	config.Pairings = []protocol.Pairing{{ID: "peer", RemoteNetworkID: "peer", RemoteBaseURL: paired.URL, Token: "pair"}}
	config.Rooms[0].Federation = &protocol.RoomFederation{Mode: protocol.RoomFederationAll}
	config.Follows = []protocol.Follow{{ID: "news", RemoteBaseURL: remote.URL, PollIntervalSeconds: 3600}}
	follower, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = follower.Close() })
	followWait(t, func() bool { return followMessageCount(follower.Handler(), "news.public") == 1 })
	followSend(t, origin.Handler(), "live", "public", protocol.TargetKindRoom, "")
	followWait(t, func() bool { return followMessageCount(follower.Handler(), "news.public") == 2 })
	if relays.Load() != 0 {
		t.Fatal("follow import was relayed to pairing")
	}
	// A successful relay control proves this setup can reach the paired server.
	followSend(t, follower.Handler(), "local-control", "public", protocol.TargetKindRoom, "")
	followWait(t, func() bool { return relays.Load() == 1 })
	// Wait past the control's delivery; any asynchronous mirror relay is a defect.
	time.Sleep(30 * time.Millisecond)
	if relays.Load() != 1 {
		t.Fatalf("pairing received %d messages, wanted only local control", relays.Load())
	}
}
