package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPairingDeliveryOutcomesReopen(t *testing.T) {
	for _, backend := range []string{"json", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state")
			open := func() pairingTestStore {
				if backend == "json" {
					storage, err := NewFileStore(path)
					if err != nil {
						t.Fatal(err)
					}
					return storage
				}
				storage, err := NewSQLiteStore(path)
				if err != nil {
					t.Fatal(err)
				}
				return storage
			}
			storage := open()
			preparePairingTestRoom(t, storage)
			appendPairingResultTest(t, storage, "first", "pair-a", "pair-b")
			head := pairingHeads(t, storage)["pair-a"]
			if err := storage.ApplyPairingDeliveryResultsContext(context.Background(), []PairingDeliveryResult{{Delivery: head, RejectedReason: "DMs unsupported"}}); err != nil {
				t.Fatal(err)
			}
			if sql, ok := storage.(*SQLStore); ok {
				if err := sql.Close(); err != nil {
					t.Fatal(err)
				}
			}
			storage = open()
			if sql, ok := storage.(*SQLStore); ok {
				defer sql.Close()
			}
			state, err := storage.PairingDeliveryStateContext(context.Background(), "pair-a")
			if err != nil || state.Pending != 0 || state.Rejected != 1 || state.LastRejectionReason != "DMs unsupported" {
				t.Fatalf("reopened rejection state: %#v, %v", state, err)
			}
			head = pairingHeads(t, storage)["pair-b"]
			if head.RemoteNetworkID != "remote-b" {
				t.Fatalf("reopen lost destination: %#v", head)
			}
		})
	}
}

func TestPairingDeliveryOutcomeMigrationPreservesLegacyQueueUntilReconcile(t *testing.T) {
	storage := pairingStoreFactories()["sqlite"](t).(*SQLStore)
	preparePairingTestRoom(t, storage)
	appendPairingResultTest(t, storage, "legacy", "pair-a")
	// Recreate the previous version's schema around a real queued message.
	for _, query := range []string{
		`DROP INDEX pairing_deliveries_pairing_seq`,
		`ALTER TABLE pairing_deliveries DROP COLUMN remote_network_id`,
		`ALTER TABLE pairing_delivery_states DROP COLUMN rejected`,
		`ALTER TABLE pairing_delivery_states DROP COLUMN last_rejection_reason`,
		`DELETE FROM schema_migrations WHERE version = 13`,
	} {
		if _, err := storage.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := storage.migrate(); err != nil {
		t.Fatal(err)
	}
	head := pairingHeads(t, storage)["pair-a"]
	if head.MessageID != "legacy" || head.RemoteNetworkID != "" {
		t.Fatalf("migration guessed a destination: %#v", head)
	}
	var indexName string
	if err := storage.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'pairing_deliveries_pairing_seq'`).Scan(&indexName); err != nil {
		t.Fatalf("pairing head index absent: %v", err)
	}
	dropped, err := storage.ReconcilePairingDeliveriesContext(context.Background(), map[string]string{"pair-a": "remote-a"})
	if err != nil || len(dropped) != 0 {
		t.Fatalf("unbound legacy delivery was purged: %#v, %v", dropped, err)
	}
	state, err := storage.PairingDeliveryStateContext(context.Background(), "pair-a")
	if err != nil || state.Pending != 1 || state.Dropped != 0 || state.Rejected != 0 {
		t.Fatalf("legacy purge state: %#v, %v", state, err)
	}
}
