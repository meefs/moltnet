package rooms_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/pairings"
	"github.com/noopolis/moltnet/internal/rooms"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestDeliveryBacklogPurgedWhenRemoteNetworkChanges(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		name := "config remote change"
		if revoke {
			name = "revoke then reuse id"
		}
		t.Run(name, func(t *testing.T) {
			memory := store.NewMemoryStore()
			first, origin := deliveryOrigin(t, memory, protocol.Pairing{ID: "peer", RemoteNetworkID: "old-network", RemoteBaseURL: "http://127.0.0.1:1"})
			postDeliveryMessage(t, origin.URL, "old-destination-only")
			waitDelivery(t, func() bool { return deliveryStatus(t, origin.URL).Pending == 1 })
			origin.Close()
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			heads, err := memory.ListPairingDeliveryHeadsContext(context.Background())
			if err != nil || len(heads) != 1 || heads[0].RemoteNetworkID != "old-network" {
				t.Fatalf("enqueue binding = %#v, %v", heads, err)
			}
			if revoke {
				revoked := rooms.NewService(rooms.ServiceConfig{NetworkID: "origin", Store: memory, Messages: memory, Broker: events.NewBroker(), PairingClient: pairings.NewClient(), DeliveryPollInterval: time.Millisecond})
				revoked.StartPairingDelivery()
				waitDelivery(t, func() bool {
					state, err := memory.PairingDeliveryStateContext(context.Background(), "peer")
					return err == nil && state.Pending == 0 && state.Dropped == 1
				})
				if err := revoked.Close(); err != nil {
					t.Fatal(err)
				}
			}
			peer, receive := deliveryReceiver(t)
			server := httptest.NewServer(receive)
			defer server.Close()
			_, replacement := deliveryOrigin(t, memory, protocol.Pairing{ID: "peer", RemoteNetworkID: "peer", RemoteBaseURL: server.URL, Token: "pair-secret"})
			waitDelivery(t, func() bool {
				state := deliveryStatus(t, replacement.URL)
				return state.Pending == 0 && state.Dropped == 1
			})
			// A fresh message proves the reused pairing is live and old backlog did
			// not merely remain unsent because the replacement could not connect.
			postDeliveryMessage(t, replacement.URL, "fresh-destination")
			waitDelivery(t, func() bool { return deliveryStatus(t, replacement.URL).Pending == 0 })
			assertDeliveryTexts(t, peer, "fresh-destination")
		})
	}
}
