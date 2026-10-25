package rooms

import (
	"context"
	"errors"
	"fmt"
	"strings"

	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/internal/netkeys"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// AdmitHubMemberContext is the home redeeming a room invite (POST
// /v1/hub/admit): over the invite's pairing, a network proves possession
// of its key and asks to join. The home pins that key through the pairing
// (unconfirmed, as any inviter-side first contact), binds the pairing to
// the network, appends the add with the invite's role, and consumes the
// grant. A retry by an already admitted member is answered with the log.
func (s *Service) AdmitHubMemberContext(ctx context.Context, signed protocol.SignedRoomAdmission) (protocol.RoomControlPage, error) {
	pairing, peer := s.pairingForPairScopedContext(ctx)
	if !peer {
		return protocol.RoomControlPage{}, hubForbiddenError("room admission arrives over the invite's pairing")
	}
	if err := s.requireHubs(); err != nil {
		return protocol.RoomControlPage{}, err
	}
	admission, err := protocol.VerifyRoomAdmission(signed)
	if err != nil {
		return protocol.RoomControlPage{}, signatureRejectedError(err.Error())
	}
	logger := logHub(ctx, admission.GlobalRoomID).With("network_id", admission.NetworkID, "pairing_id", pairing.ID)
	hub, ok, err := s.hubRoomByGlobal(ctx, admission.GlobalRoomID)
	if err != nil {
		return protocol.RoomControlPage{}, err
	}
	if !ok || !hub.IsHome() || admission.NetworkID == s.networkID {
		return protocol.RoomControlPage{}, hubNotFoundError(admission.GlobalRoomID)
	}
	s.hubMu.Lock()
	roster, _, err := s.hubRoster(ctx, hub, 0)
	s.hubMu.Unlock()
	if err != nil {
		return protocol.RoomControlPage{}, err
	}
	if member, ok := roster.Members[admission.NetworkID]; ok {
		if member.Fingerprint != admission.Fingerprint || !s.pairingSpeaksFor(ctx, pairing, admission.NetworkID) {
			return protocol.RoomControlPage{}, hubForbiddenError("this network is already a member under another key or pairing")
		}
		return s.HubControlPageContext(ctx, hub.GlobalRoomID, 0)
	}
	now, err := s.now()
	if err != nil {
		return protocol.RoomControlPage{}, err
	}
	grant, ok, err := s.hubInvites.Grant(pairing.ID, hub.GlobalRoomID, now)
	if err != nil {
		return protocol.RoomControlPage{}, trustStoreUnavailableError(err)
	}
	if !ok {
		logger.Warn("refusing room admission without a live invite for this pairing")
		return protocol.RoomControlPage{}, hubForbiddenError("no live room invite for this pairing and room")
	}
	claims, _ := authn.ClaimsFromContext(ctx)
	if !s.pairCredentialMatchesOrigin(ctx, claims, admission.NetworkID, s.requirePairNetworkBinding) {
		return protocol.RoomControlPage{}, hubForbiddenError("this pairing does not speak for the admitted network")
	}
	trust := protocol.KeyTrustUnconfirmed
	if strings.TrimSpace(claims.Network()) == admission.NetworkID {
		trust = protocol.KeyTrustVerified
	}
	pin, _, err := s.keyPins.PinKeyCreated(netkeys.Pin{NetworkID: admission.NetworkID, Fingerprint: admission.Fingerprint, PublicKey: admission.PublicKey,
		Trust: trust, Source: netkeys.PinSourcePairing, SourceID: pairing.ID})
	switch {
	case errors.Is(err, netkeys.ErrPinConflict):
		logger.Warn("refusing room admission under a key that conflicts with the pinned one")
		return protocol.RoomControlPage{}, signatureRejectedError(fmt.Sprintf("network %q is pinned to a different key", admission.NetworkID))
	case err != nil:
		return protocol.RoomControlPage{}, trustStoreUnavailableError(err)
	case pin.Fingerprint != admission.Fingerprint:
		return protocol.RoomControlPage{}, signatureRejectedError(fmt.Sprintf("network %q is pinned to a different key", admission.NetworkID))
	}
	if err := s.keyPins.BindPairing(pairing.ID, admission.NetworkID); err != nil {
		return protocol.RoomControlPage{}, trustStoreUnavailableError(err)
	}
	if _, err := s.appendHubControl(ctx, HubControlRequest{GlobalRoomID: hub.GlobalRoomID, Kind: protocol.RoomControlAdd,
		PairingID: pairing.ID, NetworkID: admission.NetworkID, Role: grant.Role}); err != nil {
		return protocol.RoomControlPage{}, err
	}
	if err := s.hubInvites.RemoveGrant(pairing.ID, hub.GlobalRoomID); err != nil {
		logger.Error("admitted a member but could not consume its room invite", "error", err)
	}
	logger.Info("admitted a network to a hub room through its invite", "role", grant.Role, "fingerprint", admission.Fingerprint)
	return s.HubControlPageContext(ctx, hub.GlobalRoomID, 0)
}

// pairingSpeaksFor reports whether pairing is known to speak for network.
func (s *Service) pairingSpeaksFor(_ context.Context, pairing protocol.Pairing, network string) bool {
	speaks, err := s.pairingNetworkID(pairing)
	return err == nil && speaks == network
}

// LeaveHubPeerContext is the home honoring a member's leave (POST
// /v1/hub/leave): the caller's network, by its pairing, leaves the room.
func (s *Service) LeaveHubPeerContext(ctx context.Context, request protocol.RoomLeaveRequest) (protocol.RoomControlPage, error) {
	pairing, peer := s.pairingForPairScopedContext(ctx)
	if !peer {
		return protocol.RoomControlPage{}, hubForbiddenError("a member leaves over its own room link")
	}
	if err := s.requireHubs(); err != nil {
		return protocol.RoomControlPage{}, err
	}
	hub, ok, err := s.hubRoomByGlobal(ctx, request.GlobalRoomID)
	if err != nil {
		return protocol.RoomControlPage{}, err
	}
	network, netErr := s.pairingNetworkID(pairing)
	if !ok || !hub.IsHome() || netErr != nil {
		return protocol.RoomControlPage{}, hubNotFoundError(request.GlobalRoomID)
	}
	roster, _, err := s.hubRoster(ctx, hub, 0)
	if err != nil {
		return protocol.RoomControlPage{}, err
	}
	if _, member := roster.Members[network]; member {
		if _, err := s.appendHubControl(ctx, HubControlRequest{GlobalRoomID: hub.GlobalRoomID, Kind: protocol.RoomControlLeave, NetworkID: network}); err != nil {
			return protocol.RoomControlPage{}, err
		}
		logHub(ctx, hub.GlobalRoomID).Info("member network left the hub room", "network_id", network)
	}
	return s.HubControlPageContext(ctx, hub.GlobalRoomID, 0)
}
