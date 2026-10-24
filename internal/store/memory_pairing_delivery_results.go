package store

import "context"

func (s *MemoryStore) ApplyPairingDeliveryResultsContext(_ context.Context, results []PairingDeliveryResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, result := range results {
		result.LastError = pairingDeliveryText(result.LastError)
		result.RejectedReason = pairingDeliveryText(result.RejectedReason)
		for index := range s.pairingDeliveries {
			current := &s.pairingDeliveries[index]
			delivery := result.Delivery
			if current.Sequence != delivery.Sequence || current.PairingID != delivery.PairingID ||
				current.MessageID != delivery.MessageID || current.RemoteNetworkID != delivery.RemoteNetworkID {
				continue
			}
			state := s.pairingDeliveryStates[delivery.PairingID]
			if result.Retry {
				current.Attempts, current.NextAttempt = delivery.Attempts, delivery.NextAttempt
				state.LastError = result.LastError
			} else {
				s.pairingDeliveries = append(s.pairingDeliveries[:index], s.pairingDeliveries[index+1:]...)
				state.LastError = ""
				if result.RejectedReason != "" {
					state.Rejected++
					state.LastRejectionReason = result.RejectedReason
				}
			}
			s.pairingDeliveryStates[delivery.PairingID] = state
			break
		}
	}
	return nil
}

// Reconciliation fails closed for legacy rows without a bound destination.
func (s *MemoryStore) ReconcilePairingDeliveriesContext(_ context.Context, destinations map[string]string) (map[string]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dropped := make(map[string]int)
	retained := s.pairingDeliveries[:0]
	for _, delivery := range s.pairingDeliveries {
		remote, exists := destinations[delivery.PairingID]
		if !exists || delivery.RemoteNetworkID == "" || remote != delivery.RemoteNetworkID {
			dropped[delivery.PairingID]++
			continue
		}
		retained = append(retained, delivery)
	}
	s.pairingDeliveries = retained
	for id, count := range dropped {
		state := s.pairingDeliveryStates[id]
		state.Dropped += int64(count)
		state.LastError = ""
		s.pairingDeliveryStates[id] = state
	}
	return dropped, nil
}
