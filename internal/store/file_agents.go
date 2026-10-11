package store

import (
	"context"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func (s *FileStore) RegisterAgentContext(
	_ context.Context,
	registration protocol.AgentRegistration,
) (RegistrationResult, error) {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	working := memoryStoreFromSnapshot(s.snapshot())
	registered, err := working.RegisterAgentContext(context.Background(), registration)
	if err != nil {
		return RegistrationResult{}, err
	}

	next := snapshotFromMemoryStore(working)
	if err := s.persistSnapshot(next); err != nil {
		return RegistrationResult{}, err
	}

	s.restore(next)
	return registered, nil
}

func (s *FileStore) ReconcileRegisteredAgentContext(
	_ context.Context,
	registration protocol.AgentRegistration,
) (protocol.AgentRegistration, error) {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	working := memoryStoreFromSnapshot(s.snapshot())
	registered, err := working.ReconcileRegisteredAgentContext(context.Background(), registration)
	if err != nil {
		return protocol.AgentRegistration{}, err
	}

	next := snapshotFromMemoryStore(working)
	if err := s.persistSnapshot(next); err != nil {
		return protocol.AgentRegistration{}, err
	}

	s.restore(next)
	return registered, nil
}

func (s *FileStore) RemoveRegisteredAgentContext(_ context.Context, agentID string) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	working := memoryStoreFromSnapshot(s.snapshot())
	if err := working.RemoveRegisteredAgentContext(context.Background(), agentID); err != nil {
		return err
	}

	next := snapshotFromMemoryStore(working)
	if err := s.persistSnapshot(next); err != nil {
		return err
	}

	s.restore(next)
	return nil
}
