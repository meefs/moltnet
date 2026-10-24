package store

import "fmt"

func pairingDeliveryMigration() migration {
	common := []string{
		`CREATE TABLE pairing_delivery_states (
			pairing_id TEXT PRIMARY KEY, last_error TEXT NOT NULL DEFAULT '', dropped BIGINT NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE pairing_receive_receipts (
			origin_network_id TEXT NOT NULL, origin_message_id TEXT NOT NULL, stored_message_id TEXT NOT NULL,
			PRIMARY KEY (origin_network_id, origin_message_id)
		)`,
	}
	queue := `CREATE TABLE pairing_deliveries (
		seq %s, pairing_id TEXT NOT NULL, message_id TEXT NOT NULL, message_json TEXT NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0, next_attempt TEXT NOT NULL,
		created_at TEXT NOT NULL, UNIQUE(pairing_id, message_id)
	)`
	sqlite := append([]string(nil), common...)
	postgres := append([]string(nil), common...)
	sqlite = append(sqlite, fmt.Sprintf(queue, "INTEGER PRIMARY KEY AUTOINCREMENT"),
		`INSERT INTO pairing_receive_receipts (origin_network_id, origin_message_id, stored_message_id)
		SELECT json_extract(origin_json, '$.network_id'), json_extract(origin_json, '$.message_id'), id
		FROM messages WHERE COALESCE(json_extract(origin_json, '$.received_via'), '') <> ''
		AND json_extract(origin_json, '$.received_via') NOT LIKE 'follow:%'
		AND COALESCE(json_extract(origin_json, '$.network_id'), '') <> ''
		AND COALESCE(json_extract(origin_json, '$.message_id'), '') <> ''
		ON CONFLICT DO NOTHING`)
	postgres = append(postgres, fmt.Sprintf(queue, "BIGSERIAL PRIMARY KEY"),
		`INSERT INTO pairing_receive_receipts (origin_network_id, origin_message_id, stored_message_id)
		SELECT origin_json::jsonb->>'network_id', origin_json::jsonb->>'message_id', id
		FROM messages WHERE COALESCE(origin_json::jsonb->>'received_via', '') <> ''
		AND origin_json::jsonb->>'received_via' NOT LIKE 'follow:%'
		AND COALESCE(origin_json::jsonb->>'network_id', '') <> ''
		AND COALESCE(origin_json::jsonb->>'message_id', '') <> ''
		ON CONFLICT DO NOTHING`)
	return migration{Version: 12, Name: "pairing_delivery", SQLiteStatements: sqlite, PostgresStatements: postgres}
}

func pairingDeliveryOutcomeMigration() migration {
	return migration{Version: 13, Name: "pairing_delivery_outcomes", Statements: []string{
		`ALTER TABLE pairing_deliveries ADD COLUMN remote_network_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE pairing_delivery_states ADD COLUMN rejected BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE pairing_delivery_states ADD COLUMN last_rejection_reason TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX pairing_deliveries_pairing_seq ON pairing_deliveries (pairing_id, seq)`,
	}}
}

func pairingDeliveryBindingMigration() migration {
	return migration{Version: 14, Name: "pairing_delivery_binding", Statements: []string{
		`ALTER TABLE pairing_delivery_states ADD COLUMN remote_network_id TEXT NOT NULL DEFAULT ''`,
	}}
}
