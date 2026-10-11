package loop

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/noopolis/moltnet/internal/testutil"
	"github.com/noopolis/moltnet/pkg/bridgeconfig"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestAttachmentClockAgreement(t *testing.T) {
	clock := &bridgeconfig.ClockConfig{Version: bridgeconfig.TaskClockVersion, Origin: "2025-10-01T08:00:00Z"}
	identity, err := clock.Identity()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		clock  *bridgeconfig.ClockConfig
		values []string
		err    string
	}{
		{"matching", clock, []string{identity}, ""},
		{"unclocked", nil, nil, ""},
		{"different", clock, []string{"different"}, "clock mismatch"},
		{"missing", clock, nil, "clock mismatch"},
		{"unexpected", nil, []string{identity}, "clock mismatch"},
		{"empty", nil, []string{""}, "invalid server clock identity header"},
		{"duplicate", clock, []string{identity, identity}, "invalid server clock identity header"},
	} {
		t.Run(test.name, func(t *testing.T) {
			identified := make(chan bool, 1)
			dial := testutil.HTTPDialContext(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upgrader := websocket.Upgrader{}
				connection, err := upgrader.Upgrade(w, r, http.Header{bridgeconfig.TaskClockHeader: test.values})
				if err != nil {
					t.Error(err)
					identified <- false
					return
				}
				defer connection.Close()
				_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
				_ = connection.WriteJSON(protocol.AttachmentFrame{Op: protocol.AttachmentOpHello, Version: protocol.AttachmentProtocolV1})
				var frame protocol.AttachmentFrame
				if err := connection.ReadJSON(&frame); err != nil {
					identified <- false
					return
				}
				identified <- frame.Op == protocol.AttachmentOpIdentify
				_ = connection.WriteJSON(protocol.AttachmentFrame{Op: protocol.AttachmentOpReady, Version: protocol.AttachmentProtocolV1, NetworkID: "local", AgentID: "reader"})
				_ = connection.WriteJSON(protocol.AttachmentFrame{Op: protocol.AttachmentOpEvent, Version: protocol.AttachmentProtocolV1, Cursor: "evt_1", Event: &protocol.Event{ID: "evt_1"}})
				_ = connection.ReadJSON(&frame)
				_ = connection.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"), time.Now().Add(time.Second))
			}))
			config := bridgeconfig.Config{Clock: test.clock, Agent: bridgeconfig.AgentConfig{ID: "reader"}, Moltnet: bridgeconfig.MoltnetConfig{BaseURL: "http://moltnet.test", NetworkID: "local"}}
			ready, bootstrap, deliveries := 0, 0, 0
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			ctx = WithAttachmentReady(ctx, func() { ready++ })
			client := NewMoltnetClient(config)
			client.dialer = &websocket.Dialer{NetDialContext: dial}
			err := client.StreamEventsReady(ctx, config, func() { bootstrap++ }, func(protocol.Event) error { deliveries++; return nil })
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), test.err) {
					t.Errorf("expected %q, got %v", test.err, err)
				}
				if <-identified || ready != 0 || bootstrap != 0 || deliveries != 0 {
					t.Fatalf("mismatch allowed identify/readiness/bootstrap/delivery: %d/%d/%d", ready, bootstrap, deliveries)
				}
			} else if err != nil || !<-identified || ready != 1 || bootstrap != 1 || deliveries != 1 {
				t.Fatalf("matching connection: err=%v, readiness/bootstrap/delivery=%d/%d/%d", err, ready, bootstrap, deliveries)
			}
		})
	}
}
