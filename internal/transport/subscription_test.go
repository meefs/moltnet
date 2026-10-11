package transport

import (
	"context"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func (f *fakeService) SubscribeFromContext(ctx context.Context, cursor string) (<-chan protocol.Event, error) {
	return f.SubscribeFrom(ctx, cursor), nil
}
