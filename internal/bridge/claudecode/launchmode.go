package claudecode

import (
	"os/exec"
	"strconv"
	"strings"
)

// launchPermissionMode reads the permission mode a session was started with
// from its command line. A fresh session writes no transcript until its first
// input arrives, so this is the only signal for the first nudge; once the
// transcript records a mode, that wins (the user may have switched modes).
func launchPermissionMode(args []string) string {
	for index, arg := range args {
		switch {
		case arg == "--dangerously-skip-permissions":
			return permissionModeBypass
		case arg == "--permission-mode" && index+1 < len(args):
			return args[index+1]
		case strings.HasPrefix(arg, "--permission-mode="):
			return strings.TrimPrefix(arg, "--permission-mode=")
		}
	}
	return ""
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
