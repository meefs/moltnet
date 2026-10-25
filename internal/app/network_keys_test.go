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
	sqlite := Config{Storage: StorageConfig{Kind: storageKindSQLite, SQLite: SQLiteStorageConfig{Path: "/data/net/moltnet.db"}}}
	if got := sqlite.KeysDir(); got != "/data/net/keys" {
		t.Fatalf("sqlite keys dir = %q", got)
	}
	if got := (Config{}).KeysDir(); got != "" {
		t.Fatalf("memory keys dir = %q, want ephemeral", got)
	}
}
