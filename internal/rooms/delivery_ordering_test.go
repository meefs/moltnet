package rooms

import (
	"context"
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestPolicyRejectedDeliveryDoesNotBlockNextRoom(t *testing.T) {
	memory := store.NewMemoryStore()
	client := &federationRelayClient{relayed: make(chan protocol.SendMessageRequest, 4)}
	service := NewService(ServiceConfig{NetworkID: "local", Store: memory, Messages: memory, Broker: events.NewBroker(), PairingClient: client, Pairings: []protocol.Pairing{{ID: "pair_remote", RemoteNetworkID: "remote", RemoteBaseURL: "https://peer.example"}}, DeliveryPollInterval: time.Millisecond, DeliveryRetryMin: 10 * time.Millisecond, DeliveryRetryMax: 20 * time.Millisecond})
	defer service.Close()
	for _, id := range []string{"first-room", "second-room"} {
		if _, err := service.CreateRoom(protocol.CreateRoomRequest{ID: id, Federation: &protocol.RoomFederation{Mode: protocol.RoomFederationAll}}); err != nil {
			t.Fatal(err)
		}
	}
	first := protocol.Message{ID: "first", NetworkID: "local", Origin: protocol.MessageOrigin{NetworkID: "local", MessageID: "first"}, Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "first-room"}, From: protocol.Actor{Type: "agent", ID: "writer", NetworkID: "local"}, Parts: []protocol.Part{{Kind: "text", Text: "first"}}}
	if _, err := memory.AppendMessageWithPairingDeliveriesContext(store.WithPairingDeliveryLimits(context.Background(), map[string]store.PairingDeliveryLimits{"pair_remote": {RemoteNetworkID: "remote"}}), first, nil, []string{"pair_remote"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReconcileRoomContext(context.Background(), protocol.CreateRoomRequest{ID: "first-room", Federation: protocol.DefaultRoomFederation()}); err != nil {
		t.Fatal(err)
	}
	second := roomSend("second-room", "local")
	second.ID = "second"
	if _, err := service.SendMessage(second); err != nil {
		t.Fatal(err)
	}
	select {
	case sent := <-client.relayed:
		if sent.ID != "second" {
			t.Fatalf("sent %q, want second", sent.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("later room message blocked by rejected policy")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state, err := memory.PairingDeliveryStateContext(context.Background(), "pair_remote")
		if err != nil {
			t.Fatal(err)
		}
		if state.Pending == 0 && state.Rejected == 1 && state.LastRejectionReason != "" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("rejection not counted after draining")
}
