package hubinvites

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	fileName    = "hub-invites.json"
	fileVersion = 1
	dirMode     = 0o700
	fileMode    = 0o600
)

// Grant is a room invite the home minted: redeeming it over its pairing
// admits one network to one shared room with Role, once, before Exp.
type Grant struct {
	PairingID    string    `json:"pairing_id"`
	GlobalRoomID string    `json:"global_room_id"`
	Role         string    `json:"role"`
	Exp          time.Time `json:"exp"`
}

// PendingJoin is a room invite this network redeemed with `moltnet room
// join`; the server finishes it once the invite's pairing is live.
type PendingJoin struct {
	PairingID    string `json:"pairing_id"`
	GlobalRoomID string `json:"global_room_id"`
	LocalRoomID  string `json:"local_room_id,omitempty"`
	// Exp is the invite's expiry: the server stops retrying after it and
	// marks the join JoinExpired (zero: no expiry recorded).
	Exp time.Time `json:"exp,omitempty"`
	// Status is "" while the server still retries, or a terminal state.
	Status string `json:"status,omitempty"`
	// LastError is the last reason the join did not finish.
	LastError string `json:"last_error,omitempty"`
}

// JoinExpired is the terminal status of a pending join whose invite
// expired before the home admitted this network.
const JoinExpired = "expired"

// Pending reports whether the server should still try to finish the join.
func (j PendingJoin) Pending() bool { return j.Status == "" }

// Link records the pairing a member network was admitted over for one
// shared room (home side). The home authorizes that pairing for that room
// only, and fans the room out to the network over it: a network may hold
// several pairings to the home (a direct pairing, other rooms' links).
type Link struct {
	PairingID    string `json:"pairing_id"`
	GlobalRoomID string `json:"global_room_id"`
	NetworkID    string `json:"network_id"`
}

type state struct {
	V      int           `json:"v"`
	Grants []Grant       `json:"grants"`
	Joins  []PendingJoin `json:"joins"`
	Links  []Link        `json:"links,omitempty"`
}

// Store keeps grants and pending joins beside the network's keys, shared by
// the CLI (which writes them with the pairing) and the server (which
// redeems and finishes them). Writes take an flock and replace the file
// atomically; a damaged file fails closed.
type Store struct {
	path   string
	mu     sync.Mutex
	memory *state
}

// Open uses dir/hub-invites.json (dir is the keys directory).
func Open(dir string) *Store { return &Store{path: filepath.Join(dir, fileName)} }

// NewMemory is an in-memory store for tests and keyless servers.
func NewMemory() *Store { return &Store{memory: &state{V: fileVersion}} }

func (s *Store) AddGrant(grant Grant) error {
	return s.update(func(current *state) {
		current.Grants = append(withoutGrant(current.Grants, grant.PairingID, grant.GlobalRoomID), grant)
	})
}

// Grant returns the live grant for pairingID and room, if any.
func (s *Store) Grant(pairingID, globalRoomID string, now time.Time) (Grant, bool, error) {
	current, err := s.read()
	if err != nil {
		return Grant{}, false, err
	}
	for _, grant := range current.Grants {
		if grant.PairingID == pairingID && grant.GlobalRoomID == globalRoomID && now.Before(grant.Exp) {
			return grant, true, nil
		}
	}
	return Grant{}, false, nil
}

func (s *Store) RemoveGrant(pairingID, globalRoomID string) error {
	return s.update(func(current *state) { current.Grants = withoutGrant(current.Grants, pairingID, globalRoomID) })
}

func (s *Store) AddJoin(join PendingJoin) error {
	return s.update(func(current *state) {
		current.Joins = append(withoutJoin(current.Joins, join.GlobalRoomID), join)
	})
}

func (s *Store) Joins() ([]PendingJoin, error) {
	current, err := s.read()
	if err != nil {
		return nil, err
	}
	return append([]PendingJoin(nil), current.Joins...), nil
}

func (s *Store) RemoveJoin(globalRoomID string) error {
	return s.update(func(current *state) { current.Joins = withoutJoin(current.Joins, globalRoomID) })
}

// UpdateJoin replaces the pending join for join.GlobalRoomID, if any.
func (s *Store) UpdateJoin(join PendingJoin) error {
	return s.update(func(current *state) {
		for index := range current.Joins {
			if current.Joins[index].GlobalRoomID == join.GlobalRoomID {
				current.Joins[index] = join
			}
		}
	})
}

// SetLink records link, replacing any earlier link of its network for its
// room (a re-admission over another pairing moves the room to it).
func (s *Store) SetLink(link Link) error {
	return s.update(func(current *state) {
		kept := current.Links[:0:0]
		for _, existing := range current.Links {
			if existing.GlobalRoomID != link.GlobalRoomID || existing.NetworkID != link.NetworkID {
				kept = append(kept, existing)
			}
		}
		current.Links = append(kept, link)
	})
}

// Links lists the member links of one shared room.
func (s *Store) Links(globalRoomID string) ([]Link, error) {
	current, err := s.read()
	if err != nil {
		return nil, err
	}
	var links []Link
	for _, link := range current.Links {
		if link.GlobalRoomID == globalRoomID {
			links = append(links, link)
		}
	}
	return links, nil
}

func withoutGrant(grants []Grant, pairingID, globalRoomID string) []Grant {
	kept := make([]Grant, 0, len(grants))
	for _, grant := range grants {
		if grant.PairingID != pairingID || grant.GlobalRoomID != globalRoomID {
			kept = append(kept, grant)
		}
	}
	return kept
}

func withoutJoin(joins []PendingJoin, globalRoomID string) []PendingJoin {
	kept := make([]PendingJoin, 0, len(joins))
	for _, join := range joins {
		if join.GlobalRoomID != globalRoomID {
			kept = append(kept, join)
		}
	}
	return kept
}

func (s *Store) read() (*state, error) {
	if s.path == "" {
		s.mu.Lock()
		defer s.mu.Unlock()
		copied := *s.memory
		copied.Grants, copied.Joins = append([]Grant(nil), s.memory.Grants...), append([]PendingJoin(nil), s.memory.Joins...)
		copied.Links = append([]Link(nil), s.memory.Links...)
		return &copied, nil
	}
	return readFile(s.path)
}

func (s *Store) update(mutate func(*state)) error {
	if s.path == "" {
		s.mu.Lock()
		defer s.mu.Unlock()
		mutate(s.memory)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), dirMode); err != nil {
		return fmt.Errorf("create keys directory: %w", err)
	}
	unlock, err := lockFile(s.path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := readFile(s.path)
	if err != nil {
		return err
	}
	mutate(current)
	encoded, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomically(s.path, append(encoded, '\n'))
}

func readFile(path string) (*state, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &state{V: fileVersion}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read hub invites: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var stored state
	if err := decoder.Decode(&stored); err != nil || stored.V != fileVersion {
		return nil, fmt.Errorf("hub invites file %s is invalid; fix or remove it deliberately", path)
	}
	for _, grant := range stored.Grants {
		if strings.TrimSpace(grant.PairingID) == "" || strings.TrimSpace(grant.GlobalRoomID) == "" {
			return nil, fmt.Errorf("hub invites file %s has an incomplete grant", path)
		}
	}
	for _, link := range stored.Links {
		if strings.TrimSpace(link.PairingID) == "" || strings.TrimSpace(link.GlobalRoomID) == "" || strings.TrimSpace(link.NetworkID) == "" {
			return nil, fmt.Errorf("hub invites file %s has an incomplete member link", path)
		}
	}
	return &stored, nil
}

func writeAtomically(path string, content []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".hub-invites-*.json")
	if err != nil {
		return fmt.Errorf("write hub invites: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	_, writeErr := temp.Write(content)
	chmodErr := temp.Chmod(fileMode)
	syncErr := temp.Sync()
	closeErr := temp.Close()
	if err := errors.Join(writeErr, chmodErr, syncErr, closeErr); err != nil {
		return fmt.Errorf("write hub invites: %w", err)
	}
	return os.Rename(tempPath, path)
}

// lockFile takes an advisory flock the kernel releases if the holder dies.
func lockFile(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, fileMode)
	if err != nil {
		return nil, fmt.Errorf("lock hub invites: %w", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) || time.Now().After(deadline) {
			_ = file.Close()
			return nil, fmt.Errorf("lock hub invites: %w", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
