package rooms_test

import (
	"net/http"
	"strings"
	"testing"

	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/internal/netkeys"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// A key a room home introduced for a network id must not block a later
// direct, config-bound (verified) first contact with the real network, or
// any home could squat names on every member of its rooms.
func TestRoomIntroducedPinYieldsToADirectBoundFirstContact(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	gamma := newTestIdentity(t)
	impostor := newTestIdentity(t)
	// Beta gets a direct, config-bound pairing with the real gamma.
	h.beta.pairings = append(h.beta.pairings, protocol.Pairing{ID: "direct-gamma", RemoteNetworkID: "gamma", RemoteBaseURL: h.alpha.url, Token: "gamma-secret"})
	h.beta.tokens = append(h.beta.tokens, authn.TokenConfig{ID: "direct-gamma", Value: "gamma-secret", Network: "gamma", Scopes: []authn.Scope{authn.ScopePair}})
	h.beta.start(t)
	if _, err := h.beta.service.CreateRoom(protocol.CreateRoomRequest{ID: "plain", Members: []string{"lead", "gamma:lead"},
		Federation: &protocol.RoomFederation{Mode: protocol.RoomFederationAll}}); err != nil {
		t.Fatal(err)
	}
	// The pin exactly as acceptHubForward/introducedPin records it when a
	// (malicious) home introduces "gamma" with an impostor key.
	pinKey(t, h.beta.pins, netkeys.Pin{NetworkID: "gamma", Fingerprint: impostor.Fingerprint(), PublicKey: impostor.Public().PublicKey,
		Trust: protocol.KeyTrustIntroduced, Source: netkeys.PinSourceRoom, SourceID: h.global, IntroducedBy: "home"})

	direct := protocol.SendMessageRequest{ID: "g-1", Origin: protocol.MessageOrigin{NetworkID: "gamma", MessageID: "g-1"},
		Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "plain"},
		From:   protocol.Actor{Type: "agent", ID: "lead", NetworkID: "gamma"}, Parts: []protocol.Part{{Kind: "text", Text: "real gamma"}}}
	direct.Envelope = signEnvelopeFor(t, gamma, direct)
	status, body := deliveryPost(t, h.beta.url, "gamma-secret", direct)
	t.Logf("real gamma over its own bound pairing: %d %s", status, body)
	if status != http.StatusAccepted {
		t.Fatalf("a room home's introduced pin blocks the real network's verified first contact: %d %s", status, body)
	}
	pin, _, _ := h.beta.pins.Lookup("gamma")
	if pin.Fingerprint != gamma.Fingerprint() || pin.Trust != protocol.KeyTrustVerified {
		t.Fatalf("pin after verified direct contact = %#v", pin)
	}
}

// With the same key, a direct bound contact upgrades an introduced pin to
// verified: this operator's own bound pairing just proved it.
func TestDirectBoundContactUpgradesAnIntroducedPin(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	delta := newTestIdentity(t)
	h.beta.pairings = append(h.beta.pairings, protocol.Pairing{ID: "direct-delta", RemoteNetworkID: "delta", RemoteBaseURL: h.alpha.url, Token: "delta-secret"})
	h.beta.tokens = append(h.beta.tokens, authn.TokenConfig{ID: "direct-delta", Value: "delta-secret", Network: "delta", Scopes: []authn.Scope{authn.ScopePair}})
	h.beta.start(t)
	if _, err := h.beta.service.CreateRoom(protocol.CreateRoomRequest{ID: "plain", Members: []string{"lead", "delta:lead"},
		Federation: &protocol.RoomFederation{Mode: protocol.RoomFederationAll}}); err != nil {
		t.Fatal(err)
	}
	pinKey(t, h.beta.pins, netkeys.Pin{NetworkID: "delta", Fingerprint: delta.Fingerprint(), PublicKey: delta.Public().PublicKey,
		Trust: protocol.KeyTrustIntroduced, Source: netkeys.PinSourceRoom, SourceID: h.global, IntroducedBy: "home"})
	direct := protocol.SendMessageRequest{ID: "d-1", Origin: protocol.MessageOrigin{NetworkID: "delta", MessageID: "d-1"},
		Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "plain"},
		From:   protocol.Actor{Type: "agent", ID: "lead", NetworkID: "delta"}, Parts: []protocol.Part{{Kind: "text", Text: "real delta"}}}
	direct.Envelope = signEnvelopeFor(t, delta, direct)
	if status, body := deliveryPost(t, h.beta.url, "delta-secret", direct); status != http.StatusAccepted {
		t.Fatalf("same-key direct contact: %d %s", status, body)
	}
	pin, _, _ := h.beta.pins.Lookup("delta")
	body := pairGet(t, h.beta.url+"/v1/rooms/plain/messages", hubOperatorToken)
	t.Logf("pin after bound direct contact = trust %q source %q; wire label contains verified=%v introduced=%v",
		pin.Trust, pin.Source, strings.Contains(body, `"signature":"verified"`), strings.Contains(body, `"signature":"introduced"`))
	if pin.Trust != protocol.KeyTrustVerified {
		t.Fatalf("pin not upgraded by this operator's own bound pairing: %#v", pin)
	}
}

// An unbound credential is no evidence of identity: it never replaces a
// key a room home introduced.
func TestUnboundFirstContactNeverReplacesAnIntroducedPin(t *testing.T) {
	pinsDir := t.TempDir()
	introduced := newTestIdentity(t)
	if _, err := netkeys.OpenPinStore(pinsDir).PinKey(netkeys.Pin{NetworkID: "origin", Fingerprint: introduced.Fingerprint(),
		PublicKey: introduced.Public().PublicKey, Trust: protocol.KeyTrustIntroduced, Source: netkeys.PinSourceRoom, SourceID: "room", IntroducedBy: "home"}); err != nil {
		t.Fatal(err)
	}
	service, url := startUnboundInviter(t, pinsDir, store.NewMemoryStore())
	createDeliveryRoomWithMembers(t, service, "origin:writer")
	if status, body := deliveryPost(t, url, "pair-secret", signedRequest(t, newTestIdentity(t), "m-1", "other key")); status != http.StatusForbidden {
		t.Fatalf("unbound contact replaced an introduced pin: %d %s", status, body)
	}
	if pin, _, _ := netkeys.OpenPinStore(pinsDir).Lookup("origin"); pin.Fingerprint != introduced.Fingerprint() {
		t.Fatalf("pin = %#v", pin)
	}
}
