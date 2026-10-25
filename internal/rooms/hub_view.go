package rooms

import (
	"context"
	"sort"

	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// RoomHubContext describes the shared room a local room maps to, for anyone
// who may read the room: home, roster with this network's key trust, and
// the verified control events.
func (s *Service) RoomHubContext(ctx context.Context, roomID string) (protocol.HubRoomInfo, error) {
	room, ok, err := s.getRoom(ctx, roomID)
	if err != nil {
		return protocol.HubRoomInfo{}, err
	}
	if !ok {
		return protocol.HubRoomInfo{}, unknownRoomError(roomID)
	}
	if _, readable := s.readableRoom(ctx, room); !readable {
		return protocol.HubRoomInfo{}, readForbiddenError(roomID)
	}
	hub, ok, err := s.hubRoomByLocal(ctx, room.ID)
	if err != nil {
		return protocol.HubRoomInfo{}, err
	}
	if !ok {
		return protocol.HubRoomInfo{}, hubNotFoundError(room.ID)
	}
	return s.hubRoomInfo(ctx, hub)
}

func (s *Service) hubRoomInfo(ctx context.Context, hub store.HubRoom) (protocol.HubRoomInfo, error) {
	roster, records, err := s.hubRoster(ctx, hub, 0)
	if err != nil {
		return protocol.HubRoomInfo{}, err
	}
	info := protocol.HubRoomInfo{GlobalRoomID: hub.GlobalRoomID, LocalRoomID: hub.LocalRoomID, HomeNetworkID: hub.HomeNetworkID,
		HomePairingID: hub.HomePairingID, IsHome: hub.IsHome(), MembershipEpoch: roster.Epoch, History: roster.History,
		Members: []protocol.HubMemberInfo{}, Events: []protocol.HubControlInfo{}}
	for _, network := range roster.networks() {
		member := roster.Members[network]
		info.Members = append(info.Members, protocol.HubMemberInfo{NetworkID: network, Role: member.Role, Fingerprint: member.Fingerprint, Trust: s.memberTrust(member)})
	}
	sort.SliceStable(info.Members, func(i, j int) bool {
		return info.Members[i].NetworkID == hub.HomeNetworkID && info.Members[j].NetworkID != hub.HomeNetworkID
	})
	for _, record := range records {
		event, _, err := protocol.ParseRoomControlUnverified(record.Control)
		if err != nil {
			return protocol.HubRoomInfo{}, err
		}
		info.Events = append(info.Events, protocol.HubControlInfo{Seq: event.Seq, Epoch: event.Epoch, Kind: event.Kind,
			NetworkID: event.NetworkID, Role: event.Role, History: event.History, CreatedAt: event.CreatedAt})
	}
	return info, nil
}

// memberTrust is this network's trust in a member's key, by its own pin.
// A key the roster names that this network never pinned is introduced by
// the room home; one pinned to a different key is shown unconfirmed, since
// messages under it are refused anyway.
func (s *Service) memberTrust(member hubMember) string {
	if member.NetworkID == s.networkID {
		return protocol.HubTrustSelf
	}
	pin, ok, err := s.keyPins.Lookup(member.NetworkID)
	switch {
	case err != nil || (ok && pin.Fingerprint != member.Fingerprint):
		return protocol.KeyTrustUnconfirmed
	case !ok:
		return protocol.KeyTrustIntroduced
	}
	return pin.Trust
}
