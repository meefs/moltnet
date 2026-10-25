package rooms

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/noopolis/moltnet/internal/observability"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// ErrSignatureRejected covers every inbound envelope refusal: a bad or
// mismatched signature, a key that conflicts with a pin, or an unsigned
// message from a network whose key is pinned. It is a 403, so the sender's
// outbox counts the delivery as rejected instead of retrying it forever.
var ErrSignatureRejected = errors.New("message signature rejected")

// ErrEnvelopeConflict is store.ErrEnvelopeConflict surfaced as a 409.
var ErrEnvelopeConflict = store.ErrEnvelopeConflict

func signatureRejectedError(message string) error {
	return &Error{status: http.StatusForbidden, msg: message, cause: ErrSignatureRejected}
}

func envelopeConflictError(err error) error {
	return &Error{status: http.StatusConflict, msg: err.Error(), cause: err}
}

func trustStoreUnavailableError(err error) error {
	return &Error{status: http.StatusServiceUnavailable, msg: "network key trust store unavailable: " + err.Error(), cause: err}
}

// withOutboundEnvelope signs a locally originated message that is about to
// be queued for pairing delivery. The envelope is created once, here, and
// committed in the same transaction as the message and its outbox rows; the
// sender only ever replays these stored bytes.
//
// A message canonical-json.v1 cannot represent (for example an integer
// beyond 2^53 in part data) is refused with 422 rather than relayed
// unsigned: peers that pinned this network's key refuse unsigned messages,
// so relaying it would accept it locally and drop it silently everywhere
// else. It is never altered to make it signable.
func (s *Service) withOutboundEnvelope(ctx context.Context, message protocol.Message) (context.Context, error) {
	if s.identity == nil {
		return ctx, nil
	}
	request, ok := s.relayRequest(protocol.Pairing{}, message)
	if !ok {
		return ctx, nil
	}
	envelope := protocol.EnvelopeFromRequest(request, s.identity.Fingerprint(), message.CreatedAt.UTC().Format(time.RFC3339Nano))
	signed, digest, err := protocol.SignEnvelope(envelope, s.identity.PublicKey(), s.identity.Sign)
	if err != nil {
		observability.Logger(ctx, "rooms.signing", "message_id", message.ID).
			Warn("refusing a paired message that cannot be signed", "reason", err.Error())
		return ctx, invalidMessageRequestError("message cannot be signed for paired delivery (" + err.Error() +
			"); send large integers and other non-portable numbers as strings")
	}
	return store.WithMessageEnvelope(ctx, store.MessageEnvelopeRecord{
		OriginNetworkID: s.networkID, OriginMessageID: envelope.OriginMessageID, Digest: digest,
		State: protocol.SignatureStateVerified, Fingerprint: envelope.OriginFingerprint, Envelope: signed,
	}), nil
}

// storedOutboundEnvelope returns the envelope committed with a queued
// message, or nil for messages queued unsigned (no key, legacy queue rows,
// unrepresentable content).
func (s *Service) storedOutboundEnvelope(ctx context.Context, messageID string) (*protocol.SignedEnvelope, error) {
	if s.envelopes == nil {
		return nil, nil
	}
	records, err := s.envelopes.MessageEnvelopesContext(ctx, []string{messageID})
	if err != nil {
		return nil, err
	}
	record, ok := records[messageID]
	if !ok {
		return nil, nil
	}
	signed := record.Envelope
	return &signed, nil
}

// LocalIdentity is this network's public signing identity, if it has one.
func (s *Service) LocalIdentity() (protocol.NetworkIdentity, bool) {
	if s.identity == nil {
		return protocol.NetworkIdentity{}, false
	}
	return s.identity.Public(), true
}

// MessageSignature is the HTTP-only verification view of one message.
type MessageSignature struct {
	State       string
	Fingerprint string
	// Signed is the stored envelope's statement for verified and introduced
	// messages; HTTP renders the signed facts from it, never from the local
	// projection (which carries receive time and locally resolved mentions).
	Signed *protocol.MessageEnvelope
	// IntroducedBy names the room home that vouched for an introduced key.
	IntroducedBy string
}

// MessageSignatures reports origin signature state for messages received
// over pairings (verified, introduced, unsigned) and for local messages this
// network signed. Local unsigned messages and follow imports have no entry:
// follows keep their claimed_* semantics and are never promoted. This view
// is never part of the machine v1 projection.
func (s *Service) MessageSignatures(ctx context.Context, messages []protocol.Message) (map[string]MessageSignature, error) {
	result := make(map[string]MessageSignature)
	if len(messages) == 0 {
		return result, nil
	}
	records := map[string]store.MessageEnvelopeRecord{}
	if s.envelopes != nil {
		ids := make([]string, 0, len(messages))
		for _, message := range messages {
			ids = append(ids, message.ID)
		}
		var err error
		if records, err = s.envelopes.MessageEnvelopesContext(ctx, ids); err != nil {
			return nil, err
		}
	}
	for _, message := range messages {
		if record, ok := records[message.ID]; ok {
			signature := MessageSignature{State: record.State, Fingerprint: record.Fingerprint}
			if record.State == protocol.SignatureStateIntroduced {
				if pin, ok, err := s.keyPins.Lookup(record.OriginNetworkID); err == nil && ok && pin.Fingerprint == record.Fingerprint {
					signature.IntroducedBy = pin.IntroducedBy
				}
			}
			if signedState(record.State) {
				envelope, _, err := protocol.ParseSignedEnvelope(record.Envelope)
				if err != nil {
					// Verified at store time; damage since then is not verified.
					signature = MessageSignature{State: protocol.SignatureStateInvalid, Fingerprint: record.Fingerprint}
				} else {
					signature.Signed = &envelope
				}
			}
			result[message.ID] = signature
			continue
		}
		via := message.Origin.ReceivedVia
		if via != "" && !strings.HasPrefix(via, "follow:") {
			result[message.ID] = MessageSignature{State: protocol.SignatureStateUnsigned}
		}
	}
	return result, nil
}

// signedState reports a stored state whose signature verified at store time.
func signedState(state string) bool {
	switch state {
	case protocol.SignatureStateVerified, protocol.SignatureStateIntroduced, protocol.SignatureStateUnconfirmed:
		return true
	}
	return false
}
