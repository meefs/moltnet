package rooms

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/noopolis/moltnet/internal/netkeys"
	"github.com/noopolis/moltnet/internal/observability"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// verifyInboundEnvelope applies the chunk-A acceptance rule to a message
// relayed over a direct pairing. It runs after validateSenderIdentity, so
// the origin network is already bound to the presenting pair credential.
//
//   - signed: the envelope must parse strictly, verify under its attached
//     key, name the same origin, and state exactly the delivered message.
//     The key must match this network's pin for the origin; with no pin,
//     the key is pinned now as verified (first contact over the
//     authenticated pairing -- this is how an inviter learns its joiner's key).
//   - unsigned: accepted as unsigned only while the origin has no pin. Once
//     a key is pinned, stripping the envelope is a downgrade and is refused.
//
// The returned record is committed with the message; nil means unsigned or
// not a pairing relay at all (local callers can never supply an envelope).
func (s *Service) verifyInboundEnvelope(ctx context.Context, request protocol.SendMessageRequest) (*store.MessageEnvelopeRecord, error) {
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

	state := protocol.SignatureStateVerified
	switch {
	case pinned && pin.Fingerprint != envelope.OriginFingerprint:
		logger.Warn("rejecting message signed by a key that conflicts with the pinned key",
			"pinned_fingerprint", pin.Fingerprint, "offered_fingerprint", envelope.OriginFingerprint)
		return nil, signatureRejectedError(fmt.Sprintf("network %q is pinned to a different key", origin))
	case pinned && pin.Trust == protocol.KeyTrustIntroduced:
		state = protocol.SignatureStateIntroduced
	case pinned && pin.Trust != protocol.KeyTrustVerified:
		// Only an explicit verified pin may ever yield "verified".
		logger.Error("refusing message: pinned key has an unknown trust level", "trust", pin.Trust)
		return nil, signatureRejectedError(fmt.Sprintf("network %q has a pin with unknown trust", origin))
	case !pinned:
		if err := s.pinFirstContact(ctx, origin, pairing.ID, envelope.OriginFingerprint, request.Envelope.PublicKey); err != nil {
			return nil, err
		}
	}
	return &store.MessageEnvelopeRecord{
		OriginNetworkID: origin, OriginMessageID: envelope.OriginMessageID, Digest: digest,
		State: state, Fingerprint: envelope.OriginFingerprint, Envelope: *request.Envelope,
	}, nil
}

func (s *Service) pinFirstContact(ctx context.Context, origin, pairingID, fingerprint, publicKey string) error {
	logger := observability.Logger(ctx, "rooms.signing", "origin_network_id", origin, "pairing_id", pairingID)
	pin, err := s.keyPins.PinKey(netkeys.Pin{
		NetworkID: origin, Fingerprint: fingerprint, PublicKey: publicKey,
		Trust: protocol.KeyTrustVerified, Source: netkeys.PinSourcePairing, SourceID: pairingID,
	})
	switch {
	case errors.Is(err, netkeys.ErrPinConflict):
		// A concurrent first contact pinned a different key first.
		logger.Warn("rejecting message signed by a key that conflicts with the pinned key", "offered_fingerprint", fingerprint)
		return signatureRejectedError(fmt.Sprintf("network %q is pinned to a different key", origin))
	case err != nil:
		logger.Error("could not pin network key at first contact", "error", err)
		return trustStoreUnavailableError(err)
	}
	logger.Info("pinned network key at first contact over the pairing", "fingerprint", pin.Fingerprint)
	return nil
}
