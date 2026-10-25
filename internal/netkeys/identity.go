// Package netkeys holds a network's Ed25519 signing identity and its
// durable trust store of pinned peer keys.
//
// v1 has no key rotation: losing or leaking the private key means a new
// network identity, and every peer has to re-trust it explicitly
// (`moltnet keys forget <id>` then `moltnet keys trust <id> <fingerprint>`).
package netkeys

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/noopolis/moltnet/pkg/protocol"
)

const (
	identityFileName = "network.key"
	identityVersion  = 1
	dirMode          = 0o700
	fileMode         = 0o600
)

// DirForConfig is the keys directory for a network config file.
func DirForConfig(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), ".moltnet", "keys")
}

// Identity is a network's signing key. It never exposes the private key:
// there is no accessor, and String/JSON render only public data.
type Identity struct {
	private ed25519.PrivateKey
	public  ed25519.PublicKey
}

type identityFile struct {
	V         int    `json:"v"`
	Algorithm string `json:"algorithm"`
	Seed      string `json:"seed"`
}

// NewEphemeralIdentity generates an in-memory identity for servers with no
// durable keys directory (and for tests).
func NewEphemeralIdentity() (*Identity, error) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate network key: %w", err)
	}
	return identityFromPrivate(private), nil
}

func identityFromPrivate(private ed25519.PrivateKey) *Identity {
	return &Identity{private: private, public: private.Public().(ed25519.PublicKey)}
}

// LoadOrCreateIdentity loads dir/network.key, creating it on first use.
// Creation is exclusive, so a server and a CLI racing on first start agree
// on one key. created reports whether this call generated it.
func LoadOrCreateIdentity(dir string) (identity *Identity, created bool, err error) {
	path := filepath.Join(dir, identityFileName)
	if identity, err := loadIdentity(path); err == nil {
		return identity, false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, false, fmt.Errorf("create keys directory: %w", err)
	}
	generated, err := NewEphemeralIdentity()
	if err != nil {
		return nil, false, err
	}
	encoded, err := json.Marshal(identityFile{
		V: identityVersion, Algorithm: protocol.NetworkKeyAlgorithmEd25519,
		Seed: base64.RawURLEncoding.EncodeToString(generated.private.Seed()),
	})
	if err != nil {
		return nil, false, err
	}
	// Write a complete temp file, then hard-link it into place: the link is
	// atomic and exclusive, so a concurrent reader never sees a partial key
	// and a concurrent creator loses cleanly to the first one.
	temp, err := os.CreateTemp(dir, ".network-*.key")
	if err != nil {
		return nil, false, fmt.Errorf("create network key: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	_, writeErr := temp.Write(append(encoded, '\n'))
	chmodErr := temp.Chmod(fileMode)
	syncErr := temp.Sync()
	closeErr := temp.Close()
	if err := errors.Join(writeErr, chmodErr, syncErr, closeErr); err != nil {
		return nil, false, fmt.Errorf("write network key: %w", err)
	}
	if err := os.Link(tempPath, path); errors.Is(err, os.ErrExist) {
		identity, err := loadIdentity(path)
		return identity, false, err
	} else if err != nil {
		return nil, false, fmt.Errorf("install network key: %w", err)
	}
	return generated, true, nil
}

// LoadIdentity loads an existing dir/network.key without creating one.
func LoadIdentity(dir string) (*Identity, error) {
	return loadIdentity(filepath.Join(dir, identityFileName))
}

func loadIdentity(path string) (*Identity, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("network key %s is readable by other users (mode %o); run chmod 600", path, info.Mode().Perm())
	}
	if owner, ok := fileOwner(info); ok && owner != os.Getuid() {
		return nil, fmt.Errorf("network key %s is owned by uid %d, but this process runs as uid %d; run moltnet as the key's owner or chown the keys directory", path, owner, os.Getuid())
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrPermission) {
		return nil, fmt.Errorf("network key %s is not readable by this user (uid %d); run moltnet as the key's owner or fix its permissions", path, os.Getuid())
	}
	if err != nil {
		return nil, err
	}
	var stored identityFile
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("network key %s is not valid", path)
	}
	if stored.V != identityVersion || stored.Algorithm != protocol.NetworkKeyAlgorithmEd25519 {
		return nil, fmt.Errorf("network key %s has unsupported version or algorithm", path)
	}
	seed, err := base64.RawURLEncoding.Strict().DecodeString(stored.Seed)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("network key %s is not valid", path)
	}
	return identityFromPrivate(ed25519.NewKeyFromSeed(seed)), nil
}

// fileOwner returns the owning uid where the platform reports one.
func fileOwner(info os.FileInfo) (int, bool) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(stat.Uid), true
	}
	return 0, false
}

func (i *Identity) PublicKey() ed25519.PublicKey {
	return append(ed25519.PublicKey(nil), i.public...)
}

func (i *Identity) Fingerprint() string {
	return protocol.NetworkKeyFingerprint(i.public)
}

// Public is the identity as published on GET /v1/network.
func (i *Identity) Public() protocol.NetworkIdentity {
	return protocol.NetworkIdentity{
		Algorithm:   protocol.NetworkKeyAlgorithmEd25519,
		PublicKey:   protocol.EncodeNetworkPublicKey(i.public),
		Fingerprint: i.Fingerprint(),
	}
}

func (i *Identity) Sign(message []byte) []byte {
	return ed25519.Sign(i.private, message)
}

// String and MarshalJSON keep accidental logging to public data only.
func (i *Identity) String() string { return i.Fingerprint() }

func (i *Identity) MarshalJSON() ([]byte, error) { return json.Marshal(i.Public()) }
