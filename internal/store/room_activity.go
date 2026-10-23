package store

import (
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func (s *MemoryStore) latestRoomMessage(roomID string) *time.Time {
	at, ok := s.roomActivity[roomID]
	if !ok {
		return nil
	}
	return &at
}

// Called under the store lock, after a successful append; out-of-order
// history must never move activity backward.
func (s *MemoryStore) recordRoomActivity(message protocol.Message) {
	if message.Target.Kind != protocol.TargetKindRoom && message.Target.Kind != protocol.TargetKindThread {
		return
	}
	roomID := message.Target.RoomID
	if roomID == "" && message.Target.Kind == protocol.TargetKindThread {
		roomID = s.threads[message.Target.ThreadID].RoomID
	}
	previous, ok := s.roomActivity[roomID]
	if !ok || message.CreatedAt.After(previous) {
		s.roomActivity[roomID] = message.CreatedAt
	}
}

// Rebuild the derived index when restoring a snapshot, including snapshots
// written before last_message_at was added to the public protocol.
func (s *MemoryStore) rebuildRoomActivity() {
	s.roomActivity = make(map[string]time.Time)
	for _, group := range []map[string][]protocol.Message{s.roomMessages, s.threadMessages} {
		for _, messages := range group {
			for _, message := range messages {
				s.recordRoomActivity(message)
			}
		}
	}
}
