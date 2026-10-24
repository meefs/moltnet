package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallMoltnetSkillWritesEndOfTurnFiles(t *testing.T) {
	t.Parallel()

	rule := "# Moltnet\n\nBefore you finish each turn, run `moltnet read --since-last`.\n\n" +
		"- If it prints nothing, there is nothing new: finish normally.\n" +
		"- If it prints messages, handle them before finishing: tell the user what arrived, and reply with `moltnet send <target> \"<text>\"` when a reply is appropriate (see the Moltnet skill).\n"
	tests := []struct {
		runtime string
		path    string
		want    string
	}{
		{runtime: "gemini", path: ".agent/rules/moltnet.md", want: rule},
		{runtime: "antigravity", path: ".agent/rules/moltnet.md", want: rule},
		{runtime: "cursor", path: ".cursor/rules/moltnet.mdc", want: "---\nalwaysApply: true\n---\n" + rule},
		{runtime: "opencode", path: ".opencode/rules/moltnet.md", want: rule},
		{runtime: "grok-build", path: ".grok/rules/moltnet.md", want: rule},
	}
	for _, test := range tests {
		t.Run(test.runtime, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			installed, err := installMoltnetSkill(test.runtime, root, "skill")
			if err != nil {
				t.Fatalf("installMoltnetSkill() error = %v", err)
			}
			got, err := os.ReadFile(filepath.Join(root, test.path))
			if err != nil {
				t.Fatalf("read %s: %v", test.path, err)
			}
			if string(got) != test.want {
				t.Fatalf("%s = %q, want %q", test.path, got, test.want)
			}
			if !strings.Contains(installed, filepath.Join(root, test.path)) {
				t.Fatalf("installed paths %q do not list %s", installed, test.path)
			}
			if _, err := os.Stat(filepath.Join(root, ".agents", "skills", "moltnet", "SKILL.md")); err != nil {
				t.Fatalf("skill file not installed: %v", err)
			}
		})
	}
}

func TestInstallMoltnetSkillWritesCopilotStopHook(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if _, err := installMoltnetSkill("copilot", root, "skill"); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join(root, ".github", "hooks", "moltnet.json"))
	if err != nil {
		t.Fatal(err)
	}
	var hooks struct {
		Version int `json:"version"`
		Hooks   map[string][]struct {
			Type       string `json:"type"`
			Bash       string `json:"bash"`
			TimeoutSec int    `json:"timeoutSec"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(payload, &hooks); err != nil {
		t.Fatalf("hook file is not JSON: %v", err)
	}
	stop := hooks.Hooks["Stop"]
	if hooks.Version != 1 || len(hooks.Hooks) != 1 || len(stop) != 1 ||
		stop[0].Type != "command" || stop[0].Bash != "moltnet read --since-last --stop-hook" || stop[0].TimeoutSec != 30 {
		t.Fatalf("unexpected hook file %s", payload)
	}
}

func TestInstallMoltnetSkillManualRuntimesGetOnlyTheSkill(t *testing.T) {
	t.Parallel()
	for _, runtime := range []string{"hermes", "devin", "claude-code", "codex"} {
		root := t.TempDir()
		installed, err := installMoltnetSkill(runtime, root, "skill")
		if err != nil {
			t.Fatalf("%s: %v", runtime, err)
		}
		for _, dir := range []string{".agent", ".cursor", ".opencode", ".grok", ".github"} {
			if _, err := os.Stat(filepath.Join(root, dir)); err == nil {
				t.Fatalf("%s: unexpected %s written (installed %q)", runtime, dir, installed)
			}
		}
	}
}
