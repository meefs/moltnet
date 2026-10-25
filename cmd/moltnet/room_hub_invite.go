package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/noopolis/moltnet/internal/app"
	"github.com/noopolis/moltnet/internal/hubinvites"
	"github.com/noopolis/moltnet/internal/netkeys"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// runRoomInvite implements `moltnet room invite <room>` on the room's home:
// a room-scoped pairing invite (the pairing is a room link that carries
// this shared room only), plus a one-time grant the joiner redeems over it.
// Like `pair invite`, the pairing takes effect after a restart.
func runRoomInvite(ctx context.Context, args []string) error {
	flags, flagArgs, values, err := parseRoomHubArgs("invite", args, 1)
	if err != nil {
		return err
	}
	var (
		role          = flags.String("role", protocol.RoomRoleWriter, "role the joining network gets: writer, reader or admin")
		pairingID     = flags.String("id", "", "pairing id for the room link (default: generated)")
		relayURLFlag  = flags.String("relay-url", "", "relay Worker URL (default: the relay from the last `moltnet relay deploy`)")
		relayTokenEnv = flags.String("relay-token-env", "", "environment variable containing the relay's RELAY_TOKEN")
		restart       = flags.Bool("restart", false, "restart the managed service after writing the pairing")
	)
	target := bindRoomHubFlags(flags)
	if err := flags.Parse(flagArgs); err != nil {
		return err
	}
	if !protocol.ValidMemberRole(*role) {
		return fmt.Errorf("--role must be writer, reader or admin")
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
		return fmt.Errorf("only the room's home (%s) can invite to %s", info.HomeNetworkID, values[0])
	}
	relayURL, relayToken, err := resolvePairInviteRelayCredentials(resolved.path, resolved.config.NetworkID, *relayURLFlag, *relayTokenEnv)
	if err != nil {
		return err
	}
	id := strings.TrimSpace(*pairingID)
	if id == "" {
		if id, err = defaultPairingID(); err != nil {
			return err
		}
		id = "room-" + strings.TrimPrefix(id, "friend-")
	}
	relayRoom, err := app.GenerateRandomToken(128)
	if err != nil {
		return err
	}
	pairingToken, err := app.GenerateRandomToken(256)
	if err != nil {
		return err
	}
	identity, _, err := netkeys.LoadOrCreateIdentity(app.KeysDirForConfigPath(resolved.path))
	if err != nil {
		return err
	}
	public := identity.Public()
	invite := protocol.Invite{
		V: protocol.InviteVersion, RelayURL: relayURL, Room: relayRoom, RelayToken: relayToken, PairingToken: pairingToken, PairingID: id,
		NetworkID: resolved.config.NetworkID, NetworkName: resolved.config.NetworkName, Exp: time.Now().UTC().Add(protocol.DefaultInviteTTL),
		NetworkPublicKey: public.PublicKey, NetworkFingerprint: public.Fingerprint,
		HubRoom: &protocol.InviteHubRoom{GlobalRoomID: info.GlobalRoomID, Role: *role},
	}
	if err := invite.Validate(); err != nil {
		return fmt.Errorf("generated room invite failed validation: %w", err)
	}
	code, err := protocol.EncodeInvite(invite)
	if err != nil {
		return err
	}
	// The grant goes first: a pairing without a grant only refuses, while
	// a grant without its pairing can never be redeemed.
	grants := hubinvites.Open(app.KeysDirForConfigPath(resolved.path))
	if err := grants.AddGrant(hubinvites.Grant{PairingID: id, GlobalRoomID: info.GlobalRoomID, Role: *role, Exp: invite.Exp}); err != nil {
		return err
	}
	pairing := app.PairingWriteback{ID: id, Token: pairingToken, Scope: protocol.PairingScopeRooms,
		Relay: &app.PairingRelayWriteback{URL: relayURL, Room: relayRoom, Token: relayToken}}
	authToken := app.AuthTokenWriteback{ID: id, Value: pairingToken, Scopes: []string{"pair"}}
	receipt := app.InviteReceipt{PairingID: id, Code: code, Exp: invite.Exp}
	if err := writePairingWithRollback(resolved.path, pairing, authToken, nil, false, &receipt); err != nil {
		_ = grants.RemoveGrant(id, info.GlobalRoomID)
		if errors.Is(err, app.ErrPairingExists) {
			return fmt.Errorf("%w, or choose a different --id", err)
		}
		return err
	}
	fmt.Fprintf(stdout, "%s room invite for %s (role %s, expires %s)\n", green("✓"), values[0], *role, invite.Exp.Format(time.RFC3339))
	fmt.Fprintf(stdout, "  home fingerprint %s — the joiner should confirm it with you out of band\n", protocol.DisplayFingerprint(public.Fingerprint))
	outcome := printPairStatusBlock(ctx, resolved.config, resolved.path, id, *restart, false)
	fmt.Fprintf(stdout, "  send this code to the joining network; it runs: moltnet room join <code>\n%s\n", code)
	return outcome.err
}

// runRoomHubJoin implements `moltnet room join <code>` on the joining
// network: it pins the home's key from the invite as verified (the code
// came out of band), writes the room link, and records a pending join the
// server finishes once it runs with the pairing: it redeems the invite with
// a signed admission, verifies the control log against the pinned key,
// provisions the local room and catches up.
func runRoomHubJoin(ctx context.Context, args []string) error {
	flags, flagArgs, values, err := parseRoomHubArgs("join", args, 1)
	if err != nil {
		return err
	}
	var (
		localRoom = flags.String("local-room", "", "local room id for the shared room (default: the home's room id)")
		force     = flags.Bool("force", false, "overwrite an existing pairing with the same id")
		restart   = flags.Bool("restart", false, "restart the managed service after writing the pairing")
	)
	configPath := flags.String("config", "", "Moltnet server config path")
	networkID := flags.String("network", "", "Moltnet network id to select under ~/.moltnet when several exist")
	if err := flags.Parse(flagArgs); err != nil {
		return err
	}
	invite, err := protocol.DecodeInvite(values[0])
	if err != nil {
		return err
	}
	if invite.HubRoom == nil {
		return errors.New("this is a pairing invite, not a room invite; run `moltnet pair <code>`")
	}
	path, err := resolvePairConfigPath(*configPath, *networkID)
	if err != nil {
		return err
	}
	config, err := app.LoadConfigForPath(path, "")
	if err != nil {
		return err
	}
	if invite.NetworkID == config.NetworkID {
		return fmt.Errorf("the room's home %q has this network's own id; one of you must change network.id", invite.NetworkID)
	}
	_, canonical, err := protocol.ParseGlobalRoomID(invite.HubRoom.GlobalRoomID)
	if err != nil {
		return err
	}
	local := strings.TrimSpace(*localRoom)
	if local == "" {
		local = canonical
	}
	if err := protocol.ValidateRoomID(local); err != nil {
		return fmt.Errorf("--local-room %w", err)
	}
	if err := pinInviteKey(path, invite); err != nil {
		return err
	}
	pairing := app.PairingWriteback{ID: invite.PairingID, RemoteNetworkID: invite.NetworkID, RemoteNetworkName: invite.NetworkName,
		Token: invite.PairingToken, Scope: protocol.PairingScopeRooms,
		Relay: &app.PairingRelayWriteback{URL: invite.RelayURL, Room: invite.Room, Token: invite.RelayToken}}
	authToken := app.AuthTokenWriteback{ID: invite.PairingID, Value: invite.PairingToken, Network: invite.NetworkID, Scopes: []string{"pair"}}
	if err := writePairingWithRollback(path, pairing, authToken, nil, *force, nil); err != nil {
		return err
	}
	join := hubinvites.PendingJoin{PairingID: invite.PairingID, GlobalRoomID: invite.HubRoom.GlobalRoomID, LocalRoomID: local}
	if err := hubinvites.Open(app.KeysDirForConfigPath(path)).AddJoin(join); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s joining shared room %s homed by %s (%s) as %s\n", green("✓"), canonical, invite.NetworkID,
		protocol.DisplayFingerprint(invite.NetworkFingerprint), invite.HubRoom.Role)
	fmt.Fprintf(stdout, "  pinned %s's key as verified from the invite; local room %q\n", invite.NetworkID, local)
	outcome := printPairStatusBlock(ctx, config, path, invite.PairingID, *restart, false)
	fmt.Fprintln(stdout, "  the server finishes the join once it runs with this pairing; check with: moltnet room members "+local)
	return outcome.err
}
