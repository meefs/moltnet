package store

import (
	"context"
	"encoding/json"

	"github.com/noopolis/moltnet/internal/follow"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func cloneFollowCheckpoints(values map[string]follow.Checkpoint) map[string]follow.Checkpoint {
	cloned := make(map[string]follow.Checkpoint, len(values))
	for k, v := range values {
		cloned[k] = v
	}
	return cloned
}
func (s *MemoryStore) GetFollowCheckpoint(_ context.Context, key string) (follow.Checkpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.followCheckpoints[key], nil
}
func (s *MemoryStore) SetFollowCheckpoint(_ context.Context, key string, value follow.Checkpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.followCheckpoints[key] = value
	return nil
}
func (s *FileStore) SetFollowCheckpoint(ctx context.Context, key string, value follow.Checkpoint) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	state := s.snapshot()
	if state.FollowCheckpoints == nil {
		state.FollowCheckpoints = make(map[string]follow.Checkpoint)
	}
	state.FollowCheckpoints[key] = value
	if err := s.persistSnapshot(state); err != nil {
		return err
	}
	s.restore(state)
	return nil
}
func (s *SQLStore) GetFollowCheckpoint(ctx context.Context, key string) (follow.Checkpoint, error) {
	var encoded string
	var checkpoint follow.Checkpoint
	err := s.db.QueryRowContext(ctx, bindQuery(s.dialect, `SELECT checkpoint_json FROM follow_checkpoints WHERE key = ?`), key).Scan(&encoded)
	if isNoRows(err) {
		return checkpoint, nil
	}
	if err != nil {
		return checkpoint, err
	}
	err = json.Unmarshal([]byte(encoded), &checkpoint)
	return checkpoint, err
}
func (s *SQLStore) SetFollowCheckpoint(ctx context.Context, key string, value follow.Checkpoint) error {
	_, err := s.db.ExecContext(ctx, bindQuery(s.dialect, `INSERT INTO follow_checkpoints (key, checkpoint_json) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET checkpoint_json = excluded.checkpoint_json`), key, marshalJSON(value))
	return err
}

func parseRoomFollow(encoded string) *protocol.RoomFollow {
	var marker *protocol.RoomFollow
	_ = json.Unmarshal([]byte(encoded), &marker)
	return marker
}
