package rooms

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// hubRoomForTarget resolves the hub room a room or thread target is in.
func (s *Service) hubRoomForTarget(ctx context.Context, target protocol.Target) (store.HubRoom, bool, error) {
	if s.hubs == nil || (target.Kind != protocol.TargetKindRoom && target.Kind != protocol.TargetKindThread) {
		return store.HubRoom{}, false, nil
	}
	roomID := strings.TrimSpace(target.RoomID)
	if target.Kind == protocol.TargetKindThread {
		if thread, ok, err := s.getThread(ctx, target.ThreadID); err != nil {
			return store.HubRoom{}, false, err
		} else if ok {
			roomID = thread.RoomID
		}
	}
	return s.hubRoomByLocal(ctx, roomID)
}

// enforceHubWrite gates local writes into a hub room: peers reach hub rooms
// only through the signed hub path, hub rooms are linear in v1, and this
// network's agents write under this network's role in the control log.
func (s *Service) enforceHubWrite(ctx context.Context, target protocol.Target, roomID string) error {
	hub, ok, err := s.hubRoomForTarget(ctx, target)
	if err != nil || !ok {
		return err
	}
	if claims, peer := authn.ClaimsFromContext(ctx); peer && claims.Allows(authn.ScopePair) && !claims.Operator() {
		return hubForbiddenError("hub room messages from peers must be signed hub submissions")
	}
	if target.Kind != protocol.TargetKindRoom {
		return hubInvalidError("hub rooms carry room messages only (no threads in v1)")
	}
	roster, _, err := s.hubRoster(ctx, hub, 0)
	if err != nil {
		return err
	}
	if _, writer := roster.canWrite(s.networkID); !writer {
		return writeForbiddenError(roomID)
	}
	return nil
}

// hubRelayRequest is the wire form of a hub-room message: the target is the
// home's room id (the canonical id inside the global room id), never this
// network's local alias for it.
func (s *Service) hubRelayRequest(message protocol.Message, hub store.HubRoom) (protocol.SendMessageRequest, error) {
	_, canonical, err := protocol.ParseGlobalRoomID(hub.GlobalRoomID)
	if err != nil {
		return protocol.SendMessageRequest{}, err
	}
	request, _ := s.relayRequest(protocol.Pairing{}, message)
	request.Target = protocol.Target{Kind: protocol.TargetKindRoom, RoomID: canonical}
	return request, nil
}

// signHubEnvelope signs a local message for a hub room under epoch.
func (s *Service) signHubEnvelope(message protocol.Message, hub store.HubRoom, epoch, originSeq int64, createdAt string) (store.MessageEnvelopeRecord, error) {
	request, err := s.hubRelayRequest(message, hub)
	if err != nil {
		return store.MessageEnvelopeRecord{}, err
	}
	envelope := protocol.EnvelopeFromRequest(request, s.identity.Fingerprint(), createdAt)
	envelope.GlobalRoomID, envelope.MembershipEpoch, envelope.OriginSeq = hub.GlobalRoomID, &epoch, &originSeq
	signed, digest, err := protocol.SignEnvelope(envelope, s.identity.PublicKey(), s.identity.Sign)
	if err != nil {
		return store.MessageEnvelopeRecord{}, invalidMessageRequestError("message cannot be signed for a hub room (" + err.Error() +
			"); send large integers and other non-portable numbers as strings")
	}
	return store.MessageEnvelopeRecord{OriginNetworkID: s.networkID, OriginMessageID: envelope.OriginMessageID, Digest: digest,
		State: protocol.SignatureStateVerified, Fingerprint: envelope.OriginFingerprint, Envelope: signed}, nil
}

// appendLocalHubMessage stores a local agent's hub-room message: signed
// under this network's current view of the epoch, placed and fanned out
// right here on the home, or queued for the home on a member.
func (s *Service) appendLocalHubMessage(ctx context.Context, request protocol.SendMessageRequest, message protocol.Message, event protocol.Event, hub store.HubRoom) (store.AppendLifecycle, error) {
	if err := s.requireHubs(); err != nil {
		return store.AppendLifecycle{}, err
	}
	roster, _, err := s.hubRoster(ctx, hub, 0)
	if err != nil {
		return store.AppendLifecycle{}, err
	}
	originSeq, err := s.hubs.NextHubOriginSeqContext(ctx, hub.GlobalRoomID)
	if err != nil {
		return store.AppendLifecycle{}, err
	}
	envelope, err := s.signHubEnvelope(message, hub, roster.Epoch, originSeq, message.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return store.AppendLifecycle{}, err
	}
	if !hub.IsHome() {
		return s.appendHub(ctx, request, message, event, envelope, nil, []string{hub.HomePairingID})
	}
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	placed, err := s.nextHubReceipt(ctx, hub, roster.Epoch, envelope.Digest)
	if err != nil {
		return store.AppendLifecycle{}, err
	}
	return s.appendHub(ctx, request, message, event, envelope, &placed, s.hubFanoutPairings(ctx, roster, s.networkID))
}

// placeHubMessage is the home placing a member's submission: under hubMu,
// an already placed event gets its existing seq back (idempotent retry),
// and the epoch is re-checked so no control change slips in between.
func (s *Service) placeHubMessage(ctx context.Context, hub store.HubRoom, signedEpoch int64, request protocol.SendMessageRequest, message protocol.Message, envelope store.MessageEnvelopeRecord) (protocol.MessageAccepted, error) {
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	if existing, ok, err := s.hubs.HubReceiptForEventContext(ctx, hub.GlobalRoomID, envelope.Digest); err != nil {
		return protocol.MessageAccepted{}, err
	} else if ok {
		return protocol.MessageAccepted{MessageID: existing.MessageID, EventID: eventIDForMessage(existing.MessageID), Accepted: true}, nil
	}
	roster, _, err := s.hubRoster(ctx, hub, 0)
	if err != nil {
		return protocol.MessageAccepted{}, err
	}
	if roster.Epoch != signedEpoch {
		return protocol.MessageAccepted{}, staleEpochError(roster.Epoch)
	}
	placed, err := s.nextHubReceipt(ctx, hub, roster.Epoch, envelope.Digest)
	if err != nil {
		return protocol.MessageAccepted{}, err
	}
	return s.appendHubMessage(ctx, request, message, envelope, &placed, s.hubFanoutPairings(ctx, roster, envelope.OriginNetworkID))
}

// nextHubReceipt signs the receipt for the next room seq. Callers hold hubMu.
func (s *Service) nextHubReceipt(ctx context.Context, hub store.HubRoom, epoch int64, eventID string) (store.HubReceiptRecord, error) {
	head, ok, err := s.hubs.HubReceiptHeadContext(ctx, hub.GlobalRoomID, protocol.HubEpochV1)
	if err != nil {
		return store.HubReceiptRecord{}, err
	}
	receipt := protocol.RoomReceipt{V: protocol.RoomReceiptVersionV1, GlobalRoomID: hub.GlobalRoomID, HubEpoch: protocol.HubEpochV1,
		MembershipEpoch: epoch, RoomSeq: 1, EventID: eventID}
	if ok {
		receipt.RoomSeq, receipt.PrevReceiptHash = head.RoomSeq+1, head.Digest
	}
	signed, digest, err := protocol.SignRoomReceipt(receipt, s.identity.Sign)
	if err != nil {
		return store.HubReceiptRecord{}, err
	}
	return store.HubReceiptRecord{GlobalRoomID: hub.GlobalRoomID, HubEpoch: receipt.HubEpoch, RoomSeq: receipt.RoomSeq,
		EventID: eventID, Digest: digest, PrevDigest: receipt.PrevReceiptHash, Receipt: signed}, nil
}

// appendHub commits message, envelope, receipt and fanout in one append.
func (s *Service) appendHub(ctx context.Context, request protocol.SendMessageRequest, message protocol.Message, event protocol.Event,
	envelope store.MessageEnvelopeRecord, placed *store.HubReceiptRecord, pairingIDs []string) (store.AppendLifecycle, error) {
	if s.pairingDeliveries == nil {
		return store.AppendLifecycle{}, hubUnavailableError("this message store does not support hub rooms")
	}
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	limits := make(map[string]store.PairingDeliveryLimits)
	for _, pairing := range s.snapshotRelayPairings() {
		age, size := deliveryBounds(pairing)
		limits[pairing.ID] = store.PairingDeliveryLimits{MaxAge: age, MaxPending: size, RemoteNetworkID: pairing.RemoteNetworkID}
	}
	appendCtx := store.WithPairingDeliveryLimits(store.WithMessageEnvelope(ctx, envelope), limits)
	if placed != nil {
		appendCtx = store.WithHubReceipt(appendCtx, *placed)
	}
	var durableEvent *protocol.Event
	if s.deliveryStore != nil {
		durableEvent = &event
	}
	lifecycle, err := s.pairingDeliveries.AppendMessageWithPairingDeliveriesContext(appendCtx, message, durableEvent, pairingIDs)
	if err != nil {
		return store.AppendLifecycle{}, err
	}
	s.publishAppended(ctx, request, message, event, lifecycle)
	return lifecycle, nil
}

// appendHubMessage is appendHub for peer traffic, with the acknowledgment
// rules: an identical redelivery is a duplicate; any conflicting receipt or
// envelope is refused and logged, never acknowledged.
func (s *Service) appendHubMessage(ctx context.Context, request protocol.SendMessageRequest, message protocol.Message,
	envelope store.MessageEnvelopeRecord, placed *store.HubReceiptRecord, pairingIDs []string) (protocol.MessageAccepted, error) {
	event := protocol.Event{ID: eventIDForMessage(message.ID), Type: protocol.EventTypeMessageCreated, NetworkID: s.networkID, Message: &message, CreatedAt: message.CreatedAt}
	_, err := s.appendHub(ctx, request, message, event, envelope, placed, pairingIDs)
	switch {
	case err == nil:
		s.relayMessage(message)
		return protocol.MessageAccepted{MessageID: message.ID, EventID: event.ID, Accepted: true}, nil
	case errors.Is(err, store.ErrDuplicateMessage):
		return protocol.MessageAccepted{MessageID: message.ID, EventID: event.ID, Accepted: true}, nil
	case errors.Is(err, store.ErrHubReceiptConflict), errors.Is(err, store.ErrEnvelopeConflict), errors.Is(err, store.ErrMessageIDConflict):
		logHub(ctx, message.Target.RoomID).Warn("refusing a hub message that conflicts with stored history", "message_id", message.ID, "error", err)
		return protocol.MessageAccepted{}, &Error{status: http.StatusConflict, msg: err.Error(), cause: err}
	}
	return protocol.MessageAccepted{}, err
}
