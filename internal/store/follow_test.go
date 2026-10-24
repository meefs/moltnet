package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/follow"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestFollowMetadataAndCheckpointPersist(t *testing.T) {
	for _, kind := range []string{"json", "sqlite"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "store")
			type followStorage interface {
				RoomStore
				GetFollowCheckpoint(context.Context, string) (follow.Checkpoint, error)
				SetFollowCheckpoint(context.Context, string, follow.Checkpoint) error
			}
			open := func() followStorage {
				t.Helper()
				if kind == "json" {
					value, err := NewFileStore(path)
					if err != nil {
						t.Fatal(err)
					}
					return value
				}
				value, err := NewSQLiteStore(path)
				if err != nil {
					t.Fatal(err)
				}
				return value
			}
			first := open()
			room := protocol.Room{ID: "follow.room", NetworkID: "local", Name: "Mirror", Visibility: "private", CreatedAt: time.Now().UTC(), Follow: &protocol.RoomFollow{ID: "follow", RemoteBaseURL: "https://remote.example", RemoteRoomID: "room"}}
			if err := first.CreateRoom(room); err != nil {
				t.Fatal(err)
			}
			checkpoint := follow.Checkpoint{Initialized: true, Cursor: "last", Count: 42}
			if err := first.SetFollowCheckpoint(t.Context(), "stream", checkpoint); err != nil {
				t.Fatal(err)
			}
			if closer, ok := first.(interface{ Close() error }); ok {
				if err := closer.Close(); err != nil {
					t.Fatal(err)
				}
			}
			reopened := open()
			if closer, ok := reopened.(interface{ Close() error }); ok {
				t.Cleanup(func() { closer.Close() })
			}
			got, ok, err := reopened.GetRoom(room.ID)
			if err != nil || !ok || got.Follow == nil || *got.Follow != *room.Follow {
				t.Fatalf("marker lost: %#v %v", got, err)
			}
			rooms, err := reopened.ListRooms()
			if err != nil || len(rooms) != 1 || rooms[0].Follow == nil {
				t.Fatalf("list marker lost: %#v %v", rooms, err)
			}
			state, err := reopened.GetFollowCheckpoint(t.Context(), "stream")
			if err != nil || state != checkpoint {
				t.Fatalf("checkpoint lost: %#v %v", state, err)
			}
			got.Name = "Renamed"
			if _, err = reopened.ReconcileRoom(got); err != nil {
				t.Fatal(err)
			}
			got, _, err = reopened.GetRoom(room.ID)
			if err != nil || got.Follow == nil {
				t.Fatalf("reconcile marker lost: %#v %v", got, err)
			}
		})
	}
}
