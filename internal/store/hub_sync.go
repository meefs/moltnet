package store

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/noopolis/moltnet/pkg/protocol"
)

// ErrHubSyncRollback means a member's sync cursor would move backwards or
// to a different receipt at the same seq: the home is rewriting order the
// member already verified.
var ErrHubSyncRollback = errors.New("hub room sync would roll back verified order")

// HubSyncCursor is a member's last room seq whose receipt chain it has
// verified contiguously from the first seq, and that receipt's digest.
type HubSyncCursor struct {
	GlobalRoomID string `json:"global_room_id"`
	Seq          int64  `json:"seq"`
	Digest       string `json:"digest,omitempty"`
	// MembershipEpoch is that receipt's membership epoch: the floor every
	// later receipt's epoch is held to.
	MembershipEpoch int64 `json:"membership_epoch,omitempty"`
}

// HubSyncStore is the catch-up half of HubStore.
type HubSyncStore interface {
	// HubReceiptsAfterContext lists receipts after afterSeq in room order.
	HubReceiptsAfterContext(ctx context.Context, globalRoomID string, hubEpoch, afterSeq int64, limit int) ([]HubReceiptRecord, error)
	HubMessagesContext(ctx context.Context, messageIDs []string) (map[string]protocol.Message, error)
	HubSyncCursorContext(ctx context.Context, globalRoomID string) (HubSyncCursor, error)
	// AdvanceHubSyncCursorContext moves the cursor forward only.
	AdvanceHubSyncCursorContext(ctx context.Context, cursor HubSyncCursor) error
	// AttachHubReceiptContext stores the receipt for a message already
	// stored here (an origin's own copy), under the append conflict rules.
	AttachHubReceiptContext(ctx context.Context, record HubReceiptRecord) error
	// StoreHubReceiptContext stores a verified receipt with no message (one
	// the member may not see, or refused), under the same conflict rules.
	StoreHubReceiptContext(ctx context.Context, record HubReceiptRecord) error
	// EnvelopeForOriginContext finds a stored envelope by origin tuple.
	EnvelopeForOriginContext(ctx context.Context, originNetworkID, originMessageID string) (MessageEnvelopeRecord, bool, error)
	// HubWithheldReceiptsContext lists receipts stored as withheld (a
	// visible message the home did not give) that no message has bound.
	HubWithheldReceiptsContext(ctx context.Context, globalRoomID string) ([]HubReceiptRecord, error)
}

func checkCursorAdvance(stored, next HubSyncCursor) (bool, error) {
	switch {
	case next.Seq < stored.Seq:
		return false, fmt.Errorf("%w: room %s cursor at seq %d, offered %d", ErrHubSyncRollback, next.GlobalRoomID, stored.Seq, next.Seq)
	case next.Seq == stored.Seq && next.Digest != stored.Digest && stored.Seq > 0:
		return false, fmt.Errorf("%w: room %s seq %d holds a different receipt", ErrHubSyncRollback, next.GlobalRoomID, next.Seq)
	case next.Seq > stored.Seq && next.MembershipEpoch < stored.MembershipEpoch:
		return false, fmt.Errorf("%w: room %s seq %d at membership epoch %d follows epoch %d", ErrHubSyncRollback, next.GlobalRoomID, next.Seq, next.MembershipEpoch, stored.MembershipEpoch)
	}
	return next.Seq > stored.Seq, nil
}

func (s *MemoryStore) HubReceiptsAfterContext(_ context.Context, globalRoomID string, hubEpoch, afterSeq int64, limit int) ([]HubReceiptRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []HubReceiptRecord
	for _, record := range s.hub.Receipts {
		if record.GlobalRoomID == globalRoomID && record.HubEpoch == hubEpoch && record.RoomSeq > afterSeq {
			result = append(result, record)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].RoomSeq < result[j].RoomSeq })
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (s *MemoryStore) HubMessagesContext(_ context.Context, messageIDs []string) (map[string]protocol.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	wanted := make(map[string]bool, len(messageIDs))
	for _, id := range messageIDs {
		wanted[id] = true
	}
	result := map[string]protocol.Message{}
	for _, messages := range s.roomMessages {
		for _, message := range messages {
			if wanted[message.ID] {
				result[message.ID] = message
			}
		}
	}
	return result, nil
}

func (s *MemoryStore) HubSyncCursorContext(_ context.Context, globalRoomID string) (HubSyncCursor, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cursor, ok := s.hub.Cursors[globalRoomID]
	if !ok {
		cursor = HubSyncCursor{GlobalRoomID: globalRoomID}
	}
	return cursor, nil
}

func (s *MemoryStore) AdvanceHubSyncCursorContext(_ context.Context, cursor HubSyncCursor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.advanceHubSyncCursorLocked(cursor)
}

func (s *MemoryStore) advanceHubSyncCursorLocked(cursor HubSyncCursor) error {
	if s.hub.Cursors == nil {
		s.hub.Cursors = map[string]HubSyncCursor{}
	}
	advance, err := checkCursorAdvance(s.hub.Cursors[cursor.GlobalRoomID], cursor)
	if err == nil && advance {
		s.hub.Cursors[cursor.GlobalRoomID] = cursor
	}
	return err
}

func (s *MemoryStore) AttachHubReceiptContext(_ context.Context, record HubReceiptRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attachHubReceiptLocked(record)
}

func (s *MemoryStore) attachHubReceiptLocked(record HubReceiptRecord) error {
	if _, ok := s.messageIDs[record.MessageID]; !ok {
		return fmt.Errorf("%w: no stored message %s to attach a receipt to", ErrHubReceiptConflict, record.MessageID)
	}
	duplicate, err := s.checkHubReceiptLocked(record)
	if err != nil {
		return err
	}
	if duplicate {
		s.bindHubReceiptLocked(record)
		return nil
	}
	s.hub.Receipts = append(s.hub.Receipts, record)
	return nil
}

func (s *FileStore) AdvanceHubSyncCursorContext(ctx context.Context, cursor HubSyncCursor) error {
	return s.mutatePairingDeliveries(func(working *MemoryStore) error {
		return working.AdvanceHubSyncCursorContext(ctx, cursor)
	})
}

func (s *FileStore) AttachHubReceiptContext(ctx context.Context, record HubReceiptRecord) error {
	return s.mutatePairingDeliveries(func(working *MemoryStore) error {
		return working.AttachHubReceiptContext(ctx, record)
	})
}

var (
	_ HubSyncStore = (*MemoryStore)(nil)
	_ HubSyncStore = (*FileStore)(nil)
	_ HubSyncStore = (*SQLStore)(nil)
)
