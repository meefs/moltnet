package rooms

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/noopolis/moltnet/internal/observability"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

const defaultDeliveryMaxAge = 7 * 24 * time.Hour
const defaultDeliveryMaxPending = 10000

var errDeliveryBlocked = errors.New("pairing delivery blocked")

func (s *Service) configureDelivery(config ServiceConfig) {
	s.deliveryRetryMin = positiveDuration(config.DeliveryRetryMin, time.Second)
	s.deliveryRetryMax = positiveDuration(config.DeliveryRetryMax, time.Minute)
	if s.deliveryRetryMax < s.deliveryRetryMin {
		s.deliveryRetryMax = s.deliveryRetryMin
	}
	s.deliveryPollInterval = positiveDuration(config.DeliveryPollInterval, 250*time.Millisecond)
	s.deliveryTimeout = positiveDuration(config.DeliveryTimeout, pairingRelayTimeout)
}

func positiveDuration(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

func (s *Service) outboundPairingIDs(message protocol.Message) []string {
	if !s.shouldRelayMessage(message) {
		return nil
	}
	var ids []string
	for _, pairing := range s.snapshotRelayPairings() {
		// Connectivity and compatibility are send-time concerns: neither can
		// veto queue insertion while the peer is unavailable.
		if pairingHasTransport(pairing) && s.pairingMatchesRelayTarget(pairing, message) {
			if _, ok := s.relayRequest(pairing, message); ok {
				ids = append(ids, pairing.ID)
			}
		}
	}
	return ids
}

// StartPairingDelivery resumes the durable outbox after initial configuration
// has been applied. New messages also start the worker after acceptance.
func (s *Service) StartPairingDelivery() {
	if s.pairingDeliveries == nil || s.pairingClient == nil {
		return
	}
	s.relayStart.Do(func() {
		s.relayWG.Add(1)
		go s.runDeliveryWorker()
	})
}

func (s *Service) wakeDeliveryWorker() {
	select {
	case s.relayWake <- struct{}{}:
	default:
	}
}

func (s *Service) runDeliveryWorker() {
	defer s.relayWG.Done()
	ticker := time.NewTicker(s.deliveryPollInterval)
	defer ticker.Stop()
	var jobs sync.WaitGroup
	defer jobs.Wait()
	active := make(map[string]bool)
	finished := make(chan string, 8)
	for {
		select {
		case <-s.lifecycleCtx.Done():
			return
		case id := <-finished:
			delete(active, id)
		case <-ticker.C:
		case <-s.relayWake:
		}
		if s.lifecycleCtx.Err() != nil {
			return
		}
		pairings := s.snapshotRelayPairings()
		current := make(map[string]protocol.Pairing, len(pairings))
		for _, pairing := range pairings {
			current[pairing.ID] = pairing
			if err := s.pruneDelivery(s.lifecycleCtx, pairing); err != nil {
				s.logDeliveryStoreError(err)
			}
		}
		heads, err := s.pairingDeliveries.ListPairingDeliveryHeadsContext(s.lifecycleCtx)
		if err != nil {
			s.logDeliveryStoreError(err)
			continue
		}
		for _, head := range heads {
			pairing, ok := current[head.PairingID]
			if !ok {
				// Removed/revoked pairings must not transmit; retained rows
				// still age out even when no active pairing references them.
				if err := s.pruneDelivery(s.lifecycleCtx, protocol.Pairing{ID: head.PairingID}); err != nil {
					s.logDeliveryStoreError(err)
				}
			}
			if !ok || active[head.PairingID] || len(active) >= 8 || head.NextAttempt.After(time.Now()) {
				continue
			}
			active[head.PairingID] = true
			jobs.Add(1)
			go func(pairing protocol.Pairing, head store.PairingDelivery) {
				defer jobs.Done()
				s.deliverQueuedMessage(pairing, head)
				select {
				case finished <- pairing.ID:
				case <-s.lifecycleCtx.Done():
				}
			}(pairing, head)
		}
	}
}

func (s *Service) deliverQueuedMessage(pairing protocol.Pairing, record store.PairingDelivery) {
	ctx, cancel := context.WithTimeout(s.lifecycleCtx, s.deliveryTimeout)
	defer cancel()
	err := s.sendQueuedMessage(ctx, pairing, record.Message)
	// A canceled lifecycle must leave the head intact for restart. Use the
	// lifecycle context for persistence, never the expired network deadline.
	if s.lifecycleCtx.Err() != nil {
		return
	}
	if err == nil {
		if err := s.pairingDeliveries.CompletePairingDeliveryContext(s.lifecycleCtx, pairing.ID, record.MessageID); err != nil {
			s.logDeliveryStoreError(err)
			return
		}
		observability.DefaultMetrics.RecordRelay("success")
		s.setPairingRelaySuccess(pairing.ID)
		return
	}
	record.Attempts++
	record.NextAttempt = time.Now().UTC().Add(s.deliveryBackoff(record.Attempts))
	if updateErr := s.pairingDeliveries.UpdatePairingDeliveryContext(s.lifecycleCtx, record, err.Error()); updateErr != nil {
		s.logDeliveryStoreError(updateErr)
	}
	observability.DefaultMetrics.RecordRelay("error")
	if !errors.Is(err, errDeliveryBlocked) {
		s.setPairingError(pairing.ID, "Remote relay request failed; delivery queued for retry.")
	}
	observability.Logger(s.lifecycleCtx, "rooms.relay", "message_id", record.MessageID, "pairing_id", pairing.ID).
		Warn("pairing delivery will retry", "attempts", record.Attempts, "next_attempt", record.NextAttempt, "error", err)
}

func (s *Service) sendQueuedMessage(ctx context.Context, pairing protocol.Pairing, message protocol.Message) error {
	if !s.shouldRelayMessage(message) || !s.pairingMatchesRelayTarget(pairing, message) {
		return fmt.Errorf("%w: current federation policy", errDeliveryBlocked)
	}
	scope := pairingCheckRelayRoom
	if message.Target.Kind == protocol.TargetKindDM {
		scope = pairingCheckRelayDM
	}
	status := s.currentPairingStatus(pairing)
	if s.pairingDiagnosticsNeedRefresh(status) || status.value == protocol.PairingStatusPending {
		if _, _, err := s.refreshPairingDiagnostics(ctx, pairing, scope); err != nil {
			return err
		}
	}
	if !s.shouldRelayToPairing(pairing, message) {
		return fmt.Errorf("%w: peer compatibility", errDeliveryBlocked)
	}
	request, ok := s.relayRequest(pairing, message)
	if !ok {
		return errors.New("queued delivery has invalid target")
	}
	accepted, err := s.pairingClient.RelayMessage(ctx, pairing, request)
	if err != nil {
		return err
	}
	if !accepted.Accepted {
		return errors.New("peer did not acknowledge message acceptance")
	}
	return nil
}

func (s *Service) deliveryBackoff(attempt int) time.Duration {
	delay := s.deliveryRetryMin
	for i := 1; i < attempt && delay < s.deliveryRetryMax; i++ {
		if delay > s.deliveryRetryMax/2 {
			delay = s.deliveryRetryMax
			break
		}
		delay *= 2
	}
	// Equal jitter keeps retries bounded and avoids synchronized reconnects.
	return delay/2 + time.Duration(rand.Int64N(max(int64(delay-delay/2), 1)))
}

func deliveryBounds(pairing protocol.Pairing) (time.Duration, int) {
	age, size := defaultDeliveryMaxAge, defaultDeliveryMaxPending
	if pairing.DeliveryLimits != nil {
		if pairing.DeliveryLimits.MaxAgeSeconds > 0 {
			age = time.Duration(pairing.DeliveryLimits.MaxAgeSeconds) * time.Second
		}
		if pairing.DeliveryLimits.MaxPending > 0 {
			size = pairing.DeliveryLimits.MaxPending
		}
	}
	return age, size
}

func (s *Service) pruneDelivery(ctx context.Context, pairing protocol.Pairing) error {
	age, size := deliveryBounds(pairing)
	dropped, err := s.pairingDeliveries.PrunePairingDeliveriesContext(ctx, pairing.ID, time.Now().UTC(), age, size)
	if err != nil {
		return err
	}
	s.logDroppedDeliveries(ctx, pairing.ID, dropped)
	return nil
}

func (s *Service) populateDeliveryStatus(ctx context.Context, pairing *protocol.Pairing) error {
	if s.pairingDeliveries == nil {
		return nil
	}
	state, err := s.pairingDeliveries.PairingDeliveryStateContext(ctx, pairing.ID)
	if err != nil {
		return fmt.Errorf("read pairing delivery status: %w", err)
	}
	age := float64(0)
	if !state.OldestPendingAt.IsZero() {
		age = max(0, time.Since(state.OldestPendingAt).Seconds())
	}
	pairing.Delivery = &protocol.PairingDeliveryStatus{Pending: state.Pending, OldestPendingAgeSeconds: age, LastError: state.LastError, Dropped: state.Dropped}
	return nil
}

func (s *Service) logDeliveryStoreError(err error) {
	if s.lifecycleCtx.Err() == nil {
		observability.Logger(s.lifecycleCtx, "rooms.relay").Error("pairing delivery persistence failed", "error", err)
	}
}

func (s *Service) logDroppedDeliveries(ctx context.Context, pairingID string, dropped int) {
	if dropped > 0 {
		observability.DefaultMetrics.RecordRelay("dropped")
		observability.Logger(ctx, "rooms.relay", "pairing_id", pairingID).
			Warn("pairing delivery bounds exceeded; oldest deliveries dropped", "dropped", dropped)
	}
}
