package core

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/noopolis/moltnet/internal/app"
	"github.com/noopolis/moltnet/internal/bridge/loop"
	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/rooms"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/internal/testutil"
	"github.com/noopolis/moltnet/internal/transport"
	"github.com/noopolis/moltnet/pkg/bridgeconfig"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// Offer a wake immediately after registration so a bypass can reach delivery.
type clockWakeService struct {
	*rooms.Service
	broker *events.Broker
}

func (s *clockWakeService) AgentConnected(ctx context.Context, agent protocol.Actor) {
	s.Service.AgentConnected(ctx, agent)
	s.broker.Publish(protocol.Event{
		ID: "evt_wake", Type: protocol.EventTypeMessageCreated, NetworkID: "local",
		Message: &protocol.Message{ID: "msg_wake", NetworkID: "local",
			From: protocol.Actor{ID: "writer"}, Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "room"},
			Parts: []protocol.Part{{Kind: protocol.PartKindText, Text: "wake"}}},
	})
}

func TestControlLoopRechecksClockAfterPreflight(t *testing.T) {
	clockA := &bridgeconfig.ClockConfig{Version: bridgeconfig.TaskClockVersion, Origin: "2025-10-01T08:00:00Z"}
	clockB := *clockA
	clockB.AnchorEpochMS++
	identityA, err := clockA.Identity()
	if err != nil {
		t.Fatal(err)
	}
	for _, changed := range []*bridgeconfig.ClockConfig{&clockB, nil} {
		for _, reconnect := range []bool{false, true} {
			name := "clock_B"
			if changed == nil {
				name = "unclocked"
			}
			if reconnect {
				name += "/reconnect"
			} else {
				name += "/initial"
			}
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				preflightServer, err := app.New(app.Config{NetworkID: "local", Clock: clockA})
				if err != nil {
					t.Fatal(err)
				}
				defer preflightServer.Close()
				memory := store.NewMemoryStore()
				broker := events.NewBroker(memory)
				now, err := changed.Now(time.Now)
				if err != nil {
					t.Fatal(err)
				}
				service := &clockWakeService{rooms.NewService(rooms.ServiceConfig{Now: now, NetworkID: "local", Store: memory, Messages: memory, Broker: broker}), broker}
				defer service.Close()
				identityB, err := changed.Identity()
				if err != nil {
					t.Fatal(err)
				}
				changedHandler := transport.NewHTTPHandler(service, nil, transport.HTTPConfig{ClockIdentity: identityB})
				var attempts, initialIdentify, readiness, deliveries atomic.Int32
				changedClosed := make(chan struct{})
				dial := testutil.HTTPDialContext(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/control" {
						deliveries.Add(1)
						_, _ = w.Write([]byte(`{"message":""}`))
						cancel()
						return
					}
					if r.URL.Path != "/v1/attach" {
						preflightServer.Handler().ServeHTTP(w, r)
						return
					}
					if attempts.Add(1) == 1 && reconnect {
						// Clock A accepts the upgrade, then drops before READY, forcing
						// the real control loop to reconnect with bootstrap still pending.
						upgrader := websocket.Upgrader{}
						connection, err := upgrader.Upgrade(w, r, http.Header{bridgeconfig.TaskClockHeader: []string{identityA}})
						if err != nil {
							t.Error(err)
							return
						}
						defer connection.Close()
						_ = connection.SetReadDeadline(time.Now().Add(time.Second))
						_ = connection.WriteJSON(protocol.AttachmentFrame{Op: protocol.AttachmentOpHello, Version: protocol.AttachmentProtocolV1})
						var frame protocol.AttachmentFrame
						if err := connection.ReadJSON(&frame); err == nil && frame.Op == protocol.AttachmentOpIdentify {
							initialIdentify.Add(1)
						}
						return
					}
					defer close(changedClosed)
					defer cancel()
					changedHandler.ServeHTTP(w, r)
				}))
				priorTransport, priorDialer := http.DefaultTransport, websocket.DefaultDialer
				transport := &http.Transport{DialContext: dial}
				http.DefaultTransport = transport
				websocket.DefaultDialer = &websocket.Dialer{NetDialContext: dial}
				t.Cleanup(func() {
					http.DefaultTransport, websocket.DefaultDialer = priorTransport, priorDialer
					transport.CloseIdleConnections()
				})
				config := bridgeconfig.Config{Clock: clockA, Agent: bridgeconfig.AgentConfig{ID: "reader"},
					Moltnet: bridgeconfig.MoltnetConfig{BaseURL: "http://moltnet.test", NetworkID: "local"},
					Runtime: bridgeconfig.RuntimeConfig{ControlURL: "http://moltnet.test/control"}, Rooms: []bridgeconfig.RoomBinding{{ID: "room", Wake: bridgeconfig.WakeAll}}}
				if err := Preflight(ctx, config); err != nil {
					t.Fatal(err)
				}
				ctx = loop.WithAttachmentReady(ctx, func() { readiness.Add(1) })
				if err := loop.RunControlLoop(ctx, config); err != nil {
					t.Fatal(err)
				}
				select {
				case <-changedClosed:
				case <-time.After(time.Second):
					t.Fatal("changed server's attachment was not closed")
				}
				wantAttempts := int32(1)
				if reconnect {
					wantAttempts++
					if initialIdentify.Load() != 1 {
						t.Fatal("matching first connection did not identify")
					}
				}
				registered, err := memory.ListRegisteredAgentsContext(t.Context())
				if err != nil || len(registered) != 0 || readiness.Load() != 0 || deliveries.Load() != 0 || attempts.Load() != wantAttempts {
					t.Fatalf("clock change allowed registration/readiness/bootstrap/wake: registrations=%d ready=%d deliveries=%d attempts=%d err=%v", len(registered), readiness.Load(), deliveries.Load(), attempts.Load(), err)
				}
			})
		}
	}
}
