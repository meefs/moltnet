package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func boundPairingContext() context.Context {
	return WithPairingDeliveryLimits(context.Background(), map[string]PairingDeliveryLimits{
		"pair-a": {RemoteNetworkID: "remote-a"}, "pair-b": {RemoteNetworkID: "remote-b"},
	})
}

func appendPairingResultTest(t *testing.T, storage pairingTestStore, id string, pairs ...string) {
	t.Helper()
	if _, err := storage.AppendMessageWithPairingDeliveriesContext(boundPairingContext(), pairingTestMessage(id), nil, pairs); err != nil {
		t.Fatal(err)
	}
}

func pairingHeads(t *testing.T, storage pairingTestStore) map[string]PairingDelivery {
	t.Helper()
	heads, err := storage.ListPairingDeliveryHeadsContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]PairingDelivery)
	for _, head := range heads {
		result[head.PairingID] = head
	}
	return result
}

func TestPairingDeliveryResultsAndDestinationBinding(t *testing.T) {
	for name, factory := range pairingStoreFactories() {
		t.Run(name, func(t *testing.T) {
			storage := factory(t)
			preparePairingTestRoom(t, storage)
			appendPairingResultTest(t, storage, "first", "pair-a", "pair-b")
			appendPairingResultTest(t, storage, "second", "pair-a")
			heads := pairingHeads(t, storage)
			if heads["pair-a"].RemoteNetworkID != "remote-a" || heads["pair-b"].RemoteNetworkID != "remote-b" {
				t.Fatalf("unbound enqueue: %#v", heads)
			}
			retry := heads["pair-b"]
			retry.Attempts, retry.NextAttempt = 2, time.Now().UTC().Add(time.Minute)
			results := []PairingDeliveryResult{
				{Delivery: heads["pair-a"], RejectedReason: "HTTP 403: forbidden room"},
				{Delivery: retry, Retry: true, LastError: "timeout"},
			}
			if err := storage.ApplyPairingDeliveryResultsContext(context.Background(), results); err != nil {
				t.Fatal(err)
			}
			state, err := storage.PairingDeliveryStateContext(context.Background(), "pair-a")
			if err != nil || state.Pending != 1 || state.Rejected != 1 || state.LastRejectionReason != results[0].RejectedReason || state.LastError != "" {
				t.Fatalf("rejection status: %#v, %v", state, err)
			}
			heads = pairingHeads(t, storage)
			if heads["pair-a"].MessageID != "second" || heads["pair-b"].Attempts != 2 || !heads["pair-b"].NextAttempt.Equal(retry.NextAttempt) {
				t.Fatalf("batch did not advance/retry independently: %#v", heads)
			}
			state, err = storage.PairingDeliveryStateContext(context.Background(), "pair-b")
			if err != nil || state.Pending != 1 || state.LastError != "timeout" {
				t.Fatalf("retry status: %#v, %v", state, err)
			}
			if err := storage.ApplyPairingDeliveryResultsContext(context.Background(), []PairingDeliveryResult{{Delivery: heads["pair-a"]}}); err != nil {
				t.Fatal(err)
			}
			state, err = storage.PairingDeliveryStateContext(context.Background(), "pair-a")
			if err != nil || state.Pending != 0 || state.Rejected != 1 || state.LastRejectionReason != results[0].RejectedReason {
				t.Fatalf("success erased rejection history: %#v, %v", state, err)
			}
		})
	}
}

func TestPairingDeliveryReconcilePurgesAndCounts(t *testing.T) {
	for name, factory := range pairingStoreFactories() {
		t.Run(name, func(t *testing.T) {
			storage := factory(t)
			preparePairingTestRoom(t, storage)
			appendPairingResultTest(t, storage, "first", "pair-a", "pair-b")
			appendPairingResultTest(t, storage, "second", "pair-a")
			if _, err := storage.AppendMessageWithPairingDeliveriesContext(context.Background(), pairingTestMessage("legacy"), nil, []string{"legacy"}); err != nil {
				t.Fatal(err)
			}
			current := map[string]string{"pair-a": "new-network", "legacy": "legacy-network"}
			dropped, err := storage.ReconcilePairingDeliveriesContext(context.Background(), current)
			if err != nil || dropped["pair-a"] != 2 || dropped["pair-b"] != 1 || dropped["legacy"] != 1 {
				t.Fatalf("purge counts = %#v, %v", dropped, err)
			}
			if heads := pairingHeads(t, storage); len(heads) != 0 {
				t.Fatalf("old destination retained: %#v", heads)
			}
			for id, want := range dropped {
				state, err := storage.PairingDeliveryStateContext(context.Background(), id)
				if err != nil || state.Dropped != int64(want) || state.Pending != 0 {
					t.Fatalf("%s drop state: %#v, %v", id, state, err)
				}
			}
			if again, err := storage.ReconcilePairingDeliveriesContext(context.Background(), current); err != nil || len(again) != 0 {
				t.Fatalf("purge was not idempotent: %#v, %v", again, err)
			}
			appendPairingResultTest(t, storage, "current", "pair-a")
			if dropped, err := storage.ReconcilePairingDeliveriesContext(context.Background(), map[string]string{"pair-a": "remote-a"}); err != nil || len(dropped) != 0 {
				t.Fatalf("current destination purged: %#v, %v", dropped, err)
			}
		})
	}
}

func TestPairingDeliveryStaleResultsCannotChangeCurrentRows(t *testing.T) {
	for name, factory := range pairingStoreFactories() {
		t.Run(name, func(t *testing.T) {
			storage := factory(t)
			preparePairingTestRoom(t, storage)
			appendPairingResultTest(t, storage, "first", "pair-a")
			head := pairingHeads(t, storage)["pair-a"]
			wrongSequence, wrongRemote := head, head
			wrongSequence.Sequence++
			wrongRemote.RemoteNetworkID = "different"
			for _, stale := range []PairingDelivery{wrongSequence, wrongRemote} {
				for _, retry := range []bool{true, false} {
					stale.Attempts = 99
					if err := storage.ApplyPairingDeliveryResultsContext(context.Background(), []PairingDeliveryResult{{Delivery: stale, Retry: retry, LastError: "stale", RejectedReason: "stale"}}); err != nil {
						t.Fatal(err)
					}
				}
			}
			got := pairingHeads(t, storage)["pair-a"]
			state, err := storage.PairingDeliveryStateContext(context.Background(), "pair-a")
			if err != nil || state.Pending != 1 || state.Rejected != 0 || state.LastError != "" || got.Attempts != 0 {
				t.Fatalf("stale result changed row: %#v, %#v, %v", got, state, err)
			}
		})
	}
}

func TestPairingDeliveryBatchRollback(t *testing.T) {
	for _, backend := range []string{"json", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			storage := pairingStoreFactories()[backend](t)
			preparePairingTestRoom(t, storage)
			appendPairingResultTest(t, storage, "first", "pair-a", "pair-b")
			heads := pairingHeads(t, storage)
			results := []PairingDeliveryResult{{Delivery: heads["pair-a"], RejectedReason: "forbidden"}, {Delivery: heads["pair-b"]}}
			if sql, ok := storage.(*SQLStore); ok {
				if _, err := sql.db.Exec(`CREATE TRIGGER fail_result BEFORE DELETE ON pairing_deliveries WHEN old.pairing_id = 'pair-b' BEGIN SELECT RAISE(ABORT, 'failed result'); END`); err != nil {
					t.Fatal(err)
				}
			} else {
				file := storage.(*FileStore)
				blocker := filepath.Join(t.TempDir(), "file")
				if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
					t.Fatal(err)
				}
				file.path = filepath.Join(blocker, "state")
				if dropped, err := file.ReconcilePairingDeliveriesContext(context.Background(), map[string]string{"pair-a": "remote-a", "pair-b": "remote-b"}); err != nil || len(dropped) != 0 {
					t.Fatalf("no-op reconciliation rewrote snapshot: %#v, %v", dropped, err)
				}
			}
			if err := storage.ApplyPairingDeliveryResultsContext(context.Background(), results); err == nil {
				t.Fatal("batch unexpectedly persisted")
			}
			if heads := pairingHeads(t, storage); len(heads) != 2 {
				t.Fatalf("partial batch committed: %#v", heads)
			}
			state, err := storage.PairingDeliveryStateContext(context.Background(), "pair-a")
			if err != nil || state.Rejected != 0 || state.LastRejectionReason != "" {
				t.Fatalf("rejection counter escaped rollback: %#v, %v", state, err)
			}
		})
	}
}
