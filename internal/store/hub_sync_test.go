package store

import (
	"context"
	"errors"
	"testing"
)

func TestHubSyncStoreAllBackends(t *testing.T) {
	for name, open := range hubBackends(t) {
		t.Run(name, func(t *testing.T) {
			storage := newEpochOrderStore(t, open)
			ctx := context.Background()
			for _, step := range []struct {
				id      string
				receipt HubReceiptRecord
			}{
				{"m1", epochReceipt(1, 2, "e1", "d1", "")},
				{"m2", epochReceipt(2, 2, "e2", "d2", "d1")},
				{"m4", epochReceipt(4, 3, "e4", "d4", "d3")},
			} {
				if err := appendWithReceipt(storage, hubMessage(step.id, "alpha"), step.receipt); err != nil {
					t.Fatal(err)
				}
			}
			after, err := storage.HubReceiptsAfterContext(ctx, hubTestGlobal, 1, 1, 10)
			if err != nil || len(after) != 2 || after[0].RoomSeq != 2 || after[1].RoomSeq != 4 || after[1].MembershipEpoch != 3 {
				t.Fatalf("receipts after 1 = %#v, %v", after, err)
			}
			if limited, _ := storage.HubReceiptsAfterContext(ctx, hubTestGlobal, 1, 0, 1); len(limited) != 1 || limited[0].RoomSeq != 1 {
				t.Fatalf("limited = %#v", limited)
			}
			messages, err := storage.HubMessagesContext(ctx, []string{"m2", "missing"})
			if err != nil || len(messages) != 1 || messages["m2"].Parts[0].Text != "m2" {
				t.Fatalf("messages = %#v, %v", messages, err)
			}

			// Cursor: forward only, never to a different receipt at its seq.
			if cursor, err := storage.HubSyncCursorContext(ctx, hubTestGlobal); err != nil || cursor.Seq != 0 {
				t.Fatalf("initial cursor = %#v, %v", cursor, err)
			}
			if err := storage.AdvanceHubSyncCursorContext(ctx, HubSyncCursor{GlobalRoomID: hubTestGlobal, Seq: 2, Digest: "d2"}); err != nil {
				t.Fatal(err)
			}
			if err := storage.AdvanceHubSyncCursorContext(ctx, HubSyncCursor{GlobalRoomID: hubTestGlobal, Seq: 1, Digest: "d1"}); !errors.Is(err, ErrHubSyncRollback) {
				t.Fatalf("cursor rollback: %v", err)
			}
			if err := storage.AdvanceHubSyncCursorContext(ctx, HubSyncCursor{GlobalRoomID: hubTestGlobal, Seq: 2, Digest: "other"}); !errors.Is(err, ErrHubSyncRollback) {
				t.Fatalf("cursor rewrite: %v", err)
			}
			if err := storage.AdvanceHubSyncCursorContext(ctx, HubSyncCursor{GlobalRoomID: hubTestGlobal, Seq: 2, Digest: "d2"}); err != nil {
				t.Fatalf("idempotent cursor: %v", err)
			}
			if cursor, _ := storage.HubSyncCursorContext(ctx, hubTestGlobal); cursor.Seq != 2 || cursor.Digest != "d2" {
				t.Fatalf("cursor = %#v", cursor)
			}

			// An origin's own stored message learns its placement.
			own := hubMessage("own", "member")
			if _, err := storage.AppendMessageWithPairingDeliveriesContext(context.Background(), own, nil, nil); err != nil {
				t.Fatal(err)
			}
			placed := epochReceipt(3, 3, "e3", "d3", "d2")
			placed.MessageID = "own"
			if err := storage.AttachHubReceiptContext(ctx, placed); err != nil {
				t.Fatal(err)
			}
			if err := storage.AttachHubReceiptContext(ctx, placed); err != nil {
				t.Fatalf("re-attaching the same receipt: %v", err)
			}
			conflicting := epochReceipt(3, 3, "e3", "forged", "d2")
			conflicting.MessageID = "own"
			if err := storage.AttachHubReceiptContext(ctx, conflicting); !errors.Is(err, ErrHubReceiptConflict) {
				t.Fatalf("conflicting attach: %v", err)
			}
			orphan := epochReceipt(5, 3, "e5", "d5", "d4")
			orphan.MessageID = "nope"
			if err := storage.AttachHubReceiptContext(ctx, orphan); !errors.Is(err, ErrHubReceiptConflict) {
				t.Fatalf("attach without a message: %v", err)
			}
			if byMessage, _ := storage.HubReceiptsForMessagesContext(ctx, []string{"own"}); byMessage["own"].RoomSeq != 3 {
				t.Fatalf("own receipt = %#v", byMessage)
			}
		})
	}
}
