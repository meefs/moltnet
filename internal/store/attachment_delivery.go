package store

import (
	"context"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

// AttachmentDeliveryStore makes message delivery resumable independently of
// the in-memory event broker. Preparing an agent with no cursor establishes a
// baseline at the current durable head; only later unacknowledged events replay.
type AttachmentDeliveryStore interface {
	AppendMessageEventWithLifecycleContext(context.Context, protocol.Message, protocol.Event) (AppendLifecycle, error)
	PrepareAttachmentDeliveryContext(context.Context, string) ([]protocol.Event, error)
	AcknowledgeAttachmentDeliveryContext(context.Context, string, string) error
}

type attachmentRegistrationTimeKey struct{}

// WithAttachmentRegistrationTime carries the already validated registration
// timestamp into cursor initialization. Reading the clock again after the claim
// commits could exhaust its range and prevent the issued token reaching READY.
// Only the service supplies this value; it is never taken from a wire request.
func WithAttachmentRegistrationTime(ctx context.Context, at time.Time) context.Context {
	return context.WithValue(ctx, attachmentRegistrationTimeKey{}, at)
}
