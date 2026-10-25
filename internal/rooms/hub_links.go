package rooms

import (
	"context"
	"fmt"
	"slices"
	"strings"

	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/internal/hubinvites"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// Member links (home side). A network may reach the home over several
// pairings: a direct full pairing, and one room link per room invite. The
// home records, per room, the one pairing each member was added over
// (hubinvites.Link) and treats it as that member's only credential for that
// room: it alone may submit, sync, read the control log and leave, and the
// room fans out to the member over it. A credential linked for room A is a
// stranger to room B. The record is durable, so it is also the binding that
// strict pair-network-binding mode asks for.

// hubLinkPairing is the pairing network was added to room over.
func (s *Service) hubLinkPairing(globalRoomID, network string) (string, bool, error) {
	links, err := s.hubInvites.Links(globalRoomID)
	if err != nil {
		return "", false, trustStoreUnavailableError(err)
	}
	for _, link := range links {
		if link.NetworkID == network {
			return link.PairingID, true, nil
		}
	}
	return "", false, nil
}

// hubLinkNetwork is the member network pairing is the room's link for.
func (s *Service) hubLinkNetwork(globalRoomID, pairingID string) (string, bool, error) {
	links, err := s.hubInvites.Links(globalRoomID)
	if err != nil {
		return "", false, trustStoreUnavailableError(err)
	}
	for _, link := range links {
		if link.PairingID == pairingID {
			return link.NetworkID, true, nil
		}
	}
	return "", false, nil
}

// hubPeerNetwork authorizes a peer credential for one hub room: it must be
// the link a member network was added over for this very room, and a
// config-bound credential must be bound to that network. ok is false for
// any other credential, including a valid link for another room.
func (s *Service) hubPeerNetwork(ctx context.Context, globalRoomID string, pairing protocol.Pairing) (string, bool, error) {
	network, ok, err := s.hubLinkNetwork(globalRoomID, pairing.ID)
	if err != nil || !ok {
		return "", false, err
	}
	if claims, _ := authn.ClaimsFromContext(ctx); strings.TrimSpace(claims.Network()) != "" && strings.TrimSpace(claims.Network()) != network {
		logHub(ctx, globalRoomID).Warn("refusing a hub room request: the credential is bound to another network than the member it links", "pairing_id", pairing.ID)
		return "", false, nil
	}
	return network, true, nil
}

// hubLinkSpeaksFor reports whether the caller's pairing is network's link
// for the room (a member submission's transport authorization).
func (s *Service) hubLinkSpeaksFor(ctx context.Context, globalRoomID string, pairing protocol.Pairing, network string) (bool, error) {
	linked, ok, err := s.hubPeerNetwork(ctx, globalRoomID, pairing)
	return ok && linked == network, err
}

// checkAdmissionCredential decides whether pairing may redeem a room invite
// as network. The live grant for this pairing and room is the
// authorization, and the admission's signature proves the key; this checks
// the credential is not already speaking for someone else:
//   - a config-bound credential must be bound to network;
//   - a credential that already spoke for another network may not switch;
//   - the network the pairing's peer reported in discovery must agree;
//   - a network directly paired (config-bound) elsewhere but with no pinned
//     key yet cannot be claimed here first, or a stranger holding the
//     invite could pin its own key under that network's id.
//
// It holds in strict pair-network-binding mode as well: redeeming the grant
// is the moment the inviter learns the joiner's network, as `moltnet pair`
// is for the joiner, and the member link it records is durable.
func (s *Service) checkAdmissionCredential(ctx context.Context, pairing protocol.Pairing, network string) error {
	claims, _ := authn.ClaimsFromContext(ctx)
	if bound := strings.TrimSpace(claims.Network()); bound != "" && bound != network {
		return hubForbiddenError("this pairing is bound to another network")
	}
	spoken, err := s.keyPins.PairingNetworks(pairing.ID)
	if err != nil {
		return trustStoreUnavailableError(err)
	}
	if slices.ContainsFunc(spoken, func(other string) bool { return other != network }) {
		return hubForbiddenError("this pairing already speaks for another network")
	}
	if s.pairingDeliveries != nil {
		if state, err := s.pairingDeliveries.PairingDeliveryStateContext(ctx, pairing.ID); err == nil &&
			state.RemoteNetworkID != "" && state.RemoteNetworkID != network {
			return hubForbiddenError("this pairing's peer reported another network in discovery")
		}
	}
	if _, pinned, err := s.keyPins.Lookup(network); err != nil {
		return trustStoreUnavailableError(err)
	} else if !pinned {
		if other, err := s.findPairingByRemoteNetwork(network); err == nil && other.ID != pairing.ID {
			return hubForbiddenError(fmt.Sprintf("network %q is paired directly but has no pinned key yet; it must send one signed message over that pairing first", network))
		}
	}
	return nil
}

// recordHubLink makes pairing network's link for the room. It is written
// before the add it accompanies: a link without membership authorizes
// nothing, while membership without its link would strand the member.
func (s *Service) recordHubLink(globalRoomID, pairingID, network string) error {
	if err := s.hubInvites.SetLink(hubinvites.Link{PairingID: pairingID, GlobalRoomID: globalRoomID, NetworkID: network}); err != nil {
		return trustStoreUnavailableError(err)
	}
	return nil
}

// hubMemberPairing is the live pairing a member network is linked over.
func (s *Service) hubMemberPairing(globalRoomID, network string) (protocol.Pairing, bool) {
	pairingID, ok, err := s.hubLinkPairing(globalRoomID, network)
	if err != nil || !ok {
		return protocol.Pairing{}, false
	}
	for _, pairing := range s.snapshotRelayPairings() {
		if pairing.ID == pairingID {
			return pairing, true
		}
	}
	return protocol.Pairing{}, false
}
