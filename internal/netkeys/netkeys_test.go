package netkeys

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestIdentityPersistsWithPrivatePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".moltnet", "keys")
	first, created, err := LoadOrCreateIdentity(dir)
	if err != nil || !created {
		t.Fatalf("first load created=%v err=%v", created, err)
	}
	info, err := os.Stat(filepath.Join(dir, identityFileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %o, want 600", info.Mode().Perm())
	}
	if dirInfo, err := os.Stat(dir); err != nil || dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("keys dir mode = %v, %v", dirInfo.Mode().Perm(), err)
	}
	second, created, err := LoadOrCreateIdentity(dir)
	if err != nil || created {
		t.Fatalf("reload created=%v err=%v", created, err)
	}
	if first.Fingerprint() != second.Fingerprint() {
		t.Fatal("identity changed across reload")
	}
	signature := second.Sign([]byte("payload"))
	if !ed25519.Verify(first.PublicKey(), []byte("payload"), signature) {
		t.Fatal("reloaded key does not sign for the same public key")
	}
	if err := ValidateIdentityFingerprint(first); err != nil {
		t.Fatal(err)
	}
}

func ValidateIdentityFingerprint(identity *Identity) error {
	return protocol.ValidateNetworkIdentity(identity.Public())
}

func TestIdentityNeverRendersPrivateKey(t *testing.T) {
	dir := t.TempDir()
	identity, _, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, identityFileName))
	if err != nil {
		t.Fatal(err)
	}
	var seed string
	for _, field := range strings.Split(string(raw), `"`) {
		if len(field) == 43 { // base64url of a 32-byte seed
			seed = field
		}
	}
	if seed == "" {
		t.Fatalf("seed not found in key file")
	}
	marshaled, _ := identity.MarshalJSON()
	for _, rendered := range []string{identity.String(), string(marshaled)} {
		if strings.Contains(rendered, seed) {
			t.Fatalf("private seed leaked into %q", rendered)
		}
	}
}

func TestIdentityRefusesWorldReadableKey(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := LoadOrCreateIdentity(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, identityFileName), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrCreateIdentity(dir); err == nil || !strings.Contains(err.Error(), "readable") {
		t.Fatalf("world-readable key accepted: %v", err)
	}
}

func TestIdentityConcurrentCreateAgreesOnOneKey(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	fingerprints := make([]string, 8)
	for index := range fingerprints {
		wg.Add(1)
		go func() {
			defer wg.Done()
			identity, _, err := LoadOrCreateIdentity(dir)
			if err != nil {
				t.Error(err)
				return
			}
			fingerprints[index] = identity.Fingerprint()
		}()
	}
	wg.Wait()
	for _, fingerprint := range fingerprints {
		if fingerprint != fingerprints[0] {
			t.Fatalf("concurrent creation produced different keys: %q", fingerprints)
		}
	}
}

func testPin(t *testing.T, networkID string) (Pin, *Identity) {
	t.Helper()
	identity, err := NewEphemeralIdentity()
	if err != nil {
		t.Fatal(err)
	}
	public := identity.Public()
	return Pin{NetworkID: networkID, Fingerprint: public.Fingerprint, PublicKey: public.PublicKey,
		Trust: protocol.KeyTrustVerified, Source: PinSourceInvite}, identity
}

func TestPinStorePersistsAndRejectsConflicts(t *testing.T) {
	dir := t.TempDir()
	store := OpenPinStore(dir)
	pin, _ := testPin(t, "alpha")
	if _, err := store.PinKey(pin); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dir, pinsFileName)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("pins file mode = %v, %v", info, err)
	}
	reopened := OpenPinStore(dir)
	got, ok, err := reopened.Lookup("alpha")
	if err != nil || !ok || got.Fingerprint != pin.Fingerprint || got.Trust != protocol.KeyTrustVerified {
		t.Fatalf("reopened pin = %#v, %v, %v", got, ok, err)
	}
	other, _ := testPin(t, "alpha")
	if _, err := reopened.PinKey(other); !errors.Is(err, ErrPinConflict) {
		t.Fatalf("conflicting pin error = %v", err)
	}
	if again, err := reopened.PinKey(pin); err != nil || again.Fingerprint != pin.Fingerprint {
		t.Fatalf("re-pinning the same key = %#v, %v", again, err)
	}
	removed, err := reopened.Forget("alpha")
	if err != nil || !removed {
		t.Fatalf("forget = %v, %v", removed, err)
	}
	if _, err := reopened.PinKey(other); err != nil {
		t.Fatalf("pin after forget: %v", err)
	}
}

func TestPinStoreUpgradesIntroducedToVerified(t *testing.T) {
	store := NewMemoryPinStore()
	pin, _ := testPin(t, "beta")
	introduced := pin
	introduced.Trust, introduced.Source, introduced.IntroducedBy = protocol.KeyTrustIntroduced, "room", "home"
	if _, err := store.PinKey(introduced); err != nil {
		t.Fatal(err)
	}
	pin.Source, pin.PublicKey = PinSourceOperator, ""
	upgraded, err := store.PinKey(pin)
	if err != nil || upgraded.Trust != protocol.KeyTrustVerified || upgraded.IntroducedBy != "" || upgraded.PublicKey == "" {
		t.Fatalf("upgrade = %#v, %v", upgraded, err)
	}
}

func TestPinStoreFailsClosedOnDamagedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, pinsFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenPinStore(dir).Lookup("alpha"); err == nil {
		t.Fatal("damaged pins file read as empty")
	}
}

func TestPinStoreRejectsInconsistentPin(t *testing.T) {
	pin, _ := testPin(t, "gamma")
	other, _ := testPin(t, "gamma")
	pin.PublicKey = other.PublicKey
	if _, err := NewMemoryPinStore().PinKey(pin); err == nil {
		t.Fatal("pin whose key does not match its fingerprint accepted")
	}
}

func TestPinStoreRejectsIncompleteOrInvalidFiles(t *testing.T) {
	good, _ := testPin(t, "alpha")
	good.PinnedAt = time.Now().UTC()
	record := func(mutate func(map[string]any)) string {
		raw, _ := json.Marshal(good)
		var value map[string]any
		_ = json.Unmarshal(raw, &value)
		mutate(value)
		encoded, _ := json.Marshal(value)
		return string(encoded)
	}
	cases := map[string]string{
		"version only":       `{"v":1}`,
		"null pins":          `{"v":1,"pins":null}`,
		"missing version":    `{"pins":{}}`,
		"unknown field":      `{"v":1,"pins":{},"extra":true}`,
		"misfiled record":    `{"v":1,"pins":{"beta":` + record(func(map[string]any) {}) + `}}`,
		"unknown trust":      `{"v":1,"pins":{"alpha":` + record(func(v map[string]any) { v["trust"] = "trusted" }) + `}}`,
		"empty trust":        `{"v":1,"pins":{"alpha":` + record(func(v map[string]any) { delete(v, "trust") }) + `}}`,
		"key mismatch":       `{"v":1,"pins":{"alpha":` + record(func(v map[string]any) { v["fingerprint"] = "sha256:" + strings.Repeat("ab", 32) }) + `}}`,
		"noncanonical print": `{"v":1,"pins":{"alpha":` + record(func(v map[string]any) { v["fingerprint"] = strings.ToUpper(good.Fingerprint) }) + `}}`,
		"unknown source":     `{"v":1,"pins":{"alpha":` + record(func(v map[string]any) { v["source"] = "gossip" }) + `}}`,
		"introduced no home": `{"v":1,"pins":{"alpha":` + record(func(v map[string]any) { v["trust"] = "introduced" }) + `}}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, pinsFileName), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := OpenPinStore(dir).Lookup("alpha"); err == nil {
				t.Fatalf("pins file %s loaded", content)
			}
		})
	}
	dir := t.TempDir()
	valid := `{"v":1,"pins":{"alpha":` + record(func(map[string]any) {}) + `}}`
	if err := os.WriteFile(filepath.Join(dir, pinsFileName), []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := OpenPinStore(dir).Lookup("alpha"); err != nil || !ok {
		t.Fatalf("valid pins file = %v, %v", ok, err)
	}
}

func TestPinLockIsReleasedByHolderNotByAge(t *testing.T) {
	previous := lockWait
	lockWait = 200 * time.Millisecond
	defer func() { lockWait = previous }()
	path := filepath.Join(t.TempDir(), "pins.json.lock")
	release, err := acquireFileLock(path)
	if err != nil {
		t.Fatal(err)
	}
	// An old lock file is not evidence the holder died: it is still held.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireFileLock(path); err == nil {
		t.Fatal("second writer took a held lock")
	}
	release()
	// A lock file left behind by an exited holder never blocks.
	again, err := acquireFileLock(path)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	again()
}

func TestPinStoreConcurrentWritersKeepEveryPin(t *testing.T) {
	dir := t.TempDir()
	const writers = 16
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for index := 0; index < writers; index++ {
		pin, _ := testPin(t, fmt.Sprintf("net-%02d", index))
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := OpenPinStore(dir).PinKey(pin)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	pins, err := OpenPinStore(dir).List()
	if err != nil || len(pins) != writers {
		t.Fatalf("pins after concurrent writes = %d, %v", len(pins), err)
	}
}
