package store

import "context"

func (s *SQLStore) BindPairingDeliveryDestinationContext(ctx context.Context, id, remote string) (int, error) {
	if remote == "" {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer rollback(tx)
	if err := s.lockPairingDeliveryState(ctx, tx, id); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, bindQuery(s.dialect,
		`DELETE FROM pairing_deliveries WHERE pairing_id = ? AND remote_network_id <> '' AND remote_network_id <> ?`), id, remote)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, bindQuery(s.dialect,
		`UPDATE pairing_deliveries SET remote_network_id = ? WHERE pairing_id = ? AND remote_network_id = ''`), remote, id); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, bindQuery(s.dialect,
		`UPDATE pairing_delivery_states SET remote_network_id = ?, dropped = dropped + ? WHERE pairing_id = ?`), remote, count, id); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(count), nil
}
