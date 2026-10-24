package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	authn "github.com/noopolis/moltnet/internal/auth"
	moltnetclient "github.com/noopolis/moltnet/internal/client"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func runRead(args []string) error {
	flags := flag.NewFlagSet("moltnet read", flag.ContinueOnError)
	flags.SetOutput(stdout)

	var (
		configPath = flags.String("config", "", "explicit Moltnet client config path")
		limit      = flags.Int("limit", 20, "message limit")
		memberID   = flags.String("member", "", "Moltnet member id when a network has multiple attachments")
		networkID  = flags.String("network", "", "Moltnet network id when multiple attachments are configured")
		// Cursor storage and staleness behavior (PLAN.md 7C.2) are documented
		// in read_cursor.go, not here -- this help text is user/agent-facing,
		// and those are internal doc references.
		sinceLast = flags.Bool("since-last", false, "only messages after this identity's last successfully read message for this target")
		targetArg = flags.String("target", "", "target in the form room:<id>, thread:<id>, or dm:<id> (or the first positional argument); omit with --since-last to read everything unread")
		stopHook  = flags.Bool("stop-hook", false, "with --since-last and no target, print new messages as Stop-hook JSON {\"decision\":\"block\",\"reason\":...}")
	)
	override := bindOperatorOverrideFlags(flags)

	// See send.go's runSend for why this checks only the literal word
	// "help" here, before Parse: "-h"/"--help" already reach Go's flag
	// package below and become flag.ErrHelp after printing usage.
	if len(args) > 0 && args[0] == "help" {
		flags.Usage()
		return nil
	}

	if err := flags.Parse(args); err != nil {
		return err
	}

	targetValue := *targetArg
	if targetValue == "" && flags.NArg() > 0 {
		targetValue = flags.Arg(0)
	}
	if flags.NArg() > 1 || (flags.NArg() == 1 && *targetArg != "") {
		return fmt.Errorf("read does not accept positional arguments once --target is given")
	}

	if targetValue == "" && *sinceLast {
		return runReadUnread(*configPath, *networkID, *memberID, *override, *limit, *stopHook)
	}
	if *stopHook {
		return fmt.Errorf("--stop-hook requires --since-last with no target")
	}

	target, err := parseTarget(targetValue)
	if err != nil {
		return err
	}

	attachment, client, usingFallback, err := resolveClientOrOperator(*configPath, *networkID, *memberID, *override, authn.ScopeObserve)
	if err != nil {
		return err
	}
	if !usingFallback {
		// The resolved room lookup is only needed to run the local
		// ensureTargetAllowed check below (ListThreadMessages, further
		// down, takes the thread id directly and needs no room id of its
		// own), so it is skipped entirely in the fallback path, which
		// skips that check too.
		if target.kind == protocol.TargetKindThread {
			resolved, err := resolveThreadTarget(commandContext(), client, target)
			if err != nil {
				return err
			}
			target = resolved
		}
		if err := ensureTargetAllowed(attachment, target); err != nil {
			return err
		}
	}

	pageRequest := protocol.PageRequest{Limit: *limit}

	// PLAN.md 7C.2: --since-last rides the existing PageRequest cursor
	// pagination by resolving this identity's own stored cursor for this
	// target and, if one exists, filtering the fetch to only what comes
	// after it (read_cursor.go's resolveReadCursorIdentity/lookupReadCursor
	// implement the binding storage decision -- one file, two key
	// derivations, never another identity's entry).
	var cursor *readCursorIdentity
	if *sinceLast {
		identity, err := resolveReadCursorIdentity(*configPath, *networkID, attachment, usingFallback, strings.TrimSpace(override.baseURL) != "")
		if err != nil {
			return err
		}
		cursor = &identity
	}

	page, cursorReset, err := readTargetPage(client, cursor, target, pageRequest)
	if err != nil {
		return err
	}

	if cursorReset {
		return printJSON(readPage{MessagePage: page, CursorReset: true})
	}
	return printJSON(page)
}

// readPage is `read`'s printed stdout shape. It embeds protocol.MessagePage
// unchanged (so a plain `read` or a clean --since-last delta prints exactly
// the same JSON shape as before) and only ever sets CursorReset -- via
// omitempty, so it never appears at all outside the self-heal branch above --
// on the "stored cursor was stale, this is a fresh unfiltered window, not a
// clean delta" case. The stderr note above says the same thing for a human;
// this is the machine-readable form of it for --since-last's stated
// audience, an agent parsing stdout, which cannot see stderr at all in the
// common case of a caller that only captures stdout.
type readPage struct {
	protocol.MessagePage
	CursorReset bool `json:"cursor_reset,omitempty"`
}

// readTargetPage fetches one target's page. With a cursor (--since-last) it
// reads only what follows the stored cursor and advances it; without one it
// is a plain fetch.
func readTargetPage(
	client *moltnetclient.Client,
	cursor *readCursorIdentity,
	target targetRef,
	pageRequest protocol.PageRequest,
) (protocol.MessagePage, bool, error) {
	page, cursorReset, err := fetchSinceLast(client, cursor, target, pageRequest)
	if err != nil {
		return protocol.MessagePage{}, false, err
	}
	if cursor != nil {
		if err := advanceSinceLast(*cursor, target, page); err != nil {
			return protocol.MessagePage{}, false, err
		}
	}
	return page, cursorReset, nil
}

// fetchSinceLast fetches one target's page after its stored cursor (when
// cursor is set) without advancing it, so a caller can advance only once the
// messages were actually shown.
func fetchSinceLast(
	client *moltnetclient.Client,
	cursor *readCursorIdentity,
	target targetRef,
	pageRequest protocol.PageRequest,
) (protocol.MessagePage, bool, error) {
	if cursor != nil {
		stored, err := lookupReadCursor(cursor.path, readCursorKey(*cursor, target))
		if err != nil {
			return protocol.MessagePage{}, false, err
		}
		if stored != "" {
			pageRequest.After = stored
		}
	}

	cursorReset := false
	page, err := fetchTargetMessages(commandContext(), client, target, pageRequest)
	if err != nil {
		if cursor != nil && pageRequest.After != "" && isInvalidCursorError(err) {
			// PLAN.md 7C.2's documented "cursor older than retained
			// history" behavior: rather than hard-failing an otherwise
			// healthy identity/target resolution, this self-heals to a
			// fresh baseline -- exactly first-use's unfiltered fetch --
			// and advancing persists a fresh, valid cursor from it.
			// cursorReset also stamps a machine-readable "cursor_reset"
			// marker on the printed payload, so an agent parsing stdout
			// can tell this gap apart from a clean delta.
			fmt.Fprintf(stderr, "note: stored --since-last cursor for %s:%s is older than retained history; showing the latest messages instead\n", target.kind, target.id)
			cursorReset = true
			pageRequest.After = ""
			page, err = fetchTargetMessages(commandContext(), client, target, pageRequest)
		}
		if err != nil {
			return protocol.MessagePage{}, false, err
		}
	}
	return page, cursorReset, nil
}

// advanceSinceLast moves the target's cursor to the newest fetched message.
// An empty page leaves any existing cursor untouched. Every message page is
// ascending, so the newest message fetched is last.
func advanceSinceLast(cursor readCursorIdentity, target targetRef, page protocol.MessagePage) error {
	if len(page.Messages) == 0 {
		return nil
	}
	return advanceReadCursor(cursor.path, readCursorKey(cursor, target), page.Messages[len(page.Messages)-1].ID)
}

// fetchTargetMessages dispatches to the one ListXMessages client call
// target.kind names. Factored out of runRead so the --since-last stale-cursor
// retry (above) can reuse the exact same dispatch instead of duplicating the
// switch.
func fetchTargetMessages(
	ctx context.Context,
	client *moltnetclient.Client,
	target targetRef,
	page protocol.PageRequest,
) (protocol.MessagePage, error) {
	switch target.kind {
	case protocol.TargetKindRoom:
		return client.ListRoomMessages(ctx, target.id, page)
	case protocol.TargetKindThread:
		return client.ListThreadMessages(ctx, target.id, page)
	case protocol.TargetKindDM:
		return client.ListDMMessages(ctx, target.id, page)
	default:
		return protocol.MessagePage{}, fmt.Errorf("unsupported target kind %q", target.kind)
	}
}
