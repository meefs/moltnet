package store

import (
	"context"
	"testing"
	"time"
)

func TestPairingDeliveryTransactionsUseCommonLockOrder(t *testing.T) {
	for _, operation := range []string{"results", "enqueue"} {
		t.Run(operation, func(t *testing.T) { testPairingDeliveryLockOrder(t, operation) })
	}
}

func testPairingDeliveryLockOrder(t *testing.T, operation string) {
	storage := newPairingPostgresTestStore(t).(*SQLStore)
	preparePairingTestRoom(t, storage)
	appendPairingResultTest(t, storage, "first", "pair-a", "pair-b")
	heads := pairingHeads(t, storage)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	blocker, err := storage.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(blocker)
	if err := storage.lockPairingDeliveryState(ctx, blocker, "pair-a"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	// Completion order is deliberately the reverse of enqueue's lock order.
	go func() {
		if operation == "enqueue" {
			_, err := storage.AppendMessageWithPairingDeliveriesContext(boundPairingContext(), pairingTestMessage("second"), nil, []string{"pair-b", "pair-a"})
			done <- err
		} else {
			done <- storage.ApplyPairingDeliveryResultsContext(ctx, []PairingDeliveryResult{{Delivery: heads["pair-b"]}, {Delivery: heads["pair-a"]}})
		}
	}()
	deadline := time.Now().Add(2 * time.Second)
	waiting := false
	for time.Now().Before(deadline) {
		var count int
		err := storage.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_stat_activity WHERE datname = current_database() AND application_name = current_schema() AND wait_event_type = 'Lock' AND query LIKE '%INSERT INTO pairing_delivery_states%'`).Scan(&count)
		if err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			waiting = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !waiting {
		t.Fatal("result transaction never waited on pair-a")
	}
	if _, err := blocker.ExecContext(ctx, `SET LOCAL lock_timeout = '150ms'`); err != nil {
		t.Fatal(err)
	}
	// With a common lock order, the waiting result transaction cannot hold B
	// while waiting for A, so an enqueue that already holds A can still lock B.
	lockErr := storage.lockPairingDeliveryState(ctx, blocker, "pair-b")
	if lockErr == nil {
		err = blocker.Commit()
	} else {
		err = blocker.Rollback()
	}
	if err != nil {
		t.Fatal(err)
	}
	if applyErr := <-done; applyErr != nil {
		t.Fatal(applyErr)
	}
	if lockErr != nil {
		t.Fatalf("opposing lock order can deadlock enqueue: %v", lockErr)
	}
}
