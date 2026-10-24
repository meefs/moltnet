package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	authn "github.com/noopolis/moltnet/internal/auth"
	moltnetclient "github.com/noopolis/moltnet/internal/client"
	"github.com/noopolis/moltnet/pkg/clientconfig"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// runReadUnread is `moltnet read --since-last` with no target: the one
// end-of-turn check. It reads every room and DM this identity may read,
// advances the same per-target cursors a targeted --since-last read does,
// prints a compact block of what is new, and prints nothing (exit 0) when
// nothing is. With stopHook, a non-empty result is wrapped in the
// {"decision":"block","reason":...} JSON a Stop hook (GitHub Copilot CLI)
// needs to keep the agent going; agent hosts run that hook command directly,
// so the CLI emits the shape itself rather than relying on jq being installed.
func runReadUnread(configPath string, networkID string, memberID string, override operatorOverrideOptions, limit int, stopHook bool) error {
	attachment, client, usingFallback, err := resolveClientOrOperator(configPath, networkID, memberID, override, authn.ScopeObserve)
	if err != nil {
		return err
	}
	cursor, err := resolveReadCursorIdentity(configPath, networkID, attachment, usingFallback, strings.TrimSpace(override.baseURL) != "")
	if err != nil {
		return err
	}
	targets, err := unreadTargets(client, attachment, usingFallback)
	if err != nil {
		return err
	}

	var blocks []string
	for _, target := range targets {
		page, reset, err := readTargetPage(client, &cursor, target, protocol.PageRequest{Limit: limit})
		if err != nil {
			return fmt.Errorf("read %s:%s: %w", target.kind, target.id, err)
		}
		if len(page.Messages) > 0 {
			blocks = append(blocks, renderUnreadBlock(target, page.Messages, reset))
		}
	}
	if len(blocks) == 0 {
		return nil
	}

	text := strings.Join(blocks, "\n")
	if !stopHook {
		_, err := fmt.Fprintln(stdout, text)
		return err
	}
	payload, err := json.Marshal(map[string]string{
		"decision": "block",
		"reason":   "New Moltnet messages. Handle them (reply with `moltnet send` when appropriate), then finish.\n" + text,
	})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, string(payload))
	return err
}

// unreadTargets lists the configured rooms (every visible room for the
// operator fallback) and, when DMs are enabled, every DM, in a stable order.
func unreadTargets(client *moltnetclient.Client, attachment clientconfig.AttachmentConfig, usingFallback bool) ([]targetRef, error) {
	var roomIDs []string
	if usingFallback {
		page, err := client.ListRooms(commandContext())
		if err != nil {
			return nil, err
		}
		for _, room := range page.Rooms {
			roomIDs = append(roomIDs, room.ID)
		}
	} else {
		for _, room := range attachment.Rooms {
			if strings.TrimSpace(room.ID) != "" {
				roomIDs = append(roomIDs, room.ID)
			}
		}
	}
	slices.Sort(roomIDs)
	roomIDs = slices.Compact(roomIDs)

	targets := make([]targetRef, 0, len(roomIDs))
	for _, id := range roomIDs {
		targets = append(targets, targetRef{kind: protocol.TargetKindRoom, id: id})
	}
	if usingFallback || (attachment.DMs != nil && attachment.DMs.Enabled) {
		page, err := client.ListDMs(commandContext())
		if err != nil {
			return nil, err
		}
		for _, dm := range page.DMs {
			targets = append(targets, targetRef{kind: protocol.TargetKindDM, id: dm.ID})
		}
	}
	return targets, nil
}

func renderUnreadBlock(target targetRef, messages []protocol.Message, reset bool) string {
	header := fmt.Sprintf("%s:%s (%d new)", target.kind, target.id, len(messages))
	if reset {
		header += " (cursor reset: older messages may have been missed)"
	}
	lines := []string{header}
	for _, message := range messages {
		from := message.From.ID
		if from == "" {
			from = message.From.Name
		}
		lines = append(lines, fmt.Sprintf("  %s: %s", from, messageText(message)))
	}
	return strings.Join(lines, "\n")
}

func messageText(message protocol.Message) string {
	var parts []string
	for _, part := range message.Parts {
		switch {
		case strings.TrimSpace(part.Text) != "":
			parts = append(parts, strings.TrimSpace(part.Text))
		case part.URL != "":
			parts = append(parts, "["+part.Kind+"] "+part.URL)
		}
	}
	return strings.ReplaceAll(strings.Join(parts, " "), "\n", "\n    ")
}
