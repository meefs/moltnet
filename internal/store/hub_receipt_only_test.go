package store

import (
	"context"
	"errors"
	"testing"
)

// Receipts stored without a message meet every conflict rule, and a
// message arriving later for one binds to it.
func TestHubReceiptOnlyIsConflictCheckedAndBindsLater(t *testing.T) {
	for name, open := range hubBackends(t) {
		t.Run(name, func(t *testing.T) {
			storage := newEpochOrderStore(t, open)
			ctx := context.Background()
			if err := storage.StoreHubReceiptContext(ctx, epochReceipt(1, 2, "e1", "d1", "")); err != nil {
				t.Fatal(err)
			}
			if err := storage.StoreHubReceiptContext(ctx, epochReceipt(1, 2, "e1", "d1", "")); err != nil {
				t.Fatalf("identical receipt-only redelivery: %v", err)
			}
			if err := storage.StoreHubReceiptContext(ctx, epochReceipt(1, 2, "e1x", "d1x", "")); !errors.Is(err, ErrHubReceiptConflict) {
				t.Fatalf("a different receipt at a stored seq: %v", err)
			}
			if err := storage.StoreHubReceiptContext(ctx, epochReceipt(2, 3, "e2", "d2", "d1")); err != nil {
				t.Fatal(err)
			}
			// A message placed by a receipt that contradicts the stored
			// receipt-only one is refused; one placed by the same receipt
			// binds to it.
			if err := appendWithReceipt(storage, hubMessage("m2x", "alpha"), epochReceipt(2, 3, "e2x", "d2x", "d1")); !errors.Is(err, ErrHubReceiptConflict) {
				t.Fatalf("live receipt contradicting a receipt-only one: %v", err)
			}
			if err := storage.StoreHubReceiptContext(ctx, epochReceipt(3, 1, "e3", "d3", "d2")); !errors.Is(err, ErrHubReceiptConflict) {
				t.Fatalf("epoch regression after a receipt-only entry: %v", err)
			}
			if err := appendWithReceipt(storage, hubMessage("m2", "alpha"), epochReceipt(2, 3, "e2", "d2", "d1")); err != nil {
				t.Fatalf("message for a receipt-only seq: %v", err)
			}
			bound, err := storage.HubReceiptsForMessagesContext(ctx, []string{"m2"})
			if err != nil || bound["m2"].RoomSeq != 2 {
				t.Fatalf("message did not bind to its receipt: %#v, %v", bound, err)
			}
			after, err := storage.HubReceiptsAfterContext(ctx, hubTestGlobal, 1, 0, 0)
			if err != nil || len(after) != 2 || after[0].MessageID != "" || after[1].MessageID != "m2" {
				t.Fatalf("stored receipts = %#v, %v", after, err)
			}
		})
	}
}

func TestEnvelopeForOriginFindsTheStoredEnvelope(t *testing.T) {
	for name, open := range hubBackends(t) {
		t.Run(name, func(t *testing.T) {
			storage := newEpochOrderStore(t, open)
			if err := appendWithReceipt(storage, hubMessage("m1", "alpha"), epochReceipt(1, 1, "e1", "d1", "")); err != nil {
				t.Fatal(err)
			}
			record, ok, err := storage.EnvelopeForOriginContext(context.Background(), "alpha", "m1-o")
			if err != nil || !ok || record.MessageID != "m1" || record.Digest != "env-m1" {
				t.Fatalf("envelope by origin = %#v, %v, %v", record, ok, err)
			}
			if _, ok, _ := storage.EnvelopeForOriginContext(context.Background(), "alpha", "m1"); ok {
				t.Fatal("found an envelope by local id instead of origin id")
			}
		})
	}
}

func TestHubSyncCursorKeepsItsEpochAndRefusesARegression(t *testing.T) {
	for name, open := range hubBackends(t) {
		t.Run(name, func(t *testing.T) {
			storage := newEpochOrderStore(t, open)
			ctx := context.Background()
			if err := storage.AdvanceHubSyncCursorContext(ctx, HubSyncCursor{GlobalRoomID: hubTestGlobal, Seq: 1, Digest: "d1", MembershipEpoch: 3}); err != nil {
				t.Fatal(err)
			}
			if cursor, err := storage.HubSyncCursorContext(ctx, hubTestGlobal); err != nil || cursor.MembershipEpoch != 3 {
				t.Fatalf("cursor = %#v, %v", cursor, err)
			}
			if err := storage.AdvanceHubSyncCursorContext(ctx, HubSyncCursor{GlobalRoomID: hubTestGlobal, Seq: 2, Digest: "d2", MembershipEpoch: 2}); !errors.Is(err, ErrHubSyncRollback) {
				t.Fatalf("cursor advanced to an older epoch: %v", err)
			}
		})
	}
}

func TestHubWithheldReceiptsPersistUntilAMessageBindsThem(t *testing.T) {
	for name, open := range hubBackends(t) {
		t.Run(name, func(t *testing.T) {
			storage := newEpochOrderStore(t, open)
			ctx := context.Background()
			withheld := epochReceipt(1, 1, "e1", "d1", "")
			withheld.Withheld = "too_large"
			if err := storage.StoreHubReceiptContext(ctx, withheld); err != nil {
				t.Fatal(err)
			}
			if err := storage.StoreHubReceiptContext(ctx, epochReceipt(2, 1, "e2", "d2", "d1")); err != nil {
				t.Fatal(err)
			}
			listed, err := storage.HubWithheldReceiptsContext(ctx, hubTestGlobal)
			if err != nil || len(listed) != 1 || listed[0].RoomSeq != 1 || listed[0].Withheld != "too_large" {
				t.Fatalf("withheld = %#v, %v", listed, err)
			}
			if err := appendWithReceipt(storage, hubMessage("m1", "alpha"), epochReceipt(1, 1, "e1", "d1", "")); err != nil {
				t.Fatal(err)
			}
			if listed, _ := storage.HubWithheldReceiptsContext(ctx, hubTestGlobal); len(listed) != 0 {
				t.Fatalf("a bound receipt is still listed withheld: %#v", listed)
			}
		})
	}
}
