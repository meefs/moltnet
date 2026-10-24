package rooms_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/noopolis/moltnet/internal/pairings"
	"github.com/noopolis/moltnet/internal/pairings/relay"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestPairingDeliveryDisconnectedWebSocketRetriesAfterConnect(t *testing.T) {
	peer, receive := deliveryReceiver(t)
	broker := deliveryRelayBroker(t, receive)
	client := pairings.NewClient()
	relayClient := relay.NewClient("ws"+strings.TrimPrefix(broker.URL, "http"), "relay-secret", "pair-secret", "origin")
	t.Cleanup(relayClient.Close)
	client.RegisterRelay("peer", relayClient)
	_, origin := deliveryOriginWithClient(t, store.NewMemoryStore(), protocol.Pairing{
		ID: "peer", RemoteNetworkID: "peer", Relay: &protocol.PairingRelay{URL: broker.URL},
	}, client)
	postDeliveryMessage(t, origin.URL, "queued-before-websocket")
	waitDelivery(t, func() bool {
		state := deliveryStatus(t, origin.URL)
		return state.Pending == 1 && strings.Contains(state.LastError, "relay is not connected")
	})
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- relayClient.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		relayClient.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("relay runner did not stop")
		}
	})
	waitDelivery(t, func() bool { return deliveryStatus(t, origin.URL).Pending == 0 })
	assertDeliveryTexts(t, peer, "queued-before-websocket")
}

// This broker terminates the Worker wire protocol in-process and dispatches
// requests through the actual receiving HTTP handler with frame authorization.
func deliveryRelayBroker(t *testing.T, receive http.Handler) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer relay-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil { // hello
			return
		}
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			header, body, _ := bytes.Cut(raw, []byte{'\n'})
			var frame struct {
				ID     string `json:"id"`
				Method string `json:"method"`
				Path   string `json:"path"`
				Auth   string `json:"auth"`
			}
			if err := json.Unmarshal(header, &frame); err != nil {
				t.Error(err)
				return
			}
			request := httptest.NewRequest(frame.Method, frame.Path, bytes.NewReader(body))
			request.Header.Set("Authorization", "Bearer "+frame.Auth)
			response := httptest.NewRecorder()
			receive.ServeHTTP(response, request)
			responseHeader, err := json.Marshal(struct {
				Type   string `json:"t"`
				ID     string `json:"id"`
				Status int    `json:"status"`
			}{Type: "res", ID: frame.ID, Status: response.Code})
			if err != nil {
				t.Error(err)
				return
			}
			if err := conn.WriteMessage(websocket.BinaryMessage, append(append(responseHeader, '\n'), response.Body.Bytes()...)); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}
