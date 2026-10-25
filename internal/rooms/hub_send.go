package rooms

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/noopolis/moltnet/internal/observability"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// sendQueuedHubMessage delivers one outbox row of a hub room. It replaces
// the ordinary no-onward-relay gates for exactly two shapes: the home fanning
// a placed message out to a current member other than its origin, and a
// member sending its own local message to the room's home. Members never
// forward anything.
func (s *Service) sendQueuedHubMessage(ctx context.Context, pairing protocol.Pairing, record *store.PairingDelivery, hub store.HubRoom) error {
	message := record.Message
	var receipt *protocol.SignedRoomReceipt
	if hub.IsHome() {
		roster, _, err := s.hubRoster(ctx, hub, 0)
		if err != nil {
			return err
		}
		network, linked, err := s.hubLinkNetwork(hub.GlobalRoomID, pairing.ID)
		if err != nil {
			return err
		}
		if !linked {
			return fmt.Errorf("%w: pairing %s is no member link of this hub room", errDeliveryRejected, pairing.ID)
		}
		if _, member := roster.Members[network]; !member || network == s.networkID || network == message.Origin.NetworkID {
			return fmt.Errorf("%w: %s is not a current member to fan out to", errDeliveryRejected, network)
		}
		receipts, err := s.hubs.HubReceiptsForMessagesContext(ctx, []string{message.ID})
		if err != nil {
			return err
		}
		placed, ok := receipts[message.ID]
		if !ok {
			return fmt.Errorf("%w: hub message has no receipt", errDeliveryRejected)
		}
		receipt = &placed.Receipt
	} else if message.Origin.NetworkID != s.networkID || message.Origin.ReceivedVia != "" || pairing.ID != hub.HomePairingID {
		return fmt.Errorf("%w: members send only their own messages, only to the room's home", errDeliveryRejected)
	}
	envelope, err := s.storedOutboundEnvelope(ctx, message.ID)
	if err != nil {
		return err
	}
	if envelope == nil {
		return fmt.Errorf("%w: hub message has no signed envelope", errDeliveryRejected)
	}
	// A member always sends the bytes it stored first, even if it has since
	// learned a newer epoch: the home may already have placed them (a lost
	// ack). It re-signs only when the home answers 412, which the home sends
	// only for an event it has not placed.
	if _, _, err := s.refreshPairingDiagnostics(ctx, pairing, pairingCheckRelayRoom); err != nil {
		return err
	}
	request, err := s.hubRelayRequest(message, hub)
	if err != nil {
		return fmt.Errorf("%w: %v", errDeliveryRejected, err)
	}
	request.Envelope, request.Receipt = envelope, receipt
	accepted, err := s.pairingClient.RelayMessage(ctx, pairing, request)
	if !hub.IsHome() && staleEpochResponse(err) {
		// Retry-class, never a rejection: learn the new roster first (the
		// join notice), re-sign under the new epoch, resend once.
		if request.Envelope, err = s.currentEpochEnvelope(ctx, hub, message, *envelope); err != nil {
			return err
		}
		accepted, err = s.pairingClient.RelayMessage(ctx, pairing, request)
		if staleEpochResponse(err) {
			return errors.New("hub room membership changed again while re-signing; will retry")
		}
	}
	if err != nil {
		return deliveryMessageError{err}
	}
	if !accepted.Accepted {
		return errors.New("peer did not acknowledge message acceptance")
	}
	if !hub.IsHome() && accepted.Receipt != nil {
		// Delivered either way; a receipt that does not verify is logged and
		// left for sync to settle, never a reason to resend.
		if err := s.attachOwnReceipt(ctx, hub, message.ID, *accepted.Receipt); err != nil {
			logHub(ctx, hub.GlobalRoomID).Warn("home acknowledged a message with a receipt this member cannot attach", "message_id", message.ID, "error", err)
		} else if placed, ok, err := s.ownReceipt(ctx, message.ID); err == nil && ok {
			s.notePlacement(ctx, hub, placed)
		}
	}
	return nil
}

func (s *Service) ownReceipt(ctx context.Context, messageID string) (store.HubReceiptRecord, bool, error) {
	receipts, err := s.hubs.HubReceiptsForMessagesContext(ctx, []string{messageID})
	if err != nil {
		return store.HubReceiptRecord{}, false, err
	}
	placed, ok := receipts[messageID]
	return placed, ok, nil
}

func staleEpochResponse(err error) bool {
	var response interface{ StatusCode() int }
	return err != nil && errors.As(err, &response) && response.StatusCode() == http.StatusPreconditionFailed
}

// currentEpochEnvelope returns envelope if it was signed under this
// network's current epoch for the room, else re-signs the same message
// (same origin seq and created_at) under it and replaces the stored copy.
// It first pulls the control log from the home, and runs only after the
// home answered 412 (the event was not placed).
func (s *Service) currentEpochEnvelope(ctx context.Context, hub store.HubRoom, message protocol.Message, envelope protocol.SignedEnvelope) (*protocol.SignedEnvelope, error) {
	roster, err := s.syncHubControl(ctx, hub)
	if err != nil {
		return nil, err
	}
	parsed, _, err := protocol.ParseSignedEnvelope(envelope)
	if err != nil || parsed.MembershipEpoch == nil || parsed.OriginSeq == nil {
		return nil, fmt.Errorf("%w: stored hub envelope is unusable", errDeliveryRejected)
	}
	if *parsed.MembershipEpoch == roster.Epoch {
		return &envelope, nil
	}
	if _, writer := roster.canWrite(s.networkID); !writer {
		return nil, fmt.Errorf("%w: this network may no longer write in the hub room", errDeliveryRejected)
	}
	record, err := s.signHubEnvelope(message, hub, roster.Epoch, *parsed.OriginSeq, parsed.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errDeliveryRejected, err)
	}
	record.MessageID = message.ID
	if err := s.hubs.ReplaceOutboundEnvelopeContext(ctx, record); err != nil {
		return nil, err
	}
	observability.Logger(ctx, "rooms.hub", "global_room_id", hub.GlobalRoomID, "message_id", message.ID).
		Info("re-signed a queued hub message under the room's new membership epoch", "from_epoch", *parsed.MembershipEpoch, "to_epoch", roster.Epoch)
	return &record.Envelope, nil
}
