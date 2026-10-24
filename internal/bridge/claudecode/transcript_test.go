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

	want := map[string]bool{"bypass": true, "plain": false, "moved": true, "none": false}
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
