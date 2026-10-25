package rooms_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/netkeys"
	"github.com/noopolis/moltnet/internal/pairings"
	"github.com/noopolis/moltnet/internal/rooms"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/internal/transport"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// signedPeer is the receiving network "peer" of the two-network pairing
// helpers, with a trust store and an operator-readable HTTP surface.
type signedPeer struct {
	service *rooms.Service
	pins    *netkeys.PinStore
	memory  *store.MemoryStore
	pairURL string // pair-token surface the origin relays into
	readURL string // open surface for asserting the HTTP message wire
}

func newSignedPeer(t *testing.T, pins *netkeys.PinStore) signedPeer {
	t.Helper()
	memory := store.NewMemoryStore()
	identity, err := netkeys.NewEphemeralIdentity()
	if err != nil {
		t.Fatal(err)
	}
	service := rooms.NewService(rooms.ServiceConfig{
		NetworkID: "peer", Store: memory, Messages: memory, Broker: events.NewBroker(),
		RequirePairNetworkBinding: true,
		Pairings:                  []protocol.Pairing{{ID: "origin", RemoteNetworkID: "origin", RemoteBaseURL: "http://127.0.0.1:1"}},
		PairingClient:             pairings.NewClient(),
		Identity:                  identity, KeyPins: pins,
	})
	t.Cleanup(func() { _ = service.Close() })
	createDeliveryRoom(t, service)
	pairPolicy, err := authn.NewPolicy(authn.Config{Mode: authn.ModeBearer, Tokens: []authn.TokenConfig{{
		ID: "origin", Value: "pair-secret", Network: "origin", Scopes: []authn.Scope{authn.ScopePair},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	openPolicy, err := authn.NewPolicy(authn.Config{Mode: authn.ModeNone})
	if err != nil {
		t.Fatal(err)
	}
	pairServer := httptest.NewServer(transport.NewHTTPHandler(service, pairPolicy))
	readServer := httptest.NewServer(transport.NewHTTPHandler(service, openPolicy))
	t.Cleanup(pairServer.Close)
	t.Cleanup(readServer.Close)
	return signedPeer{service: service, pins: pins, memory: memory, pairURL: pairServer.URL, readURL: readServer.URL}
}

// newSignedOrigin is the sending network "origin"; a nil identity models a
// legacy network that predates keys.
func newSignedOrigin(t *testing.T, identity *netkeys.Identity, peerURL string) (*rooms.Service, *httptest.Server) {
	t.Helper()
	memory := store.NewMemoryStore()
	service := rooms.NewService(rooms.ServiceConfig{
		NetworkID: "origin", Store: memory, Messages: memory, Broker: events.NewBroker(),
		Pairings:      []protocol.Pairing{{ID: "peer", RemoteNetworkID: "peer", RemoteBaseURL: peerURL, Token: "pair-secret"}},
		PairingClient: pairings.NewClient(), Identity: identity,
		DeliveryRetryMin: 10 * time.Millisecond, DeliveryRetryMax: 30 * time.Millisecond,
		DeliveryPollInterval: 5 * time.Millisecond, DeliveryTimeout: 500 * time.Millisecond,
	})
	t.Cleanup(func() { _ = service.Close() })
	createDeliveryRoom(t, service)
	policy, err := authn.NewPolicy(authn.Config{Mode: authn.ModeNone})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(transport.NewHTTPHandler(service, policy))
	t.Cleanup(server.Close)
	service.StartPairingDelivery()
	return service, server
}

func newTestIdentity(t *testing.T) *netkeys.Identity {
	t.Helper()
	identity, err := netkeys.NewEphemeralIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

// pinAsInvite pins identity for network the way `moltnet pair <code>` does.
func pinAsInvite(t *testing.T, pins *netkeys.PinStore, network string, identity *netkeys.Identity) {
	t.Helper()
	public := identity.Public()
	if _, err := pins.PinKey(netkeys.Pin{NetworkID: network, Fingerprint: public.Fingerprint, PublicKey: public.PublicKey,
		Trust: protocol.KeyTrustVerified, Source: netkeys.PinSourceInvite, SourceID: "peer"}); err != nil {
		t.Fatal(err)
	}
}

type wireMessage struct {
	ID     string `json:"id"`
	Origin struct {
		NetworkID   string `json:"network_id"`
		MessageID   string `json:"message_id"`
		ReceivedVia string `json:"received_via"`
		Signature   string `json:"signature"`
		Fingerprint string `json:"fingerprint"`
	} `json:"origin"`
	Parts []protocol.Part `json:"parts"`
}

func wireRoomMessages(t *testing.T, baseURL string) []wireMessage {
	t.Helper()
	response, err := http.Get(baseURL + "/v1/rooms/research/messages")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var page struct {
		Messages []wireMessage `json:"messages"`
	}
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("read messages: HTTP %d, %v", response.StatusCode, err)
	}
	return page.Messages
}

// signedRequest builds the relay request origin would send for one room
// message, signed by identity. Tests mutate it to model a hostile hop.
func signedRequest(t *testing.T, identity *netkeys.Identity, messageID, text string) protocol.SendMessageRequest {
	t.Helper()
	request := protocol.SendMessageRequest{
		ID: messageID, Origin: protocol.MessageOrigin{NetworkID: "origin", MessageID: messageID},
		Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "research"},
		From:   protocol.Actor{Type: "agent", ID: "writer", NetworkID: "origin"},
		Parts:  []protocol.Part{{Kind: "text", Text: text}},
	}
	request.Envelope = signEnvelopeFor(t, identity, request)
	return request
}

func signEnvelopeFor(t *testing.T, identity *netkeys.Identity, request protocol.SendMessageRequest) *protocol.SignedEnvelope {
	t.Helper()
	envelope := protocol.EnvelopeFromRequest(request, identity.Fingerprint(), "2026-10-25T10:00:00Z")
	signed, _, err := protocol.SignEnvelope(envelope, identity.PublicKey(), identity.Sign)
	if err != nil {
		t.Fatal(err)
	}
	return &signed
}

func assertNoDelivered(t *testing.T, service *rooms.Service) {
	t.Helper()
	page, err := service.ListRoomMessages("research", "", 100)
	if err != nil || len(page.Messages) != 0 {
		t.Fatalf("expected no delivered messages: %#v, %v", page.Messages, err)
	}
}
