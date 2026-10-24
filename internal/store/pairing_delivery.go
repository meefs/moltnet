package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

// PairingDelivery is an outbound record committed with the originating message.
// Sequence orders accepted messages independently of caller timestamps and IDs.
type PairingDelivery struct {
	Sequence    int64
	PairingID   string
	MessageID   string
	Message     protocol.Message
	Attempts    int
	NextAttempt time.Time
	CreatedAt   time.Time
}

type PairingDeliveryState struct {
	Pending         int
	OldestPendingAt time.Time
	LastError       string
	Dropped         int64
}

type PairingDeliveryStore interface {
	AppendMessageWithPairingDeliveriesContext(context.Context, protocol.Message, *protocol.Event, []string) (AppendLifecycle, error)
	ListPairingDeliveryHeadsContext(context.Context) ([]PairingDelivery, error)
	UpdatePairingDeliveryContext(context.Context, PairingDelivery, string) error
	CompletePairingDeliveryContext(context.Context, string, string) error
	PrunePairingDeliveriesContext(context.Context, string, time.Time, time.Duration, int) (int, error)
	PairingDeliveryStateContext(context.Context, string) (PairingDeliveryState, error)
}

// A pairing credential is validated by rooms before this store is called.
// Follow mirrors have their own identity and checkpoint semantics.
func messageOriginReceipt(message protocol.Message) string {
	if message.Origin.ReceivedVia == "" || strings.HasPrefix(message.Origin.ReceivedVia, "follow:") ||
		message.Origin.NetworkID == "" || message.Origin.MessageID == "" {
		return ""
	}
	key, _ := json.Marshal([2]string{message.Origin.NetworkID, message.Origin.MessageID})
	return string(key)
}

func collectOriginReceipts(groups ...map[string][]protocol.Message) map[string]string {
	receipts := make(map[string]string)
	for _, group := range groups {
		for _, messages := range group {
			for _, message := range messages {
				if key := messageOriginReceipt(message); key != "" {
					receipts[key] = message.ID
				}
			}
		}
	}
	return receipts
}

func clonePairingDeliveryStates(states map[string]PairingDeliveryState) map[string]PairingDeliveryState {
	result := make(map[string]PairingDeliveryState, len(states))
	for id, state := range states {
		result[id] = state
	}
	return result
}

// PairingDeliveryLimits apply in the same transaction as the message append.
type PairingDeliveryLimits struct {
	MaxAge     time.Duration
	MaxPending int
}

type pairingDeliveryLimitsKey struct{}

func WithPairingDeliveryLimits(ctx context.Context, limits map[string]PairingDeliveryLimits) context.Context {
	return context.WithValue(ctx, pairingDeliveryLimitsKey{}, limits)
}

func pairingDeliveryLimitsFromContext(ctx context.Context) map[string]PairingDeliveryLimits {
	limits, _ := ctx.Value(pairingDeliveryLimitsKey{}).(map[string]PairingDeliveryLimits)
	return limits
}

func collectMessageOrigins(groups ...map[string][]protocol.Message) map[string]string {
	origins := make(map[string]string)
	for _, group := range groups {
		for _, messages := range group {
			for _, message := range messages {
				origins[message.ID] = messageOriginReceipt(message)
			}
		}
	}
	return origins
}
