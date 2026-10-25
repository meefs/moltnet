package rooms_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/netkeys"
	"github.com/noopolis/moltnet/internal/pairings"
	"github.com/noopolis/moltnet/internal/rooms"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/internal/transport"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// startUnboundInviter models the real inviting side: `pair invite` mints a
// pair token with no network binding and a pairing with no remote network
// id, and require_pair_network_binding stays off (the default).
func startUnboundInviter(t *testing.T, pinsDir string, messages *store.MemoryStore) (*rooms.Service, string) {
	t.Helper()
	service := rooms.NewService(rooms.ServiceConfig{
		NetworkID: "peer", Store: messages, Messages: messages, Broker: events.NewBroker(),
		Pairings:      []protocol.Pairing{{ID: "origin", RemoteBaseURL: "http://127.0.0.1:1"}},
		PairingClient: pairings.NewClient(), KeyPins: netkeys.OpenPinStore(pinsDir),
	})
	t.Cleanup(func() { _ = service.Close() })
	policy, err := authn.NewPolicy(authn.Config{Mode: authn.ModeBearer, Tokens: []authn.TokenConfig{{
		ID: "origin", Value: "pair-secret", Scopes: []authn.Scope{authn.ScopePair},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(transport.NewHTTPHandler(service, policy))
	t.Cleanup(server.Close)
	return service, server.URL
}

func TestRestartCannotRebindPairingToAnotherNetwork(t *testing.T) {
	pinsDir := t.TempDir()
	joinerKey := newTestIdentity(t)
	memory := store.NewMemoryStore()
	first, firstURL := startUnboundInviter(t, pinsDir, memory)
	createDeliveryRoomWithMembers(t, first, "origin:writer", "victim:writer")
	if status, body := deliveryPost(t, firstURL, "pair-secret", signedRequest(t, joinerKey, "hello", "first contact")); status != http.StatusAccepted {
		t.Fatalf("first contact: %d %s", status, body)
	}
	if pin, ok, err := netkeys.OpenPinStore(pinsDir).Lookup("origin"); err != nil || !ok || pin.SourceID != "origin" {
		t.Fatalf("first-contact pin = %#v, %v, %v", pin, ok, err)
	}
	_ = first.Close()

	// Restart: the process-local learned binding is gone. The same pairing
	// credential now claims a different network that has no pin yet.
	_, restartedURL := startUnboundInviter(t, pinsDir, memory)
	attackerKey := newTestIdentity(t)
	impostor := signedRequestAs(t, attackerKey, "victim", "forged", "I am victim")
	if status, body := deliveryPost(t, restartedURL, "pair-secret", impostor); status != http.StatusForbidden {
		t.Fatalf("restarted inviter accepted a new identity for the pairing: %d %s", status, body)
	}
	if _, ok, _ := netkeys.OpenPinStore(pinsDir).Lookup("victim"); ok {
		t.Fatal("restart hole pinned an attacker key as verified")
	}
	// The real joiner still gets through after the restart.
	if status, body := deliveryPost(t, restartedURL, "pair-secret", signedRequest(t, joinerKey, "again", "after restart")); status != http.StatusAccepted {
		t.Fatalf("joiner after restart: %d %s", status, body)
	}
}

func createDeliveryRoomWithMembers(t *testing.T, service *rooms.Service, members ...string) {
	t.Helper()
	if _, err := service.CreateRoom(protocol.CreateRoomRequest{
		ID: "research", Members: members, Federation: &protocol.RoomFederation{Mode: protocol.RoomFederationAll},
	}); err != nil {
		t.Fatal(err)
	}
}

func signedRequestAs(t *testing.T, identity *netkeys.Identity, network, messageID, text string) protocol.SendMessageRequest {
	t.Helper()
	request := signedRequest(t, identity, messageID, text)
	request.Origin.NetworkID, request.From.NetworkID = network, network
	request.Envelope = signEnvelopeFor(t, identity, request)
	return request
}

// An unbound inviter credential (the shape `pair invite` mints) claims
// another network's id on its very first message. It must not be able to
// squat that network: never a verified pin, never a pin for a refused
// message, and the real network still gets through over its own pairing.
func TestUnboundCredentialCannotSquatAnotherNetwork(t *testing.T) {
	memory := store.NewMemoryStore()
	pins := netkeys.NewMemoryPinStore()
	service := rooms.NewService(rooms.ServiceConfig{
		NetworkID: "peer", Store: memory, Messages: memory, Broker: events.NewBroker(),
		Pairings: []protocol.Pairing{
			{ID: "attacker", RemoteBaseURL: "http://127.0.0.1:1"},
			{ID: "stranger", RemoteBaseURL: "http://127.0.0.1:1"},
			{ID: "victim", RemoteNetworkID: "victim", RemoteBaseURL: "http://127.0.0.1:1"},
		},
		PairingClient: pairings.NewClient(), KeyPins: pins,
	})
	t.Cleanup(func() { _ = service.Close() })
	createDeliveryRoomWithMembers(t, service, "victim:writer", "newcomer:writer", "origin:writer")
	policy, err := authn.NewPolicy(authn.Config{Mode: authn.ModeBearer, Tokens: []authn.TokenConfig{
		{ID: "attacker", Value: "attacker-secret", Scopes: []authn.Scope{authn.ScopePair}},
		{ID: "stranger", Value: "stranger-secret", Scopes: []authn.Scope{authn.ScopePair}},
		{ID: "victim", Value: "victim-secret", Network: "victim", Scopes: []authn.Scope{authn.ScopePair}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(transport.NewHTTPHandler(service, policy))
	t.Cleanup(server.Close)
	attackerKey, victimKey := newTestIdentity(t), newTestIdentity(t)

	// Claiming a network another pairing is config-bound to is refused, and
	// the refusal leaves no pin behind.
	if status, body := deliveryPost(t, server.URL, "attacker-secret", signedRequestAs(t, attackerKey, "victim", "forged-1", "I am victim")); status != http.StatusForbidden {
		t.Fatalf("unbound credential claimed a bound network: %d %s", status, body)
	}
	if _, ok, _ := pins.Lookup("victim"); ok {
		t.Fatal("a refused message left a pin")
	}
	// A refused-by-policy message pins nothing either.
	denied := signedRequestAs(t, attackerKey, "outsider", "denied-1", "not a member")
	if status, _ := deliveryPost(t, server.URL, "attacker-secret", denied); status != http.StatusForbidden {
		t.Fatalf("non-member write was not refused: %d", status)
	}
	if _, ok, _ := pins.Lookup("outsider"); ok {
		t.Fatal("pinned before authorization")
	}

	// First contact over an unbound credential pins only as unconfirmed.
	if status, body := deliveryPost(t, server.URL, "stranger-secret", signedRequestAs(t, attackerKey, "newcomer", "tofu-1", "hello")); status != http.StatusAccepted {
		t.Fatalf("first contact: %d %s", status, body)
	}
	if pin, ok, _ := pins.Lookup("newcomer"); !ok || pin.Trust != protocol.KeyTrustUnconfirmed {
		t.Fatalf("unbound first contact pin = %#v", pin)
	}
	page, _ := service.ListRoomMessages("research", "", 10)
	signatures, _ := service.MessageSignatures(context.Background(), page.Messages)
	if len(page.Messages) != 1 || signatures[page.Messages[0].ID].State != protocol.SignatureStateUnconfirmed {
		t.Fatalf("unbound first contact must not read as verified: %#v", signatures)
	}

	// The real victim, over its own bound pairing, is verified.
	if status, body := deliveryPost(t, server.URL, "victim-secret", signedRequestAs(t, victimKey, "victim", "genuine-1", "the real one")); status != http.StatusAccepted {
		t.Fatalf("genuine victim: %d %s", status, body)
	}
	if pin, ok, _ := pins.Lookup("victim"); !ok || pin.Fingerprint != victimKey.Fingerprint() || pin.Trust != protocol.KeyTrustVerified {
		t.Fatalf("victim pin = %#v", pin)
	}
}

// A verified pin replaces an unconfirmed one: a squatter who claimed a
// name first over an unbound credential loses it to the real network.
func TestVerifiedFirstContactReplacesUnconfirmedSquat(t *testing.T) {
	memory := store.NewMemoryStore()
	pins := netkeys.NewMemoryPinStore()
	squatter := newTestIdentity(t)
	if _, err := pins.PinKey(netkeys.Pin{NetworkID: "origin", Fingerprint: squatter.Fingerprint(), PublicKey: squatter.Public().PublicKey,
		Trust: protocol.KeyTrustUnconfirmed, Source: netkeys.PinSourcePairing, SourceID: "elsewhere"}); err != nil {
		t.Fatal(err)
	}
	peer := newSignedPeerWithStore(t, pins, memory) // its credential is config-bound to "origin"
	genuine := newTestIdentity(t)
	if status, body := deliveryPost(t, peer.pairURL, "pair-secret", signedRequest(t, genuine, "real-1", "real")); status != http.StatusAccepted {
		t.Fatalf("bound first contact over an unconfirmed squat: %d %s", status, body)
	}
	if pin, _, _ := pins.Lookup("origin"); pin.Fingerprint != genuine.Fingerprint() || pin.Trust != protocol.KeyTrustVerified {
		t.Fatalf("pin after verified contact = %#v", pin)
	}
	if status, _ := deliveryPost(t, peer.pairURL, "pair-secret", signedRequest(t, squatter, "squat-2", "me again")); status != http.StatusForbidden {
		t.Fatalf("squatter still accepted: %d", status)
	}
}
