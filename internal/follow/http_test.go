package follow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func testNetwork() protocol.Network {
	return protocol.Network{ID: "origin", Protocols: protocol.NetworkProtocols{HTTP: []string{protocol.HTTPProtocolV1}}, Capabilities: protocol.NetworkCapabilities{PublicRead: true, MessagePagination: "cursor", EventStream: "sse"}}
}

func TestFollowRefusesRedirectsForDiscoveryAndSSE(t *testing.T) {
	for _, path := range []string{"/v1/network", "/v1/events/stream"} {
		t.Run(path, func(t *testing.T) {
			var requests atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.Write([]byte("{}")) }))
			defer target.Close()
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == path {
					http.Redirect(w, r, target.URL, http.StatusFound)
					return
				}
				if r.URL.Path == "/v1/network" {
					json.NewEncoder(w).Encode(testNetwork())
					return
				}
				json.NewEncoder(w).Encode(protocol.RoomPage{})
			}))
			defer origin.Close()
			w := &worker{config: protocol.Follow{ID: "news", RemoteBaseURL: origin.URL}.WithDefaults(), client: newHTTPClient(), sink: &checkpointSink{values: map[string]Checkpoint{}}, rooms: map[string]protocol.Room{}}
			defer w.client.CloseIdleConnections()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			err := w.session(ctx)
			if err == nil || !strings.Contains(err.Error(), "302") {
				t.Fatalf("redirect refusal: %v", err)
			}
			if requests.Load() != 0 {
				t.Fatalf("redirect destination received %d requests", requests.Load())
			}
		})
	}
}

func TestFollowConnectionSetupTimeouts(t *testing.T) {
	client := newHTTPClient()
	defer client.CloseIdleConnections()
	transport := client.Transport.(*http.Transport)
	if transport.DialContext == nil || transport.TLSHandshakeTimeout <= 0 || transport.ResponseHeaderTimeout <= 0 {
		t.Fatal("JSON/SSE client lacks bounded connection setup")
	}
}
