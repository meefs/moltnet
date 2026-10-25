package rooms

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func (s *Service) sendQueuedMessage(ctx context.Context, pairing protocol.Pairing, record *store.PairingDelivery) error {
	message := record.Message
	if message.Target.Kind == protocol.TargetKindDM && s.disableDirectMessages {
		return fmt.Errorf("%w: local direct messages disabled", errDeliveryRejected)
	}
	if hub, ok, err := s.hubRoomForTarget(ctx, message.Target); err != nil {
		return err
	} else if ok {
		return s.sendQueuedHubMessage(ctx, pairing, record, hub)
	}
	if message.Origin.NetworkID != s.networkID || message.Origin.ReceivedVia != "" {
		return fmt.Errorf("%w: invalid relay origin", errDeliveryRejected)
	}
	if message.Target.Kind == protocol.TargetKindRoom || message.Target.Kind == protocol.TargetKindThread {
		room, err := s.roomForWrite(ctx, message.Target)
		if err != nil {
			var response interface{ StatusCode() int }
			if errors.As(err, &response) && response.StatusCode() == 404 {
				return fmt.Errorf("%w: %v", errDeliveryRejected, err)
			}
			return err
		}
		if room.Follow != nil || !protocol.RoomFederationAllows(room.Federation, pairing.ID) {
			return fmt.Errorf("%w: current federation policy", errDeliveryRejected)
		}
	} else if !s.pairingMatchesRelayTarget(pairing, message) {
		return fmt.Errorf("%w: current target policy", errDeliveryRejected)
	}
	scope := pairingCheckRelayRoom
	if message.Target.Kind == protocol.TargetKindDM {
		scope = pairingCheckRelayDM
	}
	// Prove credentials and route on every attempt, including when metadata is
	// cached. A generic proxy 404 must never become a message rejection.
	network, _, err := s.refreshPairingDiagnostics(ctx, pairing, scope)
	if err != nil {
		return err
	}
	remote := strings.TrimSpace(network.ID)
	if remote == "" {
		return errors.New("peer discovery returned no network id")
	}
	if !s.shouldRelayToPairing(pairing, message) {
		status := s.currentPairingStatus(pairing)
		if message.Target.Kind == protocol.TargetKindDM && status.value == protocol.PairingStatusDegraded &&
			status.diagnostics != nil && status.diagnostics.Reason == pairingDiagnosticDirectMessagesOff {
			return fmt.Errorf("%w: %s", errDeliveryRejected, status.diagnosticsMessage("peer direct messages disabled"))
		}
		return errors.New(status.diagnosticsMessage("peer is not ready for this delivery"))
	}
	if record.RemoteNetworkID != "" && record.RemoteNetworkID != remote {
		return errors.New("peer destination changed; queued delivery purged")
	}
	record.RemoteNetworkID = remote
	request, ok := s.relayRequest(pairing, message)
	if !ok {
		return fmt.Errorf("%w: invalid target", errDeliveryRejected)
	}
	if request.Envelope, err = s.storedOutboundEnvelope(ctx, message.ID); err != nil {
		return err
	}
	accepted, err := s.pairingClient.RelayMessage(ctx, pairing, request)
	if err != nil {
		return deliveryMessageError{err}
	}
	if !accepted.Accepted {
		return errors.New("peer did not acknowledge message acceptance")
	}
	return nil
}
