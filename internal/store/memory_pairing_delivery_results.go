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

// Empty destinations and rows are unbound, not evidence of a changed peer.
func (s *MemoryStore) ReconcilePairingDeliveriesContext(_ context.Context, destinations map[string]string) (map[string]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dropped := make(map[string]int)
	retained := s.pairingDeliveries[:0]
	for _, delivery := range s.pairingDeliveries {
		remote, exists := destinations[delivery.PairingID]
		if !exists || remote != "" && delivery.RemoteNetworkID != "" && remote != delivery.RemoteNetworkID {
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
	for id, state := range s.pairingDeliveryStates {
		if remote, exists := destinations[id]; !exists {
			state.RemoteNetworkID = ""
		} else if remote != "" {
			state.RemoteNetworkID = remote
		}
		s.pairingDeliveryStates[id] = state
	}
	return dropped, nil
}

// Discovery binds the queue and its durable default in the same critical section.
func (s *MemoryStore) BindPairingDeliveryDestinationContext(_ context.Context, id, remote string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if remote == "" {
		return 0, nil
	}
	dropped := 0
	retained := s.pairingDeliveries[:0]
	for _, delivery := range s.pairingDeliveries {
		if delivery.PairingID == id {
			if delivery.RemoteNetworkID != "" && delivery.RemoteNetworkID != remote {
				dropped++
				continue
			}
			delivery.RemoteNetworkID = remote
		}
		retained = append(retained, delivery)
	}
	s.pairingDeliveries = retained
	state := s.pairingDeliveryStates[id]
	state.RemoteNetworkID = remote
	state.Dropped += int64(dropped)
	s.pairingDeliveryStates[id] = state
	return dropped, nil
}
