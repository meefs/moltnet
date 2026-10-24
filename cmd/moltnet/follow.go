package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/noopolis/moltnet/internal/app"
	moltnetclient "github.com/noopolis/moltnet/internal/client"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func runFollowCommand(ctx context.Context, args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		fmt.Fprint(stdout, buildFollowUsage())
		return nil
	}
	switch args[0] {
	case "list":
		return runFollowList(args[1:])
	case "add", "remove":
		return runFollowEdit(ctx, args[0], args[1:])
	default:
		return fmt.Errorf("unknown follow command %q", args[0])
	}
}

func runFollowList(args []string) error {
	flags := flag.NewFlagSet("moltnet follow list", flag.ContinueOnError)
	flags.SetOutput(stdout)
	options := bindAdminClientResolverFlags(flags, false)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("follow list does not accept positional arguments")
	}
	client, err := resolveAdminClient(flags, options)
	if err != nil {
		return err
	}
	page, err := client.ListFollows(commandContext())
	if err != nil {
		return err
	}
	return printJSON(page)
}

func runFollowEdit(ctx context.Context, verb string, args []string) error {
	for _, key := range []string{"MOLTNET_FOLLOWS_JSON", "MOLTNET_FOLLOWS"} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return fmt.Errorf("cannot edit file follows while %s overrides them; unset it first", key)
		}
	}
	values := map[string]bool{"--id": true, "--config": true, "--network": true, "--base-url": true, "--token-env": true, "--room": true, "--backfill": true, "--visibility": true, "--remote-network-id": true}
	flagArgs, positional, err := splitSingleRoomIDArgs(args, values, "follow "+verb)
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("moltnet follow "+verb, flag.ContinueOnError)
	flags.SetOutput(stdout)
	pathFlag := flags.String("config", "", "local Moltnet config path")
	network := flags.String("network", "", "local network id")
	baseURL := flags.String("base-url", "", "local server URL override")
	tokenEnv := flags.String("token-env", "", "local operator token environment variable")
	var follow protocol.Follow
	var backfill *int
	if verb == "add" {
		flags.StringVar(&follow.ID, "id", "", "local follow id (default: remote hostname)")
		flags.StringVar(&follow.RemoteNetworkID, "remote-network-id", "", "expected origin network id (optional pin)")
		flags.StringVar(&follow.Visibility, "visibility", "private", "mirror visibility: private or public")
		backfill = flags.Int("backfill", 200, "recent messages per room; 0 imports none")
		var roomFlags repeatedStringFlag
		flags.Var(&roomFlags, "room", "remote room id (repeatable)")
		if err := flags.Parse(flagArgs); err != nil {
			return err
		}
		follow.Rooms = protocol.UniqueTrimmedStrings(roomFlags)
		follow.RemoteBaseURL = strings.TrimRight(strings.TrimSpace(positional), "/")
		follow.Backfill = backfill
		if follow.ID == "" {
			follow.ID = defaultFollowID(follow.RemoteBaseURL)
		}
	} else {
		if err := flags.Parse(flagArgs); err != nil {
			return err
		}
	}
	if positional == "" || flags.NArg() != 0 {
		return fmt.Errorf("follow %s requires exactly one argument", verb)
	}
	path, found, err := app.ResolveConfigPath(*pathFlag, *network)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("moltnet config not found; run `moltnet init` first")
	}
	before, err := app.LoadConfigForPath(path, "")
	if err != nil {
		return err
	}
	attachment := resolveRoomApplyAttachment(before, *baseURL, *tokenEnv)
	if err := writeConfigWithRollback(path, "updated follows", func() error {
		if verb == "add" {
			return app.WriteFollow(path, follow)
		}
		return app.RemoveFollow(path, positional)
	}); err != nil {
		return err
	}
	after, err := app.LoadConfigForPath(path, "")
	if err != nil {
		return err
	}
	if !probeConsoleHealth(ctx, attachment.BaseURL) {
		fmt.Fprintf(stderr, "Follow config saved. Start or restart network %s to apply it.\n", after.NetworkID)
		return printJSON(map[string]any{"saved": true, "applied": false, "follows": after.Follows})
	}
	client, err := moltnetclient.New(attachment)
	if err != nil {
		return err
	}
	follows := append([]protocol.Follow{}, after.Follows...)
	result, err := client.ApplyConfig(commandContext(), protocol.ApplyConfigRequest{NetworkID: after.NetworkID, Follows: &follows})
	if err != nil {
		return fmt.Errorf("follow config saved but live apply failed: %w", err)
	}
	return printJSON(result)
}

func defaultFollowID(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return "remote"
	}
	var out strings.Builder
	for _, r := range strings.ToLower(u.Hostname()) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			out.WriteRune(r)
		} else {
			out.WriteByte('-')
		}
	}
	if out.Len() == 0 {
		return "remote"
	}
	return out.String()
}

func buildFollowUsage() string {
	return `Usage:
  moltnet follow list [--network <id>] [--base-url <url>]
  moltnet follow add <url> [--id <id>] [--room <id>] [--backfill <N>] [--visibility private|public] [--remote-network-id <id>] [--config <path>] [--network <id>]
  moltnet follow remove <id> [--config <path>] [--network <id>]

Follows pull public messages into read-only local rooms. Mirrors default to
private with ids <follow-id>.<remote-room-id>. Removing a follow stops syncing;
existing mirror history is retained and remains read-only.
`
}
