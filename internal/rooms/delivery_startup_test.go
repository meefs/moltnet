package rooms

import (
	"context"
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestPairingBacklogWaitsForReadyConfiguration(t *testing.T) {
	memory := store.NewMemoryStore()
	if err := memory.CreateRoom(protocol.Room{ID: "research", NetworkID: "net_a", Federation: &protocol.RoomFederation{Mode: protocol.RoomFederationAll}}); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.AppendMessageWithPairingDeliveriesContext(store.WithPairingDeliveryLimits(context.Background(), map[string]store.PairingDeliveryLimits{"pair_b": {RemoteNetworkID: "net_b"}}), roomRelayMessage(), nil, []string{"pair_b"}); err != nil {
		t.Fatal(err)
	}
	client := &recordingPairingClient{notify: make(chan struct{}, 2)}
	service := NewService(ServiceConfig{NetworkID: "net_a", Store: memory, Messages: memory, Broker: events.NewBroker(), PairingClient: client, Pairings: []protocol.Pairing{{ID: "pair_b", RemoteNetworkID: "net_b", RemoteBaseURL: "http://peer.example"}}, DeliveryPollInterval: time.Millisecond, DeliveryRetryMin: time.Hour})
	defer service.Close()
	select {
	case <-client.notify:
		t.Fatal("backlog replay started before initial configuration was ready")
	case <-time.After(30 * time.Millisecond):
	}
	if _, err := service.ReconcileRoomContext(context.Background(), protocol.CreateRoomRequest{ID: "research", Federation: protocol.DefaultRoomFederation()}); err != nil {
		t.Fatal(err)
	}
	service.StartPairingDelivery()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state, err := memory.PairingDeliveryStateContext(context.Background(), "pair_b")
		if err != nil {
			t.Fatal(err)
		}
		if state.Rejected == 1 {
			if state.Pending != 0 || len(client.snapshotCalls()) != 0 {
				t.Fatalf("new federation policy did not reject blocked backlog: %#v", state)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("ready worker did not evaluate the queued message against the new policy")
}
