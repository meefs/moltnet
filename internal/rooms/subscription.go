package rooms

import (
	"context"

	"github.com/noopolis/moltnet/internal/observability"
	"github.com/noopolis/moltnet/pkg/protocol"
)

type replayingBroker interface {
	SubscribeFromContext(context.Context, string) (<-chan protocol.Event, error)
}

func (s *Service) SubscribeFrom(ctx context.Context, lastEventID string) <-chan protocol.Event {
	stream, err := s.SubscribeFromContext(ctx, lastEventID)
	if err != nil {
		observability.Logger(ctx, "rooms.events", "error", err).Error("cannot subscribe to events")
		closed := make(chan protocol.Event)
		close(closed)
		return closed
	}
	return stream
}

func (s *Service) SubscribeFromContext(ctx context.Context, lastEventID string) (<-chan protocol.Event, error) {
	if broker, ok := s.broker.(replayingBroker); ok {
		stream, err := broker.SubscribeFromContext(ctx, lastEventID)
		if err != nil {
			return nil, err
		}
		return s.filterEvents(ctx, stream), nil
	}
	return s.filterEvents(ctx, s.broker.Subscribe(ctx)), nil
}
