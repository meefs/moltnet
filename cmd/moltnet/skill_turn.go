package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// endOfTurnCheck is the single check every end-of-turn integration runs.
const endOfTurnCheck = "moltnet read --since-last"

const endOfTurnRule = `# Moltnet

Before you finish each turn, run ` + "`" + endOfTurnCheck + "`" + `.

- If it prints nothing, there is nothing new: finish normally.
- If it prints messages, handle them before finishing: tell the user what arrived, and reply with ` + "`moltnet send <target> \"<text>\"`" + ` when a reply is appropriate (see the Moltnet skill).
`

// copilotStopHook keeps GitHub Copilot CLI going when there are new messages:
// --stop-hook prints {"decision":"block","reason":...} only then, and nothing
// otherwise, so the agent stops normally when the inbox is empty.
const copilotStopHook = `{
  "version": 1,
  "hooks": {
    "Stop": [
      {
        "type": "command",
        "bash": "` + endOfTurnCheck + ` --stop-hook",
        "timeoutSec": 30
      }
    ]
  }
}
`

// endOfTurnFiles returns the always-on files that make a runtime without a
// live push path check Moltnet at the end of every turn, keyed by workspace
// path. Locations and formats follow each runtime's own conventions (verified
// against agmsg's per-runtime drivers). Claude Code and Codex get live push
// through attached sessions instead; hermes and devin have no hook, so they
// read manually with the skill. Grok Build's hooks discard stdout, so it gets
// a rule file only.
func endOfTurnFiles(runtime string, root string) map[string]string {
	switch strings.TrimSpace(runtime) {
	case "gemini", "antigravity":
		return map[string]string{filepath.Join(root, ".agent", "rules", "moltnet.md"): endOfTurnRule}
	case "cursor":
		return map[string]string{filepath.Join(root, ".cursor", "rules", "moltnet.mdc"): "---\nalwaysApply: true\n---\n" + endOfTurnRule}
	case "opencode":
		return map[string]string{filepath.Join(root, ".opencode", "rules", "moltnet.md"): endOfTurnRule}
	case "grok-build":
		return map[string]string{filepath.Join(root, ".grok", "rules", "moltnet.md"): endOfTurnRule}
	case "copilot":
		return map[string]string{filepath.Join(root, ".github", "hooks", "moltnet.json"): copilotStopHook}
	default:
		return nil
	}
}

// writeEndOfTurnFiles writes the runtime's end-of-turn files (if any) and
// returns the paths written, in a stable order.
func writeEndOfTurnFiles(runtime string, root string) ([]string, error) {
	files := endOfTurnFiles(runtime, root)
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create end-of-turn directory: %w", err)
		}
		if err := os.WriteFile(path, []byte(files[path]), 0o644); err != nil {
			return nil, fmt.Errorf("write end-of-turn file: %w", err)
		}
	}
	return paths, nil
}
