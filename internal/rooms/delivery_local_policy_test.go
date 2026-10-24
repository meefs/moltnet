package rooms_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/noopolis/moltnet/internal/pairings"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestLocalDMDisabledOnRestartRejectsQueuedDM(t *testing.T) {
	memory := store.NewMemoryStore()
	pairing := protocol.Pairing{ID: "peer", RemoteNetworkID: "peer", RemoteBaseURL: "http://127.0.0.1:1", Token: "pair-secret"}
	first, origin := deliveryOrigin(t, memory, pairing)
	status, body := deliveryPost(t, origin.URL, "", protocol.SendMessageRequest{
		Target: protocol.Target{Kind: protocol.TargetKindDM, DMID: "queued-local-dm", ParticipantIDs: []string{"origin:writer", "peer:reader"}},
		From:   protocol.Actor{Type: "agent", ID: "writer"}, Parts: []protocol.Part{{Kind: "text", Text: "queued-before-local-DM-disabled"}},
	})
	if status != http.StatusAccepted {
		t.Fatalf("enqueue DM: %d %s", status, body)
	}
	origin.Close()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	peer, receive := deliveryReceiver(t)
	server := httptest.NewServer(receive)
	defer server.Close()
	pairing.RemoteBaseURL = server.URL
	_, replacement := deliveryOriginWithClient(t, memory, pairing, pairings.NewClient(), true)
	postDeliveryMessage(t, replacement.URL, "room-after-local-DM-disabled")
	waitDelivery(t, func() bool {
		state := deliveryStatus(t, replacement.URL)
		return state.Pending == 0 && state.Rejected == 1 && state.LastRejectionReason == "pairing delivery rejected: local direct messages disabled"
	})
	assertDeliveryTexts(t, peer, "room-after-local-DM-disabled")
}
