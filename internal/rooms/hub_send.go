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
		network, err := s.pairingNetworkID(pairing)
		if err != nil {
			return err
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
	if !hub.IsHome() {
		if envelope, err = s.currentEpochEnvelope(ctx, hub, message, *envelope, false); err != nil {
			return err
		}
	}
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
		if request.Envelope, err = s.currentEpochEnvelope(ctx, hub, message, *envelope, true); err != nil {
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
	return nil
}

func staleEpochResponse(err error) bool {
	var response interface{ StatusCode() int }
	return err != nil && errors.As(err, &response) && response.StatusCode() == http.StatusPreconditionFailed
}

// currentEpochEnvelope returns envelope if it was signed under this
// network's current epoch for the room, else re-signs the same message
// (same origin seq and created_at) under it and replaces the stored copy.
// sync first pulls the control log from the home.
func (s *Service) currentEpochEnvelope(ctx context.Context, hub store.HubRoom, message protocol.Message, envelope protocol.SignedEnvelope, sync bool) (*protocol.SignedEnvelope, error) {
	roster, _, err := s.hubRoster(ctx, hub, 0)
	if sync {
		roster, err = s.syncHubControl(ctx, hub)
	}
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
