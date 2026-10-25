package store

import (
	"context"
	"fmt"
	"sort"

	"github.com/noopolis/moltnet/pkg/protocol"
)

// hubState is the memory (and JSON snapshot) form of HubStore data.
type hubState struct {
	Rooms      map[string]HubRoom            `json:"rooms,omitempty"`
	Control    map[string][]HubControlRecord `json:"control,omitempty"`
	Receipts   []HubReceiptRecord            `json:"receipts,omitempty"`
	OriginSeqs map[string]int64              `json:"origin_seqs,omitempty"`
}

func newHubState() hubState {
	return hubState{Rooms: map[string]HubRoom{}, Control: map[string][]HubControlRecord{}, OriginSeqs: map[string]int64{}}
}

func (h hubState) clone() hubState {
	cloned := newHubState()
	for key, room := range h.Rooms {
		cloned.Rooms[key] = room
	}
	for key, records := range h.Control {
		cloned.Control[key] = append([]HubControlRecord(nil), records...)
	}
	cloned.Receipts = append([]HubReceiptRecord(nil), h.Receipts...)
	for key, seq := range h.OriginSeqs {
		cloned.OriginSeqs[key] = seq
	}
	return cloned
}

func (s *MemoryStore) CreateHubRoomContext(_ context.Context, room HubRoom, genesis HubControlRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createHubRoomLocked(room, genesis)
}

func (s *MemoryStore) createHubRoomLocked(room HubRoom, genesis HubControlRecord) error {
	if _, exists := s.hub.Rooms[room.GlobalRoomID]; exists {
		return fmt.Errorf("%w: %s", ErrHubRoomExists, room.GlobalRoomID)
	}
	for _, existing := range s.hub.Rooms {
		if existing.LocalRoomID == room.LocalRoomID {
			return fmt.Errorf("%w: local room %s already maps %s", ErrHubRoomExists, room.LocalRoomID, existing.GlobalRoomID)
		}
	}
	if genesis.Seq != 1 || genesis.GlobalRoomID != room.GlobalRoomID {
		return controlConflict(room.GlobalRoomID, genesis.Seq, "a hub room starts with its genesis")
	}
	s.hub.Rooms[room.GlobalRoomID] = room
	s.hub.Control[room.GlobalRoomID] = []HubControlRecord{genesis}
	return nil
}

func (s *MemoryStore) HubRoomsContext(context.Context) ([]HubRoom, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rooms := make([]HubRoom, 0, len(s.hub.Rooms))
	for _, room := range s.hub.Rooms {
		rooms = append(rooms, room)
	}
	sort.Slice(rooms, func(i, j int) bool { return rooms[i].GlobalRoomID < rooms[j].GlobalRoomID })
	return rooms, nil
}

func (s *MemoryStore) AppendHubControlContext(_ context.Context, records []HubControlRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendHubControlLocked(records)
}

func (s *MemoryStore) appendHubControlLocked(records []HubControlRecord) error {
	staged := map[string][]HubControlRecord{}
	for _, record := range records {
		if _, ok := s.hub.Rooms[record.GlobalRoomID]; !ok {
			return fmt.Errorf("%w: unknown hub room %s", ErrHubControlConflict, record.GlobalRoomID)
		}
		log, ok := staged[record.GlobalRoomID]
		if !ok {
			log = append([]HubControlRecord(nil), s.hub.Control[record.GlobalRoomID]...)
		}
		switch {
		case record.Seq <= int64(len(log)):
			if log[record.Seq-1].Digest != record.Digest {
				return controlConflict(record.GlobalRoomID, record.Seq, "a different event is stored at this seq")
			}
		case record.Seq == int64(len(log))+1:
			log = append(log, record)
		default:
			return controlConflict(record.GlobalRoomID, record.Seq, "the log would have a gap")
		}
		staged[record.GlobalRoomID] = log
	}
	for global, log := range staged {
		s.hub.Control[global] = log
	}
	return nil
}

func (s *MemoryStore) HubControlLogContext(_ context.Context, globalRoomID string, afterSeq int64) ([]HubControlRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []HubControlRecord
	for _, record := range s.hub.Control[globalRoomID] {
		if record.Seq > afterSeq {
			result = append(result, record)
		}
	}
	return result, nil
}

func (s *MemoryStore) HubReceiptHeadContext(_ context.Context, globalRoomID string, hubEpoch int64) (HubReceiptRecord, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var head HubReceiptRecord
	found := false
	for _, record := range s.hub.Receipts {
		if record.GlobalRoomID == globalRoomID && record.HubEpoch == hubEpoch && (!found || record.RoomSeq > head.RoomSeq) {
			head, found = record, true
		}
	}
	return head, found, nil
}

func (s *MemoryStore) HubReceiptForEventContext(_ context.Context, globalRoomID, eventID string) (HubReceiptRecord, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, record := range s.hub.Receipts {
		if record.GlobalRoomID == globalRoomID && record.EventID == eventID {
			return record, true, nil
		}
	}
	return HubReceiptRecord{}, false, nil
}

func (s *MemoryStore) HubReceiptsForMessagesContext(_ context.Context, messageIDs []string) (map[string]HubReceiptRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	wanted := make(map[string]bool, len(messageIDs))
	for _, id := range messageIDs {
		wanted[id] = true
	}
	result := map[string]HubReceiptRecord{}
	for _, record := range s.hub.Receipts {
		if wanted[record.MessageID] {
			result[record.MessageID] = record
		}
	}
	return result, nil
}

func (s *MemoryStore) NextHubOriginSeqContext(_ context.Context, globalRoomID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hub.OriginSeqs[globalRoomID]++
	return s.hub.OriginSeqs[globalRoomID], nil
}

func (s *MemoryStore) ReplaceOutboundEnvelopeContext(_ context.Context, record MessageEnvelopeRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.replaceOutboundEnvelopeLocked(record)
}

func (s *MemoryStore) replaceOutboundEnvelopeLocked(record MessageEnvelopeRecord) error {
	existing, ok := s.envelopes[record.MessageID]
	if !ok || existing.OriginNetworkID != record.OriginNetworkID || existing.OriginMessageID != record.OriginMessageID {
		return fmt.Errorf("no outbound envelope for message %s to replace", record.MessageID)
	}
	s.envelopes[record.MessageID] = record
	return nil
}

// checkHubReceiptLocked runs checkHubReceipt against the stored receipts.
func (s *MemoryStore) checkHubReceiptLocked(record HubReceiptRecord) (bool, error) {
	var atSeq, forEvent, previous, next *HubReceiptRecord
	for index := range s.hub.Receipts {
		stored := &s.hub.Receipts[index]
		if stored.GlobalRoomID != record.GlobalRoomID {
			continue
		}
		if stored.EventID == record.EventID {
			forEvent = stored
		}
		if stored.HubEpoch != record.HubEpoch {
			continue
		}
		switch stored.RoomSeq {
		case record.RoomSeq:
			atSeq = stored
		case record.RoomSeq - 1:
			previous = stored
		case record.RoomSeq + 1:
			next = stored
		}
	}
	return checkHubReceipt(record, atSeq, forEvent, previous, next)
}

// appendWithHubReceiptLocked guards a message append with its receipt:
// receipt conflicts are checked before any duplicate is acknowledged.
func (s *MemoryStore) appendWithHubReceiptLocked(ctx context.Context, message protocol.Message) (AppendLifecycle, error) {
	receipt := hubReceiptForMessage(ctx, message)
	duplicateReceipt := false
	if receipt != nil {
		var err error
		if duplicateReceipt, err = s.checkHubReceiptLocked(*receipt); err != nil {
			return AppendLifecycle{}, err
		}
	}
	lifecycle, err := s.appendMessageLocked(message, envelopeForMessage(ctx, message))
	if err != nil {
		return AppendLifecycle{}, err
	}
	if receipt != nil && !duplicateReceipt {
		s.hub.Receipts = append(s.hub.Receipts, *receipt)
	}
	return lifecycle, nil
}
