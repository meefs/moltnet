package rooms

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestFollowImportedIdentityReloadsWithoutMigration(t *testing.T) {
	for _, kind := range []string{"json", "sqlite"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "store")
			type storage interface {
				store.RoomStore
				store.MessageStore
			}
			open := func() storage {
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
			closeStore := func(s storage) {
				if c, ok := s.(interface{ Close() error }); ok {
					if err := c.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
			s := open()
			service := NewService(ServiceConfig{NetworkID: "local", Store: s, Messages: s, Broker: events.NewBroker()})
			config := protocol.Follow{ID: "news", RemoteBaseURL: "https://origin.example"}
			if err := service.EnsureFollowRoom(t.Context(), config, protocol.Room{ID: "public"}); err != nil {
				t.Fatal(err)
			}
			remote := protocol.Message{ID: "served", Origin: protocol.MessageOrigin{NetworkID: "peer", MessageID: "claim"}, Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "public"}, From: protocol.Actor{Type: "agent", ID: "alice", NetworkID: "peer", FQID: "molt://peer/agents/alice"}, CreatedAt: time.Now().UTC()}
			if _, err := service.ImportFollowMessage(t.Context(), config, "origin", remote); err != nil {
				t.Fatal(err)
			}
			// Unreleased records written before this fix must still load unchanged.
			legacy := remote
			legacy.ID = "legacy"
			legacy.NetworkID = "local"
			legacy.Target.RoomID = "news.public"
			legacy.Origin = protocol.MessageOrigin{NetworkID: "origin", MessageID: "legacy", ReceivedVia: "follow:news"}
			if err := s.AppendMessage(legacy); err != nil {
				t.Fatal(err)
			}
			before, err := service.ListRoomMessages("news.public", "", 100)
			if err != nil {
				t.Fatal(err)
			}
			service.Close()
			closeStore(s)
			s = open()
			defer closeStore(s)
			service = NewService(ServiceConfig{NetworkID: "local", Store: s, Messages: s, Broker: events.NewBroker()})
			defer service.Close()
			after, err := service.ListRoomMessages("news.public", "", 100)
			if err != nil || !reflect.DeepEqual(before.Messages, after.Messages) {
				t.Fatalf("reload lost imported identity: %+v %v", after, err)
			}
			if added, err := service.ImportFollowMessage(t.Context(), config, "origin", remote); err != nil || added {
				t.Fatalf("reload lost dedupe: %v %v", added, err)
			}
		})
	}
}
