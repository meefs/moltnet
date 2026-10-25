package rooms_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/noopolis/moltnet/internal/rooms"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// loseFirstAck serves the first message submission for real and then
// answers 502, the way a dropped connection loses an acknowledgment.
func loseFirstAck(network *hubNet) {
	real := network.handler.Load().(http.Handler)
	var lost atomic.Bool
	network.handler.Store(http.Handler(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost && request.URL.Path == "/v1/messages" && lost.CompareAndSwap(false, true) {
			real.ServeHTTP(httptest.NewRecorder(), request)
			http.Error(response, "connection lost", http.StatusBadGateway)
			return
		}
		real.ServeHTTP(response, request)
	})))
}

func TestHubRetryOfAPlacedSubmissionAfterAnEpochChangeIsIdempotent(t *testing.T) {
	h := newHubTopology(t, memoryBackend)
	h.add(t, h.alpha)
	h.join(t, h.alpha)
	canonical := canonicalRoomOf(h.global)
	submission := hubEnvelope(t, h.alpha.identity, "alpha", h.global, canonical, "placed-1", "placed once", 2, 1)
	if status, body := hubPost(t, h.home, "alpha-link-secret", submission); status != http.StatusAccepted {
		t.Fatalf("first submission: %d %s", status, body)
	}
	h.add(t, h.beta) // the epoch moves on before alpha hears the ack
	status, body := hubPost(t, h.home, "alpha-link-secret", submission)
	if status != http.StatusAccepted {
		t.Fatalf("retry of an already placed submission after an epoch change: %d %s", status, body)
	}
	if got := h.home.texts(t); !equalStrings(got, []string{"placed once"}) {
		t.Fatalf("home history = %q", got)
	}
}

func TestHubSenderDoesNotResignAPlacedMessageWhoseAckWasLost(t *testing.T) {
	h := newHubTopology(t, memoryBackend)
	h.add(t, h.alpha)
	h.join(t, h.alpha)
	loseFirstAck(h.home)
	h.alpha.post(t, "ack lost")
	h.home.waitTexts(t, "ack lost")
	// The room moves on and alpha learns it before its retry goes out.
	h.add(t, h.beta)
	if _, err := h.alpha.service.SyncHubRoomContext(contextBackground(), h.global); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		state := h.alpha.delivery(t, "link-home")
		return state.Pending == 0 || state.Rejected != 0
	})
	if state := h.alpha.delivery(t, "link-home"); state.Rejected != 0 {
		t.Fatalf("a delivered message was reported rejected after its lost ack: %#v", state)
	}
	if got := h.home.texts(t); !equalStrings(got, []string{"ack lost"}) {
		t.Fatalf("home history = %q", got)
	}
	// Alpha still holds exactly the bytes the home placed.
	homeWire, alphaWire := h.home.wire(t), h.alpha.wire(t)
	if len(homeWire) != 1 || len(alphaWire) != 1 || homeWire[0].Origin.Fingerprint == "" {
		t.Fatalf("wire = %#v / %#v", homeWire, alphaWire)
	}
	if epoch := storedEnvelopeEpoch(t, h.alpha, alphaWire[0].ID); epoch != 2 {
		t.Fatalf("alpha re-signed its placed message under epoch %d", epoch)
	}
}

// storedEnvelopeEpoch is the membership epoch of the envelope network
// stored for one of its messages.
func storedEnvelopeEpoch(t *testing.T, network *hubNet, messageID string) int64 {
	t.Helper()
	page, err := network.service.ListRoomMessages(localRoom(network), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	signatures, err := network.service.MessageSignatures(contextBackground(), page.Messages)
	if err != nil {
		t.Fatal(err)
	}
	signed := signatures[messageID].Signed
	if signed == nil || signed.MembershipEpoch == nil {
		t.Fatalf("%s has no signed hub envelope for %s", network.id, messageID)
	}
	return *signed.MembershipEpoch
}

func TestHubMemberRefusesAnEpochRollbackDeliveredInReverseOrder(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	if _, err := h.home.service.AppendHubControlContext(contextBackground(), rooms.HubControlRequest{
		GlobalRoomID: h.global, Kind: protocol.RoomControlSetRole, NetworkID: "alpha", Role: protocol.RoomRoleAdmin}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.beta.service.SyncHubRoomContext(contextBackground(), h.global); err != nil {
		t.Fatal(err)
	}
	canonical := canonicalRoomOf(h.global)
	later := hubEnvelope(t, h.alpha.identity, "alpha", h.global, canonical, "rev-5", "placed at seq 5, epoch 3", 3, 5)
	later.Receipt = homeReceipt(t, h.home.identity, h.global, 3, 5, eventID(t, later), strings.Repeat("4", 64))
	if status, body := hubPost(t, h.beta, "beta-link-secret", later); status != http.StatusAccepted {
		t.Fatalf("seq 5: %d %s", status, body)
	}
	earlier := hubEnvelope(t, h.alpha.identity, "alpha", h.global, canonical, "rev-3", "placed at seq 3, epoch 4", 4, 3)
	earlier.Receipt = homeReceipt(t, h.home.identity, h.global, 4, 3, eventID(t, earlier), strings.Repeat("2", 64))
	if status, body := hubPost(t, h.beta, "beta-link-secret", earlier); status != http.StatusConflict {
		t.Fatalf("a newer epoch placed before an older one was accepted: %d %s", status, body)
	}
}
