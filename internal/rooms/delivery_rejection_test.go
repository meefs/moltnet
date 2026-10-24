package rooms_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/noopolis/moltnet/internal/rooms"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestDisabledPeerDMKeepsFIFOForCompatibilityRetry(t *testing.T) {
	peer, receive, _ := deliveryReceiverWithStore(t, true)
	server := httptest.NewServer(receive)
	defer server.Close()
	_, origin := deliveryOrigin(t, store.NewMemoryStore(), protocol.Pairing{ID: "peer", RemoteNetworkID: "peer", RemoteBaseURL: server.URL, Token: "pair-secret"})
	status, body := deliveryPost(t, origin.URL, "", protocol.SendMessageRequest{
		Target: protocol.Target{Kind: protocol.TargetKindDM, DMID: "disabled-peer-dm", ParticipantIDs: []string{"origin:writer", "peer:reader"}},
		From:   protocol.Actor{Type: "agent", ID: "writer"}, Parts: []protocol.Part{{Kind: "text", Text: "unsupported DM"}},
	})
	if status != http.StatusAccepted {
		t.Fatalf("DM enqueue: %d %s", status, body)
	}
	postDeliveryMessage(t, origin.URL, "room-after-DM")
	waitDelivery(t, func() bool {
		state := deliveryStatus(t, origin.URL)
		return state.Pending == 2 && state.Rejected == 0 && state.LastError != ""
	})
	assertDeliveryTexts(t, peer, []string{}...)
}

func TestPeerRoom403DoesNotBlockAnotherRoom(t *testing.T) {
	peer, receive := deliveryReceiver(t)
	createOtherDeliveryRoom(t, peer)
	var mu sync.Mutex
	var routes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		routes = append(routes, r.URL.Path)
		mu.Unlock()
		if r.Method == http.MethodPost && r.URL.Path == "/v1/messages" {
			raw, _ := io.ReadAll(r.Body)
			var message protocol.SendMessageRequest
			_ = json.Unmarshal(raw, &message)
			if message.Target.RoomID == "research" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
		}
		receive.ServeHTTP(w, r)
	}))
	defer server.Close()
	service, origin := deliveryOrigin(t, store.NewMemoryStore(), protocol.Pairing{ID: "peer", RemoteNetworkID: "peer", RemoteBaseURL: server.URL, Token: "pair-secret"})
	createOtherDeliveryRoom(t, service)
	postDeliveryMessage(t, origin.URL, "forbidden-room")
	postOtherDeliveryMessage(t, origin.URL)
	waitDelivery(t, func() bool {
		state := deliveryStatus(t, origin.URL)
		return state.Pending == 0 && state.Rejected == 1 && state.LastRejectionReason == "paired network returned HTTP 403 Forbidden"
	})
	page, err := peer.ListRoomMessages("other", "", 10)
	if err != nil || len(page.Messages) != 1 {
		t.Fatalf("other room blocked: %#v %v", page, err)
	}
	mu.Lock()
	got := append([]string(nil), routes...)
	mu.Unlock()
	if !reflect.DeepEqual(got, []string{"/v1/network", "/v1/messages", "/v1/network", "/v1/messages"}) {
		t.Fatalf("each message POST must prove discovery in the same attempt: %v", got)
	}

}

func TestTransientPeerResponsesKeepFIFO(t *testing.T) {
	for _, code := range []int{408, 429, 503} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			peer, receive := deliveryReceiver(t)
			createOtherDeliveryRoom(t, peer)
			var mu sync.Mutex
			var attempts []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/v1/messages" {
					raw, _ := io.ReadAll(r.Body)
					var message protocol.SendMessageRequest
					_ = json.Unmarshal(raw, &message)
					mu.Lock()
					attempts = append(attempts, message.Target.RoomID)
					reject := len(attempts) <= 2
					mu.Unlock()
					if reject {
						w.WriteHeader(code)
						return
					}
					r.Body = io.NopCloser(bytes.NewReader(raw))
				}
				receive.ServeHTTP(w, r)
			}))
			defer server.Close()
			service, origin := deliveryOrigin(t, store.NewMemoryStore(), protocol.Pairing{ID: "peer", RemoteNetworkID: "peer", RemoteBaseURL: server.URL, Token: "pair-secret"})
			createOtherDeliveryRoom(t, service)
			postDeliveryMessage(t, origin.URL, "retry-head")
			postOtherDeliveryMessage(t, origin.URL)
			waitDelivery(t, func() bool { return deliveryStatus(t, origin.URL).Pending == 0 })
			mu.Lock()
			got := append([]string(nil), attempts...)
			mu.Unlock()
			if !reflect.DeepEqual(got, []string{"research", "research", "research", "other"}) {
				t.Fatalf("attempt order = %v", got)
			}
			if state := deliveryStatus(t, origin.URL); state.Rejected != 0 {
				t.Fatalf("transient response rejected: %#v", state)
			}
		})
	}
}

func createOtherDeliveryRoom(t *testing.T, service *rooms.Service) {
	t.Helper()
	if _, err := service.CreateRoom(protocol.CreateRoomRequest{ID: "other", Members: []string{"writer", "origin:writer"}, Federation: &protocol.RoomFederation{Mode: protocol.RoomFederationAll}}); err != nil {
		t.Fatal(err)
	}
}

func postOtherDeliveryMessage(t *testing.T, url string) {
	t.Helper()
	status, body := deliveryPost(t, url, "", protocol.SendMessageRequest{Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "other"}, From: protocol.Actor{Type: "agent", ID: "writer"}, Parts: []protocol.Part{{Kind: "text", Text: "other-room"}}})
	if status != http.StatusAccepted {
		t.Fatalf("other enqueue: %d %s", status, body)
	}
}
