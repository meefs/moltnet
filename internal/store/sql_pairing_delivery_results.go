package store

import (
	"context"
	"database/sql"
)

func (s *SQLStore) ApplyPairingDeliveryResultsContext(ctx context.Context, results []PairingDeliveryResult) error {
	if len(results) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	for _, result := range results {
		if err := s.applyPairingDeliveryResultTx(ctx, tx, result); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLStore) applyPairingDeliveryResultTx(ctx context.Context, tx *sql.Tx, result PairingDeliveryResult) error {
	delivery := result.Delivery
	if err := s.lockPairingDeliveryState(ctx, tx, delivery.PairingID); err != nil {
		return err
	}
	identity := []any{delivery.Sequence, delivery.PairingID, delivery.MessageID, delivery.RemoteNetworkID}
	where := ` WHERE seq = ? AND pairing_id = ? AND message_id = ? AND remote_network_id = ?`
	var changed sql.Result
	var err error
	if result.Retry {
		args := append([]any{delivery.Attempts, pairingQueueTime(delivery.NextAttempt)}, identity...)
		changed, err = tx.ExecContext(ctx, bindQuery(s.dialect,
			`UPDATE pairing_deliveries SET attempts = ?, next_attempt = ?`+where), args...)
	} else {
		changed, err = tx.ExecContext(ctx, bindQuery(s.dialect, `DELETE FROM pairing_deliveries`+where), identity...)
	}
	if err != nil {
		return err
	}
	count, err := changed.RowsAffected()
	if err != nil || count == 0 {
		return err
	}
	lastError := ""
	if result.Retry {
		lastError = result.LastError
	}
	if !result.Retry && result.RejectedReason != "" {
		_, err = tx.ExecContext(ctx, bindQuery(s.dialect,
			`UPDATE pairing_delivery_states SET last_error = '', rejected = rejected + 1, last_rejection_reason = ? WHERE pairing_id = ?`),
			result.RejectedReason, delivery.PairingID)
	} else {
		_, err = tx.ExecContext(ctx, bindQuery(s.dialect,
			`UPDATE pairing_delivery_states SET last_error = ? WHERE pairing_id = ?`), lastError, delivery.PairingID)
	}
	return err
}

func (s *SQLStore) ReconcilePairingDeliveriesContext(ctx context.Context, destinations map[string]string) (map[string]int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT pairing_id FROM pairing_deliveries ORDER BY pairing_id`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	dropped := make(map[string]int)
	for _, id := range ids {
		if err := s.lockPairingDeliveryState(ctx, tx, id); err != nil {
			return nil, err
		}
		query, args := `DELETE FROM pairing_deliveries WHERE pairing_id = ?`, []any{id}
		if remote, exists := destinations[id]; exists {
			query += ` AND (remote_network_id = '' OR remote_network_id <> ?)`
			args = append(args, remote)
		}
		result, err := tx.ExecContext(ctx, bindQuery(s.dialect, query), args...)
		if err != nil {
			return nil, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if count > 0 {
			if _, err := tx.ExecContext(ctx, bindQuery(s.dialect,
				`UPDATE pairing_delivery_states SET dropped = dropped + ?, last_error = '' WHERE pairing_id = ?`), count, id); err != nil {
				return nil, err
			}
			dropped[id] = int(count)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return dropped, nil
}
