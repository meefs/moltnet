package pairings

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestHTTPPreservesPeerResponseStatus(t *testing.T) {
	for _, code := range []int{403, 404, 408, 409, 429, 500, 503} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
			defer server.Close()
			_, err := NewClient().RelayMessage(context.Background(), protocol.Pairing{RemoteBaseURL: server.URL}, protocol.SendMessageRequest{})
			var response *ResponseError
			if !errors.As(err, &response) || response.StatusCode() != code {
				t.Fatalf("lost status %d: %v", code, err)
			}
		})
	}
}

func TestRelayPreservesPeerResponseStatus(t *testing.T) {
	server, connected := newRelayTransportServer(t, func(relayTransportRequest) (int, []byte) {
		return http.StatusForbidden, []byte(`{"error":"private body"}`)
	})
	defer server.Close()
	relayClient, stop := startRelayTransportClient(t, server.URL)
	defer stop()
	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Fatal("relay did not connect")
	}
	waitForRelayTransportConnection(t, relayClient)
	client := NewClient()
	pairing := protocol.Pairing{ID: "peer", Relay: &protocol.PairingRelay{URL: server.URL}}
	client.RegisterRelay(pairing.ID, relayClient)
	_, err := client.RelayMessage(context.Background(), pairing, protocol.SendMessageRequest{})
	var response *ResponseError
	if !errors.As(err, &response) || response.StatusCode() != http.StatusForbidden {
		t.Fatalf("lost relay status: %v", err)
	}
}
