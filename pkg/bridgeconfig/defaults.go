package bridgeconfig

import "strings"

const (
	DefaultOpenClawGatewayURL = "ws://127.0.0.1:18789"
	DefaultPicoClawEventsURL  = "ws://127.0.0.1:18990/pico/ws"
	DefaultPicoClawCommand    = "picoclaw"
	DefaultClaudeCodeCommand  = "claude"
	DefaultCodexCommand       = "codex"
)

func (c Config) Normalized() Config {
	c.Runtime = c.Runtime.Normalized()
	return c
}

func (r RuntimeConfig) Normalized() RuntimeConfig {
	switch strings.TrimSpace(r.Kind) {
	case RuntimeOpenClaw:
		if noRuntimeSeamConfigured(r) {
			r.GatewayURL = DefaultOpenClawGatewayURL
		}
	case RuntimePicoClaw:
		switch {
		case strings.TrimSpace(r.Command) != "":
			return r
		case strings.TrimSpace(r.ConfigPath) != "":
			r.Command = DefaultPicoClawCommand
		case strings.TrimSpace(r.ControlURL) == "" && strings.TrimSpace(r.EventsURL) == "":
			r.EventsURL = DefaultPicoClawEventsURL
		}
	case RuntimeClaudeCode:
		if strings.TrimSpace(r.Command) == "" {
			r.Command = DefaultClaudeCodeCommand
		}
	case RuntimeCodex:
		if strings.TrimSpace(r.Command) == "" {
			r.Command = DefaultCodexCommand
		}
	}

	return r
}

func noRuntimeSeamConfigured(r RuntimeConfig) bool {
	return strings.TrimSpace(r.GatewayURL) == "" &&
		strings.TrimSpace(r.ControlURL) == "" &&
		strings.TrimSpace(r.EventsURL) == "" &&
		strings.TrimSpace(r.Command) == ""
}
