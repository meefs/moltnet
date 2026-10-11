package nodeconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noopolis/moltnet/pkg/bridgeconfig"
)

func TestClockNodeFileValidation(t *testing.T) {
	const base = `{"version":"moltnet.node.v1","moltnet":{"base_url":"http://localhost:8787","network_id":"local"},"attachments":[]`
	const clock = `{"version":"noopolis.task-clock.v1","origin":"2025-10-01T08:00:00Z","anchorEpochMs":0}`
	for _, value := range []string{"", `,"clock":` + clock, `,"clock":null`, `,"clock":{}`, `,"clock":` + strings.Replace(clock, "v1", "v2", 1)} {
		path := filepath.Join(t.TempDir(), "node.json")
		if err := os.WriteFile(path, []byte(base+value+"}"), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadFile(path)
		valid := value == "" || value == `,"clock":`+clock
		if (err == nil) != valid {
			t.Fatalf("clock %s: %v", value, err)
		}
	}
	config := Config{Clock: &bridgeconfig.ClockConfig{Version: "wrong"}}
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "clock") {
		t.Fatalf("direct validation: %v", err)
	}
}

func TestClockNodeFileNull(t *testing.T) {
	for _, value := range []string{"null", "~", ""} {
		path := filepath.Join(t.TempDir(), "MoltnetNode")
		data := "moltnet: {base_url: 'http://localhost:8787', network_id: local}\nclock: " + value + "\n"
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFile(path); err == nil || !strings.Contains(err.Error(), "clock") {
			t.Fatalf("clock %q: %v", value, err)
		}
	}
}

func TestClockNodeRejectsAttachmentOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "MoltnetNode")
	data := "moltnet: {base_url: 'http://localhost:8787', network_id: local}\nattachments:\n  - agent: {id: a}\n    runtime: {kind: openclaw}\n    clock: {}\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil || !strings.Contains(err.Error(), "clock") {
		t.Fatalf("accepted attachment clock: %v", err)
	}
}
