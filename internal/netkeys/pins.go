package netkeys

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

const (
	pinsFileName = "pins.json"
	pinsVersion  = 1
)

// ErrPinConflict means a network id is already pinned to a different key.
// It is never resolved automatically: an operator must forget the old pin.
var ErrPinConflict = errors.New("network is already pinned to a different key")

// Pin sources.
const (
	PinSourceInvite   = "invite"
	PinSourcePairing  = "pairing"
	PinSourceOperator = "operator"
	// PinSourceRoom is a key a room home introduced (trust introduced).
	PinSourceRoom = "room"
)

// Pin binds a human-readable network id to a key fingerprint.
type Pin struct {
	NetworkID   string `json:"network_id"`
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"public_key,omitempty"`
	// Trust is protocol.KeyTrustVerified or protocol.KeyTrustIntroduced.
	Trust string `json:"trust"`
	// Source records how the pin was learned: invite, pairing, operator.
	Source string `json:"source"`
	// SourceID is the pairing id for invite/pairing pins.
	SourceID string `json:"source_id,omitempty"`
	// IntroducedBy is the vouching room home's network id (chunk B).
	IntroducedBy string    `json:"introduced_by,omitempty"`
	PinnedAt     time.Time `json:"pinned_at"`
}

type pinsFile struct {
	V    int            `json:"v"`
	Pins map[string]Pin `json:"pins"`
}

// PinStore is the durable trust store. With an empty path it is in-memory
// (tests and keyless servers) but enforces the same rules.
type PinStore struct {
	path   string
	mu     sync.Mutex
	memory map[string]Pin
	now    func() time.Time
}

// OpenPinStore uses dir/pins.json; the file is created on the first pin.
func OpenPinStore(dir string) *PinStore {
	return &PinStore{path: filepath.Join(dir, pinsFileName), now: time.Now}
}

func NewMemoryPinStore() *PinStore {
	return &PinStore{memory: map[string]Pin{}, now: time.Now}
}

// Lookup reads the current pin for networkID. File-backed stores re-read
// the file so `moltnet keys trust/forget` take effect without a restart.
func (s *PinStore) Lookup(networkID string) (Pin, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pins, err := s.readLocked()
	if err != nil {
		return Pin{}, false, err
	}
	pin, ok := pins[strings.TrimSpace(networkID)]
	return pin, ok, nil
}

// List returns every pin ordered by network id.
func (s *PinStore) List() ([]Pin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pins, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	result := make([]Pin, 0, len(pins))
	for _, pin := range pins {
		result = append(result, pin)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].NetworkID < result[j].NetworkID })
	return result, nil
}

// PinKey records pin unless the network is already pinned. Pinning the same
// fingerprint again returns the stored pin, upgrading introduced to verified
// when the new pin is verified. A different fingerprint is ErrPinConflict.
func (s *PinStore) PinKey(pin Pin) (Pin, error) {
	pin.NetworkID = strings.TrimSpace(pin.NetworkID)
	fingerprint, err := protocol.NormalizeFingerprint(pin.Fingerprint)
	if err != nil {
		return Pin{}, err
	}
	pin.Fingerprint = fingerprint
	if err := validatePin(pin); err != nil {
		return Pin{}, err
	}
	if pin.PinnedAt.IsZero() {
		pin.PinnedAt = s.now().UTC()
	}

	var result Pin
	err = s.update(func(pins map[string]Pin) (bool, error) {
		existing, ok := pins[pin.NetworkID]
		if !ok {
			pins[pin.NetworkID], result = pin, pin
			return true, nil
		}
		if existing.Fingerprint != pin.Fingerprint {
			return false, fmt.Errorf("%w: %s is pinned to %s, offered %s", ErrPinConflict,
				pin.NetworkID, existing.Fingerprint, pin.Fingerprint)
		}
		changed := false
		if existing.PublicKey == "" && pin.PublicKey != "" {
			existing.PublicKey, changed = pin.PublicKey, true
		}
		if existing.Trust != protocol.KeyTrustVerified && pin.Trust == protocol.KeyTrustVerified {
			existing.Trust, existing.Source, existing.SourceID, existing.IntroducedBy = pin.Trust, pin.Source, pin.SourceID, ""
			changed = true
		}
		pins[pin.NetworkID], result = existing, existing
		return changed, nil
	})
	return result, err
}

// PairingNetworks returns the network ids pinned through pairing
// pairingID (invite or first-contact pins), so the network a pairing
// credential speaks for survives restarts with its pin.
func (s *PinStore) PairingNetworks(pairingID string) ([]string, error) {
	pins, err := s.List()
	if err != nil {
		return nil, err
	}
	var networks []string
	for _, pin := range pins {
		if (pin.Source == PinSourcePairing || pin.Source == PinSourceInvite) && pin.SourceID == pairingID && pairingID != "" {
			networks = append(networks, pin.NetworkID)
		}
	}
	return networks, nil
}

// Forget removes a pin, reporting whether one existed.
func (s *PinStore) Forget(networkID string) (bool, error) {
	removed := false
	err := s.update(func(pins map[string]Pin) (bool, error) {
		if _, ok := pins[strings.TrimSpace(networkID)]; ok {
			delete(pins, strings.TrimSpace(networkID))
			removed = true
		}
		return removed, nil
	})
	return removed, err
}

func (s *PinStore) update(mutate func(map[string]Pin) (bool, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		_, err := mutate(s.memory)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), dirMode); err != nil {
		return fmt.Errorf("create keys directory: %w", err)
	}
	unlock, err := acquireFileLock(s.path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	pins, err := s.readLocked()
	if err != nil {
		return err
	}
	changed, err := mutate(pins)
	if err != nil || !changed {
		return err
	}
	return writePinsFile(s.path, pins)
}

func (s *PinStore) readLocked() (map[string]Pin, error) {
	if s.path == "" {
		copied := make(map[string]Pin, len(s.memory))
		for id, pin := range s.memory {
			copied[id] = pin
		}
		return copied, nil
	}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Pin{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read key pins: %w", err)
	}
	pins, err := decodePinsFile(raw)
	if err != nil {
		// Fail closed: a damaged trust store must never read as "no pins",
		// which would silently re-open first-contact pinning.
		return nil, fmt.Errorf("key pins file %s is invalid (%v); fix or remove it deliberately", s.path, err)
	}
	return pins, nil
}

// decodePinsFile accepts only the exact shape this package writes: version
// 1, a present pins object, and every record valid and filed under its own
// network id. Anything else is damage, never an empty trust store.
func decodePinsFile(raw []byte) (map[string]Pin, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var stored struct {
		V    *int           `json:"v"`
		Pins map[string]Pin `json:"pins"`
	}
	if err := decoder.Decode(&stored); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, errors.New("trailing data")
	}
	if stored.V == nil || *stored.V != pinsVersion {
		return nil, errors.New("unsupported version")
	}
	if stored.Pins == nil {
		return nil, errors.New("pins object is missing")
	}
	for key, pin := range stored.Pins {
		if key != pin.NetworkID {
			return nil, fmt.Errorf("pin filed under %q names network %q", key, pin.NetworkID)
		}
		if normalized, err := protocol.NormalizeFingerprint(pin.Fingerprint); err != nil || normalized != pin.Fingerprint {
			return nil, fmt.Errorf("pin %q fingerprint is not canonical", key)
		}
		if pin.PinnedAt.IsZero() {
			return nil, fmt.Errorf("pin %q has no pinned_at", key)
		}
		if err := validatePin(pin); err != nil {
			return nil, fmt.Errorf("pin %q: %w", key, err)
		}
	}
	return stored.Pins, nil
}

// validatePin checks one record whose fingerprint is already canonical.
func validatePin(pin Pin) error {
	if pin.NetworkID != strings.TrimSpace(pin.NetworkID) {
		return errors.New("pin network id has surrounding whitespace")
	}
	if err := protocol.ValidateNetworkNamespace(pin.NetworkID); err != nil {
		return fmt.Errorf("pin network id: %w", err)
	}
	if pin.PublicKey != "" {
		key, err := protocol.ParseNetworkPublicKey(pin.PublicKey)
		if err != nil {
			return err
		}
		if protocol.NetworkKeyFingerprint(key) != pin.Fingerprint {
			return errors.New("pin public key does not match its fingerprint")
		}
	}
	switch pin.Trust {
	case protocol.KeyTrustVerified:
		if pin.IntroducedBy != "" {
			return errors.New("a verified pin cannot be introduced by a room home")
		}
	case protocol.KeyTrustIntroduced:
		if pin.IntroducedBy == "" {
			return errors.New("an introduced pin must name the room home that introduced it")
		}
	default:
		return fmt.Errorf("unsupported pin trust %q", pin.Trust)
	}
	switch pin.Source {
	case PinSourceInvite, PinSourcePairing, PinSourceOperator, PinSourceRoom:
	default:
		return fmt.Errorf("unsupported pin source %q", pin.Source)
	}
	return nil
}

func writePinsFile(path string, pins map[string]Pin) error {
	encoded, err := json.MarshalIndent(pinsFile{V: pinsVersion, Pins: pins}, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".pins-*.json")
	if err != nil {
		return fmt.Errorf("write key pins: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	_, writeErr := temp.Write(append(encoded, '\n'))
	chmodErr := temp.Chmod(fileMode)
	syncErr := temp.Sync()
	closeErr := temp.Close()
	if err := errors.Join(writeErr, chmodErr, syncErr, closeErr); err != nil {
		return fmt.Errorf("write key pins: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("write key pins: %w", err)
	}
	return nil
}
