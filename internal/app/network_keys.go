package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/noopolis/moltnet/internal/netkeys"
	"github.com/noopolis/moltnet/internal/observability"
)

// KeysDirEnv names a durable keys directory explicitly. It wins over every
// derived location, and is the only way to sign with env-only memory or
// Postgres configurations.
const KeysDirEnv = "MOLTNET_KEYS_DIR"

// KeysDir is where this network's signing key and peer pins live:
// $MOLTNET_KEYS_DIR when set, else next to the config file when there is one
// (`<dir>/.moltnet/keys`), else next to the durable store file. Empty means
// no durable location (env-only memory or Postgres without a config file).
func (c Config) KeysDir() string {
	if dir := strings.TrimSpace(os.Getenv(KeysDirEnv)); dir != "" {
		return dir
	}
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
// the durable pin store. Without a durable keys directory the network does
// not sign at all: a key that changed on every restart would get every
// message refused by peers that pinned the previous one. Relays then go out
// unsigned exactly as before keys existed.
func loadNetworkKeys(config Config) (*netkeys.Identity, *netkeys.PinStore, error) {
	dir := config.KeysDir()
	if dir == "" {
		observability.Logger(context.Background(), "app.keys", "storage", config.Storage.Kind).
			Warn("no durable keys directory; pairing messages are relayed unsigned. Set " + KeysDirEnv + " to sign them")
		return nil, netkeys.NewMemoryPinStore(), nil
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
