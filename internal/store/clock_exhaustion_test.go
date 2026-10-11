package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/bridgeconfig"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestClockSQLRuntimeExhaustion(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	real := time.UnixMilli(0)
	clock := bridgeconfig.ClockConfig{Version: bridgeconfig.TaskClockVersion, Origin: "9999-12-31T23:59:59.999Z", AnchorEpochMS: 0}
	now, err := clock.Now(func() time.Time { return real })
	if err != nil {
		t.Fatal(err)
	}
	s.SetNow(now)
	at, err := now()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRoomContext(t.Context(), protocol.Room{ID: "r", NetworkID: "local", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterAgentContext(t.Context(), protocol.AgentRegistration{AgentID: "reader", NetworkID: "local", ActorUID: "reader", ActorURI: "molt://local/agents/reader", CredentialKey: "reader", CreatedAt: at, UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareAttachmentDeliveryContext(t.Context(), "reader"); err != nil {
		t.Fatal(err)
	}
	real = real.Add(time.Millisecond)
	_, err = s.PrepareAttachmentDeliveryContext(t.Context(), "new-reader")
	for _, err := range []error{err, s.AcknowledgeAttachmentDeliveryContext(t.Context(), "reader", "event"), s.RemoveRoomContext(t.Context(), "r")} {
		if err == nil || !strings.Contains(err.Error(), "clock result is outside") {
			t.Fatalf("expected clock exhaustion, got %v", err)
		}
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM attachment_delivery_cursors").Scan(&count); err != nil || count != 1 {
		t.Fatalf("failed prepare changed cursors: count=%d, %v", count, err)
	}
	if _, ok, err := s.GetRoomContext(t.Context(), "r"); err != nil || !ok {
		t.Fatalf("failed removal changed room: %t, %v", ok, err)
	}
}
