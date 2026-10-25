package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noopolis/moltnet/internal/netkeys"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestPairInviteCarriesKeyAndJoinPinsIt(t *testing.T) {
	t.Setenv("TEST_RELAY_TOKEN", "relay-secret-value")
	alicePath := writeMoltnetConfig(t, t.TempDir(), "alice-net", "Alice")
	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"pair", "invite", "--relay-url", "wss://relay.example.dev",
			"--relay-token-env", "TEST_RELAY_TOKEN", "--id", "friend-net", "--config", alicePath}, "test"); err != nil {
			t.Fatal(err)
		}
	})
	code := extractInviteCode(t, output)
	invite, err := protocol.DecodeInvite(code)
	if err != nil {
		t.Fatal(err)
	}
	aliceKey, err := netkeys.LoadIdentity(netkeys.DirForConfig(alicePath))
	if err != nil {
		t.Fatal(err)
	}
	if invite.NetworkFingerprint != aliceKey.Fingerprint() || invite.NetworkPublicKey != aliceKey.Public().PublicKey {
		t.Fatalf("invite identity = %q / %q, want alice's key", invite.NetworkFingerprint, invite.NetworkPublicKey)
	}
	if info, err := os.Stat(filepath.Join(netkeys.DirForConfig(alicePath), "network.key")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("network.key mode = %v, %v", info, err)
	}

	bobPath := writeMoltnetConfig(t, t.TempDir(), "bob-net", "Bob")
	joinOutput := captureStdout(t, func() {
		if err := run(context.Background(), []string{"pair", code, "--config", bobPath}, "test"); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(joinOutput, "trusted alice-net key "+protocol.DisplayFingerprint(aliceKey.Fingerprint())) {
		t.Fatalf("join output does not name the key it pinned:\n%s", joinOutput)
	}
	pin, ok, err := netkeys.OpenPinStore(netkeys.DirForConfig(bobPath)).Lookup("alice-net")
	if err != nil || !ok || pin.Fingerprint != aliceKey.Fingerprint() || pin.Trust != protocol.KeyTrustVerified ||
		pin.Source != netkeys.PinSourceInvite || pin.SourceID != "friend-net" {
		t.Fatalf("joiner pin = %#v, %v, %v", pin, ok, err)
	}
	if strings.Contains(output, aliceKeySeed(t, alicePath)) {
		t.Fatal("invite output leaked the private key")
	}
}

func aliceKeySeed(t *testing.T, configPath string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(netkeys.DirForConfig(configPath), "network.key"))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range strings.Split(string(raw), `"`) {
		if len(field) == 43 {
			return field
		}
	}
	t.Fatal("seed not found")
	return ""
}

func TestPairJoinRefusesInviteKeyConflictingWithPin(t *testing.T) {
	t.Setenv("TEST_RELAY_TOKEN", "relay-secret-value")
	alicePath := writeMoltnetConfig(t, t.TempDir(), "alice-net", "Alice")
	code := extractInviteCode(t, captureStdout(t, func() {
		if err := run(context.Background(), []string{"pair", "invite", "--relay-url", "wss://relay.example.dev",
			"--relay-token-env", "TEST_RELAY_TOKEN", "--id", "friend-net", "--config", alicePath, "--print-only"}, "test"); err != nil {
			t.Fatal(err)
		}
	}))
	bobPath := writeMoltnetConfig(t, t.TempDir(), "bob-net", "Bob")
	impostor, err := netkeys.NewEphemeralIdentity()
	if err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() {
		if err := run(context.Background(), []string{"keys", "trust", "alice-net", impostor.Fingerprint(), "--config", bobPath}, "test"); err != nil {
			t.Fatal(err)
		}
	})
	err = run(context.Background(), []string{"pair", code, "--config", bobPath}, "test")
	if err == nil || !strings.Contains(err.Error(), "keys forget alice-net") {
		t.Fatalf("join with conflicting pinned key = %v", err)
	}
	if raw, _ := os.ReadFile(bobPath); strings.Contains(string(raw), "friend-net") {
		t.Fatal("pairing written despite key conflict")
	}
}

func TestKeysCommandsShowListTrustForget(t *testing.T) {
	path := writeMoltnetConfig(t, t.TempDir(), "carol-net", "Carol")
	show := captureStdout(t, func() {
		if err := run(context.Background(), []string{"keys", "show", "--config", path}, "test"); err != nil {
			t.Fatal(err)
		}
	})
	identity, err := netkeys.LoadIdentity(netkeys.DirForConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, protocol.DisplayFingerprint(identity.Fingerprint())) || !strings.Contains(show, identity.Public().PublicKey) {
		t.Fatalf("keys show output:\n%s", show)
	}
	if strings.Contains(show, aliceKeySeed(t, path)) {
		t.Fatal("keys show leaked the private key")
	}

	peer, _ := netkeys.NewEphemeralIdentity()
	other, _ := netkeys.NewEphemeralIdentity()
	keys := func(args ...string) (string, error) {
		var runErr error
		out := captureStdout(t, func() {
			runErr = run(context.Background(), append(append([]string{"keys"}, args...), "--config", path), "test")
		})
		return out, runErr
	}
	if out, err := keys("trust", "dave-net", protocol.DisplayFingerprint(peer.Fingerprint())); err != nil || !strings.Contains(out, "trusted dave-net") {
		t.Fatalf("trust: %q, %v", out, err)
	}
	if _, err := keys("trust", "dave-net", other.Fingerprint()); err == nil || !strings.Contains(err.Error(), "keys forget dave-net") {
		t.Fatalf("conflicting trust = %v", err)
	}
	if out, err := keys("list"); err != nil || !strings.Contains(out, "dave-net") || !strings.Contains(out, peer.Fingerprint()) ||
		!strings.Contains(out, "verified") || !strings.Contains(out, "operator") {
		t.Fatalf("list: %q, %v", out, err)
	}
	if out, err := keys("forget", "dave-net"); err != nil || !strings.Contains(out, "forgot") {
		t.Fatalf("forget: %q, %v", out, err)
	}
	if out, err := keys("list"); err != nil || !strings.Contains(out, "no pinned network keys") {
		t.Fatalf("list after forget: %q, %v", out, err)
	}
	if _, err := keys("trust", "dave-net"); err == nil {
		t.Fatal("trust without fingerprint accepted")
	}
}
