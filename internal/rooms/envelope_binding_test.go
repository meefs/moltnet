package rooms_test

import (
	"net/http"
	"testing"

	"github.com/noopolis/moltnet/internal/netkeys"
	"github.com/noopolis/moltnet/internal/store"
)

// restartAndImpersonate restarts the unbound inviter and has the same pair
// credential claim a network that has no pin; it must be refused.
func restartAndImpersonate(t *testing.T, pinsDir string, memory *store.MemoryStore) {
	t.Helper()
	_, restartedURL := startUnboundInviter(t, pinsDir, memory)
	impostor := signedRequestAs(t, newTestIdentity(t), "victim", "forged", "I am victim")
	if status, body := deliveryPost(t, restartedURL, "pair-secret", impostor); status != http.StatusForbidden {
		t.Fatalf("after restart the credential spoke for a new network: %d %s", status, body)
	}
}

func TestOperatorTrustKeepsThePairingBindingAcrossRestart(t *testing.T) {
	pinsDir := t.TempDir()
	joinerKey := newTestIdentity(t)
	memory := store.NewMemoryStore()
	first, firstURL := startUnboundInviter(t, pinsDir, memory)
	createDeliveryRoomWithMembers(t, first, "origin:writer", "victim:writer")
	if status, body := deliveryPost(t, firstURL, "pair-secret", signedRequest(t, joinerKey, "hello", "first contact")); status != http.StatusAccepted {
		t.Fatalf("first contact: %d %s", status, body)
	}
	// The operator confirms the unconfirmed pin out of band.
	if _, err := netkeys.OpenPinStore(pinsDir).PinKey(verifiedPin("origin", joinerKey)); err != nil {
		t.Fatal(err)
	}
	_ = first.Close()
	restartAndImpersonate(t, pinsDir, memory)
}

func TestFirstContactWithAnAlreadyPinnedKeyRecordsThePairingBinding(t *testing.T) {
	pinsDir := t.TempDir()
	joinerKey := newTestIdentity(t)
	if _, err := netkeys.OpenPinStore(pinsDir).PinKey(verifiedPin("origin", joinerKey)); err != nil {
		t.Fatal(err)
	}
	memory := store.NewMemoryStore()
	first, firstURL := startUnboundInviter(t, pinsDir, memory)
	createDeliveryRoomWithMembers(t, first, "origin:writer", "victim:writer")
	if status, body := deliveryPost(t, firstURL, "pair-secret", signedRequest(t, joinerKey, "hello", "first contact")); status != http.StatusAccepted {
		t.Fatalf("first contact: %d %s", status, body)
	}
	_ = first.Close()
	restartAndImpersonate(t, pinsDir, memory)
}
