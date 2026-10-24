package rooms

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

type failingDeliveryResults struct {
	*store.MemoryStore
	calls atomic.Int32
	allow atomic.Bool
}

func (s *failingDeliveryResults) ApplyPairingDeliveryResultsContext(ctx context.Context, results []store.PairingDeliveryResult) error {
	s.calls.Add(1)
	if !s.allow.Load() {
		return errors.New("temporary result-store outage")
	}
	return s.MemoryStore.ApplyPairingDeliveryResultsContext(ctx, results)
}

func TestDeliveryPersistenceRetriesWithoutResending(t *testing.T) {
	storage := &failingDeliveryResults{MemoryStore: store.NewMemoryStore()}
	client := &federationRelayClient{relayed: make(chan protocol.SendMessageRequest, 10)}
	service := NewService(ServiceConfig{NetworkID: "local", Store: storage, Messages: storage, Broker: events.NewBroker(), PairingClient: client,
		Pairings:             []protocol.Pairing{{ID: "peer", RemoteNetworkID: "remote", RemoteBaseURL: "https://peer.example"}},
		DeliveryPollInterval: time.Millisecond, DeliveryRetryMin: 5 * time.Millisecond, DeliveryRetryMax: 10 * time.Millisecond})
	defer service.Close()
	if _, err := service.CreateRoom(protocol.CreateRoomRequest{ID: "research", Federation: &protocol.RoomFederation{Mode: protocol.RoomFederationAll}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second"} {
		request := roomSend("research", "local")
		request.ID = id
		if _, err := service.SendMessage(request); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(time.Second)
	for storage.calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if storage.calls.Load() < 3 {
		t.Fatal("persistence was not retried")
	}
	select {
	case sent := <-client.relayed:
		if sent.ID != "first" {
			t.Fatalf("first send: %q", sent.ID)
		}
	default:
		t.Fatal("no acknowledged send before persistence outage")
	}
	select {
	case sent := <-client.relayed:
		t.Fatalf("resent or advanced during persistence outage: %q", sent.ID)
	default:
	}
	storage.allow.Store(true)
	select {
	case sent := <-client.relayed:
		if sent.ID != "second" {
			t.Fatalf("after persistence recovery: %q", sent.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("did not advance after persistence recovered")
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state, _ := storage.PairingDeliveryStateContext(context.Background(), "peer")
		if state.Pending == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("results did not drain after recovery")
}

type interruptedDeliveryResults struct {
	*store.MemoryStore
	calls   atomic.Int32
	started chan struct{}
}

func (s *interruptedDeliveryResults) ApplyPairingDeliveryResultsContext(ctx context.Context, results []store.PairingDeliveryResult) error {
	if s.calls.Add(1) == 1 {
		close(s.started)
		<-ctx.Done()
		return ctx.Err()
	}
	return s.MemoryStore.ApplyPairingDeliveryResultsContext(ctx, results)
}

func TestDeliveryShutdownFlushesAcknowledgedResults(t *testing.T) {
	storage := &interruptedDeliveryResults{MemoryStore: store.NewMemoryStore(), started: make(chan struct{})}
	client := &federationRelayClient{relayed: make(chan protocol.SendMessageRequest, 10)}
	service := NewService(ServiceConfig{NetworkID: "local", Store: storage, Messages: storage, Broker: events.NewBroker(), PairingClient: client,
		Pairings: []protocol.Pairing{{ID: "peer", RemoteNetworkID: "remote", RemoteBaseURL: "https://peer.example"}}, DeliveryPollInterval: time.Millisecond})
	defer service.Close()
	if _, err := service.CreateRoom(protocol.CreateRoomRequest{ID: "research", Federation: &protocol.RoomFederation{Mode: protocol.RoomFederationAll}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SendMessage(roomSend("research", "local")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-storage.started:
	case <-time.After(time.Second):
		t.Fatal("acknowledged result did not reach persistence")
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	state, err := storage.PairingDeliveryStateContext(context.Background(), "peer")
	if err != nil || state.Pending != 0 || storage.calls.Load() != 2 {
		t.Fatalf("shutdown did not flush acknowledged result: %#v calls=%d %v", state, storage.calls.Load(), err)
	}
	if len(client.relayed) != 1 {
		t.Fatalf("network sends = %d", len(client.relayed))
	}
}
