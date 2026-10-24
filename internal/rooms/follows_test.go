package rooms

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func followTestService(t *testing.T) (*Service, protocol.Follow) {
	t.Helper()
	storage := store.NewMemoryStore()
	service := NewService(ServiceConfig{NetworkID: "local", Store: storage, Messages: storage, Broker: events.NewBroker(storage), PairingClient: &recordingPairingClient{}, Pairings: []protocol.Pairing{{ID: "peer", RemoteNetworkID: "peer", RemoteBaseURL: "https://peer.example"}}})
	t.Cleanup(func() { service.Close() })
	config := protocol.Follow{ID: "source", RemoteBaseURL: "https://origin.example"}.WithDefaults()
	if err := service.EnsureFollowRoom(t.Context(), config, protocol.Room{ID: "public", Name: "Public"}); err != nil {
		t.Fatal(err)
	}
	return service, config
}

func TestFollowMirrorWriteGuard(t *testing.T) {
	service, _ := followTestService(t)
	_, err := service.SendMessageContext(context.Background(), protocol.SendMessageRequest{Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "source.public"}, From: protocol.Actor{Type: "agent", ID: "writer"}, Parts: []protocol.Part{{Kind: "text", Text: "denied"}}})
	if !errors.Is(err, ErrWriteForbidden) {
		t.Fatalf("mirror write accepted: %v", err)
	}
	room, err := service.GetRoom("source.public")
	if err != nil {
		t.Fatal(err)
	}
	if service.canWriteRoom(context.Background(), room, protocol.Actor{Type: "agent", ID: "writer"}) {
		t.Fatal("mirror advertised as writable")
	}
}

func TestFollowMirrorNeverRelays(t *testing.T) {
	service, _ := followTestService(t)
	message := protocol.Message{Origin: protocol.MessageOrigin{NetworkID: "local", MessageID: "own-origin"}, Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "source.public"}}
	if service.shouldRelayMessage(message) {
		t.Fatal("mirror room relayed a local-origin message")
	}
	message.Target.RoomID = "ordinary"
	message.Origin.ReceivedVia = "follow:source"
	if service.shouldRelayMessage(message) {
		t.Fatal("follow provenance relayed outside mirror")
	}
	message.Origin.ReceivedVia = ""
	if !service.shouldRelayMessage(message) {
		t.Fatal("control local message should relay")
	}
}

func TestFollowImportDeduplicatesServedIDAndMapsForeignThreadParent(t *testing.T) {
	service, config := followTestService(t)
	parent := protocol.Message{ID: "remote-copy", Origin: protocol.MessageOrigin{NetworkID: "original", MessageID: "original-root"}, Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "public"}, From: protocol.Actor{Type: "agent", ID: "alice"}, Parts: []protocol.Part{{Kind: "text", Text: "root"}}, CreatedAt: time.Now().UTC()}
	added, err := service.ImportFollowMessage(t.Context(), config, "origin", parent)
	if err != nil || !added {
		t.Fatalf("first import %v %v", added, err)
	}
	// Repeating the served ID must deduplicate, independently of claimed origin.
	added, err = service.ImportFollowMessage(t.Context(), config, "origin", parent)
	if err != nil || added {
		t.Fatalf("duplicate import %v %v", added, err)
	}
	reply := protocol.Message{ID: "reply", Target: protocol.Target{Kind: protocol.TargetKindThread, RoomID: "public", ThreadID: "remote-copy", ParentMessageID: "remote-copy"}, From: parent.From, Parts: []protocol.Part{{Kind: "text", Text: "reply"}}, CreatedAt: parent.CreatedAt.Add(time.Second)}
	if _, err = service.ImportFollowMessage(t.Context(), config, "origin", reply); err != nil {
		t.Fatal(err)
	}
	rootID := followMessageID(config.ID, "remote-copy")
	thread, exists, err := service.getThread(t.Context(), rootID)
	if err != nil || !exists || thread.ParentMessageID != rootID {
		t.Fatalf("foreign parent mapping %#v %v %v", thread, exists, err)
	}
	page, err := service.ListRoomMessages("source.public", "", 100)
	if err != nil || len(page.Messages) != 1 {
		t.Fatalf("dedupe failed %#v %v", page, err)
	}
	if page.Messages[0].Origin.ReceivedVia != "follow:source" || page.Messages[0].Origin.NetworkID != protocol.FollowNamespace(config.ID) || page.Messages[0].Origin.ClaimedNetworkID != "original" {
		t.Fatalf("origin not preserved %#v", page.Messages[0].Origin)
	}
}

func TestFollowEmptyMirrorPublishesRoomCreated(t *testing.T) {
	service, config := followTestService(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream := service.Subscribe(ctx)
	if err := service.EnsureFollowRoom(ctx, config, protocol.Room{ID: "empty", Name: "Empty"}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-stream:
		if event.Type != protocol.EventTypeRoomCreated || event.Room == nil || event.Room.ID != "source.empty" || event.Room.Follow == nil {
			t.Fatalf("mirror discovery event: %+v", event)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("empty mirror never announced to connected consoles")
	}
}
