package rooms_test

import (
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
