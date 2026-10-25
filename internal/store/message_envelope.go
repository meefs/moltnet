package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/noopolis/moltnet/pkg/protocol"
)

// ErrEnvelopeConflict means a pairing delivered an origin message tuple that
// is already stored under a different envelope digest: an origin fork or a
// forgery attempt. It is never acknowledged as a duplicate.
var ErrEnvelopeConflict = errors.New("origin message already stored with a different envelope")

// MessageEnvelopeRecord is the immutable signed envelope stored beside the
// local message projection. It is written once with the message and never
// updated; display and verification facts come from it, not the projection.
type MessageEnvelopeRecord struct {
	MessageID       string                  `json:"message_id"`
	OriginNetworkID string                  `json:"origin_network_id"`
	OriginMessageID string                  `json:"origin_message_id"`
	Digest          string                  `json:"digest"`
	State           string                  `json:"state"`
	Fingerprint     string                  `json:"fingerprint"`
	Envelope        protocol.SignedEnvelope `json:"envelope"`
}

// MessageEnvelopeStore reads stored envelopes by local message id.
type MessageEnvelopeStore interface {
	MessageEnvelopesContext(ctx context.Context, messageIDs []string) (map[string]MessageEnvelopeRecord, error)
}

type messageEnvelopeKey struct{}

// WithMessageEnvelope attaches the envelope to persist atomically with the
// next message append on ctx. The same digest also guards the origin
// receipt: a retry with a different digest is ErrEnvelopeConflict.
func WithMessageEnvelope(ctx context.Context, record MessageEnvelopeRecord) context.Context {
	return context.WithValue(ctx, messageEnvelopeKey{}, &record)
}

func messageEnvelopeFromContext(ctx context.Context) *MessageEnvelopeRecord {
	if ctx == nil {
		return nil
	}
	record, _ := ctx.Value(messageEnvelopeKey{}).(*MessageEnvelopeRecord)
	return record
}

// envelopeForMessage binds a context envelope to the message being stored.
func envelopeForMessage(ctx context.Context, message protocol.Message) *MessageEnvelopeRecord {
	record := messageEnvelopeFromContext(ctx)
	if record == nil {
		return nil
	}
	bound := *record
	bound.MessageID = message.ID
	if bound.OriginNetworkID == "" {
		bound.OriginNetworkID = message.Origin.NetworkID
	}
	if bound.OriginMessageID == "" {
		bound.OriginMessageID = message.Origin.MessageID
	}
	return &bound
}

func envelopeDigest(record *MessageEnvelopeRecord) string {
	if record == nil {
		return ""
	}
	return record.Digest
}

func envelopeConflict(origin protocol.MessageOrigin, stored, offered string) error {
	return fmt.Errorf("%w: origin %s message %s stored digest %q, offered %q",
		ErrEnvelopeConflict, origin.NetworkID, origin.MessageID, stored, offered)
}

func (s *MemoryStore) MessageEnvelopesContext(_ context.Context, messageIDs []string) (map[string]MessageEnvelopeRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]MessageEnvelopeRecord)
	for _, id := range messageIDs {
		if record, ok := s.envelopes[id]; ok {
			result[id] = record
		}
	}
	return result, nil
}

func cloneEnvelopes(values map[string]MessageEnvelopeRecord) map[string]MessageEnvelopeRecord {
	cloned := make(map[string]MessageEnvelopeRecord, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

// collectOriginDigests rebuilds receipt digests from persisted envelopes,
// keyed like originReceipts, after a JSON snapshot load.
func collectOriginDigests(receipts map[string]string, envelopes map[string]MessageEnvelopeRecord) map[string]string {
	digests := make(map[string]string, len(receipts))
	for key, messageID := range receipts {
		digests[key] = envelopes[messageID].Digest
	}
	return digests
}

func marshalEnvelope(record MessageEnvelopeRecord) (string, error) {
	encoded, err := json.Marshal(record.Envelope)
	if err != nil {
		return "", fmt.Errorf("encode message envelope: %w", err)
	}
	return string(encoded), nil
}

// checkOriginReceiptLocked reports a stored origin tuple as a duplicate only
// when its envelope digest matches; a different digest is a fork or a
// forgery, never an idempotent retry.
func (s *MemoryStore) checkOriginReceiptLocked(message protocol.Message, envelope *MessageEnvelopeRecord) error {
	key := messageOriginReceipt(message)
	if key == "" {
		return nil
	}
	id, exists := s.originReceipts[key]
	if !exists {
		return nil
	}
	if stored, offered := s.originDigests[key], envelopeDigest(envelope); stored != offered {
		return envelopeConflict(message.Origin, stored, offered)
	}
	return &DuplicateMessageError{MessageID: id}
}

func (s *MemoryStore) recordOriginReceiptLocked(message protocol.Message, envelope *MessageEnvelopeRecord) {
	if key := messageOriginReceipt(message); key != "" {
		s.originReceipts[key] = message.ID
		s.originDigests[key] = envelopeDigest(envelope)
	}
	if envelope != nil {
		s.envelopes[message.ID] = *envelope
	}
}
