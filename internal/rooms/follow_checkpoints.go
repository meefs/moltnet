package rooms

import (
	"context"
	"fmt"

	"github.com/noopolis/moltnet/internal/follow"
)

type followCheckpointStore interface {
	GetFollowCheckpoint(context.Context, string) (follow.Checkpoint, error)
	SetFollowCheckpoint(context.Context, string, follow.Checkpoint) error
	SetFollowCheckpoints(context.Context, map[string]follow.Checkpoint, []string) error
}

func (s *Service) GetFollowCheckpoint(ctx context.Context, key string) (follow.Checkpoint, error) {
	storage, ok := s.store.(followCheckpointStore)
	if !ok {
		return follow.Checkpoint{}, fmt.Errorf("store does not support durable follow checkpoints")
	}
	return storage.GetFollowCheckpoint(ctx, key)
}

func (s *Service) SetFollowCheckpoint(ctx context.Context, key string, value follow.Checkpoint) error {
	storage, ok := s.store.(followCheckpointStore)
	if !ok {
		return fmt.Errorf("store does not support durable follow checkpoints")
	}
	return storage.SetFollowCheckpoint(ctx, key, value)
}

func (s *Service) SetFollowCheckpoints(ctx context.Context, values map[string]follow.Checkpoint, deletePrefixes []string) error {
	storage, ok := s.store.(followCheckpointStore)
	if !ok {
		return fmt.Errorf("store does not support durable follow checkpoints")
	}
	return storage.SetFollowCheckpoints(ctx, values, deletePrefixes)
}
