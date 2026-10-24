package store

import (
	"context"
	"reflect"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func (s *FileStore) AppendMessageWithPairingDeliveriesContext(
	ctx context.Context, message protocol.Message, event *protocol.Event, pairingIDs []string,
) (AppendLifecycle, error) {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	working := memoryStoreFromSnapshot(s.snapshot())
	lifecycle, err := working.AppendMessageWithPairingDeliveriesContext(ctx, message, event, pairingIDs)
	if err != nil {
		return AppendLifecycle{}, err
	}
	next := snapshotFromMemoryStore(working)
	if err := s.persistSnapshot(next); err != nil {
		return AppendLifecycle{}, err
	}
	s.restore(next)
	return lifecycle, nil
}

func (s *FileStore) UpdatePairingDeliveryContext(ctx context.Context, delivery PairingDelivery, lastError string) error {
	return s.mutatePairingDeliveries(func(working *MemoryStore) error {
		return working.UpdatePairingDeliveryContext(ctx, delivery, lastError)
	})
}

func (s *FileStore) CompletePairingDeliveryContext(ctx context.Context, pairingID, messageID string) error {
	return s.mutatePairingDeliveries(func(working *MemoryStore) error {
		return working.CompletePairingDeliveryContext(ctx, pairingID, messageID)
	})
}

func (s *FileStore) PrunePairingDeliveriesContext(
	ctx context.Context, pairingID string, now time.Time, maxAge time.Duration, maxPending int,
) (int, error) {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	state, err := s.MemoryStore.PairingDeliveryStateContext(ctx, pairingID)
	if err != nil {
		return 0, err
	}
	if state.Pending == 0 || (maxAge <= 0 || now.Sub(state.OldestPendingAt) < maxAge) && (maxPending <= 0 || state.Pending <= maxPending) {
		return 0, nil
	}
	working := memoryStoreFromSnapshot(s.snapshot())
	dropped, err := working.PrunePairingDeliveriesContext(ctx, pairingID, now, maxAge, maxPending)
	if err != nil || dropped == 0 {
		return dropped, err
	}
	next := snapshotFromMemoryStore(working)
	if err := s.persistSnapshot(next); err != nil {
		return 0, err
	}
	s.restore(next)
	return dropped, nil
}

func (s *FileStore) mutatePairingDeliveries(mutate func(*MemoryStore) error) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	working := memoryStoreFromSnapshot(s.snapshot())
	if err := mutate(working); err != nil {
		return err
	}
	next := snapshotFromMemoryStore(working)
	if err := s.persistSnapshot(next); err != nil {
		return err
	}
	s.restore(next)
	return nil
}

func (s *FileStore) ApplyPairingDeliveryResultsContext(ctx context.Context, results []PairingDeliveryResult) error {
	if len(results) == 0 {
		return nil
	}
	return s.mutatePairingDeliveries(func(working *MemoryStore) error {
		return working.ApplyPairingDeliveryResultsContext(ctx, results)
	})
}

func (s *FileStore) ReconcilePairingDeliveriesContext(ctx context.Context, destinations map[string]string) (map[string]int, error) {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	previous := s.snapshot()
	working := memoryStoreFromSnapshot(previous)
	dropped, err := working.ReconcilePairingDeliveriesContext(ctx, destinations)
	if err != nil || reflect.DeepEqual(working.pairingDeliveryStates, previous.PairingDeliveryStates) && len(dropped) == 0 {
		return dropped, err
	}
	next := snapshotFromMemoryStore(working)
	if err := s.persistSnapshot(next); err != nil {
		return nil, err
	}
	s.restore(next)
	return dropped, nil
}

func (s *FileStore) BindPairingDeliveryDestinationContext(ctx context.Context, id, remote string) (int, error) {
	var dropped int
	err := s.mutatePairingDeliveries(func(working *MemoryStore) error {
		var err error
		dropped, err = working.BindPairingDeliveryDestinationContext(ctx, id, remote)
		return err
	})
	if err != nil {
		return 0, err
	}
	return dropped, nil
}
