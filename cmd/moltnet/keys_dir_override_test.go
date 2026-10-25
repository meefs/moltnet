package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/app"
	"github.com/noopolis/moltnet/internal/hubinvites"
	"github.com/noopolis/moltnet/internal/netkeys"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// With MOLTNET_KEYS_DIR set, the CLI reads and writes the same keys, pins,
// grants and pending joins the server does (app.Config.KeysDir), and
// nothing beside the config file.
func TestCLIHonorsKeysDirOverride(t *testing.T) {
	t.Setenv("TEST_RELAY_TOKEN", "relay-secret-value")
	homeDir, homeKeys := t.TempDir(), t.TempDir()
	homePath := writeMoltnetConfig(t, homeDir, "home-net", "Home")
	t.Setenv(app.KeysDirEnv, homeKeys)
	config, err := app.LoadConfigForPath(homePath, "")
	if err != nil {
		t.Fatal(err)
	}
	if config.KeysDir() != homeKeys {
		t.Fatalf("server keys dir = %q, want the override", config.KeysDir())
	}
	show := captureStdout(t, func() {
		if err := run(context.Background(), []string{"keys", "show", "--config", homePath}, "test"); err != nil {
			t.Fatal(err)
		}
	})
	identity, err := netkeys.LoadIdentity(homeKeys)
	if err != nil {
		t.Fatalf("keys show did not use the override dir: %v", err)
	}
	if !strings.Contains(show, protocol.DisplayFingerprint(identity.Fingerprint())) {
		t.Fatalf("keys show printed another key:\n%s", show)
	}
	captureStdout(t, func() {
		if err := run(context.Background(), []string{"keys", "trust", "peer-net", identity.Fingerprint(), "--config", homePath}, "test"); err != nil {
			t.Fatal(err)
		}
	})
	if _, ok, _ := netkeys.OpenPinStore(homeKeys).Lookup("peer-net"); !ok {
		t.Fatal("keys trust did not pin into the override dir")
	}

	global := protocol.GlobalRoomID(identity.Fingerprint(), "research")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(response).Encode(protocol.HubRoomInfo{GlobalRoomID: global, LocalRoomID: "research", HomeNetworkID: "home-net", IsHome: true})
	}))
	defer server.Close()
	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"room", "invite", "research", "--id", "room-lab",
			"--relay-url", "wss://relay.example", "--relay-token-env", "TEST_RELAY_TOKEN", "--config", homePath, "--base-url", server.URL}, "test"); err != nil {
			t.Fatalf("room invite: %v", err)
		}
	})
	invite, err := protocol.DecodeInvite(extractInviteCode(t, output))
	if err != nil || invite.NetworkFingerprint != identity.Fingerprint() {
		t.Fatalf("invite = %#v, %v", invite, err)
	}
	if _, ok, _ := hubinvites.Open(homeKeys).Grant("room-lab", global, time.Now()); !ok {
		t.Fatal("room invite wrote its grant outside the override dir")
	}

	labDir, labKeys := t.TempDir(), t.TempDir()
	labPath := writeMoltnetConfig(t, labDir, "lab-net", "Lab")
	t.Setenv(app.KeysDirEnv, labKeys)
	captureStdout(t, func() {
		if err := run(context.Background(), []string{"room", "join", extractInviteCode(t, output), "--config", labPath}, "test"); err != nil {
			t.Fatalf("room join: %v", err)
		}
	})
	if joins, _ := hubinvites.Open(labKeys).Joins(); len(joins) != 1 || joins[0].GlobalRoomID != global {
		t.Fatalf("pending joins in the override dir = %#v", joins)
	}
	if _, ok, _ := netkeys.OpenPinStore(labKeys).Lookup("home-net"); !ok {
		t.Fatal("room join did not pin the home into the override dir")
	}
	for _, dir := range []string{homeDir, labDir} {
		if _, err := os.Stat(filepath.Join(dir, ".moltnet", "keys")); !os.IsNotExist(err) {
			t.Fatalf("the CLI wrote a config-adjacent keys dir under %s despite the override: %v", dir, err)
		}
	}
}
