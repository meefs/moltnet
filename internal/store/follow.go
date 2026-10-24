package store

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

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

func (s *MemoryStore) SetFollowCheckpoint(ctx context.Context, key string, value follow.Checkpoint) error {
	return s.SetFollowCheckpoints(ctx, map[string]follow.Checkpoint{key: value}, nil)
}

// SetFollowCheckpoints deletes literal prefixes before applying replacements.
// The whole page is visible atomically to checkpoint readers.
func (s *MemoryStore) SetFollowCheckpoints(ctx context.Context, values map[string]follow.Checkpoint, deletePrefixes []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	updateFollowCheckpoints(s.followCheckpoints, values, deletePrefixes)
	return nil
}

func updateFollowCheckpoints(existing, values map[string]follow.Checkpoint, deletePrefixes []string) {
	for key := range existing {
		for _, prefix := range deletePrefixes {
			if strings.HasPrefix(key, prefix) {
				delete(existing, key)
				break
			}
		}
	}
	for key, value := range values {
		existing[key] = value
	}
}

func (s *FileStore) SetFollowCheckpoint(ctx context.Context, key string, value follow.Checkpoint) error {
	return s.SetFollowCheckpoints(ctx, map[string]follow.Checkpoint{key: value}, nil)
}

func (s *FileStore) SetFollowCheckpoints(ctx context.Context, values map[string]follow.Checkpoint, deletePrefixes []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	state := s.snapshot()
	if state.FollowCheckpoints == nil {
		state.FollowCheckpoints = make(map[string]follow.Checkpoint)
	}
	updateFollowCheckpoints(state.FollowCheckpoints, values, deletePrefixes)
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
	return s.SetFollowCheckpoints(ctx, map[string]follow.Checkpoint{key: value}, nil)
}

func (s *SQLStore) SetFollowCheckpoints(ctx context.Context, values map[string]follow.Checkpoint, deletePrefixes []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	for _, prefix := range deletePrefixes {
		// substr is shared by SQLite and Postgres and avoids LIKE wildcard
		// semantics for URL/identifier prefixes containing '%' or '_'.
		query := bindQuery(s.dialect, `DELETE FROM follow_checkpoints WHERE substr(key, 1, ?) = ?`)
		if _, err := tx.ExecContext(ctx, query, utf8.RuneCountInString(prefix), prefix); err != nil {
			return err
		}
	}
	query := bindQuery(s.dialect, `INSERT INTO follow_checkpoints (key, checkpoint_json) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET checkpoint_json = excluded.checkpoint_json`)
	for key, value := range values {
		if _, err := tx.ExecContext(ctx, query, key, marshalJSON(value)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func parseRoomFollow(encoded string) *protocol.RoomFollow {
	var marker *protocol.RoomFollow
	_ = json.Unmarshal([]byte(encoded), &marker)
	return marker
}
