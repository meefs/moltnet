package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestRoomActivityFractionalPrecisionAndRestore(t *testing.T) {
	for _, kind := range []string{"memory", "sqlite", "file"} {
		t.Run(kind, func(t *testing.T) {
			var fixture interface {
				RoomStore
				MessageStore
			}
			path := filepath.Join(t.TempDir(), "state.json")
			switch kind {
			case "memory":
				fixture = NewMemoryStore()
			case "sqlite":
				s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				fixture = s
			case "file":
				s, err := NewFileStore(path)
				if err != nil {
					t.Fatal(err)
				}
				fixture = s
			}
			if err := fixture.CreateRoom(protocol.Room{ID: "busy"}); err != nil {
				t.Fatal(err)
			}
			base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
			latest := base
			for i, offset := range []time.Duration{110 * time.Millisecond, 100 * time.Millisecond, 0, 999999999, 999 * time.Millisecond} {
				at := base.Add(offset)
				if at.After(latest) {
					latest = at
				}
				if err := fixture.AppendMessage(protocol.Message{ID: fmt.Sprintf("m%d", i), NetworkID: "local", CreatedAt: at, Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "busy"}}); err != nil {
					t.Fatal(err)
				}
				if kind == "file" {
					reopened, err := NewFileStore(path)
					if err != nil {
						t.Fatal(err)
					}
					fixture = reopened
				}
				rooms, err := fixture.ListRooms()
				if err != nil {
					t.Fatal(err)
				}
				if len(rooms) != 1 || rooms[0].LastMessageAt == nil || !rooms[0].LastMessageAt.Equal(latest) {
					t.Fatalf("after offset %s: activity=%+v, want %s", offset, rooms, latest.Format(time.RFC3339Nano))
				}
			}
		})
	}
}
