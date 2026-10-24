package claudecode

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Claude Code options that take a value, from `claude --help` (2.1.310):
// required values always consume the next argument, optional ones consume it
// unless it is another option, and variadic ones consume until the next option.
var (
	claudeRequiredValueFlags = setOf("--agent", "--agents", "--append-system-prompt", "--autocompact",
		"--debug-file", "--effort", "--environment", "--fallback-model", "--input-format", "--json-schema",
		"--max-budget-usd", "--model", "-n", "--name", "--output-format", "--permission-mode",
		"--permission-prompts", "--plugin-dir", "--plugin-url", "--remote-control-session-name-prefix",
		"--session-id", "--setting-sources", "--settings", "--system-prompt", "--system-prompt-snapshot")
	claudeOptionalValueFlags = setOf("--cloud", "-d", "--debug", "--from-pr", "--prompt-suggestions",
		"--remote-control", "-r", "--resume", "--teleport", "-w", "--worktree")
	claudeVariadicFlags = setOf("--add-dir", "--allowedTools", "--allowed-tools", "--betas",
		"--disallowedTools", "--disallowed-tools", "--file", "--mcp-config", "--tools")
)

func setOf(values ...string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

// launchPermissionMode reads the permission mode a session was started with
// from its command line. A fresh session writes no transcript until its first
// input arrives, so this is the only signal for the first nudge; once the
// transcript records a mode, that wins (the user may have switched modes).
// Only options before the first positional argument (the initial prompt)
// count, so a prompt that mentions a flag is never read as one.
func launchPermissionMode(args []string) string {
	args = skipProgram(args)
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" || !strings.HasPrefix(arg, "-") {
			return ""
		}
		name, value, inline := strings.Cut(arg, "=")
		switch {
		case name == "--dangerously-skip-permissions":
			return permissionModeBypass
		case name == "--permission-mode" && inline:
			return value
		case name == "--permission-mode":
			if index+1 < len(args) {
				return args[index+1]
			}
			return ""
		case inline:
		case claudeRequiredValueFlags[name]:
			index++
		case claudeOptionalValueFlags[name]:
			if index+1 < len(args) && !strings.HasPrefix(args[index+1], "-") {
				index++
			}
		case claudeVariadicFlags[name]:
			for index+1 < len(args) && !strings.HasPrefix(args[index+1], "-") {
				index++
			}
		}
	}
	return ""
}

// skipProgram drops the executable, and the script when Claude Code runs
// under a JavaScript runtime (`node /path/cli.js ...`).
func skipProgram(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	switch filepath.Base(args[0]) {
	case "node", "bun":
		if len(args) > 1 {
			return args[2:]
		}
		return nil
	}
	return args[1:]
}

// processArgs returns a process's command line split on whitespace, or nil
// when it cannot be read. Flags are all that is inspected, so arguments that
// themselves contain spaces do not matter.
func processArgs(pid int) []string {
	if pid <= 0 {
		return nil
	}
	output, err := exec.Command("ps", "-ww", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil
	}
	return strings.Fields(string(output))
}
