package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

func (s *SQLStore) insertMessageEnvelope(ctx context.Context, tx *sql.Tx, record *MessageEnvelopeRecord) error {
	if record == nil {
		return nil
	}
	encoded, err := marshalEnvelope(*record)
	if err != nil {
		return err
	}
	// Immutable: an existing row for this message id is never replaced.
	if _, err := tx.ExecContext(ctx, bindQuery(s.dialect, `
		INSERT INTO message_envelopes (message_id, origin_network_id, origin_message_id, digest, state, fingerprint, envelope_json)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING
	`), record.MessageID, record.OriginNetworkID, record.OriginMessageID, record.Digest, record.State, record.Fingerprint, encoded); err != nil {
		return fmt.Errorf("insert message envelope: %w", err)
	}
	return nil
}

func (s *SQLStore) MessageEnvelopesContext(ctx context.Context, messageIDs []string) (map[string]MessageEnvelopeRecord, error) {
	result := make(map[string]MessageEnvelopeRecord)
	for _, id := range messageIDs {
		var record MessageEnvelopeRecord
		var encoded string
		err := s.db.QueryRowContext(ctx, bindQuery(s.dialect, `
			SELECT message_id, origin_network_id, origin_message_id, digest, state, fingerprint, envelope_json
			FROM message_envelopes WHERE message_id = ?
		`), id).Scan(&record.MessageID, &record.OriginNetworkID, &record.OriginMessageID, &record.Digest, &record.State, &record.Fingerprint, &encoded)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read message envelope: %w", err)
		}
		if err := json.Unmarshal([]byte(encoded), &record.Envelope); err != nil {
			return nil, fmt.Errorf("decode message envelope: %w", err)
		}
		result[id] = record
	}
	return result, nil
}
