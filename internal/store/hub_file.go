package store

import "context"

// The JSON store persists every hub write through a working copy, exactly
// like its pairing delivery writes, so a failed persist changes nothing.
// Reads are served by the embedded MemoryStore.

func (s *FileStore) CreateHubRoomContext(ctx context.Context, room HubRoom, genesis HubControlRecord) error {
	return s.mutatePairingDeliveries(func(working *MemoryStore) error {
		return working.CreateHubRoomContext(ctx, room, genesis)
	})
}

func (s *FileStore) AppendHubControlContext(ctx context.Context, records []HubControlRecord) error {
	return s.mutatePairingDeliveries(func(working *MemoryStore) error {
		return working.AppendHubControlContext(ctx, records)
	})
}

func (s *FileStore) NextHubOriginSeqContext(ctx context.Context, globalRoomID string) (int64, error) {
	var seq int64
	err := s.mutatePairingDeliveries(func(working *MemoryStore) error {
		var err error
		seq, err = working.NextHubOriginSeqContext(ctx, globalRoomID)
		return err
	})
	return seq, err
}

func (s *FileStore) ReplaceOutboundEnvelopeContext(ctx context.Context, record MessageEnvelopeRecord) error {
	return s.mutatePairingDeliveries(func(working *MemoryStore) error {
		return working.ReplaceOutboundEnvelopeContext(ctx, record)
	})
}
