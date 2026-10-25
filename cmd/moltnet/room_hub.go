package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/noopolis/moltnet/internal/app"
	moltnetclient "github.com/noopolis/moltnet/internal/client"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// Shared rooms (hub rooms): `room share|invite|join <code>|members|remove
// <room> <network>|leave`. Every command but join talks to the running
// server, located like `room create` does: the resolved server config's
// listen address and operator token, or --base-url/--token-env.

var roomHubValueFlags = map[string]bool{
	"--config": true, "--network": true, "--base-url": true, "--token-env": true, "--history": true,
	"--role": true, "--id": true, "--relay-url": true, "--relay-token-env": true, "--local-room": true,
	"--room": true, "--member": true, "--credential-env": true,
}

type roomHubTarget struct {
	path   string
	config app.Config
	client *moltnetclient.Client
}

type roomHubFlags struct {
	configPath, networkID, baseURL, tokenEnv *string
}

func bindRoomHubFlags(flags *flag.FlagSet) roomHubFlags {
	return roomHubFlags{
		configPath: flags.String("config", "", "Moltnet server config path"),
		networkID:  flags.String("network", "", "Moltnet network id to select under ~/.moltnet when several exist"),
		baseURL:    flags.String("base-url", "", "explicit base URL for the running Moltnet service (default: from the config's server.listen_addr)"),
		tokenEnv:   flags.String("token-env", "", "environment variable holding the operator token for --base-url"),
	}
}

// resolve loads the server config and a client for its running service.
func (f roomHubFlags) resolve() (roomHubTarget, error) {
	path, err := resolvePairConfigPath(*f.configPath, *f.networkID)
	if err != nil {
		return roomHubTarget{}, err
	}
	config, err := app.LoadConfigForPath(path, "")
	if err != nil {
		return roomHubTarget{}, err
	}
	client, err := moltnetclient.New(resolveRoomApplyAttachment(config, *f.baseURL, *f.tokenEnv))
	if err != nil {
		return roomHubTarget{}, err
	}
	return roomHubTarget{path: path, config: config, client: client}, nil
}

func parseRoomHubArgs(name string, args []string, positionals int) (*flag.FlagSet, []string, []string, error) {
	flagArgs, values, err := splitRoomHubArgs(args)
	if err != nil {
		return nil, nil, nil, err
	}
	flags := flag.NewFlagSet("moltnet room "+name, flag.ContinueOnError)
	flags.SetOutput(stdout)
	if len(values) != positionals {
		return nil, nil, nil, fmt.Errorf("room %s takes %d argument(s)\n\n%s", name, positionals, buildRoomUsage())
	}
	return flags, flagArgs, values, nil
}

func splitRoomHubArgs(args []string) ([]string, []string, error) {
	var flagArgs, values []string
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case roomHubValueFlags[arg]:
			if index+1 >= len(args) {
				return nil, nil, fmt.Errorf("flag %s requires a value", arg)
			}
			flagArgs = append(flagArgs, arg, args[index+1])
			index++
		case strings.HasPrefix(arg, "-"):
			flagArgs = append(flagArgs, arg)
		default:
			values = append(values, arg)
		}
	}
	return flagArgs, values, nil
}

func runRoomShare(ctx context.Context, args []string) error {
	flags, flagArgs, values, err := parseRoomHubArgs("share", args, 1)
	if err != nil {
		return err
	}
	history := flags.String("history", protocol.RoomHistoryFromJoin, "who sees history: from_join (members see messages from their join on) or full_history")
	target := bindRoomHubFlags(flags)
	if err := flags.Parse(flagArgs); err != nil {
		return err
	}
	resolved, err := target.resolve()
	if err != nil {
		return err
	}
	view, err := resolved.client.ShareRoom(ctx, values[0], *history)
	if err != nil {
		return err
	}
	fingerprint, _, _ := protocol.ParseGlobalRoomID(view.GlobalRoomID)
	fmt.Fprintf(stdout, "%s shared room %q; this network (%s) is its home\n", green("✓"), view.LocalRoomID, view.HomeNetworkID)
	fmt.Fprintf(stdout, "  global room id   %s\n  home fingerprint %s\n  history          %s\n", view.GlobalRoomID, protocol.DisplayFingerprint(fingerprint), view.History)
	fmt.Fprintf(stdout, "  next: moltnet room invite %s\n", view.LocalRoomID)
	return nil
}

func runRoomMembers(ctx context.Context, args []string) error {
	flags, flagArgs, values, err := parseRoomHubArgs("members", args, 1)
	if err != nil {
		return err
	}
	target := bindRoomHubFlags(flags)
	if err := flags.Parse(flagArgs); err != nil {
		return err
	}
	resolved, err := target.resolve()
	if err != nil {
		return err
	}
	info, err := resolved.client.RoomHub(ctx, values[0])
	if err != nil {
		return notSharedError(values[0], err)
	}
	fmt.Fprintf(stdout, "shared room %s (home: %s, epoch %d, history %s)\n", info.LocalRoomID, info.HomeNetworkID, info.MembershipEpoch, info.History)
	table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "NETWORK\tROLE\tKEY TRUST\tFINGERPRINT")
	for _, member := range info.Members {
		trust := member.Trust
		if trust == protocol.KeyTrustIntroduced {
			trust = "introduced by " + info.HomeNetworkID
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", member.NetworkID, member.Role, trust, protocol.DisplayFingerprint(member.Fingerprint))
	}
	return table.Flush()
}

func runRoomRemoveMember(ctx context.Context, args []string) error {
	flags, flagArgs, values, err := parseRoomHubArgs("remove", args, 2)
	if err != nil {
		return err
	}
	target := bindRoomHubFlags(flags)
	if err := flags.Parse(flagArgs); err != nil {
		return err
	}
	resolved, err := target.resolve()
	if err != nil {
		return err
	}
	info, err := resolved.client.RoomHub(ctx, values[0])
	if err != nil {
		return notSharedError(values[0], err)
	}
	if !info.IsHome {
		return fmt.Errorf("only the room's home (%s) can remove members; use `moltnet room leave %s` to leave it", info.HomeNetworkID, values[0])
	}
	view, err := resolved.client.AppendHubControl(ctx, info.GlobalRoomID, protocol.RoomControlRemove, values[1], "")
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s removed %s from shared room %s (epoch %d)\n", green("✓"), values[1], values[0], view.MembershipEpoch)
	fmt.Fprintln(stdout, "  it receives nothing new and its writes are refused; copies it already holds cannot be erased")
	return nil
}

func runRoomLeave(ctx context.Context, args []string) error {
	flags, flagArgs, values, err := parseRoomHubArgs("leave", args, 1)
	if err != nil {
		return err
	}
	target := bindRoomHubFlags(flags)
	if err := flags.Parse(flagArgs); err != nil {
		return err
	}
	resolved, err := target.resolve()
	if err != nil {
		return err
	}
	info, err := resolved.client.RoomHub(ctx, values[0])
	if err != nil {
		return notSharedError(values[0], err)
	}
	if info.IsHome {
		return fmt.Errorf("this network is the home of %s and cannot leave it", values[0])
	}
	view, err := resolved.client.LeaveHubRoom(ctx, info.GlobalRoomID)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s left shared room %s (epoch %d); its local history stays\n", green("✓"), values[0], view.MembershipEpoch)
	return nil
}

func notSharedError(roomID string, err error) error {
	var apiError *moltnetclient.APIError
	if errors.As(err, &apiError) && apiError.Status == 404 {
		return fmt.Errorf("room %q is not a shared room here (share it with `moltnet room share %s`, or join one with `moltnet room join <code>`)", roomID, roomID)
	}
	return err
}
