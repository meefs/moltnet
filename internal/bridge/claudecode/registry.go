package claudecode

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

var registryFilePattern = regexp.MustCompile(`^([0-9]+)\.json$`)

// sessionRecord is the subset of Claude Code's on-disk session registry
// entry (<config>/sessions/<pid>.json) the attached driver reads.
type sessionRecord struct {
	PID                 int    `json:"pid"`
	SessionID           string `json:"sessionId"`
	Name                string `json:"name"`
	MessagingSocketPath string `json:"messagingSocketPath"`
}

// sessionTarget is a resolved, reachable session inbox. Token is a secret and
// must never be logged or included in an error.
type sessionTarget struct {
	SocketPath string
	Token      string
}

type registry struct {
	dir   string
	uid   int
	alive func(pid int) bool
}

func newRegistry(configDir string) registry {
	return registry{dir: filepath.Join(configDir, "sessions"), uid: os.Getuid(), alive: processAlive}
}

// claudeConfigDir mirrors Claude Code's own lookup: CLAUDE_CONFIG_DIR, else
// <home>/.claude, where home is runtime.home_path when set.
func claudeConfigDir(homePath string) (string, error) {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return dir, nil
	}
	home := strings.TrimSpace(homePath)
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return "", fmt.Errorf("resolve home directory for Claude Code registry: %w", err)
		}
	}
	return filepath.Join(home, ".claude"), nil
}

// find resolves session (a session name or id) to exactly one live session
// whose messaging socket is safe to write to.
func (r registry) find(session string) (sessionTarget, error) {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return sessionTarget{}, fmt.Errorf("read Claude Code session registry %s: %w", r.dir, err)
	}

	var reachable []sessionRecord
	var problems []string
	matched := 0
	for _, entry := range entries {
		groups := registryFilePattern.FindStringSubmatch(entry.Name())
		if groups == nil {
			continue
		}
		record, ok := r.readRecord(filepath.Join(r.dir, entry.Name()))
		if !ok || (record.Name != session && record.SessionID != session) {
			continue
		}
		matched++
		if strconv.Itoa(record.PID) != groups[1] {
			problems = append(problems, fmt.Sprintf("%s: pid does not match file name", entry.Name()))
			continue
		}
		if !r.alive(record.PID) {
			problems = append(problems, fmt.Sprintf("pid %d: process is not running", record.PID))
			continue
		}
		if err := checkSocket(record.MessagingSocketPath, r.uid); err != nil {
			problems = append(problems, fmt.Sprintf("pid %d: %v", record.PID, err))
			continue
		}
		reachable = append(reachable, record)
	}

	switch {
	case matched == 0:
		return sessionTarget{}, fmt.Errorf("claude-code session %q not found in %s", session, r.dir)
	case len(reachable) == 0:
		return sessionTarget{}, fmt.Errorf("claude-code session %q is not reachable: %s", session, strings.Join(problems, "; "))
	case len(reachable) > 1:
		return sessionTarget{}, fmt.Errorf("claude-code session %q is ambiguous: %d open sessions match; use the session id", session, len(reachable))
	}

	record := reachable[0]
	return sessionTarget{
		SocketPath: record.MessagingSocketPath,
		Token:      r.peerToken(record.PID, record.MessagingSocketPath),
	}, nil
}

func (r registry) readRecord(path string) (sessionRecord, bool) {
	if !r.ownedRegularFile(path) {
		return sessionRecord{}, false
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return sessionRecord{}, false
	}
	var record sessionRecord
	if err := json.Unmarshal(payload, &record); err != nil || record.PID <= 0 {
		return sessionRecord{}, false
	}
	return record, true
}

// peerToken reads the session's inbox token from
// <pid>.<sha256(socket path)>.key. A missing key yields an empty token and the
// nudge is sent without the auth line.
func (r registry) peerToken(pid int, socketPath string) string {
	candidates := []string{socketPath}
	if resolved, err := filepath.EvalSymlinks(socketPath); err == nil && resolved != socketPath {
		candidates = append(candidates, resolved)
	}
	for _, candidate := range candidates {
		sum := sha256.Sum256([]byte(candidate))
		path := filepath.Join(r.dir, fmt.Sprintf("%d.%s.key", pid, hex.EncodeToString(sum[:])))
		if !r.ownedRegularFile(path) {
			continue
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var key struct {
			PeerToken string `json:"peerToken"`
		}
		if json.Unmarshal(payload, &key) == nil && strings.TrimSpace(key.PeerToken) != "" {
			return strings.TrimSpace(key.PeerToken)
		}
	}
	return ""
}

func (r registry) ownedRegularFile(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == r.uid
}

// checkSocket refuses any messaging socket path that is not an absolute,
// non-symlink Unix socket owned by uid: a path read from the registry must
// never be written to on trust alone.
func checkSocket(path string, uid int) error {
	if path == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("messaging socket path is missing or not absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("messaging socket is missing")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("messaging socket is a symlink")
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("messaging socket is not a socket")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != uid {
		return fmt.Errorf("messaging socket is not owned by the current user")
	}
	return nil
}

func processAlive(pid int) bool {
	return pid > 0 && syscall.Kill(pid, 0) == nil
}
