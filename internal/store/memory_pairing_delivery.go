package store

import (
	"context"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func (s *MemoryStore) AppendMessageWithPairingDeliveriesContext(
	ctx context.Context, message protocol.Message, event *protocol.Event, pairingIDs []string,
) (AppendLifecycle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lifecycle, err := s.appendMessageLocked(message, envelopeForMessage(ctx, message))
	if err != nil {
		return AppendLifecycle{}, err
	}
	if event != nil {
		s.deliveryEvents = append(s.deliveryEvents, *event)
	}
	now := time.Now().UTC()
	limits := pairingDeliveryLimitsFromContext(ctx)
	for _, id := range protocol.UniqueTrimmedStrings(pairingIDs) {
		remote := limits[id].RemoteNetworkID
		if remote == "" {
			remote = s.pairingDeliveryStates[id].RemoteNetworkID
		}
		state := s.pairingDeliveryStates[id]
		state.RemoteNetworkID = remote
		s.pairingDeliveryStates[id] = state
		s.pairingDeliverySequence++
		s.pairingDeliveries = append(s.pairingDeliveries, PairingDelivery{
			Sequence: s.pairingDeliverySequence, PairingID: id, MessageID: message.ID,
			Message: message, CreatedAt: now, RemoteNetworkID: remote,
		})
		if limit, ok := limits[id]; ok {
			if dropped := s.prunePairingDeliveriesLocked(id, now, limit.MaxAge, limit.MaxPending); dropped > 0 {
				if lifecycle.PairingDeliveriesDropped == nil {
					lifecycle.PairingDeliveriesDropped = make(map[string]int)
				}
				lifecycle.PairingDeliveriesDropped[id] = dropped
			}
		}
	}
	return lifecycle, nil
}

func (s *MemoryStore) ListPairingDeliveryHeadsContext(_ context.Context) ([]PairingDelivery, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]bool)
	var heads []PairingDelivery
	for _, delivery := range s.pairingDeliveries {
		if !seen[delivery.PairingID] {
			heads = append(heads, delivery)
			seen[delivery.PairingID] = true
		}
	}
	return heads, nil
}

func (s *MemoryStore) UpdatePairingDeliveryContext(_ context.Context, delivery PairingDelivery, lastError string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.pairingDeliveries {
		current := &s.pairingDeliveries[index]
		if current.PairingID == delivery.PairingID && current.MessageID == delivery.MessageID {
			current.Attempts, current.NextAttempt = delivery.Attempts, delivery.NextAttempt
			state := s.pairingDeliveryStates[delivery.PairingID]
			state.LastError = lastError
			s.pairingDeliveryStates[delivery.PairingID] = state
			break
		}
	}
	return nil
}

func (s *MemoryStore) CompletePairingDeliveryContext(_ context.Context, pairingID, messageID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, delivery := range s.pairingDeliveries {
		if delivery.PairingID == pairingID && delivery.MessageID == messageID {
			s.pairingDeliveries = append(s.pairingDeliveries[:index], s.pairingDeliveries[index+1:]...)
			state := s.pairingDeliveryStates[pairingID]
			state.LastError = ""
			s.pairingDeliveryStates[pairingID] = state
			break
		}
	}
	return nil
}

func (s *MemoryStore) PrunePairingDeliveriesContext(
	_ context.Context, pairingID string, now time.Time, maxAge time.Duration, maxPending int,
) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prunePairingDeliveriesLocked(pairingID, now, maxAge, maxPending), nil
}

func (s *MemoryStore) prunePairingDeliveriesLocked(pairingID string, now time.Time, maxAge time.Duration, maxPending int) int {
	count := 0
	for _, delivery := range s.pairingDeliveries {
		if delivery.PairingID == pairingID {
			count++
		}
	}
	dropped := 0
	retained := s.pairingDeliveries[:0]
	for _, delivery := range s.pairingDeliveries {
		if delivery.PairingID == pairingID &&
			(maxAge > 0 && now.Sub(delivery.CreatedAt) >= maxAge || maxPending > 0 && count > maxPending) {
			count--
			dropped++
			continue
		}
		retained = append(retained, delivery)
	}
	s.pairingDeliveries = retained
	state := s.pairingDeliveryStates[pairingID]
	state.Dropped += int64(dropped)
	s.pairingDeliveryStates[pairingID] = state
	return dropped
}

func (s *MemoryStore) PairingDeliveryStateContext(_ context.Context, pairingID string) (PairingDeliveryState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state := s.pairingDeliveryStates[pairingID]
	for _, delivery := range s.pairingDeliveries {
		if delivery.PairingID == pairingID {
			state.Pending++
			if state.OldestPendingAt.IsZero() || delivery.CreatedAt.Before(state.OldestPendingAt) {
				state.OldestPendingAt = delivery.CreatedAt
			}
		}
	}
	return state, nil
}
