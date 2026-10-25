package netkeys

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	bindingsFileName = "pairing-bindings.json"
	bindingsVersion  = 1
)

// Pairing bindings record which network a pairing credential has spoken
// for, durably and apart from key provenance: a pin's source may change
// (`moltnet keys trust` re-sources an unconfirmed pin as operator), but the
// credential that first spoke for the network is still held to it after a
// restart. Bindings only grow; `ForgetPairing` clears one pairing's
// bindings when its credential is revoked or re-minted.
type bindingsFile struct {
	V        int                 `json:"v"`
	Bindings map[string][]string `json:"bindings"`
}

func (s *PinStore) bindingsPath() string {
	return filepath.Join(filepath.Dir(s.path), bindingsFileName)
}

// BindPairing records that pairingID's credential speaks for networkID.
func (s *PinStore) BindPairing(pairingID, networkID string) error {
	pairingID, networkID = strings.TrimSpace(pairingID), strings.TrimSpace(networkID)
	if pairingID == "" || networkID == "" {
		return nil
	}
	if known, err := s.pairingBindings(pairingID); err != nil {
		return err
	} else if slices.Contains(known, networkID) {
		return nil
	}
	return s.updateBindings(func(bindings map[string][]string) bool {
		if slices.Contains(bindings[pairingID], networkID) {
			return false
		}
		bindings[pairingID] = append(bindings[pairingID], networkID)
		slices.Sort(bindings[pairingID])
		return true
	})
}

// ForgetPairing drops every binding of pairingID (its credential is gone).
func (s *PinStore) ForgetPairing(pairingID string) error {
	pairingID = strings.TrimSpace(pairingID)
	return s.updateBindings(func(bindings map[string][]string) bool {
		if _, ok := bindings[pairingID]; !ok {
			return false
		}
		delete(bindings, pairingID)
		return true
	})
}

func (s *PinStore) pairingBindings(pairingID string) ([]string, error) {
	if s.path == "" {
		s.mu.Lock()
		defer s.mu.Unlock()
		return slices.Clone(s.bindings[pairingID]), nil
	}
	bindings, err := readBindingsFile(s.bindingsPath())
	if err != nil {
		return nil, err
	}
	return bindings[pairingID], nil
}

func (s *PinStore) updateBindings(mutate func(map[string][]string) bool) error {
	if s.path == "" {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.bindings == nil {
			s.bindings = map[string][]string{}
		}
		mutate(s.bindings)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), dirMode); err != nil {
		return fmt.Errorf("create keys directory: %w", err)
	}
	unlock, err := acquireFileLock(s.path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	bindings, err := readBindingsFile(s.bindingsPath())
	if err != nil {
		return err
	}
	if !mutate(bindings) {
		return nil
	}
	encoded, err := json.MarshalIndent(bindingsFile{V: bindingsVersion, Bindings: bindings}, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomically(s.bindingsPath(), append(encoded, '\n'))
}

// readBindingsFile fails closed like the pins file: damage never reads as
// "no bindings", which would reopen first contact for every credential.
func readBindingsFile(path string) (map[string][]string, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string][]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read pairing bindings: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var stored struct {
		V        *int                `json:"v"`
		Bindings map[string][]string `json:"bindings"`
	}
	if err := decoder.Decode(&stored); err != nil || stored.V == nil || *stored.V != bindingsVersion || stored.Bindings == nil {
		return nil, fmt.Errorf("pairing bindings file %s is invalid; fix or remove it deliberately", path)
	}
	return stored.Bindings, nil
}

func writeFileAtomically(path string, content []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*.json")
	if err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	_, writeErr := temp.Write(content)
	chmodErr := temp.Chmod(fileMode)
	syncErr := temp.Sync()
	closeErr := temp.Close()
	if err := errors.Join(writeErr, chmodErr, syncErr, closeErr); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}
