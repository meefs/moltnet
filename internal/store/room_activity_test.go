package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestRoomLastMessageAt(t *testing.T) {
	for _, kind := range []string{"memory", "sqlite", "file"} {
		t.Run(kind, func(t *testing.T) {
			var fixture interface {
				RoomStore
				MessageStore
			}
			switch kind {
			case "memory":
				fixture = NewMemoryStore()
			case "file":
				s, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
				if err != nil {
					t.Fatal(err)
				}
				fixture = s
			case "sqlite":
				s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				fixture = s
			}
			for _, id := range []string{"busy", "empty"} {
				if err := fixture.CreateRoom(protocol.Room{ID: id, Name: id, Members: []string{"a", "b"}}); err != nil {
					t.Fatal(err)
				}
			}
			latest := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
			for i, at := range []time.Time{latest.Add(-time.Hour), latest, latest.Add(-2 * time.Hour)} {
				target := protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "busy"}
				if i == 1 {
					target.Kind = protocol.TargetKindThread
					target.ThreadID = "thread"
				}
				msg := protocol.Message{ID: []string{"first", "reply", "old"}[i], NetworkID: "local", Target: target, CreatedAt: at}
				if err := fixture.AppendMessage(msg); err != nil {
					t.Fatal(err)
				}
			}
			rooms, err := fixture.ListRooms()
			if err != nil {
				t.Fatal(err)
			}
			if len(rooms) != 2 || rooms[0].LastMessageAt == nil || !rooms[0].LastMessageAt.Equal(latest) || rooms[1].LastMessageAt != nil {
				t.Fatalf("unexpected room activity: %+v", rooms)
			}
		})
	}
}
