package store_test

import (
	"database/sql"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/rooms"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/bridgeconfig"
	"github.com/noopolis/moltnet/pkg/protocol"
)

type clockStore interface {
	store.RoomStore
	store.MessageStore
	store.AttachmentDeliveryStore
}

func TestClockStoreRoundTripPreservesHistory(t *testing.T) {
	for _, kind := range []string{"json", "sqlite"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state."+kind)
			open := func() clockStore {
				t.Helper()
				if kind == "json" {
					s, err := store.NewFileStore(path)
					if err != nil {
						t.Fatal(err)
					}
					return s
				}
				s, err := store.NewSQLiteStore(path)
				if err != nil {
					t.Fatal(err)
				}
				return s
			}
			closeStore := func(s clockStore) {
				t.Helper()
				if closer, ok := s.(io.Closer); ok {
					if err := closer.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
			origin := time.Date(2025, 10, 1, 8, 0, 0, 0, time.UTC)
			old := origin.Add(-24 * time.Hour)
			fixture := open()
			if err := fixture.CreateRoom(protocol.Room{ID: "research", NetworkID: "local", CreatedAt: old, Members: []string{"writer"}}); err != nil {
				t.Fatal(err)
			}
			message := protocol.Message{ID: "old", NetworkID: "local", Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "research"}, From: protocol.Actor{Type: "agent", ID: "writer"}, Parts: []protocol.Part{{Kind: protocol.PartKindText, Text: "recorded history"}}, CreatedAt: old}
			if err := fixture.AppendMessage(message); err != nil {
				t.Fatal(err)
			}
			closeStore(fixture)

			real := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
			config := bridgeconfig.ClockConfig{Version: bridgeconfig.TaskClockVersion, Origin: origin.Format(time.RFC3339), AnchorEpochMS: real.UnixMilli()}
			real = real.Add(1234 * time.Millisecond)
			now, err := config.Now(func() time.Time { return real })
			if err != nil {
				t.Fatal(err)
			}
			want := origin.Add(1234 * time.Millisecond)
			restored := open()
			broker := events.NewBroker(restored)
			service := rooms.NewService(rooms.ServiceConfig{Now: now, NetworkID: "local", Store: restored, Messages: restored, Broker: broker})
			defer service.Close()
			if _, err := service.RegisterAgentContext(t.Context(), protocol.RegisterAgentRequest{RequestedAgentID: "reader"}); err != nil {
				t.Fatal(err)
			}
			// Reconciliation must retain the old room's creation time as well.
			room, err := service.ReconcileRoomContext(t.Context(), protocol.CreateRoomRequest{ID: "research", Members: []string{"writer"}})
			if err != nil || !room.CreatedAt.Equal(old) {
				t.Fatalf("restored room: %#v, %v", room, err)
			}
			if _, err := broker.SubscribeAgent(t.Context(), "reader"); err != nil {
				t.Fatal(err)
			}
			if kind == "sqlite" {
				assertClockSQLTime(t, path, "SELECT updated_at FROM attachment_delivery_cursors WHERE agent_id = 'reader'", want)
			}
			accepted, err := service.SendMessage(protocol.SendMessageRequest{ID: "new", Target: message.Target, From: message.From, Parts: message.Parts})
			if err != nil {
				t.Fatal(err)
			}
			closeStore(restored)

			again := open()
			defer closeStore(again)
			replayBroker := events.NewBroker(again)
			replayService := rooms.NewService(rooms.ServiceConfig{Now: now, NetworkID: "local", Store: again, Messages: again, Broker: replayBroker})
			defer replayService.Close()
			page, err := again.ListRoomMessages("research", "", 10)
			if err != nil || len(page.Messages) != 2 {
				t.Fatalf("restored history: %#v, %v", page, err)
			}
			for _, m := range page.Messages {
				expected := want
				if m.ID == "old" {
					expected = old
				} else if m.ID != "new" {
					t.Fatalf("unexpected message %q", m.ID)
				}
				if !m.CreatedAt.Equal(expected) {
					t.Fatalf("%s timestamp: %s, want %s", m.ID, m.CreatedAt, expected)
				}
			}
			replay, err := again.PrepareAttachmentDeliveryContext(t.Context(), "reader")
			if err != nil || len(replay) != 1 {
				t.Fatalf("delivery replay: %#v %v", replay, err)
			}
			if !replay[0].CreatedAt.Equal(want) || replay[0].Message == nil || !replay[0].Message.CreatedAt.Equal(want) {
				t.Fatalf("delivery timestamps: %#v", replay)
			}
			real = real.Add(2 * time.Second)
			if err := replayBroker.AcknowledgeAgent(t.Context(), "reader", accepted.EventID); err != nil {
				t.Fatal(err)
			}
			if kind == "sqlite" {
				assertClockSQLTime(t, path, "SELECT updated_at FROM attachment_delivery_cursors WHERE agent_id = 'reader'", want.Add(2*time.Second))
			}
			if _, err := replayService.RemoveRoomContext(t.Context(), "research"); err != nil {
				t.Fatal(err)
			}
			if kind == "sqlite" {
				assertClockSQLTime(t, path, "SELECT deleted_at FROM rooms WHERE id = 'research'", want.Add(2*time.Second))
			}
		})
	}
}

func assertClockSQLTime(t *testing.T, path, query string, want time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var raw string
	if err := db.QueryRow(query).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	got, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || !got.Equal(want) {
		t.Fatalf("%s: got %s, want %s (%v)", query, raw, want, err)
	}
}
