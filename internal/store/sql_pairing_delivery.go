package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func (s *SQLStore) AppendMessageWithPairingDeliveriesContext(
	ctx context.Context, message protocol.Message, event *protocol.Event, pairingIDs []string,
) (AppendLifecycle, error) {
	return s.appendMessageWithLifecycleContext(ctx, message, event, pairingIDs)
}

func (s *SQLStore) insertPairingReceiveReceipt(ctx context.Context, tx *sql.Tx, message protocol.Message) error {
	if messageOriginReceipt(message) == "" {
		return nil
	}
	result, err := tx.ExecContext(ctx, bindQuery(s.dialect, `
		INSERT INTO pairing_receive_receipts (origin_network_id, origin_message_id, stored_message_id)
		VALUES (?, ?, ?) ON CONFLICT DO NOTHING
	`), message.Origin.NetworkID, message.Origin.MessageID, message.ID)
	if err != nil {
		return fmt.Errorf("insert pairing receive receipt: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		var id string
		if err := tx.QueryRowContext(ctx, bindQuery(s.dialect,
			`SELECT stored_message_id FROM pairing_receive_receipts WHERE origin_network_id = ? AND origin_message_id = ?`), message.Origin.NetworkID, message.Origin.MessageID).Scan(&id); err != nil {
			return err
		}
		return &DuplicateMessageError{MessageID: id}
	}
	return nil
}

func (s *SQLStore) lockPairingDeliveryState(ctx context.Context, tx *sql.Tx, pairingID string) error {
	// The upsert takes a row lock on Postgres, serializing enqueue/prune and
	// counter updates for one pairing without excluding unrelated pairings.
	_, err := tx.ExecContext(ctx, bindQuery(s.dialect, `
		INSERT INTO pairing_delivery_states (pairing_id) VALUES (?)
		ON CONFLICT (pairing_id) DO UPDATE SET pairing_id = excluded.pairing_id
	`), pairingID)
	return err
}

func (s *SQLStore) insertPairingDeliveries(
	ctx context.Context, tx *sql.Tx, message protocol.Message, pairingIDs []string,
) (map[string]int, error) {
	var dropped map[string]int
	now := time.Now().UTC()
	limits := pairingDeliveryLimitsFromContext(ctx)
	ids := protocol.UniqueTrimmedStrings(pairingIDs)
	slices.Sort(ids) // All multi-pairing transactions acquire state locks in this order.
	for _, id := range ids {
		if err := s.lockPairingDeliveryState(ctx, tx, id); err != nil {
			return nil, err
		}
		remote := limits[id].RemoteNetworkID
		if remote == "" {
			if err := tx.QueryRowContext(ctx, bindQuery(s.dialect,
				`SELECT remote_network_id FROM pairing_delivery_states WHERE pairing_id = ?`), id).Scan(&remote); err != nil {
				return nil, err
			}
		}
		if _, err := tx.ExecContext(ctx, bindQuery(s.dialect,
			`UPDATE pairing_delivery_states SET remote_network_id = ? WHERE pairing_id = ?`), remote, id); err != nil {
			return nil, err
		}
		_, err := tx.ExecContext(ctx, bindQuery(s.dialect, `
			INSERT INTO pairing_deliveries (pairing_id, message_id, message_json, next_attempt, created_at, remote_network_id)
			VALUES (?, ?, ?, ?, ?, ?)
		`), id, message.ID, marshalJSON(message), pairingQueueTime(time.Time{}), pairingQueueTime(now), remote)
		if err != nil {
			return nil, fmt.Errorf("insert pairing delivery: %w", err)
		}
		if limit, ok := limits[id]; ok {
			count, err := s.prunePairingDeliveriesTx(ctx, tx, id, now, limit.MaxAge, limit.MaxPending)
			if err != nil {
				return nil, err
			}
			if count > 0 {
				if dropped == nil {
					dropped = make(map[string]int)
				}
				dropped[id] = count
			}
		}
	}
	return dropped, nil
}

// Fixed-width UTC text keeps comparison portable across SQLite and Postgres.
func pairingQueueTime(at time.Time) string {
	return at.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

func (s *SQLStore) ListPairingDeliveryHeadsContext(ctx context.Context) ([]PairingDelivery, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq, pairing_id, remote_network_id, message_id, message_json,
		attempts, next_attempt, created_at FROM pairing_deliveries
		WHERE seq IN (SELECT MIN(seq) FROM pairing_deliveries GROUP BY pairing_id) ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var heads []PairingDelivery
	for rows.Next() {
		var delivery PairingDelivery
		var messageJSON, nextAttempt, createdAt string
		if err := rows.Scan(&delivery.Sequence, &delivery.PairingID, &delivery.RemoteNetworkID, &delivery.MessageID, &messageJSON,
			&delivery.Attempts, &nextAttempt, &createdAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(messageJSON), &delivery.Message); err != nil {
			return nil, fmt.Errorf("decode pairing delivery: %w", err)
		}
		delivery.NextAttempt, err = time.Parse(time.RFC3339Nano, nextAttempt)
		if err != nil {
			return nil, err
		}
		delivery.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		heads = append(heads, delivery)
	}
	return heads, rows.Err()
}

func (s *SQLStore) UpdatePairingDeliveryContext(ctx context.Context, delivery PairingDelivery, lastError string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err := s.lockPairingDeliveryState(ctx, tx, delivery.PairingID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, bindQuery(s.dialect, `
		UPDATE pairing_deliveries SET attempts = ?, next_attempt = ? WHERE pairing_id = ? AND message_id = ?
	`), delivery.Attempts, pairingQueueTime(delivery.NextAttempt), delivery.PairingID, delivery.MessageID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count > 0 {
		if _, err := tx.ExecContext(ctx, bindQuery(s.dialect,
			`UPDATE pairing_delivery_states SET last_error = ? WHERE pairing_id = ?`), lastError, delivery.PairingID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLStore) CompletePairingDeliveryContext(ctx context.Context, pairingID, messageID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err := s.lockPairingDeliveryState(ctx, tx, pairingID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, bindQuery(s.dialect,
		`DELETE FROM pairing_deliveries WHERE pairing_id = ? AND message_id = ?`), pairingID, messageID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count > 0 {
		if _, err := tx.ExecContext(ctx, bindQuery(s.dialect,
			`UPDATE pairing_delivery_states SET last_error = '' WHERE pairing_id = ?`), pairingID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLStore) requireMatchingMessageOrigin(ctx context.Context, tx *sql.Tx, message protocol.Message) error {
	var encoded string
	if err := tx.QueryRowContext(ctx, bindQuery(s.dialect, `SELECT origin_json FROM messages WHERE id = ?`), message.ID).Scan(&encoded); err != nil {
		return err
	}
	var existing protocol.Message
	if err := json.Unmarshal([]byte(encoded), &existing.Origin); err != nil {
		return err
	}
	if messageOriginReceipt(existing) != messageOriginReceipt(message) {
		return ErrMessageIDConflict
	}
	return nil
}
