package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// hubRoomsMigration stores hub rooms, their control logs and receipts.
// Unique keys back the conflict rules: one receipt per (room, hub epoch,
// room seq), one placement per (room, event).
func hubRoomsMigration() migration {
	return migration{Version: 16, Name: "hub_rooms", Statements: []string{
		`CREATE TABLE IF NOT EXISTS hub_rooms (
			global_room_id TEXT PRIMARY KEY, local_room_id TEXT NOT NULL UNIQUE,
			home_network_id TEXT NOT NULL, home_pairing_id TEXT NOT NULL, created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS hub_control (
			global_room_id TEXT NOT NULL, seq BIGINT NOT NULL, digest TEXT NOT NULL, control_json TEXT NOT NULL,
			PRIMARY KEY (global_room_id, seq)
		)`,
		`CREATE TABLE IF NOT EXISTS hub_receipts (
			global_room_id TEXT NOT NULL, hub_epoch BIGINT NOT NULL, room_seq BIGINT NOT NULL,
			event_id TEXT NOT NULL, digest TEXT NOT NULL, prev_digest TEXT NOT NULL, message_id TEXT NOT NULL,
			receipt_json TEXT NOT NULL, membership_epoch BIGINT NOT NULL DEFAULT 0,
			PRIMARY KEY (global_room_id, hub_epoch, room_seq), UNIQUE (global_room_id, event_id)
		)`,
		`CREATE INDEX IF NOT EXISTS hub_receipts_message ON hub_receipts (message_id)`,
		`CREATE TABLE IF NOT EXISTS hub_origin_seqs (global_room_id TEXT PRIMARY KEY, seq BIGINT NOT NULL)`,
	}}
}

type sqlQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (s *SQLStore) CreateHubRoomContext(ctx context.Context, room HubRoom, genesis HubControlRecord) error {
	if genesis.Seq != 1 || genesis.GlobalRoomID != room.GlobalRoomID {
		return controlConflict(room.GlobalRoomID, genesis.Seq, "a hub room starts with its genesis")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var count int
	if err := tx.QueryRowContext(ctx, bindQuery(s.dialect, `SELECT COUNT(*) FROM hub_rooms WHERE global_room_id = ? OR local_room_id = ?`),
		room.GlobalRoomID, room.LocalRoomID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("%w: %s (local room %s)", ErrHubRoomExists, room.GlobalRoomID, room.LocalRoomID)
	}
	if _, err := tx.ExecContext(ctx, bindQuery(s.dialect, `INSERT INTO hub_rooms (global_room_id, local_room_id, home_network_id, home_pairing_id, created_at) VALUES (?, ?, ?, ?, ?)`),
		room.GlobalRoomID, room.LocalRoomID, room.HomeNetworkID, room.HomePairingID, room.CreatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("insert hub room: %w", err)
	}
	if err := s.insertHubControl(ctx, tx, genesis); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLStore) insertHubControl(ctx context.Context, tx *sql.Tx, record HubControlRecord) error {
	_, err := tx.ExecContext(ctx, bindQuery(s.dialect, `INSERT INTO hub_control (global_room_id, seq, digest, control_json) VALUES (?, ?, ?, ?)`),
		record.GlobalRoomID, record.Seq, record.Digest, marshalJSON(record.Control))
	if err != nil {
		return fmt.Errorf("insert hub control: %w", err)
	}
	return nil
}

func (s *SQLStore) HubRoomsContext(ctx context.Context) ([]HubRoom, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT global_room_id, local_room_id, home_network_id, home_pairing_id, created_at FROM hub_rooms ORDER BY global_room_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rooms []HubRoom
	for rows.Next() {
		var room HubRoom
		var createdAt string
		if err := rows.Scan(&room.GlobalRoomID, &room.LocalRoomID, &room.HomeNetworkID, &room.HomePairingID, &createdAt); err != nil {
			return nil, err
		}
		if room.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
			return nil, err
		}
		rooms = append(rooms, room)
	}
	return rooms, rows.Err()
}

func (s *SQLStore) AppendHubControlContext(ctx context.Context, records []HubControlRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	for _, record := range records {
		var head int64
		if err := tx.QueryRowContext(ctx, bindQuery(s.dialect, `SELECT COALESCE(MAX(seq), 0) FROM hub_control WHERE global_room_id = ?`), record.GlobalRoomID).Scan(&head); err != nil {
			return err
		}
		switch {
		case head == 0:
			return fmt.Errorf("%w: unknown hub room %s", ErrHubControlConflict, record.GlobalRoomID)
		case record.Seq <= head:
			var digest string
			if err := tx.QueryRowContext(ctx, bindQuery(s.dialect, `SELECT digest FROM hub_control WHERE global_room_id = ? AND seq = ?`), record.GlobalRoomID, record.Seq).Scan(&digest); err != nil {
				return err
			}
			if digest != record.Digest {
				return controlConflict(record.GlobalRoomID, record.Seq, "a different event is stored at this seq")
			}
		case record.Seq == head+1:
			if err := s.insertHubControl(ctx, tx, record); err != nil {
				return err
			}
		default:
			return controlConflict(record.GlobalRoomID, record.Seq, "the log would have a gap")
		}
	}
	return tx.Commit()
}

func (s *SQLStore) HubControlLogContext(ctx context.Context, globalRoomID string, afterSeq int64) ([]HubControlRecord, error) {
	rows, err := s.db.QueryContext(ctx, bindQuery(s.dialect, `SELECT seq, digest, control_json FROM hub_control WHERE global_room_id = ? AND seq > ? ORDER BY seq`), globalRoomID, afterSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []HubControlRecord
	for rows.Next() {
		record := HubControlRecord{GlobalRoomID: globalRoomID}
		var encoded string
		if err := rows.Scan(&record.Seq, &record.Digest, &encoded); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(encoded), &record.Control); err != nil {
			return nil, fmt.Errorf("decode hub control: %w", err)
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

const hubReceiptColumns = `global_room_id, hub_epoch, room_seq, event_id, digest, prev_digest, message_id, receipt_json, membership_epoch`

func scanHubReceipt(row interface{ Scan(...any) error }) (HubReceiptRecord, error) {
	var record HubReceiptRecord
	var encoded string
	if err := row.Scan(&record.GlobalRoomID, &record.HubEpoch, &record.RoomSeq, &record.EventID, &record.Digest, &record.PrevDigest, &record.MessageID, &encoded, &record.MembershipEpoch); err != nil {
		return HubReceiptRecord{}, err
	}
	if err := json.Unmarshal([]byte(encoded), &record.Receipt); err != nil {
		return HubReceiptRecord{}, fmt.Errorf("decode hub receipt: %w", err)
	}
	return record, nil
}

func (s *SQLStore) hubReceiptWhere(ctx context.Context, querier sqlQuerier, where string, args ...any) (*HubReceiptRecord, error) {
	record, err := scanHubReceipt(querier.QueryRowContext(ctx, bindQuery(s.dialect, `SELECT `+hubReceiptColumns+` FROM hub_receipts WHERE `+where), args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *SQLStore) HubReceiptHeadContext(ctx context.Context, globalRoomID string, hubEpoch int64) (HubReceiptRecord, bool, error) {
	record, err := s.hubReceiptWhere(ctx, s.db, `global_room_id = ? AND hub_epoch = ? ORDER BY room_seq DESC LIMIT 1`, globalRoomID, hubEpoch)
	if err != nil || record == nil {
		return HubReceiptRecord{}, false, err
	}
	return *record, true, nil
}

func (s *SQLStore) HubReceiptForEventContext(ctx context.Context, globalRoomID, eventID string) (HubReceiptRecord, bool, error) {
	record, err := s.hubReceiptWhere(ctx, s.db, `global_room_id = ? AND event_id = ?`, globalRoomID, eventID)
	if err != nil || record == nil {
		return HubReceiptRecord{}, false, err
	}
	return *record, true, nil
}

func (s *SQLStore) HubReceiptsForMessagesContext(ctx context.Context, messageIDs []string) (map[string]HubReceiptRecord, error) {
	result := map[string]HubReceiptRecord{}
	for _, id := range messageIDs {
		record, err := s.hubReceiptWhere(ctx, s.db, `message_id = ?`, id)
		if err != nil {
			return nil, err
		}
		if record != nil {
			result[id] = *record
		}
	}
	return result, nil
}

// guardHubReceipt runs the receipt conflict rules inside the append
// transaction, before origin dedupe can acknowledge anything as duplicate.
func (s *SQLStore) guardHubReceipt(ctx context.Context, tx *sql.Tx, record *HubReceiptRecord) (bool, error) {
	if record == nil {
		return false, nil
	}
	// The no-op update row-locks the hub room (and takes SQLite's write
	// lock), so concurrent receipt appends for one room are serialized and
	// each sees the other's neighbours.
	if _, err := tx.ExecContext(ctx, bindQuery(s.dialect, `UPDATE hub_rooms SET home_network_id = home_network_id WHERE global_room_id = ?`), record.GlobalRoomID); err != nil {
		return false, err
	}
	lookup := func(where string, args ...any) (*HubReceiptRecord, error) {
		return s.hubReceiptWhere(ctx, tx, where, args...)
	}
	atSeq, err := lookup(`global_room_id = ? AND hub_epoch = ? AND room_seq = ?`, record.GlobalRoomID, record.HubEpoch, record.RoomSeq)
	if err != nil {
		return false, err
	}
	forEvent, err := lookup(`global_room_id = ? AND event_id = ?`, record.GlobalRoomID, record.EventID)
	if err != nil {
		return false, err
	}
	previous, err := lookup(`global_room_id = ? AND hub_epoch = ? AND room_seq = ?`, record.GlobalRoomID, record.HubEpoch, record.RoomSeq-1)
	if err != nil {
		return false, err
	}
	next, err := lookup(`global_room_id = ? AND hub_epoch = ? AND room_seq = ?`, record.GlobalRoomID, record.HubEpoch, record.RoomSeq+1)
	if err != nil {
		return false, err
	}
	before, err := lookup(`global_room_id = ? AND hub_epoch = ? AND room_seq < ? ORDER BY room_seq DESC LIMIT 1`, record.GlobalRoomID, record.HubEpoch, record.RoomSeq)
	if err != nil {
		return false, err
	}
	after, err := lookup(`global_room_id = ? AND hub_epoch = ? AND room_seq > ? ORDER BY room_seq ASC LIMIT 1`, record.GlobalRoomID, record.HubEpoch, record.RoomSeq)
	if err != nil {
		return false, err
	}
	return checkHubReceipt(*record, atSeq, forEvent, previous, next, before, after)
}

func (s *SQLStore) insertHubReceipt(ctx context.Context, tx *sql.Tx, record *HubReceiptRecord) error {
	if record == nil {
		return nil
	}
	_, err := tx.ExecContext(ctx, bindQuery(s.dialect, `INSERT INTO hub_receipts (`+hubReceiptColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		record.GlobalRoomID, record.HubEpoch, record.RoomSeq, record.EventID, record.Digest, record.PrevDigest, record.MessageID, marshalJSON(record.Receipt), record.MembershipEpoch)
	if err != nil {
		return fmt.Errorf("%w: insert hub receipt: %v", ErrHubReceiptConflict, err)
	}
	return nil
}

func (s *SQLStore) NextHubOriginSeqContext(ctx context.Context, globalRoomID string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer rollback(tx)
	if _, err := tx.ExecContext(ctx, bindQuery(s.dialect, `
		INSERT INTO hub_origin_seqs (global_room_id, seq) VALUES (?, 1)
		ON CONFLICT (global_room_id) DO UPDATE SET seq = hub_origin_seqs.seq + 1
	`), globalRoomID); err != nil {
		return 0, err
	}
	var seq int64
	if err := tx.QueryRowContext(ctx, bindQuery(s.dialect, `SELECT seq FROM hub_origin_seqs WHERE global_room_id = ?`), globalRoomID).Scan(&seq); err != nil {
		return 0, err
	}
	return seq, tx.Commit()
}

func (s *SQLStore) ReplaceOutboundEnvelopeContext(ctx context.Context, record MessageEnvelopeRecord) error {
	encoded, err := marshalEnvelope(record)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, bindQuery(s.dialect, `
		UPDATE message_envelopes SET digest = ?, state = ?, fingerprint = ?, envelope_json = ?
		WHERE message_id = ? AND origin_network_id = ? AND origin_message_id = ?
	`), record.Digest, record.State, record.Fingerprint, encoded, record.MessageID, record.OriginNetworkID, record.OriginMessageID)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return fmt.Errorf("no outbound envelope for message %s to replace", record.MessageID)
	}
	return nil
}
