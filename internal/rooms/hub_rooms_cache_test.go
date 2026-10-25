package rooms_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

type countingHubRoomsStore struct {
	*store.MemoryStore
	lists atomic.Int64
}

func (s *countingHubRoomsStore) HubRoomsContext(ctx context.Context) ([]store.HubRoom, error) {
	s.lists.Add(1)
	return s.MemoryStore.HubRoomsContext(ctx)
}

// Room messages do not list hub rooms from the store each time; the list is
// cached and refreshed when this network creates or joins one.
func TestHubRoomListIsCachedAcrossMessages(t *testing.T) {
	counting := &countingHubRoomsStore{MemoryStore: store.NewMemoryStore()}
	n := newHubNet(t, "solo", counting)
	n.start(t)
	if _, err := n.service.CreateRoom(protocol.CreateRoomRequest{ID: "plain", Members: []string{"lead"}}); err != nil {
		t.Fatal(err)
	}
	before := counting.lists.Load()
	for index := 0; index < 20; index++ {
		if _, err := n.service.SendMessage(protocol.SendMessageRequest{Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "plain"},
			From: protocol.Actor{Type: "agent", ID: "lead"}, Parts: []protocol.Part{{Kind: "text", Text: "hi"}}}); err != nil {
			t.Fatal(err)
		}
	}
	if listed := counting.lists.Load() - before; listed > 1 {
		t.Fatalf("20 messages listed hub rooms %d times", listed)
	}
	// Creating a hub room refreshes the cache.
	if _, err := n.service.CreateRoom(protocol.CreateRoomRequest{ID: "shared", Members: []string{"lead"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := n.service.CreateHubRoomContext(context.Background(), "shared", ""); err != nil {
		t.Fatal(err)
	}
	views, err := n.service.HubRoomsContext(context.Background())
	if err != nil || len(views) != 1 {
		t.Fatalf("hub rooms after create = %#v, %v", views, err)
	}
}
