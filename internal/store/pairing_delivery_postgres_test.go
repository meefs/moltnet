package store

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// Each backend test gets an isolated schema, leaving other integration tests
// and any existing test data in the configured database untouched.
func newPairingPostgresTestStore(t *testing.T) pairingTestStore {
	t.Helper()
	dsn := os.Getenv("MOLTNET_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("MOLTNET_TEST_POSTGRES_DSN is not set")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config)
	schema := fmt.Sprintf("pairing_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = schema
	config.RuntimeParams["application_name"] = schema
	db := stdlib.OpenDB(*config)
	storage, err := newSQLStore(db, dialectPostgres)
	t.Cleanup(func() {
		db.Close()
		if _, err := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
	return storage
}
