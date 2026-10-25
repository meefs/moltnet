package rooms_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/noopolis/moltnet/internal/netkeys"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestSignedDeliveryVerifiedEndToEnd(t *testing.T) {
	originKey := newTestIdentity(t)
	peer := newSignedPeer(t, netkeys.NewMemoryPinStore())
	pinAsInvite(t, peer.pins, "origin", originKey) // the joiner pinned the inviter
	origin, originServer := newSignedOrigin(t, originKey, peer.pairURL)

	postDeliveryMessage(t, originServer.URL, "signed hello")
	waitDelivery(t, func() bool { return deliveryStatus(t, originServer.URL).Pending == 0 })
	assertDeliveryTexts(t, peer.service, "signed hello")

	messages := wireRoomMessages(t, peer.readURL)
	if len(messages) != 1 || messages[0].Origin.Signature != protocol.SignatureStateVerified ||
		messages[0].Origin.Fingerprint != originKey.Fingerprint() || messages[0].Origin.ReceivedVia != "origin" {
		t.Fatalf("received wire message = %#v", messages)
	}
	local := wireRoomMessages(t, originServer.URL)
	if len(local) != 1 || local[0].Origin.Signature != protocol.SignatureStateVerified {
		t.Fatalf("origin's own signed message = %#v", local)
	}
	if network := origin.Network(); network.Identity == nil || network.Identity.Fingerprint != originKey.Fingerprint() {
		t.Fatalf("/v1/network identity = %#v", network.Identity)
	}
	records, err := peer.memory.MessageEnvelopesContext(context.Background(), []string{messages[0].ID})
	if err != nil || len(records) != 1 {
		t.Fatalf("stored envelope = %#v, %v", records, err)
	}
	envelope, _, err := protocol.VerifySignedEnvelope(records[messages[0].ID].Envelope)
	if err != nil || envelope.Parts[0].Text != "signed hello" || envelope.Author.ID != "writer" {
		t.Fatalf("stored envelope does not verify: %#v, %v", envelope, err)
	}
}

func TestSignedDeliveryFirstContactPinsJoinerKey(t *testing.T) {
	// The inviter never learns its joiner's key from the invite; it pins the
	// key carried by the first signed message over the authenticated pairing.
	joinerKey := newTestIdentity(t)
	inviterPins := netkeys.NewMemoryPinStore()
	inviter := newSignedPeer(t, inviterPins)
	_, joiner := newSignedOrigin(t, joinerKey, inviter.pairURL)
	postDeliveryMessage(t, joiner.URL, "first contact")
	waitDelivery(t, func() bool { return deliveryStatus(t, joiner.URL).Pending == 0 })
	pin, ok, err := inviterPins.Lookup("origin")
	if err != nil || !ok || pin.Fingerprint != joinerKey.Fingerprint() || pin.Trust != protocol.KeyTrustVerified ||
		pin.Source != netkeys.PinSourcePairing || pin.SourceID != "origin" {
		t.Fatalf("first-contact pin = %#v, %v, %v", pin, ok, err)
	}
	if messages := wireRoomMessages(t, inviter.readURL); len(messages) != 1 || messages[0].Origin.Signature != protocol.SignatureStateVerified {
		t.Fatalf("first-contact message = %#v", messages)
	}
}

func TestSignedDeliveryWrongKeyRejectedAndCounted(t *testing.T) {
	peer := newSignedPeer(t, netkeys.NewMemoryPinStore())
	pinAsInvite(t, peer.pins, "origin", newTestIdentity(t))
	_, originServer := newSignedOrigin(t, newTestIdentity(t), peer.pairURL)
	postDeliveryMessage(t, originServer.URL, "signed by an impostor")
	waitDelivery(t, func() bool {
		state := deliveryStatus(t, originServer.URL)
		return state.Pending == 0 && state.Rejected == 1 && strings.Contains(state.LastRejectionReason, "403")
	})
	assertNoDelivered(t, peer.service)
}

func TestUnsignedLegacyPeerAcceptedAsUnsigned(t *testing.T) {
	peer := newSignedPeer(t, netkeys.NewMemoryPinStore())
	_, legacy := newSignedOrigin(t, nil, peer.pairURL)
	postDeliveryMessage(t, legacy.URL, "from an old network")
	waitDelivery(t, func() bool { return deliveryStatus(t, legacy.URL).Pending == 0 })
	messages := wireRoomMessages(t, peer.readURL)
	if len(messages) != 1 || messages[0].Origin.Signature != protocol.SignatureStateUnsigned {
		t.Fatalf("legacy message = %#v", messages)
	}
	if _, ok, _ := peer.pins.Lookup("origin"); ok {
		t.Fatal("unsigned message created a pin")
	}
	if local := wireRoomMessages(t, legacy.URL); len(local) != 1 || local[0].Origin.Signature != "" {
		t.Fatalf("local unsigned messages must carry no signature label: %#v", local)
	}
}

func TestUnsignedMessageFromPinnedNetworkRejected(t *testing.T) {
	peer := newSignedPeer(t, netkeys.NewMemoryPinStore())
	pinAsInvite(t, peer.pins, "origin", newTestIdentity(t))
	request := signedRequest(t, newTestIdentity(t), "stripped", "envelope stripped in transit")
	request.Envelope = nil
	if status, body := deliveryPost(t, peer.pairURL, "pair-secret", request); status != http.StatusForbidden {
		t.Fatalf("unsigned from pinned network: %d %s", status, body)
	}
	assertNoDelivered(t, peer.service)
}

func TestTamperedSignedMessageRejected(t *testing.T) {
	originKey := newTestIdentity(t)
	peer := newSignedPeer(t, netkeys.NewMemoryPinStore())
	pinAsInvite(t, peer.pins, "origin", originKey)

	// A hop rewrites the delivered body but keeps the original envelope.
	outer := signedRequest(t, originKey, "outer-tamper", "original text")
	outer.Parts[0].Text = "rewritten text"
	if status, body := deliveryPost(t, peer.pairURL, "pair-secret", outer); status != http.StatusForbidden {
		t.Fatalf("body/envelope mismatch accepted: %d %s", status, body)
	}

	// A hop rewrites the signed bytes themselves.
	inner := signedRequest(t, originKey, "inner-tamper", "original text")
	raw, _ := base64.RawURLEncoding.DecodeString(inner.Envelope.Envelope)
	inner.Envelope.Envelope = base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(raw), "original", "rewritten", 1)))
	inner.Parts[0].Text = "rewritten text"
	if status, body := deliveryPost(t, peer.pairURL, "pair-secret", inner); status != http.StatusForbidden {
		t.Fatalf("tampered envelope accepted: %d %s", status, body)
	}

	// A hop renames the author in both places.
	author := signedRequest(t, originKey, "author-tamper", "original text")
	author.From.Name = "Operator"
	if status, body := deliveryPost(t, peer.pairURL, "pair-secret", author); status != http.StatusForbidden {
		t.Fatalf("author rename accepted: %d %s", status, body)
	}
	assertNoDelivered(t, peer.service)

	if status, body := deliveryPost(t, peer.pairURL, "pair-secret", signedRequest(t, originKey, "untouched", "original text")); status != http.StatusAccepted {
		t.Fatalf("untouched signed message: %d %s", status, body)
	}
	assertDeliveryTexts(t, peer.service, "original text")
}

func TestConflictingSignedDuplicateRejected(t *testing.T) {
	originKey := newTestIdentity(t)
	peer := newSignedPeer(t, netkeys.NewMemoryPinStore())
	pinAsInvite(t, peer.pins, "origin", originKey)
	first := signedRequest(t, originKey, "origin-message", "first version")
	status, body := deliveryPost(t, peer.pairURL, "pair-secret", first)
	if status != http.StatusAccepted {
		t.Fatalf("first: %d %s", status, body)
	}
	var accepted protocol.MessageAccepted
	if err := json.Unmarshal([]byte(body), &accepted); err != nil {
		t.Fatal(err)
	}

	retry := first
	retry.ID = "retry-attempt"
	if status, body := deliveryPost(t, peer.pairURL, "pair-secret", retry); status != http.StatusAccepted || !strings.Contains(body, accepted.MessageID) {
		t.Fatalf("identical retry should be an idempotent duplicate: %d %s", status, body)
	}

	fork := signedRequest(t, originKey, "origin-message", "second version")
	fork.ID = "fork-attempt"
	if status, body := deliveryPost(t, peer.pairURL, "pair-secret", fork); status != http.StatusConflict {
		t.Fatalf("forked origin message acknowledged: %d %s", status, body)
	}
	assertDeliveryTexts(t, peer.service, "first version")
}

func TestSignedDMDeliveryVerified(t *testing.T) {
	originKey := newTestIdentity(t)
	peer := newSignedPeer(t, netkeys.NewMemoryPinStore())
	pinAsInvite(t, peer.pins, "origin", originKey)
	_, originServer := newSignedOrigin(t, originKey, peer.pairURL)
	status, body := deliveryPost(t, originServer.URL, "", protocol.SendMessageRequest{
		Target: protocol.Target{Kind: protocol.TargetKindDM, DMID: "signed-dm", ParticipantIDs: []string{"peer:reader", "writer"}},
		From:   protocol.Actor{Type: "agent", ID: "writer"}, Parts: []protocol.Part{{Kind: "text", Text: "private hello"}},
	})
	if status != http.StatusAccepted {
		t.Fatalf("DM send: %d %s", status, body)
	}
	waitDelivery(t, func() bool { return deliveryStatus(t, originServer.URL).Pending == 0 })
	if state := deliveryStatus(t, originServer.URL); state.Rejected != 0 {
		t.Fatalf("signed DM rejected: %#v", state)
	}
	page, err := peer.service.ListDMMessages("signed-dm", "", 10)
	if err != nil || len(page.Messages) != 1 {
		t.Fatalf("peer DM = %#v, %v", page, err)
	}
	signatures, err := peer.service.MessageSignatures(context.Background(), page.Messages)
	if err != nil || signatures[page.Messages[0].ID].State != protocol.SignatureStateVerified {
		t.Fatalf("DM signature = %#v, %v", signatures, err)
	}
}

func TestUnrepresentablePartDataRelaysUnsigned(t *testing.T) {
	peer := newSignedPeer(t, netkeys.NewMemoryPinStore())
	_, originServer := newSignedOrigin(t, newTestIdentity(t), peer.pairURL)
	status, body := deliveryPost(t, originServer.URL, "", protocol.SendMessageRequest{
		Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "research"},
		From:   protocol.Actor{Type: "agent", ID: "writer"},
		Parts:  []protocol.Part{{Kind: "data", Data: map[string]any{"big": 9007199254740993.0 * 4}}},
	})
	if status != http.StatusAccepted {
		t.Fatalf("send: %d %s", status, body)
	}
	waitDelivery(t, func() bool { return deliveryStatus(t, originServer.URL).Pending == 0 })
	messages := wireRoomMessages(t, peer.readURL)
	if len(messages) != 1 || messages[0].Origin.Signature != protocol.SignatureStateUnsigned {
		t.Fatalf("unrepresentable message should arrive unsigned and unaltered: %#v", messages)
	}
}
