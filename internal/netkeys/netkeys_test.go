package netkeys

import (
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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
