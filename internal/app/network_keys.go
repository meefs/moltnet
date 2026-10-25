package app

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/noopolis/moltnet/internal/netkeys"
	"github.com/noopolis/moltnet/internal/observability"
)

// KeysDir is where this network's signing key and peer pins live: next to
// the config file when there is one (`<dir>/.moltnet/keys`), else next to
// the durable store file. Empty means no durable location (env-only memory
// or Postgres without a config file): keys are then ephemeral.
func (c Config) KeysDir() string {
	if c.roomCredentialBaseDir != "" {
		return filepath.Join(c.roomCredentialBaseDir, ".moltnet", "keys")
	}
	switch c.Storage.Kind {
	case storageKindSQLite:
		if path := strings.TrimSpace(c.Storage.SQLite.Path); path != "" {
			return filepath.Join(filepath.Dir(path), "keys")
		}
	case storageKindJSON:
		if path := strings.TrimSpace(c.Storage.JSON.Path); path != "" {
			return filepath.Join(filepath.Dir(path), "keys")
		}
	}
	return ""
}

// loadNetworkKeys opens the signing identity (created on first start) and
// the durable pin store. Without a keys directory the identity is ephemeral
// and pins live in memory; peers that pinned a previous key will refuse it,
// which is the safe failure.
func loadNetworkKeys(config Config) (*netkeys.Identity, *netkeys.PinStore, error) {
	dir := config.KeysDir()
	if dir == "" {
		identity, err := netkeys.NewEphemeralIdentity()
		if err != nil {
			return nil, nil, err
		}
		if config.Storage.Kind == storageKindPostgres {
			observability.Logger(context.Background(), "app.keys").Warn("no config file directory for network keys; using an ephemeral signing key that changes on restart")
		}
		return identity, netkeys.NewMemoryPinStore(), nil
	}
	identity, created, err := netkeys.LoadOrCreateIdentity(dir)
	if err != nil {
		return nil, nil, err
	}
	if created {
		observability.Logger(context.Background(), "app.keys", "fingerprint", identity.Fingerprint()).Info("generated network signing key")
	}
	return identity, netkeys.OpenPinStore(dir), nil
}
