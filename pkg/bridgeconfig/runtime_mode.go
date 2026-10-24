package bridgeconfig

import (
	"fmt"
	"strings"
)

const (
	RuntimeModeManaged  = "managed"
	RuntimeModeAttached = "attached"
)

// IsAttached reports whether the runtime nudges an already-open session
// instead of Moltnet running the runtime command per wake.
func (r RuntimeConfig) IsAttached() bool {
	return strings.TrimSpace(r.Mode) == RuntimeModeAttached
}

func validateRuntimeMode(runtime RuntimeConfig) error {
	switch strings.TrimSpace(runtime.Mode) {
	case "", RuntimeModeManaged:
		if strings.TrimSpace(runtime.Session) != "" {
			return fmt.Errorf("bridge config runtime.session requires runtime.mode %q", RuntimeModeAttached)
		}
		return nil
	case RuntimeModeAttached:
	default:
		return fmt.Errorf("bridge config runtime.mode %q is unsupported; use %q or %q", runtime.Mode, RuntimeModeManaged, RuntimeModeAttached)
	}

	if runtime.Kind != RuntimeClaudeCode && runtime.Kind != RuntimeCodex {
		return fmt.Errorf("bridge config runtime.mode %q is only supported for claude-code or codex", RuntimeModeAttached)
	}
	session := runtime.Session
	if strings.TrimSpace(session) == "" {
		return fmt.Errorf("bridge config runtime.session is required when runtime.mode is %q", RuntimeModeAttached)
	}
	if session != strings.TrimSpace(session) || strings.ContainsAny(session, "\x00\r\n") {
		return fmt.Errorf("bridge config runtime.session must be a trimmed single-line name or id")
	}
	return nil
}
