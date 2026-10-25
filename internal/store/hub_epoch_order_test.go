package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func epochReceipt(seq, epoch int64, event, digest, prev string) HubReceiptRecord {
	receipt := hubReceipt(seq, event, digest, prev)
	receipt.MembershipEpoch = epoch
	return receipt
}

func newEpochOrderStore(t *testing.T, open func(t *testing.T) hubTestBackend) hubTestBackend {
	t.Helper()
	storage := open(t)
	if err := storage.CreateRoom(protocol.Room{ID: "local-research", NetworkID: "member", Name: "r"}); err != nil {
		t.Fatal(err)
	}
	room := HubRoom{GlobalRoomID: hubTestGlobal, LocalRoomID: "local-research", HomeNetworkID: "home", HomePairingID: "home-link", CreatedAt: time.Now().UTC()}
	if err := storage.CreateHubRoomContext(context.Background(), room, hubControl(1, "g")); err != nil {
		t.Fatal(err)
	}
	return storage
}

// Receipts may arrive out of order and with gaps, but membership epochs
// never decrease along room seqs, whichever neighbour is stored first.
func TestHubReceiptEpochsNeverDecreaseAlongRoomSeqs(t *testing.T) {
	for name, open := range hubBackends(t) {
		t.Run(name, func(t *testing.T) {
			storage := newEpochOrderStore(t, open)
			// Successor stored first at an older epoch: a later placement
			// at a newer epoch before it is a rollback.
			if err := appendWithReceipt(storage, hubMessage("m5", "alpha"), epochReceipt(5, 2, "e5", "d5", "x4")); err != nil {
				t.Fatal(err)
			}
			if err := appendWithReceipt(storage, hubMessage("m3", "alpha"), epochReceipt(3, 4, "e3", "d3", "x2")); !errors.Is(err, ErrHubReceiptConflict) {
				t.Fatalf("newer epoch before a stored older-epoch successor: %v", err)
			}
			// Predecessor stored first at a newer epoch.
			if err := appendWithReceipt(storage, hubMessage("m7", "alpha"), epochReceipt(7, 6, "e7", "d7", "x6")); err != nil {
				t.Fatal(err)
			}
			if err := appendWithReceipt(storage, hubMessage("m9", "alpha"), epochReceipt(9, 5, "e9", "d9", "x8")); !errors.Is(err, ErrHubReceiptConflict) {
				t.Fatalf("older epoch after a stored newer-epoch predecessor: %v", err)
			}
			// Reversed but monotonic delivery is fine.
			if err := appendWithReceipt(storage, hubMessage("m2", "alpha"), epochReceipt(2, 2, "e2", "d2", "x1")); err != nil {
				t.Fatalf("monotonic out-of-order receipt: %v", err)
			}
		})
	}
}

func TestHubReceiptEpochOrderHoldsUnderConcurrentAppends(t *testing.T) {
	for name, open := range hubBackends(t) {
		t.Run(name, func(t *testing.T) {
			for round := 0; round < 10; round++ {
				storage := newEpochOrderStore(t, open)
				var wg sync.WaitGroup
				errs := make([]error, 2)
				for index, receipt := range []HubReceiptRecord{
					epochReceipt(3, 4, "e3", "d3", "x2"), epochReceipt(5, 2, "e5", "d5", "x4"),
				} {
					wg.Add(1)
					go func() {
						defer wg.Done()
						errs[index] = appendWithReceipt(storage, hubMessage(fmt.Sprintf("m%d", receipt.RoomSeq), "alpha"), receipt)
					}()
				}
				wg.Wait()
				accepted := 0
				for _, err := range errs {
					if err == nil {
						accepted++
					} else if !errors.Is(err, ErrHubReceiptConflict) {
						t.Fatalf("round %d: %v", round, err)
					}
				}
				if accepted != 1 {
					t.Fatalf("round %d: %d of two contradicting receipts stored: %v", round, accepted, errs)
				}
			}
		})
	}
}
