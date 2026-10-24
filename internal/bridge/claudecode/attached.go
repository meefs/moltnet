package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/noopolis/moltnet/internal/bridge/clisession"
	"github.com/noopolis/moltnet/pkg/bridgeconfig"
)

const socketSendTimeout = 5 * time.Second

// AttachedNotifier nudges an open interactive Claude Code session through its
// cross-session messaging socket, found in Claude Code's session registry.
type AttachedNotifier struct {
	// configDir overrides the Claude Code config directory (tests only).
	configDir string
}

func (AttachedNotifier) Name() string {
	return bridgeconfig.RuntimeClaudeCode
}

func (n AttachedNotifier) Notify(ctx context.Context, config bridgeconfig.Config, text string) error {
	dir := n.configDir
	if dir == "" {
		var err error
		if dir, err = claudeConfigDir(config.Runtime.HomePath); err != nil {
			return err
		}
	}
	target, err := newRegistry(dir).find(config.Runtime.Session)
	if err != nil {
		return err
	}
	return sendSocketMessage(ctx, target, text)
}

// sendSocketMessage writes one connection: an optional auth line carrying the
// session's own peer token, then one user-message frame.
func sendSocketMessage(ctx context.Context, target sessionTarget, text string) error {
	frame, err := messageFrame(text)
	if err != nil {
		return err
	}
	var payload []byte
	if target.Token != "" {
		auth, err := json.Marshal(map[string]string{"type": "auth", "token": target.Token})
		if err != nil {
			return fmt.Errorf("encode claude-code auth line")
		}
		payload = append(auth, '\n')
	}
	payload = append(payload, frame...)
	payload = append(payload, '\n')

	dialer := net.Dialer{Timeout: socketSendTimeout}
	conn, err := dialer.DialContext(ctx, "unix", target.SocketPath)
	if err != nil {
		return fmt.Errorf("connect claude-code messaging socket %s: %w", target.SocketPath, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(socketSendTimeout))
	if _, err := conn.Write(payload); err != nil {
		return fmt.Errorf("write claude-code messaging socket %s: %w", target.SocketPath, err)
	}
	return nil
}

func messageFrame(text string) ([]byte, error) {
	messageID, err := clisession.GenerateUUID()
	if err != nil {
		return nil, err
	}
	frame := map[string]any{
		"msgV":   1,
		"msg_id": messageID,
		"type":   "user",
		"message": map[string]string{
			"role":    "user",
			"content": "<cross-session-message from=\"moltnet\">\n" + text + "\n</cross-session-message>",
		},
		"priority": "next",
		"from":     "moltnet",
	}
	return json.Marshal(frame)
}

var _ clisession.Notifier = AttachedNotifier{}
