package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTranscript(t *testing.T, dir string, sessionID string, modes ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, mode := range modes {
		lines = append(lines, fmt.Sprintf(`{"type":"user","permissionMode":%q}`, mode), `{"type":"assistant"}`)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// addSessionWithCwd registers a live session whose record names cwd.
func (f *fixture) addSessionWithCwd(t *testing.T, pid int, id string, name string, cwd string) {
	socketPath, _ := f.addSession(t, pid, id, name)
	writeRecord(t, f.sessions, fmt.Sprintf("%d.json", pid), sessionRecord{PID: pid, SessionID: id, Name: name, Cwd: cwd, MessagingSocketPath: socketPath})
}

func TestRegistryReadsBypassModeFromTranscript(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	projects := filepath.Join(f.configDir, "projects")

	f.addSessionWithCwd(t, 501, "bypass-id", "bypass", "/work/a")
	writeTranscript(t, filepath.Join(projects, "-work-a"), "bypass-id", "default", "bypassPermissions")
	f.addSessionWithCwd(t, 502, "plain-id", "plain", "/work/b")
	writeTranscript(t, filepath.Join(projects, "-work-b"), "plain-id", "bypassPermissions", "default")
	// Started elsewhere: the transcript lives under another project directory.
	f.addSessionWithCwd(t, 503, "moved-id", "moved", "/work/c")
	writeTranscript(t, filepath.Join(projects, "-elsewhere"), "moved-id", "bypassPermissions")
	f.addSessionWithCwd(t, 504, "none-id", "none", "/work/d")
	// A fresh session has no transcript yet: its launch flags decide.
	f.addSessionWithCwd(t, 505, "fresh-id", "fresh", "/work/e")
	f.args[505] = []string{"claude", "-n", "fresh", "--dangerously-skip-permissions"}
	f.addSessionWithCwd(t, 506, "flagmode-id", "flagmode", "/work/f")
	f.args[506] = []string{"claude", "--permission-mode=bypassPermissions"}
	// Once the transcript records a mode, it wins over the launch flags.
	f.addSessionWithCwd(t, 507, "switched-id", "switched", "/work/g")
	f.args[507] = []string{"claude", "--permission-mode", "bypassPermissions"}
	writeTranscript(t, filepath.Join(projects, "-work-g"), "switched-id", "default")

	want := map[string]bool{"bypass": true, "plain": false, "moved": true, "none": false, "fresh": true, "flagmode": true, "switched": false}
	for session, bypass := range want {
		target, err := f.registry().find(session)
		if err != nil {
			t.Fatalf("find(%q) error = %v", session, err)
		}
		if target.Bypass != bypass || target.SessionID != session+"-id" {
			t.Fatalf("find(%q) bypass=%v session=%q, want bypass=%v", session, target.Bypass, target.SessionID, bypass)
		}
	}
}

func TestMessageFrameNamesModeAndSession(t *testing.T) {
	t.Parallel()

	decode := func(target sessionTarget) (string, string) {
		payload, err := messageFrame("[moltnet] hi", target)
		if err != nil {
			t.Fatal(err)
		}
		var frame struct {
			SessionID string `json:"session_id"`
			Message   struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(payload, &frame); err != nil {
			t.Fatal(err)
		}
		return frame.Message.Content, frame.SessionID
	}

	content, sessionID := decode(sessionTarget{SessionID: "s-1", Bypass: true})
	if content != "<cross-session-message from=\"moltnet\" from-name=\"moltnet\" from-mode=\"bypass\">\n[moltnet] hi\n</cross-session-message>" || sessionID != "s-1" {
		t.Fatalf("bypass frame content=%q session=%q", content, sessionID)
	}
	content, sessionID = decode(sessionTarget{})
	if content != "<cross-session-message from=\"moltnet\">\n[moltnet] hi\n</cross-session-message>" || sessionID != "" {
		t.Fatalf("plain frame content=%q session=%q", content, sessionID)
	}
}

func TestLaunchPermissionMode(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		permissionModeBypass: {"claude", "--dangerously-skip-permissions"},
		"plan":               {"claude", "--permission-mode", "plan"},
		"acceptEdits":        {"claude", "--permission-mode=acceptEdits"},
		"":                   {"claude", "--allow-dangerously-skip-permissions", "--permission-mode"},
	}
	bypass := [][]string{
		{"claude", "-n", "work", "--model", "haiku", "--dangerously-skip-permissions"},
		{"claude", "--add-dir", "a", "b", "--resume", "--dangerously-skip-permissions"},
		{"node", "/opt/claude/cli.js", "--name=work", "--permission-mode", "bypassPermissions"},
	}
	for _, args := range bypass {
		if got := launchPermissionMode(args); got != permissionModeBypass {
			t.Fatalf("launchPermissionMode(%q) = %q, want bypass", args, got)
		}
	}
	// A prompt (the first positional argument) that mentions the flags is not a flag.
	prompts := [][]string{
		{"claude", "explain", "--dangerously-skip-permissions"},
		{"claude", "-n", "work", "what", "does", "--permission-mode", "bypassPermissions", "do?"},
		{"claude", "--verbose", "--", "--dangerously-skip-permissions"},
	}
	for _, args := range prompts {
		if got := launchPermissionMode(args); got != "" {
			t.Fatalf("launchPermissionMode(%q) = %q, want no mode from prompt text", args, got)
		}
	}
	for want, args := range cases {
		if got := launchPermissionMode(args); got != want {
			t.Fatalf("launchPermissionMode(%q) = %q, want %q", args, got, want)
		}
	}
	if got := launchPermissionMode(processArgs(os.Getpid())); got != "" {
		t.Fatalf("test process launch mode = %q, want empty", got)
	}
}
