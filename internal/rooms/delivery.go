package rooms

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/noopolis/moltnet/internal/observability"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

const defaultDeliveryMaxAge = 7 * 24 * time.Hour
const defaultDeliveryMaxPending = 10000

var errDeliveryRejected = errors.New("pairing delivery rejected")

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
	active := make(map[string]bool)
	finished := make(chan store.PairingDeliveryResult, 8)
	var configuredDestinations map[string]string
	var pendingResults []store.PairingDeliveryResult
	defer func() {
		jobs.Wait()
		for len(finished) > 0 {
			pendingResults = append(pendingResults, <-finished)
		}
		if len(pendingResults) > 0 {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(s.lifecycleCtx), time.Second)
			defer cancel()
			if err := s.pairingDeliveries.ApplyPairingDeliveryResultsContext(ctx, pendingResults); err != nil {
				observability.Logger(ctx, "rooms.relay").Error("pairing delivery shutdown flush failed; results remain queued", "error", err)
			}
		}
	}()
	var persistAfter time.Time
	persistAttempts := 0
	for {
		select {
		case <-s.lifecycleCtx.Done():
			return
		case result := <-finished:
			pendingResults = append(pendingResults, result)
			for len(finished) > 0 {
				pendingResults = append(pendingResults, <-finished)
			}
		case <-ticker.C:
		case <-s.relayWake:
		}
		if s.lifecycleCtx.Err() != nil {
			return
		}
		if len(pendingResults) > 0 && !persistAfter.After(time.Now()) {
			// Keep these pairings active until their results commit. A store
			// outage retries persistence with backoff, never the network send.
			if err := s.pairingDeliveries.ApplyPairingDeliveryResultsContext(s.lifecycleCtx, pendingResults); err != nil {
				s.logDeliveryStoreError(err)
				persistAttempts++
				persistAfter = time.Now().Add(s.deliveryBackoff(persistAttempts))
			} else {
				for _, result := range pendingResults {
					delete(active, result.Delivery.PairingID)
				}
				pendingResults = nil
				persistAttempts = 0
				persistAfter = time.Time{}
			}
		}
		pairings := s.snapshotRelayPairings()
		destinations := make(map[string]string, len(pairings))
		for _, pairing := range pairings {
			destinations[pairing.ID] = pairing.RemoteNetworkID
		}
		if configuredDestinations == nil || !maps.Equal(configuredDestinations, destinations) {
			if err := s.reconcileDeliveryDestinations(destinations); err != nil {
				s.logDeliveryStoreError(err)
				continue // Fail closed: never send before stale destinations are purged.
			}
			configuredDestinations = destinations
		}
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
			if !ok || head.RemoteNetworkID != pairing.RemoteNetworkID || active[head.PairingID] || len(active) >= 8 || head.NextAttempt.After(time.Now()) {
				continue
			}
			active[head.PairingID] = true
			jobs.Add(1)
			go func(pairing protocol.Pairing, head store.PairingDelivery) {
				defer jobs.Done()
				result := s.deliverQueuedMessage(pairing, head)
				// At most eight active jobs share an eight-slot result channel,
				// so every result fits even while shutdown waits for the jobs.
				finished <- result
			}(pairing, head)
		}
	}
}

func (s *Service) deliverQueuedMessage(pairing protocol.Pairing, record store.PairingDelivery) store.PairingDeliveryResult {
	ctx, cancel := context.WithTimeout(s.lifecycleCtx, s.deliveryTimeout)
	defer cancel()
	err := s.sendQueuedMessage(ctx, pairing, record.Message)
	result := store.PairingDeliveryResult{Delivery: record}
	if err == nil {
		observability.DefaultMetrics.RecordRelay("success")
		s.setPairingRelaySuccess(pairing.ID)
		return result
	}
	if s.lifecycleCtx.Err() != nil && !deliveryRejected(err) {
		result.Retry, result.LastError = true, "delivery interrupted by shutdown"
		return result
	}
	if deliveryRejected(err) {
		result.RejectedReason = err.Error()
		observability.DefaultMetrics.RecordRelay("rejected")
		observability.Logger(s.lifecycleCtx, "rooms.relay", "message_id", record.MessageID, "pairing_id", pairing.ID).
			Warn("pairing delivery rejected", "reason", result.RejectedReason)
		return result
	}
	record.Attempts++
	record.NextAttempt = time.Now().UTC().Add(s.deliveryBackoff(record.Attempts))
	result.Delivery, result.Retry, result.LastError = record, true, err.Error()
	observability.DefaultMetrics.RecordRelay("error")
	s.setPairingError(pairing.ID, "Remote relay request failed; delivery queued for retry.")
	observability.Logger(s.lifecycleCtx, "rooms.relay", "message_id", record.MessageID, "pairing_id", pairing.ID).
		Warn("pairing delivery will retry", "attempts", record.Attempts, "next_attempt", record.NextAttempt, "error", err)
	return result
}

// Only explicit policy/compatibility refusals and definitive peer 4xx responses
// are rejected. Unknown failures (including ambiguous acknowledgments) retry.
func deliveryRejected(err error) bool {
	if errors.Is(err, errDeliveryRejected) {
		return true
	}
	var response interface{ StatusCode() int }
	if errors.As(err, &response) {
		code := response.StatusCode()
		return code >= 400 && code < 500 && code != 408 && code != 429
	}
	return false
}

func (s *Service) reconcileDeliveryDestinations(destinations map[string]string) error {
	dropped, err := s.pairingDeliveries.ReconcilePairingDeliveriesContext(s.lifecycleCtx, destinations)
	if err != nil {
		return err
	}
	for id, count := range dropped {
		observability.Logger(s.lifecycleCtx, "rooms.relay", "pairing_id", id).
			Warn("pairing destination removed or changed; backlog purged", "dropped", count)
	}
	return nil
}

func (s *Service) sendQueuedMessage(ctx context.Context, pairing protocol.Pairing, message protocol.Message) error {
	if message.Target.Kind == protocol.TargetKindDM && s.disableDirectMessages {
		return fmt.Errorf("%w: local direct messages disabled", errDeliveryRejected)
	}
	if message.Origin.NetworkID != s.networkID || message.Origin.ReceivedVia != "" {
		return fmt.Errorf("%w: invalid relay origin", errDeliveryRejected)
	}
	if message.Target.Kind == protocol.TargetKindRoom || message.Target.Kind == protocol.TargetKindThread {
		room, err := s.roomForWrite(ctx, message.Target)
		if err != nil {
			return err // Store failures remain retryable; a missing target is a 404.
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
	status := s.currentPairingStatus(pairing)
	if s.pairingDiagnosticsNeedRefresh(status) || status.value == protocol.PairingStatusPending {
		if _, _, err := s.refreshPairingDiagnostics(ctx, pairing, scope); err != nil {
			return err
		}
	}
	if !s.shouldRelayToPairing(pairing, message) {
		status = s.currentPairingStatus(pairing)
		if status.value == protocol.PairingStatusPending || status.value == protocol.PairingStatusError || status.value == protocol.PairingStatusUnknown {
			return errors.New("peer is not ready for relay")
		}
		return fmt.Errorf("%w: %s", errDeliveryRejected, status.diagnosticsMessage("peer does not support this delivery"))
	}
	request, ok := s.relayRequest(pairing, message)
	if !ok {
		return fmt.Errorf("%w: invalid target", errDeliveryRejected)
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
	pairing.Delivery = &protocol.PairingDeliveryStatus{Pending: state.Pending, OldestPendingAgeSeconds: age, LastError: state.LastError, Dropped: state.Dropped, Rejected: state.Rejected, LastRejectionReason: state.LastRejectionReason}
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
