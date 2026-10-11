package transport

import (
	"context"
	"database/sql"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/rooms"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/internal/testutil"
	"github.com/noopolis/moltnet/pkg/bridgeconfig"
	"github.com/noopolis/moltnet/pkg/protocol"
)

type expiringRegistrationStore struct {
	*store.SQLStore
	afterCommit func()
}

func (s *expiringRegistrationStore) RegisterAgentContext(ctx context.Context, registration protocol.AgentRegistration) (store.RegistrationResult, error) {
	result, err := s.SQLStore.RegisterAgentContext(ctx, registration)
	if err == nil {
		s.afterCommit()
	}
	return result, err
}

func TestAttachmentRegistrationClockExhaustionAfterCommit(t *testing.T) {
	for _, bound := range []struct {
		origin string
		step   int64
	}{
		{"9999-12-31T23:59:59.999Z", 1},
		{"0001-01-01T00:00:00.001Z", -1},
	} {
		for _, before := range []bool{false, true} {
			name := bound.origin + "/after_commit"
			if before {
				name = bound.origin + "/before_registration"
			}
			t.Run(name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "store.db")
				sqlStore, err := store.NewSQLiteStore(path)
				if err != nil {
					t.Fatal(err)
				}
				defer sqlStore.Close()
				var real atomic.Int64
				clock := &bridgeconfig.ClockConfig{Version: bridgeconfig.TaskClockVersion, Origin: bound.origin}
				now, err := clock.Now(func() time.Time { return time.UnixMilli(real.Load()) })
				if err != nil {
					t.Fatal(err)
				}
				var committed atomic.Bool
				s := &expiringRegistrationStore{SQLStore: sqlStore, afterCommit: func() {
					committed.Store(true)
					// The SQL registration has committed; baseline initialization
					// has not started. Every new clock read now fails.
					real.Store(bound.step)
				}}
				service := rooms.NewService(rooms.ServiceConfig{Now: now, NetworkID: "local", Store: s, Messages: s, Broker: events.NewBroker(s)})
				defer service.Close()
				policy, err := authn.NewPolicy(authn.Config{Mode: authn.ModeOpen})
				if err != nil {
					t.Fatal(err)
				}
				if before {
					real.Store(bound.step)
				}
				handler := NewHTTPHandler(service, policy)
				closed := make(chan struct{})
				dial := testutil.HTTPDialContext(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer close(closed)
					handler.ServeHTTP(w, r)
				}))
				dialer := websocket.Dialer{NetDialContext: dial}
				connection, _, err := dialer.DialContext(t.Context(), "ws://moltnet.test/v1/attach", nil)
				if err != nil {
					t.Fatal(err)
				}
				defer connection.Close()
				_ = connection.SetReadDeadline(time.Now().Add(time.Second))
				var hello, reply protocol.AttachmentFrame
				if err := connection.ReadJSON(&hello); err != nil || hello.Op != protocol.AttachmentOpHello {
					t.Fatalf("HELLO: %s, %v", hello.Op, err)
				}
				if err := connection.WriteJSON(protocol.AttachmentFrame{Op: protocol.AttachmentOpIdentify, Version: protocol.AttachmentProtocolV1, NetworkID: "local", Agent: &protocol.Actor{ID: "reader"}}); err != nil {
					t.Fatal(err)
				}
				if err := connection.ReadJSON(&reply); err != nil {
					t.Fatal(err)
				}
				_ = connection.Close()
				select {
				case <-closed:
				case <-time.After(time.Second):
					t.Fatal("attachment did not close")
				}
				registered, exists, err := s.GetRegisteredAgentContext(t.Context(), "reader")
				if err != nil {
					t.Fatal(err)
				}
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if before {
					if reply.Op != protocol.AttachmentOpError || !strings.Contains(reply.Error, "clock result is outside") || exists || committed.Load() {
						t.Fatalf("pre-exhausted attachment: op=%s error=%q registered=%t committed=%t", reply.Op, reply.Error, exists, committed.Load())
					}
					var cursors int
					if err := db.QueryRow("SELECT COUNT(*) FROM attachment_delivery_cursors").Scan(&cursors); err != nil || cursors != 0 {
						t.Fatalf("failed registration left cursors: %d, %v", cursors, err)
					}
					return
				}
				if reply.Op != protocol.AttachmentOpReady || !exists || !committed.Load() || !strings.HasPrefix(reply.AgentToken, authn.AgentTokenPrefix) {
					t.Fatalf("claim lost its token: op=%s error=%q registered=%t committed=%t token_present=%t", reply.Op, reply.Error, exists, committed.Load(), reply.AgentToken != "")
				}
				claims, authenticated, err := service.AuthenticateAgentTokenContext(t.Context(), reply.AgentToken)
				if err != nil || !authenticated || !claims.AllowsAgent("reader") || registered.CredentialKey != authn.AgentTokenCredentialKey(reply.AgentToken) {
					t.Fatalf("delivered token does not authenticate persisted claim: %t, %v", authenticated, err)
				}
				var cursorTime string
				if err := db.QueryRow("SELECT updated_at FROM attachment_delivery_cursors WHERE agent_id = 'reader'").Scan(&cursorTime); err != nil {
					t.Fatal(err)
				}
				at, err := time.Parse(time.RFC3339Nano, bound.origin)
				if err != nil || !registered.CreatedAt.Equal(at) || !registered.UpdatedAt.Equal(at) || cursorTime != at.Format(time.RFC3339Nano) {
					t.Fatalf("registration and cursor did not retain operation timestamp: created=%v updated=%v cursor=%s, %v", registered.CreatedAt, registered.UpdatedAt, cursorTime, err)
				}
			})
		}
	}
}
