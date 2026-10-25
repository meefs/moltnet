package rooms

import (
	"context"
	"slices"
	"sync"

	"github.com/noopolis/moltnet/internal/store"
)

// hubRoomsCache keeps the list of hub rooms in memory: it is consulted for
// every room message and every agent read, and changes only when this
// process creates or joins a hub room (one active process per store; see
// docs on multi-instance homes). gen guards against a load that started
// before an invalidation storing a stale list after it.
type hubRoomsCache struct {
	mu    sync.Mutex
	rooms []store.HubRoom
	valid bool
	gen   uint64
}

func (s *Service) hubRooms(ctx context.Context) ([]store.HubRoom, error) {
	if s.hubs == nil {
		return nil, nil
	}
	cache := &s.hubRoomList
	cache.mu.Lock()
	if cache.valid {
		rooms := slices.Clone(cache.rooms)
		cache.mu.Unlock()
		return rooms, nil
	}
	gen := cache.gen
	cache.mu.Unlock()
	rooms, err := s.hubs.HubRoomsContext(ctx)
	if err != nil {
		return nil, err
	}
	cache.mu.Lock()
	if cache.gen == gen {
		cache.rooms, cache.valid = slices.Clone(rooms), true
	}
	cache.mu.Unlock()
	return rooms, nil
}

// invalidateHubRooms drops the cached list after a hub room is created.
func (s *Service) invalidateHubRooms() {
	s.hubRoomList.mu.Lock()
	s.hubRoomList.gen++
	s.hubRoomList.valid = false
	s.hubRoomList.rooms = nil
	s.hubRoomList.mu.Unlock()
}
