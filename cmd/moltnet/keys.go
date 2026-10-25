package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/noopolis/moltnet/internal/app"
	"github.com/noopolis/moltnet/internal/netkeys"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// runKeysCommand implements `moltnet keys`: this network's signing identity
// and the pinned keys of peer networks. Everything here reads and writes the
// dedicated keys directory beside the config (.moltnet/keys/), never the
// config itself; the running server re-reads pins on every check, so trust
// and forget take effect without a restart.
func runKeysCommand(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		fmt.Fprint(stdout, buildKeysUsage())
		return nil
	}
	switch args[0] {
	case "show":
		return runKeysShow(args[1:])
	case "list":
		return runKeysList(args[1:])
	case "trust":
		return runKeysTrust(args[1:])
	case "forget":
		return runKeysForget(args[1:])
	default:
		return fmt.Errorf("unknown keys command %q\n\n%s", args[0], buildKeysUsage())
	}
}

// parseKeysArgs accepts --config/--id in any position around the command's
// positional arguments and resolves the network config they select.
func parseKeysArgs(name string, args []string, positional int) (string, []string, error) {
	flagArgs := make([]string, 0, len(args))
	var values []string
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--config" || arg == "--id" {
			if index+1 >= len(args) {
				return "", nil, fmt.Errorf("flag %s requires a value", arg)
			}
			flagArgs = append(flagArgs, arg, args[index+1])
			index++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			flagArgs = append(flagArgs, arg)
			continue
		}
		values = append(values, arg)
	}
	flags := flag.NewFlagSet("moltnet keys "+name, flag.ContinueOnError)
	flags.SetOutput(stdout)
	configPath := flags.String("config", "", "Moltnet config path")
	networkID := flags.String("id", "", "network id to select under ~/.moltnet when several exist")
	if err := flags.Parse(flagArgs); err != nil {
		return "", nil, err
	}
	if len(values) != positional {
		return "", nil, fmt.Errorf("keys %s takes %d argument(s)\n\n%s", name, positional, buildKeysUsage())
	}
	path, err := resolvePairConfigPath(*configPath, *networkID)
	if err != nil {
		return "", nil, err
	}
	return path, values, nil
}

func runKeysShow(args []string) error {
	path, _, err := parseKeysArgs("show", args, 0)
	if err != nil {
		return err
	}
	config, err := app.LoadConfigForPath(path, "")
	if err != nil {
		return err
	}
	identity, created, err := netkeys.LoadOrCreateIdentity(netkeys.DirForConfig(path))
	if err != nil {
		return err
	}
	public := identity.Public()
	if created {
		fmt.Fprintln(stdout, "generated a new network signing key")
	}
	fmt.Fprintf(stdout, "network:     %s\n", config.NetworkID)
	fmt.Fprintf(stdout, "fingerprint: %s\n", protocol.DisplayFingerprint(public.Fingerprint))
	fmt.Fprintf(stdout, "public key:  %s\n", public.PublicKey)
	fmt.Fprintln(stdout, "\nShare the fingerprint out of band; a peer pins it with:")
	fmt.Fprintf(stdout, "  moltnet keys trust %s %s\n", config.NetworkID, public.Fingerprint)
	return nil
}

func runKeysList(args []string) error {
	path, _, err := parseKeysArgs("list", args, 0)
	if err != nil {
		return err
	}
	pins, err := netkeys.OpenPinStore(netkeys.DirForConfig(path)).List()
	if err != nil {
		return err
	}
	if len(pins) == 0 {
		fmt.Fprintln(stdout, "no pinned network keys")
		return nil
	}
	table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "NETWORK\tTRUST\tSOURCE\tFINGERPRINT\tPINNED")
	for _, pin := range pins {
		source := pin.Source
		if pin.SourceID != "" {
			source += ":" + pin.SourceID
		}
		if pin.IntroducedBy != "" {
			source += " (introduced by " + pin.IntroducedBy + ")"
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", pin.NetworkID, pin.Trust, source, pin.Fingerprint, pin.PinnedAt.Format("2006-01-02"))
	}
	return table.Flush()
}

func runKeysTrust(args []string) error {
	path, values, err := parseKeysArgs("trust", args, 2)
	if err != nil {
		return err
	}
	pin, err := netkeys.OpenPinStore(netkeys.DirForConfig(path)).PinKey(netkeys.Pin{
		NetworkID: values[0], Fingerprint: values[1], Trust: protocol.KeyTrustVerified, Source: netkeys.PinSourceOperator,
	})
	if errors.Is(err, netkeys.ErrPinConflict) {
		return fmt.Errorf("%w\nif the network really has a new key (v1 has no rotation: a lost or leaked key means a new identity), run `moltnet keys forget %s` first", err, values[0])
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "trusted %s as %s (%s)\n", pin.NetworkID, protocol.DisplayFingerprint(pin.Fingerprint), pin.Trust)
	return nil
}

func runKeysForget(args []string) error {
	path, values, err := parseKeysArgs("forget", args, 1)
	if err != nil {
		return err
	}
	removed, err := netkeys.OpenPinStore(netkeys.DirForConfig(path)).Forget(values[0])
	if err != nil {
		return err
	}
	if !removed {
		fmt.Fprintf(stdout, "no pinned key for %s\n", values[0])
		return nil
	}
	fmt.Fprintf(stdout, "forgot the pinned key for %s; its next signed message over a pairing pins a key again\n", values[0])
	return nil
}

func buildKeysUsage() string {
	return `Usage:
  moltnet keys show [--config <path>] [--id <network-id>]
  moltnet keys list [--config <path>] [--id <network-id>]
  moltnet keys trust <network-id> <fingerprint> [--config <path>] [--id <network-id>]
  moltnet keys forget <network-id> [--config <path>] [--id <network-id>]

Every network signs the messages it relays over pairings with an Ed25519 key
kept in .moltnet/keys/network.key (mode 0600) beside its config. Its
fingerprint is sha256:<hex> of the public key; network ids stay readable,
the fingerprint is what peers trust.

"keys show" prints this network's fingerprint and public key (creating the
key if the server has not yet). "keys list" prints pinned peer keys: verified
pins come from an invite, the first signed message over a pairing, or
"keys trust"; introduced pins are vouched for by a room home only.

"keys trust" pins a fingerprint you checked out of band as verified; it
refuses to replace a different pinned key. "keys forget" removes a pin. v1
has no key rotation: a lost or leaked key is a new identity, so its peers
forget the old pin and trust the new fingerprint.
`
}
