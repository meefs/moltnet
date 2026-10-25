package rooms_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	pinsDir := t.TempDir()
	inviter := newSignedPeer(t, netkeys.OpenPinStore(pinsDir))
	_, joiner := newSignedOrigin(t, joinerKey, inviter.pairURL)
	postDeliveryMessage(t, joiner.URL, "first contact")
	waitDelivery(t, func() bool { return deliveryStatus(t, joiner.URL).Pending == 0 })
	// The pin is durable: a restarted server reads it from the same keys dir.
	pin, ok, err := netkeys.OpenPinStore(pinsDir).Lookup("origin")
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
	// A hop swaps only the unsigned FQID to point at another identity.
	fqid := signedRequest(t, originKey, "fqid-tamper", "original text")
	fqid.From.FQID = protocol.AgentFQID("origin", "operator")
	if status, body := deliveryPost(t, peer.pairURL, "pair-secret", fqid); status != http.StatusForbidden {
		t.Fatalf("fqid swap accepted: %d %s", status, body)
	}
	consistent := signedRequest(t, originKey, "fqid-consistent", "consistent fqid")
	consistent.From.FQID = protocol.AgentFQID("origin", "writer")
	if status, body := deliveryPost(t, peer.pairURL, "pair-secret", consistent); status != http.StatusAccepted {
		t.Fatalf("consistent fqid refused: %d %s", status, body)
	}

	author := signedRequest(t, originKey, "author-tamper", "original text")
	author.From.Name = "Operator"
	if status, body := deliveryPost(t, peer.pairURL, "pair-secret", author); status != http.StatusForbidden {
		t.Fatalf("author rename accepted: %d %s", status, body)
	}
	if page, _ := peer.service.ListRoomMessages("research", "", 100); len(page.Messages) != 1 {
		t.Fatalf("only the consistent message may land: %#v", page.Messages)
	}

	if status, body := deliveryPost(t, peer.pairURL, "pair-secret", signedRequest(t, originKey, "untouched", "original text")); status != http.StatusAccepted {
		t.Fatalf("untouched signed message: %d %s", status, body)
	}
	assertDeliveryTexts(t, peer.service, "consistent fqid", "original text")
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

func TestUnsignablePartDataIsRefusedNotDroppedSilently(t *testing.T) {
	values := map[string]any{
		"snowflake": float64(1849012345678901234),
		"unix_nano": float64(1761400000123456789),
		"1e21":      1e21,
		"2^53+2":    9007199254740993.0 * 4,
	}
	for name, value := range values {
		t.Run(name, func(t *testing.T) {
			originKey := newTestIdentity(t)
			peer := newSignedPeer(t, netkeys.NewMemoryPinStore())
			pinAsInvite(t, peer.pins, "origin", originKey)
			_, originServer := newSignedOrigin(t, originKey, peer.pairURL)
			status, body := deliveryPost(t, originServer.URL, "", protocol.SendMessageRequest{
				Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "research"},
				From:   protocol.Actor{Type: "agent", ID: "writer"},
				Parts:  []protocol.Part{{Kind: "text", Text: "see id"}, {Kind: "data", Data: map[string]any{"id": value}}},
			})
			// The author learns now; nothing is queued to be dropped later.
			if status != http.StatusUnprocessableEntity || !strings.Contains(body, "strings") {
				t.Fatalf("unsignable message: %d %s", status, body)
			}
			if state := deliveryStatus(t, originServer.URL); state.Pending != 0 || state.Rejected != 0 {
				t.Fatalf("unsignable message reached the outbox: %#v", state)
			}
			// The same id as a string signs and arrives.
			status, body = deliveryPost(t, originServer.URL, "", protocol.SendMessageRequest{
				Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "research"},
				From:   protocol.Actor{Type: "agent", ID: "writer"},
				Parts:  []protocol.Part{{Kind: "data", Data: map[string]any{"id": "1849012345678901234"}}},
			})
			if status != http.StatusAccepted {
				t.Fatalf("string id: %d %s", status, body)
			}
			waitDelivery(t, func() bool { return deliveryStatus(t, originServer.URL).Pending == 0 })
			if messages := wireRoomMessages(t, peer.readURL); len(messages) != 1 || messages[0].Origin.Signature != protocol.SignatureStateVerified {
				t.Fatalf("string id delivery: %#v", messages)
			}
		})
	}
}

func TestOriginIDWhitespaceVariantsCannotReplayASignedMessage(t *testing.T) {
	for _, backend := range envelopeTestBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			originKey := newTestIdentity(t)
			peer := newSignedPeerWithStore(t, netkeys.NewMemoryPinStore(), backend.open(t))
			pinAsInvite(t, peer.pins, "origin", originKey)
			first := signedRequest(t, originKey, "msg-1", "only once")
			if status, body := deliveryPost(t, peer.pairURL, "pair-secret", first); status != http.StatusAccepted {
				t.Fatalf("first: %d %s", status, body)
			}
			var accepted protocol.MessageAccepted
			for index, variant := range []string{" msg-1", "msg-1 ", "\tmsg-1"} {
				replay := first
				replay.ID = fmt.Sprintf("replay-%d", index)
				replay.Origin.MessageID = variant
				replay.Origin.NetworkID = " origin\t"
				status, body := deliveryPost(t, peer.pairURL, "pair-secret", replay)
				if status != http.StatusAccepted || json.Unmarshal([]byte(body), &accepted) != nil || accepted.MessageID != "msg-1" {
					t.Fatalf("whitespace variant %q must be the same message: %d %s", variant, status, body)
				}
				fork := signedRequest(t, originKey, "msg-1", "forked")
				fork.ID = fmt.Sprintf("fork-%d", index)
				fork.Origin.MessageID = variant
				if status, body := deliveryPost(t, peer.pairURL, "pair-secret", fork); status != http.StatusConflict {
					t.Fatalf("whitespace fork %q not detected as a conflict: %d %s", variant, status, body)
				}
			}
			assertDeliveryTexts(t, peer.service, "only once")
		})
	}
}

func TestHTTPRendersSignedFactsFromTheEnvelope(t *testing.T) {
	originKey := newTestIdentity(t)
	peer := newSignedPeer(t, netkeys.NewMemoryPinStore())
	pinAsInvite(t, peer.pins, "origin", originKey)
	// The origin mentions an agent the receiver does not know, and the
	// envelope was signed long before the receiver stores it.
	request := signedRequest(t, originKey, "delayed", "hello @ghost")
	request.Mentions = []string{"ghost"}
	request.Envelope = signEnvelopeFor(t, originKey, request)
	if status, body := deliveryPost(t, peer.pairURL, "pair-secret", request); status != http.StatusAccepted {
		t.Fatalf("send: %d %s", status, body)
	}
	response, err := http.Get(peer.readURL + "/v1/rooms/research/messages")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var page struct {
		Messages []struct {
			CreatedAt     string         `json:"created_at"`
			ReceivedAt    string         `json:"received_at"`
			Mentions      []string       `json:"mentions"`
			LocalMentions []string       `json:"local_mentions"`
			From          protocol.Actor `json:"from"`
			Origin        struct {
				Signature string `json:"signature"`
			} `json:"origin"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil || len(page.Messages) != 1 {
		t.Fatalf("decode: %v %#v", err, page)
	}
	got := page.Messages[0]
	if got.Origin.Signature != protocol.SignatureStateVerified || got.CreatedAt != "2026-10-25T10:00:00Z" {
		t.Fatalf("signed created_at must come from the envelope: %#v", got)
	}
	if got.ReceivedAt == "" || got.ReceivedAt == got.CreatedAt {
		t.Fatalf("receive time must be a separate field: %#v", got)
	}
	if len(got.Mentions) != 1 || got.Mentions[0] != "ghost" || len(got.LocalMentions) != 0 {
		t.Fatalf("signed mentions vs local resolution: %#v", got)
	}
	if got.From.FQID != protocol.AgentFQID("origin", "writer") || got.From.NetworkID != "origin" {
		t.Fatalf("author must be the signed author: %#v", got.From)
	}
}
