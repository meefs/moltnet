package codex

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/noopolis/moltnet/internal/bridge/clisession"
	"github.com/noopolis/moltnet/pkg/bridgeconfig"
)

const queueCommandTimeout = 30 * time.Second

// AttachedNotifier nudges an open Codex session with `codex queue`. Codex
// delivers the queued message once the session's current turn ends.
type AttachedNotifier struct{}

func (AttachedNotifier) Name() string {
	return bridgeconfig.RuntimeCodex
}

func (AttachedNotifier) Notify(ctx context.Context, config bridgeconfig.Config, text string) error {
	_, err := clisession.RunCommand(ctx, QueueCommand(config, text))
	return err
}

// QueueCommand builds `codex queue --thread <session> --message <text>`,
// honouring runtime.command and runtime.home_path like the managed driver.
func QueueCommand(config bridgeconfig.Config, text string) clisession.CommandSpec {
	command := strings.TrimSpace(config.Runtime.Command)
	if command == "" {
		command = bridgeconfig.DefaultCodexCommand
	}
	env := []string{}
	if homePath := strings.TrimSpace(config.Runtime.HomePath); homePath != "" {
		env = append(env, "CODEX_HOME="+filepath.Join(homePath, ".codex"))
	}
	return clisession.CommandSpec{
		Command: command,
		Args:    []string{"queue", "--thread", config.Runtime.Session, "--message", text},
		Env:     env,
		Dir:     strings.TrimSpace(config.Runtime.WorkspacePath),
		Timeout: queueCommandTimeout,
	}
}

var _ clisession.Notifier = AttachedNotifier{}
