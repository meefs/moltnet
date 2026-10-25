package store

import (
	"context"
	"testing"
)

// An attempt that dequeued an unbound row (an inviter-side pairing whose
// peer was never discovered) binds the destination during its own
// discovery. Its result must still settle that row, or a rejected message
// is sent again and a delivered one is resent.
func TestPairingDeliveryResultSettlesARowItsOwnDiscoveryBound(t *testing.T) {
	for name, factory := range pairingStoreFactories() {
		t.Run(name, func(t *testing.T) {
			storage := factory(t)
			preparePairingTestRoom(t, storage)
			for _, id := range []string{"rejected", "delivered"} {
				if _, err := storage.AppendMessageWithPairingDeliveriesContext(context.Background(), pairingTestMessage(id), nil, []string{"pair-u"}); err != nil {
					t.Fatal(err)
				}
			}
			head := pairingHeads(t, storage)["pair-u"]
			if head.RemoteNetworkID != "" {
				t.Fatalf("head should be unbound: %#v", head)
			}
			if _, err := storage.BindPairingDeliveryDestinationContext(context.Background(), "pair-u", "peer"); err != nil {
				t.Fatal(err)
			}
			if err := storage.ApplyPairingDeliveryResultsContext(context.Background(), []PairingDeliveryResult{{Delivery: head, RejectedReason: "403"}}); err != nil {
				t.Fatal(err)
			}
			state, err := storage.PairingDeliveryStateContext(context.Background(), "pair-u")
			if err != nil || state.Rejected != 1 || state.Pending != 1 {
				t.Fatalf("after a rejected result for a row its discovery bound: %#v, %v", state, err)
			}
			if next := pairingHeads(t, storage)["pair-u"]; next.MessageID == head.MessageID {
				t.Fatalf("the rejected row is still queued: %#v", next)
			}
		})
	}
}
