package rooms

import (
	"context"
	"errors"
	"fmt"

	"github.com/noopolis/moltnet/internal/netkeys"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// hubIngress is a pair-scoped hub-room message after the checks every hub
// path shares: a strictly parsed, signature-valid envelope that states
// exactly the delivered message for one known hub room.
type hubIngress struct {
	pairing  protocol.Pairing
	hub      store.HubRoom
	envelope protocol.MessageEnvelope
	digest   string
	request  protocol.SendMessageRequest
}

// isHubRequest reports whether a pair-scoped request claims a hub room. It
// is the signed-envelope branch of sender validation: such requests never
// reach the ordinary pairing path, and nothing about that path changes.
func isHubRequest(request protocol.SendMessageRequest) bool {
	if request.Receipt != nil {
		return true
	}
	if request.Envelope == nil {
		return false
	}
	envelope, _, err := protocol.ParseSignedEnvelope(*request.Envelope)
	return err == nil && envelope.GlobalRoomID != ""
}

// receiveHubMessage is SendMessageContext for hub traffic from a peer: a
// member's submission to this home, or a home forwarding to this member.
func (s *Service) receiveHubMessage(ctx context.Context, request protocol.SendMessageRequest) (protocol.MessageAccepted, error) {
	ingress, err := s.checkHubIngress(ctx, request)
	if err != nil {
		return protocol.MessageAccepted{}, err
	}
	if ingress.hub.IsHome() {
		return s.acceptHubSubmission(ctx, ingress)
	}
	return s.acceptHubForward(ctx, ingress, true)
}

func (s *Service) checkHubIngress(ctx context.Context, request protocol.SendMessageRequest) (hubIngress, error) {
	pairing, ok := s.pairingForPairScopedContext(ctx)
	if !ok {
		return hubIngress{}, hubForbiddenError("hub room messages arrive only signed, over a pairing")
	}
	return s.checkHubIngressFrom(ctx, pairing, request)
}

// checkHubIngressFrom is checkHubIngress for a request known to come over
// pairing: live over its credential, or pulled from the home by sync.
func (s *Service) checkHubIngressFrom(ctx context.Context, pairing protocol.Pairing, request protocol.SendMessageRequest) (hubIngress, error) {
	if s.hubs == nil || request.Envelope == nil {
		return hubIngress{}, hubForbiddenError("hub room messages arrive only signed, over a pairing")
	}
	envelope, digest, err := protocol.VerifySignedEnvelope(*request.Envelope)
	if err != nil {
		return hubIngress{}, signatureRejectedError("invalid message envelope: " + err.Error())
	}
	hub, known, err := s.hubRoomByGlobal(ctx, envelope.GlobalRoomID)
	if err != nil {
		return hubIngress{}, err
	}
	if !known {
		return hubIngress{}, hubNotFoundError(envelope.GlobalRoomID)
	}
	_, canonicalRoom, _ := protocol.ParseGlobalRoomID(envelope.GlobalRoomID)
	origin := request.Origin.NetworkID
	switch {
	case envelope.OriginNetworkID != origin || origin == "" || origin == s.networkID:
		return hubIngress{}, signatureRejectedError("hub message origin does not match its signed origin")
	case envelope.MembershipEpoch == nil || envelope.OriginSeq == nil || *envelope.OriginSeq < 1:
		return hubIngress{}, signatureRejectedError("hub message envelope lacks membership_epoch or origin_seq")
	case envelope.Target.Kind != protocol.TargetKindRoom || envelope.Target.RoomID != canonicalRoom || request.Target.RoomID != canonicalRoom:
		// The signed target binds the message to this room and no other:
		// replaying it into another hub room changes the global room id.
		return hubIngress{}, signatureRejectedError("hub message target is not this hub room")
	case !actorHasExplicitConsistentNetworkID(request.From, origin):
		return hubIngress{}, signatureRejectedError("hub message author is not of its origin network")
	case request.From.FQID != "" && request.From.FQID != protocol.AgentFQID(origin, envelope.Author.ID):
		return hubIngress{}, signatureRejectedError("author fqid does not match the signed origin and author")
	}
	if err := protocol.EnvelopeMatchesRequest(envelope, request); err != nil {
		return hubIngress{}, signatureRejectedError(err.Error())
	}
	return hubIngress{pairing: pairing, hub: hub, envelope: envelope, digest: digest, request: request}, nil
}

// acceptHubSubmission is the home's acceptance rule: transport auth (the
// member link speaks for the origin) AND origin signature (the key the
// roster and this home's pin agree on) AND writer role at the current
// epoch AND the envelope signed under that epoch.
func (s *Service) acceptHubSubmission(ctx context.Context, in hubIngress) (protocol.MessageAccepted, error) {
	logger := logHub(ctx, in.hub.GlobalRoomID).With("origin_network_id", in.envelope.OriginNetworkID, "pairing_id", in.pairing.ID)
	if in.request.Receipt != nil {
		return protocol.MessageAccepted{}, hubForbiddenError("members submit messages; only the home issues receipts")
	}
	origin := in.envelope.OriginNetworkID
	// Transport authorization is the member link recorded for this room
	// (hub_links.go), not the ordinary pairing binding: a member may also
	// hold a direct pairing or other rooms' links to this home.
	if linked, err := s.hubLinkSpeaksFor(ctx, in.hub.GlobalRoomID, in.pairing, origin); err != nil {
		return protocol.MessageAccepted{}, err
	} else if !linked {
		logger.Warn("refusing hub submission over a pairing that is not the origin's link for this room")
		return protocol.MessageAccepted{}, hubForbiddenError("this pairing does not speak for the message's origin network in this room")
	}
	// A retry of an event already placed gets its placement back before any
	// epoch or role check: the ack may have been lost while the room moved
	// on, and a 412 here would make the sender re-sign a delivered message.
	if existing, placed, err := s.hubs.HubReceiptForEventContext(ctx, in.hub.GlobalRoomID, in.digest); err != nil {
		return protocol.MessageAccepted{}, err
	} else if placed {
		return protocol.MessageAccepted{MessageID: existing.MessageID, EventID: eventIDForMessage(existing.MessageID), Accepted: true, Receipt: &existing.Receipt}, nil
	}
	roster, _, err := s.hubRoster(ctx, in.hub, 0)
	if err != nil {
		return protocol.MessageAccepted{}, err
	}
	member, writer := roster.canWrite(origin)
	if !writer {
		logger.Warn("refusing hub submission from a network that may not write", "epoch", roster.Epoch)
		return protocol.MessageAccepted{}, hubForbiddenError(fmt.Sprintf("network %q may not write in this hub room", origin))
	}
	if member.Fingerprint != in.envelope.OriginFingerprint {
		logger.Warn("refusing hub submission signed by a key other than the member's")
		return protocol.MessageAccepted{}, signatureRejectedError("hub message is not signed by the member's key")
	}
	state, err := s.hubOriginState(origin, member, false)
	if err != nil {
		return protocol.MessageAccepted{}, err
	}
	if *in.envelope.MembershipEpoch != roster.Epoch {
		logger.Info("hub submission signed under a stale membership epoch", "signed_epoch", *in.envelope.MembershipEpoch, "epoch", roster.Epoch)
		return protocol.MessageAccepted{}, staleEpochError(roster.Epoch)
	}
	message, err := s.hubMessage(ctx, in)
	if err != nil {
		return protocol.MessageAccepted{}, err
	}
	envelope := store.MessageEnvelopeRecord{OriginNetworkID: origin, OriginMessageID: in.envelope.OriginMessageID,
		Digest: in.digest, State: state, Fingerprint: in.envelope.OriginFingerprint, Envelope: *in.request.Envelope}
	if err := s.keyPins.BindPairing(in.pairing.ID, origin); err != nil {
		return protocol.MessageAccepted{}, trustStoreUnavailableError(err)
	}
	return s.placeHubMessage(ctx, in.hub, *in.envelope.MembershipEpoch, in.request, message, envelope)
}

// acceptHubForward is a member's acceptance rule for home-forwarded
// messages: only over the room's home link, with a receipt the home key
// signed for this event, from an origin that may write at the signed epoch
// in this member's own copy of the control log, signed by the key that log
// names, and never contradicting a key this network pinned itself. A live
// forward also extends (or, past a gap, schedules catch-up of) the verified
// receipt chain; sync does that itself for the entries it pulls.
func (s *Service) acceptHubForward(ctx context.Context, in hubIngress, live bool) (protocol.MessageAccepted, error) {
	logger := logHub(ctx, in.hub.GlobalRoomID).With("origin_network_id", in.envelope.OriginNetworkID, "pairing_id", in.pairing.ID)
	if in.pairing.ID != in.hub.HomePairingID {
		logger.Warn("refusing hub message that did not come from the room's home")
		return protocol.MessageAccepted{}, hubForbiddenError("hub room messages are accepted only from the room's home")
	}
	if in.request.Receipt == nil {
		return protocol.MessageAccepted{}, hubForbiddenError("home-forwarded hub messages carry a receipt")
	}
	epoch := *in.envelope.MembershipEpoch
	roster, _, err := s.hubRoster(ctx, in.hub, 0)
	if err != nil {
		return protocol.MessageAccepted{}, err
	}
	if epoch > roster.Epoch {
		if roster, err = s.syncHubControl(ctx, in.hub); err != nil {
			return protocol.MessageAccepted{}, err
		}
		if epoch > roster.Epoch {
			return protocol.MessageAccepted{}, hubUnavailableError("hub room control log has not reached the message's epoch yet")
		}
	}
	receipt, receiptDigest, err := protocol.VerifyRoomReceipt(*in.request.Receipt, roster.HomeKey)
	if err != nil {
		logger.Warn("refusing hub message with an invalid receipt", "error", err)
		return protocol.MessageAccepted{}, signatureRejectedError("invalid room receipt: " + err.Error())
	}
	if receipt.GlobalRoomID != in.hub.GlobalRoomID || receipt.EventID != in.digest || receipt.MembershipEpoch != epoch || receipt.HubEpoch != protocol.HubEpochV1 {
		logger.Warn("refusing hub message whose receipt places a different event")
		return protocol.MessageAccepted{}, signatureRejectedError("room receipt does not place this message")
	}
	// Epoch order along room seqs (no rollback, in any delivery order) is
	// enforced by the store inside the atomic receipt append; the receipt
	// right after the verified cursor must chain to it.
	if live {
		if err := s.checkCursorSuccessor(ctx, in.hub, receipt); err != nil {
			logger.Warn("refusing hub message whose receipt does not chain to the verified sync cursor", "error", err)
			return protocol.MessageAccepted{}, err
		}
	}
	at, _, err := s.hubRoster(ctx, in.hub, epoch)
	if err != nil {
		return protocol.MessageAccepted{}, err
	}
	origin := in.envelope.OriginNetworkID
	member, writer := at.canWrite(origin)
	if !writer || member.Fingerprint != in.envelope.OriginFingerprint {
		logger.Warn("refusing hub message from an origin that could not write at its epoch, or under another key", "epoch", epoch)
		return protocol.MessageAccepted{}, hubForbiddenError(fmt.Sprintf("network %q could not write with this key at epoch %d", origin, epoch))
	}
	state, err := s.hubOriginState(origin, member, true)
	if err != nil {
		return protocol.MessageAccepted{}, err
	}
	message, err := s.hubMessage(ctx, in)
	if err != nil {
		return protocol.MessageAccepted{}, err
	}
	envelope := store.MessageEnvelopeRecord{OriginNetworkID: origin, OriginMessageID: in.envelope.OriginMessageID,
		Digest: in.digest, State: state, Fingerprint: in.envelope.OriginFingerprint, Envelope: *in.request.Envelope}
	placed := store.HubReceiptRecord{GlobalRoomID: receipt.GlobalRoomID, HubEpoch: receipt.HubEpoch, MembershipEpoch: receipt.MembershipEpoch, RoomSeq: receipt.RoomSeq,
		EventID: receipt.EventID, Digest: receiptDigest, PrevDigest: receipt.PrevReceiptHash, Receipt: *in.request.Receipt}
	var introduce *netkeys.Pin
	if state == protocol.SignatureStateIntroduced {
		if _, pinned, _ := s.keyPins.Lookup(origin); !pinned {
			pin := introducedPin(at, member)
			introduce = &pin
		}
	}
	accepted, err := s.storeHubForward(ctx, in, message, envelope, placed, introduce)
	if live && err == nil {
		s.notePlacement(ctx, in.hub, placed)
	}
	return accepted, err
}

// hubOriginState labels an origin's signature by this network's own pin
// for it. A key this network pinned (any trust) that differs from the
// roster's is a forgery attempt by the home and is refused. With no pin, a
// member records the roster key as introduced by the home (allowIntroduce);
// a home never meets a member it did not pin itself.
func (s *Service) hubOriginState(origin string, member hubMember, allowIntroduce bool) (string, error) {
	pin, pinned, err := s.keyPins.Lookup(origin)
	if err != nil {
		return "", trustStoreUnavailableError(err)
	}
	if !pinned {
		if !allowIntroduce {
			return "", signatureRejectedError(fmt.Sprintf("network %q has no pinned key on this home", origin))
		}
		return protocol.SignatureStateIntroduced, nil
	}
	if pin.Fingerprint != member.Fingerprint {
		return "", signatureRejectedError(fmt.Sprintf("network %q is pinned to a different key than the room introduces", origin))
	}
	state, ok := signatureStateForTrust(pin.Trust)
	if !ok {
		return "", signatureRejectedError(fmt.Sprintf("network %q has a pin with unknown trust", origin))
	}
	return state, nil
}

func (s *Service) hubMessage(ctx context.Context, in hubIngress) (protocol.Message, error) {
	now, err := s.now()
	if err != nil {
		return protocol.Message{}, err
	}
	if err := validateSendMessageRequest(in.request); err != nil {
		return protocol.Message{}, err
	}
	origin := in.envelope.OriginNetworkID
	id := hubMessageID(in.digest)
	author := in.envelope.Author
	// The link it came over, whether delivered live or pulled by sync, so
	// both paths store the same projection and dedupe against each other.
	received := s.normalizeOrigin(ctx, protocol.MessageOrigin{NetworkID: origin, MessageID: in.envelope.OriginMessageID}, id)
	received.ReceivedVia = in.pairing.ID
	return protocol.Message{
		ID: id, NetworkID: s.networkID,
		Origin: received,
		Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: in.hub.LocalRoomID},
		From:   protocol.NormalizeActor(origin, protocol.Actor{Type: author.Type, ID: author.ID, Name: author.Name, NetworkID: origin}),
		Parts:  append([]protocol.Part(nil), in.request.Parts...),
		// Mentions stay as signed: third-network agents are not resolved
		// against this network's membership.
		Mentions:  append([]string(nil), in.envelope.Mentions...),
		CreatedAt: now,
	}, nil
}

func (s *Service) storeHubForward(ctx context.Context, in hubIngress, message protocol.Message, envelope store.MessageEnvelopeRecord, placed store.HubReceiptRecord, introduce *netkeys.Pin) (protocol.MessageAccepted, error) {
	// The introduced pin is recorded once the forward is fully authorized
	// and is never undone, so a failed append cannot remove a pin another
	// accepted message was checked against.
	if introduce != nil {
		pin, created, err := s.keyPins.PinKeyCreated(*introduce)
		switch {
		case errors.Is(err, netkeys.ErrPinConflict):
			return protocol.MessageAccepted{}, signatureRejectedError(fmt.Sprintf("network %q is pinned to a different key", introduce.NetworkID))
		case err != nil:
			return protocol.MessageAccepted{}, trustStoreUnavailableError(err)
		case created:
			logHub(ctx, in.hub.GlobalRoomID).Info("pinned a network key introduced by the room home", "network_id", pin.NetworkID, "fingerprint", pin.Fingerprint)
		}
	}
	return s.appendHubMessage(ctx, in.request, message, envelope, &placed, nil)
}
