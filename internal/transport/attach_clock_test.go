package transport

import (
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/noopolis/moltnet/internal/testutil"
	"github.com/noopolis/moltnet/pkg/bridgeconfig"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestAttachmentUpgradeClockIdentity(t *testing.T) {
	clock := &bridgeconfig.ClockConfig{Version: bridgeconfig.TaskClockVersion, Origin: "2025-10-01T08:00:00Z"}
	identity, err := clock.Identity()
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{identity, ""} {
		t.Run(expected, func(t *testing.T) {
			handler := NewHTTPHandler(&fakeService{network: protocol.Network{ID: "local"}}, nil, HTTPConfig{ClockIdentity: expected})
			closed := make(chan struct{})
			dial := testutil.HTTPDialContext(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(closed)
				handler.ServeHTTP(w, r)
			}))
			// A request header must not be reflected, even on an unclocked server.
			headers := http.Header{bridgeconfig.TaskClockHeader: []string{"forged"}}
			dialer := websocket.Dialer{NetDialContext: dial}
			connection, response, err := dialer.DialContext(t.Context(), "ws://moltnet.test/v1/attach", headers)
			if err != nil {
				t.Fatal(err)
			}
			_ = connection.Close()
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("server did not observe attachment close")
			}
			if got := response.Header.Get(bridgeconfig.TaskClockHeader); got != expected {
				t.Fatalf("upgrade clock = %q, want %q", got, expected)
			}
			if expected == "" && len(response.Header.Values(bridgeconfig.TaskClockHeader)) != 0 {
				t.Fatal("unclocked upgrade must omit the clock header")
			}
		})
	}
}
