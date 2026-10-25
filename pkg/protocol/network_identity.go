package protocol

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// NetworkKeyAlgorithmEd25519 is the only network signing key algorithm in v1.
const NetworkKeyAlgorithmEd25519 = "ed25519"

// FingerprintPrefix marks a network key fingerprint: "sha256:" followed by
// the 64 lowercase hex digits of SHA-256 over the raw 32-byte public key.
const FingerprintPrefix = "sha256:"

// Key trust levels. A verified key was pinned independently by this
// operator: an invite, a pairing credential config binds to the network, or
// `moltnet keys trust`. An introduced key was vouched for by a room home
// only; its signatures check out but the network identity behind it is not
// proven. An unconfirmed key was learned on first contact over a pairing
// credential config never bound to a network (the inviting side): it holds
// the network id against other keys, but any verified pin replaces it, so a
// peer cannot squat another network's name by claiming it first.
const (
	KeyTrustVerified    = "verified"
	KeyTrustIntroduced  = "introduced"
	KeyTrustUnconfirmed = "unconfirmed"
)

// Origin signature states exposed on the HTTP message API. They are never
// part of the machine v1 projection.
const (
	SignatureStateVerified   = "verified"
	SignatureStateIntroduced  = "introduced"
	SignatureStateUnconfirmed = "unconfirmed"
	SignatureStateInvalid    = "invalid"
	SignatureStateUnsigned   = "unsigned"
)

// NetworkIdentity is a network's public signing identity, published on
// GET /v1/network. Network ids stay human-readable; the fingerprint is the
// cryptographic identity shown beside them.
//
// v1 has no key rotation: losing or leaking the private key means a new
// identity, and every peer must re-trust it (`moltnet keys forget` then
// `moltnet keys trust`).
type NetworkIdentity struct {
	Algorithm   string `json:"algorithm"`
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
}

// EncodeNetworkPublicKey renders a raw Ed25519 public key as unpadded
// base64url, the wire form used by invites, envelopes and /v1/network.
func EncodeNetworkPublicKey(key ed25519.PublicKey) string {
	return base64.RawURLEncoding.EncodeToString(key)
}

// ParseNetworkPublicKey decodes the wire form of an Ed25519 public key.
func ParseNetworkPublicKey(value string) (ed25519.PublicKey, error) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("network public key is not unpadded base64url: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("network public key must be %d bytes, got %d", ed25519.PublicKeySize, len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

// NetworkKeyFingerprint is "sha256:<hex>" over the raw public key bytes.
func NetworkKeyFingerprint(key ed25519.PublicKey) string {
	sum := sha256.Sum256(key)
	return FingerprintPrefix + hex.EncodeToString(sum[:])
}

// NormalizeFingerprint accepts "sha256:<hex>", bare hex, and the grouped
// display form (spaces or colons between hex groups), case-insensitively,
// and returns the canonical "sha256:<64 lowercase hex>" form.
func NormalizeFingerprint(value string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	trimmed = strings.TrimPrefix(trimmed, FingerprintPrefix)
	trimmed = strings.NewReplacer(" ", "", ":", "", "-", "").Replace(trimmed)
	if len(trimmed) != sha256.Size*2 {
		return "", fmt.Errorf("fingerprint must be %d hex digits", sha256.Size*2)
	}
	if _, err := hex.DecodeString(trimmed); err != nil {
		return "", fmt.Errorf("fingerprint is not hex: %w", err)
	}
	return FingerprintPrefix + trimmed, nil
}

// DisplayFingerprint groups a canonical fingerprint into 4-digit blocks for
// humans comparing it out of band. Non-canonical input is returned as is.
func DisplayFingerprint(fingerprint string) string {
	normalized, err := NormalizeFingerprint(fingerprint)
	if err != nil {
		return fingerprint
	}
	digits := strings.TrimPrefix(normalized, FingerprintPrefix)
	groups := make([]string, 0, len(digits)/4)
	for index := 0; index < len(digits); index += 4 {
		groups = append(groups, digits[index:index+4])
	}
	return FingerprintPrefix + strings.Join(groups, " ")
}

// ValidateNetworkIdentity checks that the fingerprint matches the key.
func ValidateNetworkIdentity(identity NetworkIdentity) error {
	if identity.Algorithm != NetworkKeyAlgorithmEd25519 {
		return fmt.Errorf("unsupported network key algorithm %q", identity.Algorithm)
	}
	key, err := ParseNetworkPublicKey(identity.PublicKey)
	if err != nil {
		return err
	}
	if NetworkKeyFingerprint(key) != identity.Fingerprint {
		return fmt.Errorf("network key fingerprint does not match its public key")
	}
	return nil
}
