package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

type pairingTestStore interface {
	RoomStore
	MessageStore
	PairingDeliveryStore
}

func pairingStoreFactories() map[string]func(*testing.T) pairingTestStore {
	return map[string]func(*testing.T) pairingTestStore{
		"memory": func(*testing.T) pairingTestStore { return NewMemoryStore() },
		"json": func(t *testing.T) pairingTestStore {
			value, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			return value
		},
		"sqlite": func(t *testing.T) pairingTestStore {
			value, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = value.Close() })
			return value
		},
	}
}

func pairingTestMessage(id string) protocol.Message {
	return protocol.Message{
		ID: id, NetworkID: "local",
		Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "delivery-room"},
		From:   protocol.Actor{ID: "sender", NetworkID: "local", Type: "agent"},
		Parts:  []protocol.Part{{Kind: protocol.PartKindText, Text: id}},
		// Queue age is operational time, regardless of a caller's domain clock.
		CreatedAt: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func preparePairingTestRoom(t *testing.T, storage pairingTestStore) {
	t.Helper()
	if err := storage.CreateRoom(protocol.Room{ID: "delivery-room", NetworkID: "local", Name: "delivery", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
}

func TestPairingDeliveryBackends(t *testing.T) {
	for name, factory := range pairingStoreFactories() {
		t.Run(name, func(t *testing.T) { testPairingDeliveryBackend(t, factory(t)) })
	}
}

func testPairingDeliveryBackend(t *testing.T, storage pairingTestStore) {
	t.Helper()
	preparePairingTestRoom(t, storage)
	ctx := WithPairingDeliveryLimits(context.Background(), map[string]PairingDeliveryLimits{
		"pair-a": {MaxAge: 7 * 24 * time.Hour, MaxPending: 2},
		"pair-b": {MaxAge: 7 * 24 * time.Hour, MaxPending: 10},
	})
	for _, id := range []string{"z-first", "a-second", "m-third"} {
		lifecycle, err := storage.AppendMessageWithPairingDeliveriesContext(ctx, pairingTestMessage(id), nil, []string{"pair-a", "pair-b", "pair-b"})
		if err != nil {
			t.Fatal(err)
		}
		if id == "m-third" && lifecycle.PairingDeliveriesDropped["pair-a"] != 1 {
			t.Fatalf("atomic overflow drops = %#v", lifecycle.PairingDeliveriesDropped)
		}
	}
	if _, err := storage.AppendMessageWithPairingDeliveriesContext(ctx, pairingTestMessage("m-third"), nil, []string{"pair-a"}); !errors.Is(err, ErrDuplicateMessage) {
		t.Fatalf("duplicate append = %v", err)
	}
	state, err := storage.PairingDeliveryStateContext(ctx, "pair-a")
	if err != nil || state.Pending != 2 || state.Dropped != 1 || time.Since(state.OldestPendingAt) > time.Minute {
		t.Fatalf("pair-a state = %#v, %v", state, err)
	}
	heads, err := storage.ListPairingDeliveryHeadsContext(ctx)
	if err != nil || len(heads) != 2 {
		t.Fatalf("heads = %#v, %v", heads, err)
	}
	byPair := map[string]PairingDelivery{}
	for _, head := range heads {
		byPair[head.PairingID] = head
	}
	if byPair["pair-a"].MessageID != "a-second" || byPair["pair-b"].MessageID != "z-first" {
		t.Fatalf("FIFO heads = %#v", byPair)
	}
	head := byPair["pair-a"]
	head.Attempts, head.NextAttempt = 3, time.Now().UTC().Add(time.Minute)
	if err := storage.UpdatePairingDeliveryContext(ctx, head, "peer unavailable"); err != nil {
		t.Fatal(err)
	}
	state, err = storage.PairingDeliveryStateContext(ctx, "pair-a")
	if err != nil || state.LastError != "peer unavailable" {
		t.Fatalf("failure state = %#v, %v", state, err)
	}
	if err := storage.CompletePairingDeliveryContext(ctx, "pair-a", head.MessageID); err != nil {
		t.Fatal(err)
	}
	state, err = storage.PairingDeliveryStateContext(ctx, "pair-a")
	if err != nil || state.Pending != 1 || state.LastError != "" || state.Dropped != 1 {
		t.Fatalf("success state = %#v, %v", state, err)
	}
	dropped, err := storage.PrunePairingDeliveriesContext(ctx, "pair-b", time.Now().Add(8*24*time.Hour), 7*24*time.Hour, 10)
	if err != nil || dropped != 3 {
		t.Fatalf("age prune = %d, %v", dropped, err)
	}
	state, err = storage.PairingDeliveryStateContext(ctx, "pair-b")
	if err != nil || state.Pending != 0 || state.Dropped != 3 || !state.OldestPendingAt.IsZero() {
		t.Fatalf("expired state = %#v, %v", state, err)
	}
	state, err = storage.PairingDeliveryStateContext(ctx, "pair-a")
	if err != nil || state.Pending != 1 || state.Dropped != 1 {
		t.Fatalf("pairing isolation state = %#v, %v", state, err)
	}
}

func TestPairingReceiveDedupeBackends(t *testing.T) {
	for name, factory := range pairingStoreFactories() {
		t.Run(name, func(t *testing.T) {
			storage := factory(t)
			preparePairingTestRoom(t, storage)
			message := pairingTestMessage("stored-first")
			message.Origin = protocol.MessageOrigin{NetworkID: "remote", MessageID: "origin-first", ReceivedVia: "pair-a"}
			if err := storage.AppendMessage(message); err != nil {
				t.Fatal(err)
			}
			message.ID, message.Origin.ReceivedVia = "retry-new-id", "another-pair-to-same-network"
			err := storage.AppendMessage(message)
			var duplicate *DuplicateMessageError
			if !errors.Is(err, ErrDuplicateMessage) || !errors.As(err, &duplicate) || duplicate.MessageID != "stored-first" {
				t.Fatalf("origin retry = %v, duplicate = %#v", err, duplicate)
			}
			message.ID, message.Origin.NetworkID = "stored-first", "different-network"
			if err := storage.AppendMessage(message); !errors.Is(err, ErrMessageIDConflict) {
				t.Fatalf("different origin wire ID collision = %v", err)
			}
			local := pairingTestMessage("stored-first")
			if err := storage.AppendMessage(local); !errors.Is(err, ErrMessageIDConflict) {
				t.Fatalf("local send collided with remote wire ID = %v", err)
			}
			message.ID = "new-origin"
			if err := storage.AppendMessage(message); err != nil {
				t.Fatal(err)
			}
			page, err := storage.ListRoomMessages("delivery-room", "", 10)
			if err != nil || len(page.Messages) != 2 {
				t.Fatalf("stored origins = %#v, %v", page, err)
			}
		})
	}
}

func TestPairingReceiveConcurrentRetry(t *testing.T) {
	for name, factory := range pairingStoreFactories() {
		t.Run(name, func(t *testing.T) {
			storage := factory(t)
			preparePairingTestRoom(t, storage)
			var workers sync.WaitGroup
			results := make(chan error, 16)
			for index := 0; index < 16; index++ {
				workers.Add(1)
				go func(index int) {
					defer workers.Done()
					message := pairingTestMessage(string(rune('a' + index)))
					message.Origin = protocol.MessageOrigin{NetworkID: "remote", MessageID: "once", ReceivedVia: "pair-a"}
					results <- storage.AppendMessage(message)
				}(index)
			}
			workers.Wait()
			close(results)
			accepted := 0
			for err := range results {
				if err == nil {
					accepted++
				} else if !errors.Is(err, ErrDuplicateMessage) {
					t.Fatal(err)
				}
			}
			if accepted != 1 {
				t.Fatalf("accepted %d concurrent attempts", accepted)
			}
			page, err := storage.ListRoomMessages("delivery-room", "", 100)
			if err != nil || len(page.Messages) != 1 {
				t.Fatalf("concurrent retry stored = %#v, %v", page, err)
			}
		})
	}
}
