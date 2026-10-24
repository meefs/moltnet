package rooms

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestFollowUntrustedClaimsCannotForgeAuthorsOrSuppressAnotherOrigin(t *testing.T) {
	service, evil := followTestService(t)
	good := protocol.Follow{ID: "good", RemoteBaseURL: "https://good.example"}.WithDefaults()
	if err := service.EnsureFollowRoom(t.Context(), good, protocol.Room{ID: "public"}); err != nil {
		t.Fatal(err)
	}
	forged := protocol.Message{ID: "evil-copy", Origin: protocol.MessageOrigin{NetworkID: "good", MessageID: "g1"}, Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "public"}, From: protocol.Actor{Type: "agent", ID: "writer", NetworkID: "local", FQID: "molt://local/agents/writer", CredentialBound: true}, CreatedAt: time.Now().UTC()}
	if added, err := service.ImportFollowMessage(t.Context(), evil, "evil", forged); err != nil || !added {
		t.Fatalf("evil import: %v %v", added, err)
	}
	page, err := service.ListRoomMessages("source.public", "", 100)
	if err != nil || len(page.Messages) != 1 {
		t.Fatalf("evil history: %+v %v", page, err)
	}
	got := page.Messages[0]
	if got.From.NetworkID != protocol.FollowNamespace(evil.ID) || got.From.FQID != protocol.AgentFQID(got.From.NetworkID, got.From.ID) || got.From.CredentialBound {
		t.Fatalf("forged author survived: %+v", got.From)
	}
	if got.Origin.ClaimedAuthor == nil || *got.Origin.ClaimedAuthor != forged.From {
		t.Fatalf("claimed author lost: %+v", got.Origin)
	}
	real := forged
	real.ID = "g1"
	real.From.NetworkID = "good"
	if added, err := service.ImportFollowMessage(t.Context(), good, "good", real); err != nil || !added {
		t.Fatalf("evil suppressed good: %v %v", added, err)
	}
	page, err = service.ListRoomMessages("good.public", "", 100)
	if err != nil || len(page.Messages) != 1 {
		t.Fatalf("good history: %+v %v", page, err)
	}
	// A different served ID remains distinct even when the origin claim repeats.
	forged.ID = "another-copy"
	if added, err := service.ImportFollowMessage(t.Context(), evil, "evil", forged); err != nil || !added {
		t.Fatalf("claimed tuple deduped: %v %v", added, err)
	}
	if added, err := service.ImportFollowMessage(t.Context(), evil, "local", forged); err != nil || added {
		t.Fatalf("discovery claim changed identity: %v %v", added, err)
	}
}

func TestFollowMirrorDeletionRequiresRemovingConfiguredFollow(t *testing.T) {
	for _, kind := range []string{"memory", "sqlite"} {
		t.Run(kind, func(t *testing.T) {
			var storage interface {
				store.RoomStore
				store.MessageStore
			}
			if kind == "memory" {
				storage = store.NewMemoryStore()
			} else {
				sql, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "rooms.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { sql.Close() })
				storage = sql
			}
			service := NewService(ServiceConfig{NetworkID: "local", Store: storage, Messages: storage, Broker: events.NewBroker()})
			t.Cleanup(func() { service.Close() })
			cfg := protocol.Follow{ID: "news", RemoteBaseURL: "http://127.0.0.1:1"}.WithDefaults()
			follows := []protocol.Follow{cfg}
			if _, err := service.ApplyConfigContext(t.Context(), protocol.ApplyConfigRequest{Follows: &follows}); err != nil {
				t.Fatal(err)
			}
			if err := service.EnsureFollowRoom(t.Context(), cfg, protocol.Room{ID: "public"}); err != nil {
				t.Fatal(err)
			}
			remote := protocol.Message{ID: "one", Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "public"}, From: protocol.Actor{Type: "agent", ID: "writer"}, CreatedAt: time.Now().UTC()}
			if _, err := service.ImportFollowMessage(t.Context(), cfg, "origin", remote); err != nil {
				t.Fatal(err)
			}
			if _, err := service.RemoveRoomContext(t.Context(), "news.public"); err == nil || !strings.Contains(err.Error(), "remove follow \"news\" first") {
				t.Fatalf("delete configured mirror: %v", err)
			}
			if err := service.EnsureFollowRoom(t.Context(), cfg, protocol.Room{ID: "public"}); err != nil {
				t.Fatalf("follow broken: %v", err)
			}
			follows = nil
			if _, err := service.ApplyConfigContext(t.Context(), protocol.ApplyConfigRequest{Follows: &follows}); err != nil {
				t.Fatal(err)
			}
			page, err := service.ListRoomMessages("news.public", "", 100)
			if err != nil || len(page.Messages) != 1 {
				t.Fatalf("remove lost history: %+v %v", page, err)
			}
			if _, err := service.RemoveRoomContext(t.Context(), "news.public"); err != nil {
				t.Fatalf("delete after remove: %v", err)
			}
		})
	}
}
