package rooms_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/pairings"
	"github.com/noopolis/moltnet/internal/rooms"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestConfiguredIdentityMismatchKeepsBacklogUntilRecovery(t *testing.T) {
	for _, bound := range []bool{true, false} {
		t.Run(fmt.Sprintf("bound=%t", bound), func(t *testing.T) {
			peer, receive := deliveryReceiver(t)
			var misrouted atomic.Bool
			misrouted.Store(true)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/network" && misrouted.Load() {
					network := protocol.Network{ID: "wrong-peer", Protocols: protocol.NetworkProtocols{HTTP: []string{protocol.HTTPProtocolV1}, Pair: []string{protocol.PairProtocolV1}}}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(network)
					return
				}
				receive.ServeHTTP(w, r)
			}))
			defer server.Close()
			memory := store.NewMemoryStore()
			service := rooms.NewService(rooms.ServiceConfig{
				NetworkID: "origin", Store: memory, Messages: memory, Broker: events.NewBroker(), PairingClient: pairings.NewClient(),
				Pairings: []protocol.Pairing{{ID: "peer", RemoteNetworkID: "peer", RemoteBaseURL: server.URL, Token: "pair-secret"}},
			})
			defer service.Close()
			createDeliveryRoom(t, service)
			ctx := context.Background()
			expected := ""
			if bound {
				expected = "peer"
				ctx = store.WithPairingDeliveryLimits(ctx, map[string]store.PairingDeliveryLimits{"peer": {RemoteNetworkID: expected}})
			}
			for i := 0; i < 5; i++ {
				id := fmt.Sprintf("queued-%d", i)
				message := protocol.Message{ID: id, NetworkID: "origin", Origin: protocol.MessageOrigin{NetworkID: "origin", MessageID: id}, Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "research"}, From: protocol.Actor{Type: "agent", ID: "writer", NetworkID: "origin"}, Parts: []protocol.Part{{Kind: "text", Text: id}}}
				if _, err := memory.AppendMessageWithPairingDeliveriesContext(ctx, message, nil, []string{"peer"}); err != nil {
					t.Fatal(err)
				}
			}
			service.StartPairingDelivery()
			// Explicit discovery must also preserve the configured identity and queue.
			if _, err := service.PairingNetwork(context.Background(), "peer"); err != nil {
				t.Fatal(err)
			}
			heads, err := memory.ListPairingDeliveryHeadsContext(context.Background())
			if err != nil || len(heads) != 1 || heads[0].RemoteNetworkID != expected {
				t.Fatalf("misrouted discovery changed backlog: %#v %v", heads, err)
			}
			state, err := memory.PairingDeliveryStateContext(context.Background(), "peer")
			if err != nil || state.Pending != 5 || state.Dropped != 0 || state.Rejected != 0 || state.RemoteNetworkID == "wrong-peer" {
				t.Fatalf("misroute state: %#v %v", state, err)
			}
			waitDelivery(t, func() bool {
				heads, err := memory.ListPairingDeliveryHeadsContext(context.Background())
				return err == nil && len(heads) == 1 && heads[0].Attempts > 0
			})
			misrouted.Store(false)
			waitDelivery(t, func() bool {
				state, err := memory.PairingDeliveryStateContext(context.Background(), "peer")
				return err == nil && state.Pending == 0
			})
			state, err = memory.PairingDeliveryStateContext(context.Background(), "peer")
			if err != nil || state.Dropped != 0 || state.Rejected != 0 {
				t.Fatalf("recovery lost backlog: %#v %v", state, err)
			}
			assertDeliveryTexts(t, peer, "queued-0", "queued-1", "queued-2", "queued-3", "queued-4")
		})
	}
}
