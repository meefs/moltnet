package rooms

import (
	"context"

	"errors"
	"fmt"
	authn "github.com/noopolis/moltnet/internal/auth"
	"strings"

	"github.com/noopolis/moltnet/internal/netkeys"
	"github.com/noopolis/moltnet/internal/observability"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// inboundEnvelope is a verified envelope plus the first-contact pin it
// would create. The pin is written only after the message is authorized,
// immediately before the append (commitFirstContactPin), and undone if the
// append fails, so a refused message can never leave a pin behind.
type inboundEnvelope struct {
	record       store.MessageEnvelopeRecord
	firstContact *netkeys.Pin
}

// verifyInboundEnvelope applies the chunk-A acceptance rule to a message
// relayed over a direct pairing. It runs after validateSenderIdentity, so
// the origin network is already bound to the presenting pair credential.
//
//   - signed: the envelope must parse strictly, verify under its attached
//     key, name the same origin, and state exactly the delivered message.
//     The key must match this network's pin for the origin. With no pin,
//     first contact proposes one: verified when config binds the credential
//     to the origin network, otherwise only unconfirmed (the inviting side
//     cannot know who it invited). A verified first contact replaces an
//     unconfirmed pin, so claiming another network's id first squats nothing.
//   - unsigned: accepted as unsigned only while the origin has no pin. Once
//     a key is pinned, stripping the envelope is a downgrade and is refused.
//
// A nil result means unsigned or not a pairing relay at all (local callers
// can never supply an envelope).
func (s *Service) verifyInboundEnvelope(ctx context.Context, request protocol.SendMessageRequest) (*inboundEnvelope, error) {
	pairing, ok := s.pairingForPairScopedContext(ctx)
	origin := strings.TrimSpace(request.Origin.NetworkID)
	if !ok || origin == "" || origin == s.networkID {
		return nil, nil
	}
	logger := observability.Logger(ctx, "rooms.signing", "origin_network_id", origin, "pairing_id", pairing.ID)
	pin, pinned, err := s.keyPins.Lookup(origin)
	if err != nil {
		logger.Error("network key trust store unavailable", "error", err)
		return nil, trustStoreUnavailableError(err)
	}
	if request.Envelope == nil {
		if pinned {
			logger.Warn("rejecting unsigned message from a network whose key is pinned")
			return nil, signatureRejectedError(fmt.Sprintf("network %q has a pinned key; unsigned messages are refused", origin))
		}
		return nil, nil
	}

	envelope, digest, err := protocol.VerifySignedEnvelope(*request.Envelope)
	if err != nil {
		logger.Warn("rejecting message with an invalid signed envelope", "error", err)
		return nil, signatureRejectedError("invalid message envelope: " + err.Error())
	}
	if envelope.OriginNetworkID != origin {
		logger.Warn("rejecting envelope signed for a different origin network", "envelope_origin", envelope.OriginNetworkID)
		return nil, signatureRejectedError("message envelope origin does not match the message origin")
	}
	// The FQID is not a signed field, so it must be exactly the one the
	// signed origin and author id determine; anything else is unsigned
	// testimony that would render as verified.
	if fqid := request.From.FQID; fqid != "" && fqid != protocol.AgentFQID(envelope.OriginNetworkID, envelope.Author.ID) {
		logger.Warn("rejecting signed message whose author fqid differs from the signed author", "fqid", fqid)
		return nil, signatureRejectedError("author fqid does not match the signed origin and author")
	}
	if err := protocol.EnvelopeMatchesRequest(envelope, request); err != nil {
		logger.Warn("rejecting message whose content differs from its signed envelope")
		return nil, signatureRejectedError(err.Error())
	}

	result := &inboundEnvelope{record: store.MessageEnvelopeRecord{
		OriginNetworkID: origin, OriginMessageID: envelope.OriginMessageID, Digest: digest,
		Fingerprint: envelope.OriginFingerprint, Envelope: *request.Envelope,
	}}
	firstContactTrust := protocol.KeyTrustUnconfirmed
	if claims, ok := authn.ClaimsFromContext(ctx); ok && strings.TrimSpace(claims.Network()) == origin {
		firstContactTrust = protocol.KeyTrustVerified
	}
	switch {
	case pinned && pin.Fingerprint == envelope.OriginFingerprint:
		state, ok := signatureStateForTrust(pin.Trust)
		if !ok {
			// Only an explicit verified pin may ever yield "verified".
			logger.Error("refusing message: pinned key has an unknown trust level", "trust", pin.Trust)
			return nil, signatureRejectedError(fmt.Sprintf("network %q has a pin with unknown trust", origin))
		}
		result.record.State = state
		return result, nil
	case pinned && !(pin.Trust == protocol.KeyTrustUnconfirmed && firstContactTrust == protocol.KeyTrustVerified):
		logger.Warn("rejecting message signed by a key that conflicts with the pinned key",
			"pinned_fingerprint", pin.Fingerprint, "offered_fingerprint", envelope.OriginFingerprint)
		return nil, signatureRejectedError(fmt.Sprintf("network %q is pinned to a different key", origin))
	}
	result.record.State, _ = signatureStateForTrust(firstContactTrust)
	result.firstContact = &netkeys.Pin{
		NetworkID: origin, Fingerprint: envelope.OriginFingerprint, PublicKey: request.Envelope.PublicKey,
		Trust: firstContactTrust, Source: netkeys.PinSourcePairing, SourceID: pairing.ID,
	}
	return result, nil
}

func signatureStateForTrust(trust string) (string, bool) {
	switch trust {
	case protocol.KeyTrustVerified:
		return protocol.SignatureStateVerified, true
	case protocol.KeyTrustIntroduced:
		return protocol.SignatureStateIntroduced, true
	case protocol.KeyTrustUnconfirmed:
		return protocol.SignatureStateUnconfirmed, true
	}
	return "", false
}

// commitFirstContactPin writes an authorized message's first-contact pin.
// The returned undo removes exactly that pin if the append then fails.
func (s *Service) commitFirstContactPin(ctx context.Context, inbound *inboundEnvelope) (func(), error) {
	noop := func() {}
	if inbound == nil || inbound.firstContact == nil {
		return noop, nil
	}
	proposed := *inbound.firstContact
	logger := observability.Logger(ctx, "rooms.signing", "origin_network_id", proposed.NetworkID, "pairing_id", proposed.SourceID)
	pin, created, err := s.keyPins.PinKeyCreated(proposed)
	switch {
	case errors.Is(err, netkeys.ErrPinConflict):
		// A concurrent first contact pinned a different key first.
		logger.Warn("rejecting message signed by a key that conflicts with the pinned key", "offered_fingerprint", proposed.Fingerprint)
		return noop, signatureRejectedError(fmt.Sprintf("network %q is pinned to a different key", proposed.NetworkID))
	case err != nil:
		logger.Error("could not pin network key at first contact", "error", err)
		return noop, trustStoreUnavailableError(err)
	}
	if state, ok := signatureStateForTrust(pin.Trust); ok {
		inbound.record.State = state
	}
	if !created {
		return noop, nil
	}
	logger.Info("pinned network key at first contact over the pairing", "fingerprint", pin.Fingerprint, "trust", pin.Trust)
	return func() {
		if err := s.keyPins.ForgetExact(pin); err != nil {
			logger.Error("could not undo a first-contact pin after the message was not stored", "error", err)
		}
	}, nil
}
