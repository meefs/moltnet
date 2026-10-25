package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Receipt-only storage. A member's catch-up carries receipts whose message
// it may not see (or refuses for its content). They are stored anyway, with
// no message id, so every verified receipt meets the conflict rules (seq,
// event, chain, epoch order) and the receipt at the sync cursor, with its
// membership epoch, is always on record. A message that later arrives for
// such a receipt binds to it.

// hubReceiptOnlyMigration indexes stored envelopes by origin tuple, so a
// member resolves its own messages in a sync page by origin, not by id.
func hubReceiptOnlyMigration() migration {
	return migration{Version: 18, Name: "message_envelopes_origin_index", Statements: []string{
		`CREATE INDEX IF NOT EXISTS message_envelopes_origin ON message_envelopes (origin_network_id, origin_message_id)`,
	}}
}

func (s *MemoryStore) StoreHubReceiptContext(_ context.Context, record HubReceiptRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.storeHubReceiptLocked(record)
}

func (s *MemoryStore) storeHubReceiptLocked(record HubReceiptRecord) error {
	if _, ok := s.hub.Rooms[record.GlobalRoomID]; !ok {
		return fmt.Errorf("%w: unknown hub room %s", ErrHubReceiptConflict, record.GlobalRoomID)
	}
	record.MessageID = ""
	duplicate, err := s.checkHubReceiptLocked(record)
	if err != nil || duplicate {
		return err
	}
	s.hub.Receipts = append(s.hub.Receipts, record)
	return nil
}

// bindHubReceiptLocked gives a receipt stored without a message (or this
// one) the message id it now places.
func (s *MemoryStore) bindHubReceiptLocked(record HubReceiptRecord) {
	for index := range s.hub.Receipts {
		stored := &s.hub.Receipts[index]
		if stored.GlobalRoomID == record.GlobalRoomID && stored.HubEpoch == record.HubEpoch && stored.RoomSeq == record.RoomSeq &&
			stored.Digest == record.Digest && stored.MessageID == "" {
			stored.MessageID = record.MessageID
		}
	}
}

func (s *MemoryStore) EnvelopeForOriginContext(_ context.Context, originNetworkID, originMessageID string) (MessageEnvelopeRecord, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, record := range s.envelopes {
		if record.OriginNetworkID == originNetworkID && record.OriginMessageID == originMessageID {
			return record, true, nil
		}
	}
	return MessageEnvelopeRecord{}, false, nil
}

func (s *FileStore) StoreHubReceiptContext(ctx context.Context, record HubReceiptRecord) error {
	return s.mutatePairingDeliveries(func(working *MemoryStore) error {
		return working.StoreHubReceiptContext(ctx, record)
	})
}

func (s *SQLStore) StoreHubReceiptContext(ctx context.Context, record HubReceiptRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var rooms int
	if err := tx.QueryRowContext(ctx, bindQuery(s.dialect, `SELECT COUNT(*) FROM hub_rooms WHERE global_room_id = ?`), record.GlobalRoomID).Scan(&rooms); err != nil {
		return err
	}
	if rooms == 0 {
		return fmt.Errorf("%w: unknown hub room %s", ErrHubReceiptConflict, record.GlobalRoomID)
	}
	record.MessageID = ""
	duplicate, err := s.guardHubReceipt(ctx, tx, &record)
	if err != nil || duplicate {
		return err
	}
	if err := s.insertHubReceipt(ctx, tx, &record); err != nil {
		return err
	}
	return tx.Commit()
}

// bindHubReceipt is bindHubReceiptLocked inside an append transaction.
func (s *SQLStore) bindHubReceipt(ctx context.Context, tx *sql.Tx, record *HubReceiptRecord) error {
	if record == nil {
		return nil
	}
	_, err := tx.ExecContext(ctx, bindQuery(s.dialect, `UPDATE hub_receipts SET message_id = ?
		WHERE global_room_id = ? AND hub_epoch = ? AND room_seq = ? AND digest = ? AND message_id = ''`),
		record.MessageID, record.GlobalRoomID, record.HubEpoch, record.RoomSeq, record.Digest)
	return err
}

func (s *SQLStore) EnvelopeForOriginContext(ctx context.Context, originNetworkID, originMessageID string) (MessageEnvelopeRecord, bool, error) {
	var record MessageEnvelopeRecord
	var encoded string
	err := s.db.QueryRowContext(ctx, bindQuery(s.dialect, `
		SELECT message_id, origin_network_id, origin_message_id, digest, state, fingerprint, envelope_json
		FROM message_envelopes WHERE origin_network_id = ? AND origin_message_id = ? ORDER BY message_id LIMIT 1
	`), originNetworkID, originMessageID).Scan(&record.MessageID, &record.OriginNetworkID, &record.OriginMessageID, &record.Digest, &record.State, &record.Fingerprint, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return MessageEnvelopeRecord{}, false, nil
	}
	if err != nil {
		return MessageEnvelopeRecord{}, false, fmt.Errorf("read message envelope: %w", err)
	}
	if err := json.Unmarshal([]byte(encoded), &record.Envelope); err != nil {
		return MessageEnvelopeRecord{}, false, fmt.Errorf("decode message envelope: %w", err)
	}
	return record, true, nil
}

func (s *MemoryStore) HubWithheldReceiptsContext(_ context.Context, globalRoomID string) ([]HubReceiptRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []HubReceiptRecord
	for _, record := range s.hub.Receipts {
		if record.GlobalRoomID == globalRoomID && record.Withheld != "" && record.MessageID == "" {
			result = append(result, record)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].RoomSeq < result[j].RoomSeq })
	return result, nil
}

func (s *SQLStore) HubWithheldReceiptsContext(ctx context.Context, globalRoomID string) ([]HubReceiptRecord, error) {
	rows, err := s.db.QueryContext(ctx, bindQuery(s.dialect, `SELECT `+hubReceiptColumns+` FROM hub_receipts
		WHERE global_room_id = ? AND withheld <> '' AND message_id = '' ORDER BY room_seq ASC`), globalRoomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []HubReceiptRecord
	for rows.Next() {
		record, err := scanHubReceipt(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}
