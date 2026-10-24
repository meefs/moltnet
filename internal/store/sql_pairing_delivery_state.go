package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *SQLStore) PrunePairingDeliveriesContext(
	ctx context.Context, pairingID string, now time.Time, maxAge time.Duration, maxPending int,
) (int, error) {
	state, err := s.PairingDeliveryStateContext(ctx, pairingID)
	if err != nil {
		return 0, err
	}
	if state.Pending == 0 || (maxAge <= 0 || now.Sub(state.OldestPendingAt) < maxAge) && (maxPending <= 0 || state.Pending <= maxPending) {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer rollback(tx)
	if err := s.lockPairingDeliveryState(ctx, tx, pairingID); err != nil {
		return 0, err
	}
	dropped, err := s.prunePairingDeliveriesTx(ctx, tx, pairingID, now, maxAge, maxPending)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return dropped, nil
}

func (s *SQLStore) prunePairingDeliveriesTx(
	ctx context.Context, tx *sql.Tx, pairingID string, now time.Time, maxAge time.Duration, maxPending int,
) (int, error) {
	var dropped int64
	if maxAge > 0 {
		result, err := tx.ExecContext(ctx, bindQuery(s.dialect,
			`DELETE FROM pairing_deliveries WHERE pairing_id = ? AND created_at <= ?`), pairingID, pairingQueueTime(now.Add(-maxAge)))
		if err != nil {
			return 0, err
		}
		dropped, err = result.RowsAffected()
		if err != nil {
			return 0, err
		}
	}
	if maxPending > 0 {
		var pending int
		if err := tx.QueryRowContext(ctx, bindQuery(s.dialect,
			`SELECT COUNT(*) FROM pairing_deliveries WHERE pairing_id = ?`), pairingID).Scan(&pending); err != nil {
			return 0, err
		}
		if pending > maxPending {
			result, err := tx.ExecContext(ctx, bindQuery(s.dialect, `DELETE FROM pairing_deliveries WHERE seq IN (
				SELECT seq FROM pairing_deliveries WHERE pairing_id = ? ORDER BY seq LIMIT ?
			)`), pairingID, pending-maxPending)
			if err != nil {
				return 0, err
			}
			count, err := result.RowsAffected()
			if err != nil {
				return 0, err
			}
			dropped += count
		}
	}
	if dropped > 0 {
		if _, err := tx.ExecContext(ctx, bindQuery(s.dialect,
			`UPDATE pairing_delivery_states SET dropped = dropped + ? WHERE pairing_id = ?`), dropped, pairingID); err != nil {
			return 0, err
		}
	}
	return int(dropped), nil
}

func (s *SQLStore) PairingDeliveryStateContext(ctx context.Context, pairingID string) (PairingDeliveryState, error) {
	// A single statement gives counters and the backlog from the same snapshot.
	var state PairingDeliveryState
	var oldest sql.NullString
	err := s.db.QueryRowContext(ctx, bindQuery(s.dialect, `
		SELECT last_error, dropped,
		(SELECT COUNT(*) FROM pairing_deliveries WHERE pairing_id = ?),
		(SELECT MIN(created_at) FROM pairing_deliveries WHERE pairing_id = ?)
		FROM pairing_delivery_states WHERE pairing_id = ?
	`), pairingID, pairingID, pairingID).Scan(&state.LastError, &state.Dropped, &state.Pending, &oldest)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return PairingDeliveryState{}, err
	}
	if oldest.Valid {
		state.OldestPendingAt, err = time.Parse(time.RFC3339Nano, oldest.String)
	}
	return state, err
}
