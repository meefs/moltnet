package claudecode

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noopolis/moltnet/pkg/bridgeconfig"
)

const testToken = "test-peer-token-not-a-secret"

// shortTempDir keeps Unix socket paths under the ~104 byte sun_path limit.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mna")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func listenSocket(t *testing.T, path string) net.Listener {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func writeRecord(t *testing.T, sessionsDir string, fileName string, record sessionRecord) {
	t.Helper()
	payload, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(sessionsDir, fileName), payload, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeKey(t *testing.T, sessionsDir string, pid int, socketPath string) {
	t.Helper()
	sum := sha256.Sum256([]byte(socketPath))
	path := filepath.Join(sessionsDir, fmt.Sprintf("%d.%s.key", pid, hex.EncodeToString(sum[:])))
	if err := os.WriteFile(path, []byte(`{"peerToken":"`+testToken+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	configDir string
	sessions  string
	root      string
	alive     map[int]bool
	args      map[int][]string
}

func newFixture(t *testing.T) *fixture {
	root := shortTempDir(t)
	configDir := filepath.Join(root, "c")
	sessions := filepath.Join(configDir, "sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	return &fixture{configDir: configDir, sessions: sessions, root: root, alive: map[int]bool{}, args: map[int][]string{}}
}

func (f *fixture) registry() registry {
	r := newRegistry(f.configDir)
	r.alive = func(pid int) bool { return f.alive[pid] }
	r.args = func(pid int) []string { return f.args[pid] }
	return r
}

// addSession registers a live session with a real listening socket.
func (f *fixture) addSession(t *testing.T, pid int, id string, name string) (string, net.Listener) {
	socketPath := filepath.Join(f.root, fmt.Sprintf("%d.sock", pid))
	listener := listenSocket(t, socketPath)
	writeRecord(t, f.sessions, fmt.Sprintf("%d.json", pid), sessionRecord{PID: pid, SessionID: id, Name: name, MessagingSocketPath: socketPath})
	writeKey(t, f.sessions, pid, socketPath)
	f.alive[pid] = true
	return socketPath, listener
}

func TestRegistryFindsByNameAndID(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	socketPath, _ := f.addSession(t, 101, "11111111-2222-4333-8444-555555555555", "lmc-work")
	f.addSession(t, 102, "99999999-2222-4333-8444-555555555555", "other")

	for _, session := range []string{"lmc-work", "11111111-2222-4333-8444-555555555555"} {
		target, err := f.registry().find(session)
		if err != nil {
			t.Fatalf("find(%q) error = %v", session, err)
		}
		if target.SocketPath != socketPath || target.Token != testToken {
			t.Fatalf("find(%q) = socket %q token-set %v", session, target.SocketPath, target.Token != "")
		}
	}
}

func TestRegistryRejectsAmbiguousNotFoundAndStale(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.addSession(t, 201, "a-id", "dup")
	f.addSession(t, 202, "b-id", "dup")
	f.addSession(t, 203, "c-id", "stale")
	f.alive[203] = false

	cases := map[string]string{
		"dup":     "ambiguous",
		"missing": "not found",
		"stale":   "process is not running",
	}
	for session, want := range cases {
		_, err := f.registry().find(session)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("find(%q) error = %v, want %q", session, err, want)
		}
	}
	// A stale duplicate does not make a live session ambiguous.
	f.alive[202] = false
	if _, err := f.registry().find("dup"); err != nil {
		t.Fatalf("find(dup) with one live match error = %v", err)
	}
}

func TestRegistryRefusesUnsafeSockets(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	realSocket, _ := f.addSession(t, 301, "real-id", "real")
	link := filepath.Join(f.root, "link.sock")
	if err := os.Symlink(realSocket, link); err != nil {
		t.Fatal(err)
	}
	writeRecord(t, f.sessions, "302.json", sessionRecord{PID: 302, SessionID: "l-id", Name: "linked", MessagingSocketPath: link})
	f.alive[302] = true

	plain := filepath.Join(f.root, "plain.sock")
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	writeRecord(t, f.sessions, "303.json", sessionRecord{PID: 303, SessionID: "p-id", Name: "plain", MessagingSocketPath: plain})
	f.alive[303] = true

	writeRecord(t, f.sessions, "304.json", sessionRecord{PID: 305, SessionID: "m-id", Name: "mismatch", MessagingSocketPath: realSocket})
	f.alive[305] = true

	cases := map[string]string{
		"linked":   "is a symlink",
		"plain":    "is not a socket",
		"mismatch": "pid does not match",
	}
	for session, want := range cases {
		_, err := f.registry().find(session)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("find(%q) error = %v, want %q", session, err, want)
		}
	}
}

func TestCheckSocketRefusesForeignOwner(t *testing.T) {
	t.Parallel()
	root := shortTempDir(t)
	socketPath := filepath.Join(root, "s.sock")
	listenSocket(t, socketPath)

	if err := checkSocket(socketPath, os.Getuid()); err != nil {
		t.Fatalf("checkSocket(own) error = %v", err)
	}
	err := checkSocket(socketPath, os.Getuid()+1)
	if err == nil || !strings.Contains(err.Error(), "not owned by the current user") {
		t.Fatalf("checkSocket(foreign) error = %v", err)
	}
}

func TestAttachedNotifierWritesAuthThenFrame(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, listener := f.addSession(t, 99999901, "n-id", "notify-me")

	lines := make(chan []string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			lines <- nil
			return
		}
		defer conn.Close()
		var got []string
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			got = append(got, scanner.Text())
		}
		lines <- got
	}()

	config := bridgeconfig.Config{Runtime: bridgeconfig.RuntimeConfig{Kind: bridgeconfig.RuntimeClaudeCode, Mode: bridgeconfig.RuntimeModeAttached, Session: "notify-me"}}
	notifier := AttachedNotifier{configDir: f.configDir}
	// Swap in the fixture's liveness view: the fake pid 401 is not a real process.
	notifyErr := func() error {
		target, err := f.registry().find(config.Runtime.Session)
		if err != nil {
			return err
		}
		return sendSocketMessage(context.Background(), target, "[moltnet] hello")
	}()
	if notifyErr != nil {
		t.Fatalf("notify error = %v", notifyErr)
	}

	got := <-lines
	if len(got) != 2 {
		t.Fatalf("socket received %d lines, want 2", len(got))
	}
	var auth map[string]string
	if err := json.Unmarshal([]byte(got[0]), &auth); err != nil || auth["type"] != "auth" || auth["token"] != testToken {
		t.Fatalf("first line is not the auth line")
	}
	var frame struct {
		Type    string `json:"type"`
		From    string `json:"from"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(got[1]), &frame); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	if frame.Type != "user" || frame.From != "moltnet" || frame.Message.Role != "user" ||
		frame.Message.Content != "<cross-session-message from=\"moltnet\">\n[moltnet] hello\n</cross-session-message>" {
		t.Fatalf("unexpected frame %+v", frame)
	}

	// The real Notify path uses real process liveness, so the fake pid is unreachable;
	// its error must not leak the peer token.
	err := notifier.Notify(context.Background(), config, "x")
	if err == nil || strings.Contains(err.Error(), testToken) {
		t.Fatalf("Notify() with dead pid error = %v", err)
	}
}
