package rooms_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/netkeys"
	"github.com/noopolis/moltnet/internal/pairings"
	"github.com/noopolis/moltnet/internal/rooms"
	"github.com/noopolis/moltnet/internal/transport"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// hubNet is one network of the three-network hub topology: home invited
// alpha and beta (so home's links are unbound, inviter-side credentials),
// and alpha and beta each joined home (bound, with home's key pinned from
// the invite). Alpha and beta are not paired with each other.
type hubNet struct {
	id       string
	service  *rooms.Service
	identity *netkeys.Identity
	pins     *netkeys.PinStore
	store    backendStore
	url      string
	handler  *atomic.Value
	tokens   []authn.TokenConfig
	pairings []protocol.Pairing
}

const hubOperatorToken = "operator-secret"

type hubTopology struct {
	home, alpha, beta *hubNet
	global            string
}

func newHubNet(t *testing.T, id string, storage backendStore) *hubNet {
	t.Helper()
	handler := &atomic.Value{}
	handler.Store(http.Handler(http.NotFoundHandler()))
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		handler.Load().(http.Handler).ServeHTTP(response, request)
	}))
	t.Cleanup(server.Close)
	return &hubNet{id: id, identity: newTestIdentity(t), pins: netkeys.NewMemoryPinStore(), store: storage, url: server.URL, handler: handler,
		tokens: []authn.TokenConfig{{ID: "operator", Value: hubOperatorToken, Scopes: []authn.Scope{authn.ScopeObserve, authn.ScopeWrite, authn.ScopeAdmin}}}}
}

// start (re)creates the service over the network's durable state: store,
// pins and key survive, process-local state does not.
func (n *hubNet) start(t *testing.T) {
	t.Helper()
	n.service = rooms.NewService(rooms.ServiceConfig{
		NetworkID: n.id, Store: n.store, Messages: n.store, Broker: events.NewBroker(),
		Pairings: n.pairings, PairingClient: pairings.NewClient(), Identity: n.identity, KeyPins: n.pins,
		DeliveryRetryMin: 10 * time.Millisecond, DeliveryRetryMax: 30 * time.Millisecond,
		DeliveryPollInterval: 5 * time.Millisecond, DeliveryTimeout: time.Second,
	})
	service := n.service
	t.Cleanup(func() { _ = service.Close() })
	policy, err := authn.NewPolicy(authn.Config{Mode: authn.ModeBearer, Tokens: n.tokens})
	if err != nil {
		t.Fatal(err)
	}
	n.handler.Store(transport.NewHTTPHandler(n.service, policy))
	n.service.StartPairingDelivery()
}

func pinKey(t *testing.T, pins *netkeys.PinStore, pin netkeys.Pin) {
	t.Helper()
	if _, err := pins.PinKey(pin); err != nil {
		t.Fatal(err)
	}
}

// link pairs home (inviter) with member (joiner) as a room link.
func link(t *testing.T, home, member *hubNet) {
	t.Helper()
	secret := member.id + "-link-secret"
	homeLink, memberLink := "link-"+member.id, "link-home"
	home.pairings = append(home.pairings, protocol.Pairing{ID: homeLink, RemoteBaseURL: member.url, Token: protocol.SecretString(secret), Scope: protocol.PairingScopeRooms})
	home.tokens = append(home.tokens, authn.TokenConfig{ID: homeLink, Value: secret, Scopes: []authn.Scope{authn.ScopePair}})
	member.pairings = append(member.pairings, protocol.Pairing{ID: memberLink, RemoteNetworkID: home.id, RemoteBaseURL: home.url, Token: protocol.SecretString(secret), Scope: protocol.PairingScopeRooms})
	member.tokens = append(member.tokens, authn.TokenConfig{ID: memberLink, Value: secret, Network: home.id, Scopes: []authn.Scope{authn.ScopePair}})
	// The joiner pinned the inviter from the invite; the inviter pinned the
	// joiner at its first signed message, unconfirmed (unbound credential).
	pinKey(t, member.pins, netkeys.Pin{NetworkID: home.id, Fingerprint: home.identity.Fingerprint(), PublicKey: home.identity.Public().PublicKey,
		Trust: protocol.KeyTrustVerified, Source: netkeys.PinSourceInvite, SourceID: memberLink})
	pinKey(t, home.pins, netkeys.Pin{NetworkID: member.id, Fingerprint: member.identity.Fingerprint(), PublicKey: member.identity.Public().PublicKey,
		Trust: protocol.KeyTrustUnconfirmed, Source: netkeys.PinSourcePairing, SourceID: homeLink})
}

func newHubTopology(t *testing.T, open func(t *testing.T) backendStore) hubTopology {
	t.Helper()
	topology := hubTopology{home: newHubNet(t, "home", open(t)), alpha: newHubNet(t, "alpha", open(t)), beta: newHubNet(t, "beta", open(t))}
	link(t, topology.home, topology.alpha)
	link(t, topology.home, topology.beta)
	for _, network := range []*hubNet{topology.home, topology.alpha, topology.beta} {
		network.start(t)
	}
	if _, err := topology.home.service.CreateRoom(protocol.CreateRoomRequest{ID: "research", Members: []string{"lead"}}); err != nil {
		t.Fatal(err)
	}
	view, err := topology.home.service.CreateHubRoomContext(context.Background(), "research", "")
	if err != nil {
		t.Fatal(err)
	}
	topology.global = view.GlobalRoomID
	return topology
}

func (h hubTopology) add(t *testing.T, member *hubNet) {
	t.Helper()
	if _, err := h.home.service.AppendHubControlContext(context.Background(), rooms.HubControlRequest{
		GlobalRoomID: h.global, Kind: protocol.RoomControlAdd, PairingID: "link-" + member.id, Role: protocol.RoomRoleWriter}); err != nil {
		t.Fatalf("add %s: %v", member.id, err)
	}
}

func (h hubTopology) join(t *testing.T, member *hubNet) {
	t.Helper()
	if _, err := member.service.JoinHubRoomContext(context.Background(), rooms.HubJoinRequest{
		PairingID: "link-home", GlobalRoomID: h.global, LocalRoomID: "shared-" + member.id}); err != nil {
		t.Fatalf("%s join: %v", member.id, err)
	}
}

func (h hubTopology) control(t *testing.T, kind, network string) {
	t.Helper()
	if _, err := h.home.service.AppendHubControlContext(context.Background(), rooms.HubControlRequest{GlobalRoomID: h.global, Kind: kind, NetworkID: network}); err != nil {
		t.Fatalf("%s %s: %v", kind, network, err)
	}
}

// localRoom is the network's local room id for the hub room.
func localRoom(network *hubNet) string {
	if network.id == "home" {
		return "research"
	}
	return "shared-" + network.id
}

func (n *hubNet) post(t *testing.T, text string) {
	t.Helper()
	if _, err := n.service.SendMessage(protocol.SendMessageRequest{
		Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: localRoom(n)},
		From:   protocol.Actor{Type: "agent", ID: "lead"}, Parts: []protocol.Part{{Kind: "text", Text: text}},
	}); err != nil {
		t.Fatalf("%s post %q: %v", n.id, text, err)
	}
}

func (n *hubNet) texts(t *testing.T) []string {
	t.Helper()
	page, err := n.service.ListRoomMessages(localRoom(n), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	texts := make([]string, 0, len(page.Messages))
	for _, message := range page.Messages {
		texts = append(texts, message.Parts[0].Text)
	}
	return texts
}

func (n *hubNet) waitTexts(t *testing.T, want ...string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := n.texts(t)
		if equalStrings(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s texts = %q, want %q", n.id, got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// wire reads the room over HTTP as the operator, with signature labels.
func (n *hubNet) wire(t *testing.T) []wireMessage {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, n.url+"/v1/rooms/"+localRoom(n)+"/messages", nil)
	request.Header.Set("Authorization", "Bearer "+hubOperatorToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var page struct {
		Messages []wireMessage `json:"messages"`
	}
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("%s wire: HTTP %d %v", n.id, response.StatusCode, err)
	}
	return page.Messages
}

func (n *hubNet) delivery(t *testing.T, pairingID string) protocol.PairingDeliveryStatus {
	t.Helper()
	page, err := n.service.ListPairingsContext(context.Background(), protocol.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, pairing := range page.Pairings {
		if pairing.ID == pairingID && pairing.Delivery != nil {
			return *pairing.Delivery
		}
	}
	return protocol.PairingDeliveryStatus{}
}

// hubPost delivers a hand-built hub request to network as the peer holding
// the given link secret: how tests play a malicious home or member.
func hubPost(t *testing.T, network *hubNet, secret string, request protocol.SendMessageRequest) (int, string) {
	t.Helper()
	return deliveryPost(t, network.url, secret, request)
}

func hubEnvelope(t *testing.T, signer *netkeys.Identity, origin, global, canonicalRoom, messageID, text string, epoch, seq int64) protocol.SendMessageRequest {
	t.Helper()
	request := protocol.SendMessageRequest{
		ID: messageID, Origin: protocol.MessageOrigin{NetworkID: origin, MessageID: messageID},
		Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: canonicalRoom},
		From:   protocol.Actor{Type: "agent", ID: "lead", NetworkID: origin},
		Parts:  []protocol.Part{{Kind: "text", Text: text}},
	}
	envelope := protocol.EnvelopeFromRequest(request, signer.Fingerprint(), "2026-10-25T10:00:00Z")
	envelope.GlobalRoomID, envelope.MembershipEpoch, envelope.OriginSeq = global, &epoch, &seq
	signed, _, err := protocol.SignEnvelope(envelope, signer.PublicKey(), signer.Sign)
	if err != nil {
		t.Fatal(err)
	}
	request.Envelope = &signed
	return request
}

func eventID(t *testing.T, request protocol.SendMessageRequest) string {
	t.Helper()
	_, digest, err := protocol.ParseSignedEnvelope(*request.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func homeReceipt(t *testing.T, home *netkeys.Identity, global string, epoch, seq int64, event, prev string) *protocol.SignedRoomReceipt {
	t.Helper()
	signed, _, err := protocol.SignRoomReceipt(protocol.RoomReceipt{V: protocol.RoomReceiptVersionV1, GlobalRoomID: global,
		HubEpoch: protocol.HubEpochV1, MembershipEpoch: epoch, RoomSeq: seq, EventID: event, PrevReceiptHash: prev}, home.Sign)
	if err != nil {
		t.Fatal(err)
	}
	return &signed
}


func verifiedPin(network string, identity *netkeys.Identity) netkeys.Pin {
	return netkeys.Pin{NetworkID: network, Fingerprint: identity.Fingerprint(), PublicKey: identity.Public().PublicKey,
		Trust: protocol.KeyTrustVerified, Source: netkeys.PinSourceOperator}
}

func unconfirmedPin(network string, identity *netkeys.Identity, pairingID string) netkeys.Pin {
	return netkeys.Pin{NetworkID: network, Fingerprint: identity.Fingerprint(), PublicKey: identity.Public().PublicKey,
		Trust: protocol.KeyTrustUnconfirmed, Source: netkeys.PinSourcePairing, SourceID: pairingID}
}

func canonicalRoomOf(global string) string {
	_, room, _ := protocol.ParseGlobalRoomID(global)
	return room
}

func contextBackground() context.Context { return context.Background() }

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
