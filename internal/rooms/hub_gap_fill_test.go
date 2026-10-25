package rooms_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/noopolis/moltnet/pkg/protocol"
)

// Epoch order is a property of room seqs, not of delivery order. A message
// placed at an older epoch AFTER a newer-epoch placement is refused in
// either delivery order (see also the reverse-order rollback test). A
// message placed BEFORE it, at a seq the newer receipt already chains to,
// is a delayed delivery the chain committed to, and filling that gap is
// exactly what catch-up does: it is accepted. Delaying is within a home's
// powers; reordering after the fact is not.
func TestHubEpochOrderFollowsRoomSeqsNotDeliveryOrder(t *testing.T) {
	h := joinedTopology(t, memoryBackend)            // epoch 3: home, alpha, beta
	h.control(t, protocol.RoomControlRemove, "beta") // epoch 4
	canonical := canonicalRoomOf(h.global)

	// Beta's message, legitimately signed while it was a writer (epoch 3).
	held := hubEnvelope(t, h.beta.identity, "beta", h.global, canonical, "held-1", "beta, held back", 3, 1)
	held.Receipt = homeReceipt(t, h.home.identity, h.global, 3, 1, eventID(t, held), "")
	_, heldDigest, err := protocol.VerifyRoomReceipt(*held.Receipt, h.home.identity.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	// A home message placed at seq 2 under the current epoch 4, chained to seq 1.
	fresh := hubEnvelope(t, h.home.identity, "home", h.global, canonical, "fresh-1", "after removal", 4, 1)
	fresh.Receipt = homeReceipt(t, h.home.identity, h.global, 4, 2, eventID(t, fresh), heldDigest)

	// In-order sanity: seq 2 (epoch 4) then a seq 3 at epoch 3 is refused.
	inOrder := hubEnvelope(t, h.beta.identity, "beta", h.global, canonical, "held-2", "in order", 3, 2)
	if status, _ := hubPost(t, h.alpha, "alpha-link-secret", fresh); status != http.StatusAccepted {
		t.Fatalf("fresh seq 2: %d", status)
	}
	_, freshDigest, _ := protocol.VerifyRoomReceipt(*fresh.Receipt, h.home.identity.PublicKey())
	inOrder.Receipt = homeReceipt(t, h.home.identity, h.global, 3, 3, eventID(t, inOrder), freshDigest)
	if status, body := hubPost(t, h.alpha, "alpha-link-secret", inOrder); status != http.StatusConflict {
		t.Fatalf("in-order older epoch after newer should be 409, got %d %s", status, body)
	}

	// Delayed: the message seq 2 already chains to arrives late at seq 1.
	if status, body := hubPost(t, h.alpha, "alpha-link-secret", held); status != http.StatusAccepted {
		t.Fatalf("a committed earlier seq at an older epoch is a delayed delivery: %d %s", status, body)
	}
	// Reordered: placing an epoch-3 event after the epoch-4 seq is refused
	// even when it arrives before anything else at that seq range.
	late := hubEnvelope(t, h.beta.identity, "beta", h.global, canonical, "held-3", "released later", 3, 3)
	late.Receipt = homeReceipt(t, h.home.identity, h.global, 3, 9, eventID(t, late), strings.Repeat("8", 64))
	if status, body := hubPost(t, h.alpha, "alpha-link-secret", late); status != http.StatusConflict {
		t.Fatalf("an older epoch placed after a newer one: %d %s", status, body)
	}
}
