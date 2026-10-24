package rooms_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/pairings"
	"github.com/noopolis/moltnet/internal/rooms"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/internal/transport"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestPairingDeliveryPeerDownDrainsInOrder(t *testing.T) {
	peer, handler := deliveryReceiver(t)
	url, start := stoppedDeliveryPeer(t, handler)
	origin, server := deliveryOrigin(t, store.NewMemoryStore(), protocol.Pairing{ID: "peer", RemoteNetworkID: "peer", RemoteBaseURL: url, Token: "pair-secret"})
	for i := 0; i < 4; i++ {
		postDeliveryMessage(t, server.URL, fmt.Sprintf("ordered-%d", i))
	}
	waitDelivery(t, func() bool {
		return deliveryStatus(t, server.URL).Pending == 4 && deliveryStatus(t, server.URL).LastError != ""
	})
	start()
	waitDelivery(t, func() bool { return deliveryStatus(t, server.URL).Pending == 0 })
	assertDeliveryTexts(t, peer, "ordered-0", "ordered-1", "ordered-2", "ordered-3")
	local, err := origin.ListRoomMessages("research", "", 100)
	if err != nil || len(local.Messages) != 4 {
		t.Fatalf("origin history changed through relay: %#v, %v", local, err)
	}
}

func TestPairingDeliveryLostAcknowledgmentRetriesOnce(t *testing.T) {
	peer, receive := deliveryReceiver(t)
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			receive.ServeHTTP(w, r)
			return
		}
		if posts.Add(1) == 1 {
			accepted := httptest.NewRecorder()
			receive.ServeHTTP(accepted, r)
			if accepted.Code != http.StatusAccepted {
				t.Errorf("first receive status = %d: %s", accepted.Code, accepted.Body.String())
			}
			<-r.Context().Done()
			return
		}
		receive.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	_, origin := deliveryOrigin(t, store.NewMemoryStore(), protocol.Pairing{ID: "peer", RemoteNetworkID: "peer", RemoteBaseURL: server.URL, Token: "pair-secret"})
	postDeliveryMessage(t, origin.URL, "accepted-before-timeout")
	waitDelivery(t, func() bool { return posts.Load() >= 2 && deliveryStatus(t, origin.URL).Pending == 0 })
	assertDeliveryTexts(t, peer, "accepted-before-timeout")
}

func TestPairingDeliveryOverflowDropsOldestAndCounts(t *testing.T) {
	peer, handler := deliveryReceiver(t)
	url, start := stoppedDeliveryPeer(t, handler)
	_, origin := deliveryOrigin(t, store.NewMemoryStore(), protocol.Pairing{
		ID: "peer", RemoteNetworkID: "peer", RemoteBaseURL: url, Token: "pair-secret",
		DeliveryLimits: &protocol.PairingDeliveryLimits{MaxPending: 2},
	})
	for i := 0; i < 4; i++ {
		postDeliveryMessage(t, origin.URL, fmt.Sprintf("bounded-%d", i))
	}
	waitDelivery(t, func() bool {
		state := deliveryStatus(t, origin.URL)
		return state.Pending == 2 && state.Dropped == 2
	})
	start()
	waitDelivery(t, func() bool { return deliveryStatus(t, origin.URL).Pending == 0 })
	assertDeliveryTexts(t, peer, "bounded-2", "bounded-3")
	if state := deliveryStatus(t, origin.URL); state.Dropped != 2 {
		t.Fatalf("drop counter lost after draining: %#v", state)
	}
}

func TestPairingDeliverySQLiteSenderRestart(t *testing.T) {
	peer, handler := deliveryReceiver(t)
	url, start := stoppedDeliveryPeer(t, handler)
	path := filepath.Join(t.TempDir(), "sender.db")
	first, err := store.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	pairing := protocol.Pairing{ID: "peer", RemoteNetworkID: "peer", RemoteBaseURL: url, Token: "pair-secret"}
	service, origin := deliveryOrigin(t, first, pairing)
	postDeliveryMessage(t, origin.URL, "durable-0")
	postDeliveryMessage(t, origin.URL, "durable-1")
	waitDelivery(t, func() bool {
		return deliveryStatus(t, origin.URL).Pending == 2 && deliveryStatus(t, origin.URL).LastError != ""
	})
	origin.Close()
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := store.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	_, restarted := deliveryOrigin(t, second, pairing)
	if state := deliveryStatus(t, restarted.URL); state.Pending != 2 {
		t.Fatalf("restart lost queue: %#v", state)
	}
	start()
	waitDelivery(t, func() bool { return deliveryStatus(t, restarted.URL).Pending == 0 })
	assertDeliveryTexts(t, peer, "durable-0", "durable-1")
}

func TestPairingDeliveryDedupeUsesBoundOriginTuple(t *testing.T) {
	peer, receive, memory := deliveryReceiverWithStore(t)
	server := httptest.NewServer(receive)
	t.Cleanup(server.Close)
	request := protocol.SendMessageRequest{
		ID: "attempt-1", Origin: protocol.MessageOrigin{NetworkID: "origin", MessageID: "origin-message"},
		Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "research"},
		From:   protocol.Actor{Type: "agent", ID: "writer", NetworkID: "origin"},
		Parts:  []protocol.Part{{Kind: "text", Text: "one-origin-message"}},
	}
	var first protocol.MessageAccepted
	for _, id := range []string{"attempt-1", "attempt-2", "attempt-3"} {
		request.ID = id
		status, body := deliveryPost(t, server.URL, "pair-secret", request)
		if status != http.StatusAccepted {
			t.Fatalf("duplicate receive status = %d: %s", status, body)
		}
		var accepted protocol.MessageAccepted
		if err := json.Unmarshal([]byte(body), &accepted); err != nil {
			t.Fatal(err)
		}
		if id == "attempt-1" {
			first = accepted
			if !first.Accepted || first.MessageID != "attempt-1" || first.EventID == "" {
				t.Fatalf("first acknowledgment = %#v", first)
			}
		} else if !accepted.Accepted || accepted.MessageID != first.MessageID || accepted.EventID != first.EventID {
			t.Fatalf("retry acknowledgment = %#v, want stored identity %#v", accepted, first)
		}
	}
	request.ID, request.Origin.MessageID = "attempt-1", "another-origin-message"
	if status, body := deliveryPost(t, server.URL, "pair-secret", request); status != http.StatusConflict {
		t.Fatalf("wire ID collision should be refused: %d: %s", status, body)
	}
	request.ID = "forged-attempt"
	request.Origin.NetworkID, request.From.NetworkID = "forged", "forged"
	if status, body := deliveryPost(t, server.URL, "pair-secret", request); status != http.StatusForbidden {
		t.Fatalf("credential origin binding bypassed: %d: %s", status, body)
	}
	assertDeliveryTexts(t, peer, "one-origin-message")
	if heads, err := memory.ListPairingDeliveryHeadsContext(context.Background()); err != nil || len(heads) != 0 {
		t.Fatalf("received messages queued for another hop: %#v, %v", heads, err)
	}
}

func deliveryReceiver(t *testing.T) (*rooms.Service, http.Handler) {
	t.Helper()
	service, handler, _ := deliveryReceiverWithStore(t)
	return service, handler
}

func deliveryReceiverWithStore(t *testing.T) (*rooms.Service, http.Handler, *store.MemoryStore) {
	t.Helper()
	memory := store.NewMemoryStore()
	service := rooms.NewService(rooms.ServiceConfig{
		NetworkID: "peer", Store: memory, Messages: memory, Broker: events.NewBroker(),
		RequirePairNetworkBinding: true,
		Pairings:                  []protocol.Pairing{{ID: "origin", RemoteNetworkID: "origin", RemoteBaseURL: "http://127.0.0.1:1"}},
		PairingClient:             pairings.NewClient(),
	})
	t.Cleanup(func() { _ = service.Close() })
	createDeliveryRoom(t, service)
	policy, err := authn.NewPolicy(authn.Config{Mode: authn.ModeBearer, Tokens: []authn.TokenConfig{{
		ID: "origin", Value: "pair-secret", Network: "origin", Scopes: []authn.Scope{authn.ScopePair},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return service, transport.NewHTTPHandler(service, policy), memory
}

type deliveryStorage interface {
	store.RoomStore
	store.MessageStore
}

func deliveryOrigin(t *testing.T, storage deliveryStorage, pairing protocol.Pairing) (*rooms.Service, *httptest.Server) {
	t.Helper()
	return deliveryOriginWithClient(t, storage, pairing, pairings.NewClient())
}

func deliveryOriginWithClient(t *testing.T, storage deliveryStorage, pairing protocol.Pairing, client rooms.PairingClient) (*rooms.Service, *httptest.Server) {
	t.Helper()
	service := rooms.NewService(rooms.ServiceConfig{
		NetworkID: "origin", Store: storage, Messages: storage, Broker: events.NewBroker(),
		Pairings: []protocol.Pairing{pairing}, PairingClient: client,
		DeliveryRetryMin: 10 * time.Millisecond, DeliveryRetryMax: 30 * time.Millisecond,
		DeliveryPollInterval: 5 * time.Millisecond, DeliveryTimeout: 60 * time.Millisecond,
	})
	t.Cleanup(func() { _ = service.Close() })
	if _, exists, err := storage.GetRoom("research"); err != nil {
		t.Fatal(err)
	} else if !exists {
		createDeliveryRoom(t, service)
	}
	policy, err := authn.NewPolicy(authn.Config{Mode: authn.ModeNone})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(transport.NewHTTPHandler(service, policy))
	t.Cleanup(server.Close)
	return service, server
}

func createDeliveryRoom(t *testing.T, service *rooms.Service) {
	t.Helper()
	_, err := service.CreateRoom(protocol.CreateRoomRequest{
		ID: "research", Members: []string{"writer", "origin:writer"},
		Federation: &protocol.RoomFederation{Mode: protocol.RoomFederationAll},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func stoppedDeliveryPeer(t *testing.T, handler http.Handler) (string, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	url := server.URL
	address := server.Listener.Addr().String()
	server.Close()
	return url, func() {
		t.Helper()
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		restarted := httptest.NewUnstartedServer(handler)
		_ = restarted.Listener.Close()
		restarted.Listener = listener
		restarted.Start()
		t.Cleanup(restarted.Close)
	}
}

func postDeliveryMessage(t *testing.T, url, text string) {
	t.Helper()
	status, body := deliveryPost(t, url, "", protocol.SendMessageRequest{
		Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "research"},
		From:   protocol.Actor{Type: "agent", ID: "writer"},
		Parts:  []protocol.Part{{Kind: "text", Text: text}},
	})
	if status != http.StatusAccepted {
		t.Fatalf("send status = %d: %s", status, body)
	}
}

func deliveryPost(t *testing.T, url, token string, payload protocol.SendMessageRequest) (int, string) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, url+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(raw)
}

func deliveryStatus(t *testing.T, url string) protocol.PairingDeliveryStatus {
	t.Helper()
	response, err := http.Get(url + "/v1/pairings")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var page protocol.PairingPage
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || len(page.Pairings) != 1 || page.Pairings[0].Delivery == nil {
		t.Fatalf("missing delivery status: HTTP %d %#v", response.StatusCode, page)
	}
	return *page.Pairings[0].Delivery
}

func assertDeliveryTexts(t *testing.T, service *rooms.Service, want ...string) {
	t.Helper()
	page, err := service.ListRoomMessages("research", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	texts := make([]string, 0, len(page.Messages))
	for _, message := range page.Messages {
		if len(message.Parts) != 1 {
			t.Fatalf("unexpected message: %#v", message)
		}
		texts = append(texts, message.Parts[0].Text)
	}
	if !reflect.DeepEqual(texts, want) {
		t.Fatalf("delivered texts = %q, want %q", texts, want)
	}
}

func waitDelivery(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("delivery condition did not become true")
}
