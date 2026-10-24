package rooms_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/pairings"
	"github.com/noopolis/moltnet/internal/rooms"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestUnboundInviterDeliveryLearnsDestination(t *testing.T) {
	peer, receive := deliveryReceiver(t)
	var hold atomic.Bool
	hold.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hold.Load() && r.Method == http.MethodPost {
			w.WriteHeader(503)
			return
		}
		receive.ServeHTTP(w, r)
	}))
	defer server.Close()
	memory := store.NewMemoryStore()
	pairing := protocol.Pairing{ID: "peer", RemoteBaseURL: server.URL, Token: "pair-secret"}
	first, origin := deliveryOrigin(t, memory, pairing)
	postDeliveryMessage(t, origin.URL, "inviter-unbound")
	waitDelivery(t, func() bool {
		heads, err := memory.ListPairingDeliveryHeadsContext(context.Background())
		return err == nil && len(heads) == 1 && heads[0].RemoteNetworkID == "peer" && heads[0].Attempts > 0
	})
	origin.Close()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	// Empty config must preserve the learned queue binding across restart.
	hold.Store(false)
	_, restarted := deliveryOrigin(t, memory, pairing)
	waitDelivery(t, func() bool { return deliveryStatus(t, restarted.URL).Pending == 0 })
	assertDeliveryTexts(t, peer, "inviter-unbound")
	if state := deliveryStatus(t, restarted.URL); state.Dropped != 0 || state.Rejected != 0 {
		t.Fatal(state)
	}
}

func TestPairingWideHTTPFailuresRecoverFIFO(t *testing.T) {
	for _, test := range []struct {
		code     int
		postOnly bool
	}{{401, false}, {404, false}, {401, true}, {407, true}} {
		t.Run(fmt.Sprintf("%d/post-only=%t", test.code, test.postOnly), func(t *testing.T) {
			peer, receive := deliveryReceiver(t)
			var failures atomic.Int64
			until := time.Now().Add(3 * time.Second)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if time.Now().Before(until) && (!test.postOnly || r.Method == http.MethodPost) {
					failures.Add(1)
					w.WriteHeader(test.code)
					return
				}
				receive.ServeHTTP(w, r)
			}))
			defer server.Close()
			memory := store.NewMemoryStore()
			service := rooms.NewService(rooms.ServiceConfig{
				NetworkID: "origin", Store: memory, Messages: memory, Broker: events.NewBroker(), PairingClient: pairings.NewClient(),
				Pairings:         []protocol.Pairing{{ID: "peer", RemoteNetworkID: "peer", RemoteBaseURL: server.URL, Token: "pair-secret", Status: protocol.PairingStatusConnected}},
				DeliveryRetryMin: 100 * time.Millisecond, DeliveryRetryMax: time.Second, DeliveryPollInterval: time.Millisecond,
			})
			defer service.Close()
			createDeliveryRoom(t, service)
			for i := 0; i < 5; i++ {
				_, err := service.SendMessage(protocol.SendMessageRequest{
					Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "research"}, From: protocol.Actor{Type: "agent", ID: "writer"}, Parts: []protocol.Part{{Kind: "text", Text: fmt.Sprintf("ordered-%d", i)}},
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			sawPairingError := false
			deadline := time.Now().Add(7 * time.Second)
			for time.Now().Before(deadline) {
				state, err := memory.PairingDeliveryStateContext(context.Background(), "peer")
				if err != nil {
					t.Fatal(err)
				}
				if time.Now().Before(until) && failures.Load() > 0 {
					if state.Rejected != 0 || state.Pending != 5 {
						t.Fatalf("outage advanced FIFO: %#v", state)
					}
					page, err := service.ListPairings()
					if err != nil {
						t.Fatal(err)
					}
					// Successful discovery can briefly mark the peer connected
					// inside a retry. Require an errored state during backoff.
					if state.LastError != "" && page[0].Status == protocol.PairingStatusError {
						sawPairingError = true
					}
				}
				if state.Pending == 0 && time.Now().After(until) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			state, err := memory.PairingDeliveryStateContext(context.Background(), "peer")
			if err != nil || state.Pending != 0 || state.Rejected != 0 || failures.Load() == 0 || !sawPairingError {
				t.Fatalf("recovery: %#v failures=%d sawPairingError=%t err=%v", state, failures.Load(), sawPairingError, err)
			}
			// A one-millisecond poll must not turn a peer outage into poll-rate traffic.
			if failures.Load() > 16 {
				t.Fatalf("pairing did not back off: %d failed requests", failures.Load())
			}
			assertDeliveryTexts(t, peer, "ordered-0", "ordered-1", "ordered-2", "ordered-3", "ordered-4")
		})
	}
}

func TestDiscoveredDestinationChangePurgesInviterBacklog(t *testing.T) {
	_, receive := deliveryReceiver(t)
	var changed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/network" && changed.Load() {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"replacement","protocols":{"http":["moltnet.http.v1"],"pair":["moltnet.pair.v1"]},"capabilities":{"direct_messages":true,"message_pagination":"cursor"}}`))
			return
		}
		if r.Method == http.MethodPost {
			w.WriteHeader(503)
			return
		}
		receive.ServeHTTP(w, r)
	}))
	defer server.Close()
	memory := store.NewMemoryStore()
	service, origin := deliveryOrigin(t, memory, protocol.Pairing{ID: "peer", RemoteBaseURL: server.URL, Token: "pair-secret"})
	postDeliveryMessage(t, origin.URL, "old-peer-only")
	waitDelivery(t, func() bool {
		heads, err := memory.ListPairingDeliveryHeadsContext(context.Background())
		return err == nil && len(heads) == 1 && heads[0].RemoteNetworkID == "peer" && heads[0].Attempts > 0
	})
	changed.Store(true)
	if _, err := service.PairingNetwork(context.Background(), "peer"); err != nil {
		t.Fatal(err)
	}
	waitDelivery(t, func() bool {
		state := deliveryStatus(t, origin.URL)
		return state.Pending == 0 && state.Dropped == 1 && state.Rejected == 0
	})
}
