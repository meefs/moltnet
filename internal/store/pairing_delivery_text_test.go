package store

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPairingDeliveryDiagnosticsCannotPoisonBatch(t *testing.T) {
	for name, factory := range pairingStoreFactories() {
		t.Run(name, func(t *testing.T) {
			storage := factory(t)
			preparePairingTestRoom(t, storage)
			appendPairingResultTest(t, storage, "first", "pair-a", "pair-b")
			heads := pairingHeads(t, storage)
			badText := "bad\x00\x01\xff" + strings.Repeat("界", 1100)
			if err := storage.ApplyPairingDeliveryResultsContext(context.Background(), []PairingDeliveryResult{
				{Delivery: heads["pair-a"], RejectedReason: badText},
				{Delivery: heads["pair-b"], Retry: true, LastError: badText},
			}); err != nil {
				t.Fatal(err)
			}
			a, err := storage.PairingDeliveryStateContext(context.Background(), "pair-a")
			if err != nil || a.Pending != 0 || a.Rejected != 1 {
				t.Fatalf("rejection did not commit: %#v %v", a, err)
			}
			b, err := storage.PairingDeliveryStateContext(context.Background(), "pair-b")
			if err != nil || b.Pending != 1 {
				t.Fatalf("retry did not commit: %#v %v", b, err)
			}
			for _, value := range []string{a.LastRejectionReason, b.LastError} {
				if !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\x01") || utf8.RuneCountInString(value) != 1024 || !strings.HasPrefix(value, "bad�界") {
					t.Fatalf("unsafe or unbounded diagnostic: %q", value)
				}
			}
		})
	}
}

func TestPairingDeliveryControlOnlyReasonRemainsRejection(t *testing.T) {
	storage := NewMemoryStore()
	preparePairingTestRoom(t, storage)
	appendPairingResultTest(t, storage, "first", "pair-a")
	head := pairingHeads(t, storage)["pair-a"]
	if err := storage.ApplyPairingDeliveryResultsContext(context.Background(), []PairingDeliveryResult{{Delivery: head, RejectedReason: "\x00\x01"}}); err != nil {
		t.Fatal(err)
	}
	state, err := storage.PairingDeliveryStateContext(context.Background(), "pair-a")
	if err != nil || state.Rejected != 1 || state.Pending != 0 || state.LastRejectionReason == "" {
		t.Fatalf("sanitization erased rejection: %#v %v", state, err)
	}
}
