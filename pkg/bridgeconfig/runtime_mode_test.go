package bridgeconfig

import (
	"os"
	"strings"
	"testing"
)

func TestValidateRuntimeMode(t *testing.T) {
	t.Parallel()

	base := func(runtime RuntimeConfig) Config {
		return Config{
			Version: VersionV1,
			Agent:   AgentConfig{ID: "researcher"},
			Moltnet: MoltnetConfig{BaseURL: "http://127.0.0.1:8787", NetworkID: "local"},
			Runtime: runtime,
		}
	}

	tests := []struct {
		name    string
		runtime RuntimeConfig
		wantErr string
	}{
		{name: "managed default still needs workspace", runtime: RuntimeConfig{Kind: RuntimeCodex}, wantErr: "workspace_path is required"},
		{name: "explicit managed codex", runtime: RuntimeConfig{Kind: RuntimeCodex, Mode: RuntimeModeManaged, WorkspacePath: "/tmp/w"}},
		{name: "attached codex without workspace", runtime: RuntimeConfig{Kind: RuntimeCodex, Mode: RuntimeModeAttached, Session: "lmc-work"}},
		{name: "attached claude-code without workspace", runtime: RuntimeConfig{Kind: RuntimeClaudeCode, Mode: RuntimeModeAttached, Session: "0f8c2a1e-1111-4222-8333-444455556666"}},
		{name: "attached codex keeps command and home", runtime: RuntimeConfig{Kind: RuntimeCodex, Mode: RuntimeModeAttached, Session: "s", Command: "/opt/codex", HomePath: "/home/a"}},
		{name: "attached requires session", runtime: RuntimeConfig{Kind: RuntimeCodex, Mode: RuntimeModeAttached}, wantErr: "runtime.session is required"},
		{name: "attached rejects multiline session", runtime: RuntimeConfig{Kind: RuntimeCodex, Mode: RuntimeModeAttached, Session: "a\nb"}, wantErr: "single-line"},
		{name: "attached rejects untrimmed session", runtime: RuntimeConfig{Kind: RuntimeCodex, Mode: RuntimeModeAttached, Session: " a"}, wantErr: "trimmed"},
		{name: "attached unsupported for pi", runtime: RuntimeConfig{Kind: RuntimePi, Mode: RuntimeModeAttached, Session: "s", ControlURL: "http://127.0.0.1:1"}, wantErr: "only supported for claude-code or codex"},
		{name: "attached unsupported for openclaw", runtime: RuntimeConfig{Kind: RuntimeOpenClaw, Mode: RuntimeModeAttached, Session: "s"}, wantErr: "only supported for claude-code or codex"},
		{name: "unknown mode", runtime: RuntimeConfig{Kind: RuntimeCodex, Mode: "detached", WorkspacePath: "/tmp/w"}, wantErr: "runtime.mode \"detached\" is unsupported"},
		{name: "session without attached", runtime: RuntimeConfig{Kind: RuntimeCodex, Session: "s", WorkspacePath: "/tmp/w"}, wantErr: "runtime.session requires runtime.mode"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := base(test.runtime).Validate()
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestLoadFileAcceptsAttachedRuntime(t *testing.T) {
	t.Parallel()

	path := t.TempDir() + "/bridge.json"
	body := `{"version":"moltnet.bridge.v1","agent":{"id":"researcher"},` +
		`"moltnet":{"base_url":"http://127.0.0.1:8787","network_id":"local"},` +
		`"runtime":{"kind":"claude-code","mode":"attached","session":"lmc-work"},"rooms":[{"id":"research"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if !config.Runtime.IsAttached() || config.Runtime.Session != "lmc-work" {
		t.Fatalf("unexpected runtime %#v", config.Runtime)
	}
}
