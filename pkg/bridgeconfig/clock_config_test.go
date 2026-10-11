package bridgeconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClockBridgeConfigLoad(t *testing.T) {
	var clock ClockConfig
	if err := json.Unmarshal([]byte(validClockJSON), &clock); err != nil {
		t.Fatal(err)
	}
	config := Config{Version: VersionV1, Clock: &clock, Agent: AgentConfig{ID: "writer"}, Moltnet: MoltnetConfig{BaseURL: "http://localhost:8787", NetworkID: "local"}, Runtime: RuntimeConfig{Kind: RuntimePi, ControlURL: "http://localhost:9999"}}
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bridge.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFile(path)
	if err != nil || loaded.Clock == nil || *loaded.Clock != clock {
		t.Fatalf("load clock %#v, %v", loaded.Clock, err)
	}
	for _, input := range []string{strings.Replace(string(body), TaskClockVersion, "wrong", 1), strings.Replace(string(body), `"anchorEpochMs":`, `"extra":1,"anchorEpochMs":`, 1)} {
		if err := os.WriteFile(path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFile(path); err == nil {
			t.Fatal("invalid clock loaded")
		}
	}
	config.Clock.Version = "wrong"
	if err := config.Validate(); err == nil {
		t.Fatal("direct bridge config skipped clock validation")
	}
}
