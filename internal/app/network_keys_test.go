package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/noopolis/moltnet/internal/netkeys"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestServerGeneratesPersistentKeyBesideConfig(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, DefaultPath)
	if err := os.WriteFile(path, []byte("version: moltnet.v1\n\nnetwork:\n  id: keyed-net\n  name: Keyed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfigForPath(path, "test")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(directory, ".moltnet", "keys"); config.KeysDir() != want || netkeys.DirForConfig(path) != want {
		t.Fatalf("keys dir = %q / %q, want %q", config.KeysDir(), netkeys.DirForConfig(path), want)
	}
	networkIdentity := func() protocol.NetworkIdentity {
		instance, err := New(config)
		if err != nil {
			t.Fatal(err)
		}
		defer instance.close()
		response := httptest.NewRecorder()
		instance.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/network", nil))
		var network protocol.Network
		if err := json.NewDecoder(response.Body).Decode(&network); err != nil || network.Identity == nil {
			t.Fatalf("/v1/network = HTTP %d %#v, %v", response.Code, network, err)
		}
		return *network.Identity
	}
	first := networkIdentity()
	if err := protocol.ValidateNetworkIdentity(first); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(config.KeysDir(), "network.key"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("network.key = %v, %v", info, err)
	}
	if second := networkIdentity(); second != first {
		t.Fatalf("identity changed across restart: %#v != %#v", second, first)
	}
}

func TestKeysDirWithoutConfigFile(t *testing.T) {
	t.Setenv(KeysDirEnv, "")
	sqlite := Config{Storage: StorageConfig{Kind: storageKindSQLite, SQLite: SQLiteStorageConfig{Path: "/data/net/moltnet.db"}}}
	if got := sqlite.KeysDir(); got != "/data/net/keys" {
		t.Fatalf("sqlite keys dir = %q", got)
	}
	if got := (Config{}).KeysDir(); got != "" {
		t.Fatalf("memory keys dir = %q, want ephemeral", got)
	}
}

func TestNoDurableKeysDirMeansNoSigningKey(t *testing.T) {
	t.Setenv(KeysDirEnv, "")
	for _, kind := range []string{storageKindMemory, storageKindPostgres} {
		identity, pins, err := loadNetworkKeys(Config{Storage: StorageConfig{Kind: kind}})
		if err != nil || identity != nil || pins == nil {
			t.Fatalf("%s: identity=%v pins=%v err=%v; an ephemeral key would change on every restart", kind, identity, pins, err)
		}
	}
}

func TestKeysDirEnvChoosesDurableDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(KeysDirEnv, dir)
	config := Config{Storage: StorageConfig{Kind: storageKindPostgres}}
	if config.KeysDir() != dir {
		t.Fatalf("keys dir = %q, want %q", config.KeysDir(), dir)
	}
	first, _, err := loadNetworkKeys(config)
	if err != nil || first == nil {
		t.Fatalf("first load: %v %v", first, err)
	}
	second, _, err := loadNetworkKeys(config)
	if err != nil || second == nil || second.Fingerprint() != first.Fingerprint() {
		t.Fatalf("key changed across restarts: %v %v", second, err)
	}
}
