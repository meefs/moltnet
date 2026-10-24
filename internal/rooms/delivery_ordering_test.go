package rooms

import (
	"context"
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestBlockedDeliveryHeadRetainsFIFOOrderUntilPolicyRestored(t *testing.T) {
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
	if _, err := memory.AppendMessageWithPairingDeliveriesContext(context.Background(), first, nil, []string{"pair_remote"}); err != nil {
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
	deadline := time.Now().Add(time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		state, err := memory.PairingDeliveryStateContext(context.Background(), "pair_remote")
		if err != nil {
			t.Fatal(err)
		}
		if state.Pending == 2 && state.LastError != "" {
			blocked = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !blocked {
		t.Fatal("head did not retain both queued messages")
	}
	select {
	case sent := <-client.relayed:
		t.Fatalf("later message bypassed blocked head: %#v", sent)
	default:
	}
	if _, err := service.ReconcileRoomContext(context.Background(), protocol.CreateRoomRequest{ID: "first-room", Federation: &protocol.RoomFederation{Mode: protocol.RoomFederationAll}}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"first", "second"} {
		select {
		case sent := <-client.relayed:
			if sent.ID != want {
				t.Fatalf("sent %q, want %q", sent.ID, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("did not deliver %q after restoring policy", want)
		}
	}
}
