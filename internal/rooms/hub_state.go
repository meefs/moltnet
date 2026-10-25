package rooms

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

var (
	// ErrHubRoom covers hub room refusals that are definitive for a sender.
	ErrHubRoom = errors.New("hub room request refused")
	// ErrStaleMembershipEpoch is the retry-class stale epoch answer (412).
	ErrStaleMembershipEpoch = errors.New(protocol.StaleMembershipEpochCode)
)

func hubForbiddenError(message string) error {
	return &Error{status: http.StatusForbidden, msg: message, cause: ErrHubRoom}
}

func hubInvalidError(message string) error {
	return &Error{status: http.StatusUnprocessableEntity, msg: message, cause: ErrHubRoom}
}

func hubNotFoundError(globalRoomID string) error {
	return &Error{status: http.StatusNotFound, msg: fmt.Sprintf("unknown hub room %q", globalRoomID), cause: ErrHubRoom}
}

func hubConflictError(err error) error {
	return &Error{status: http.StatusConflict, msg: err.Error(), cause: err}
}

func hubUnavailableError(message string) error {
	return &Error{status: http.StatusServiceUnavailable, msg: message, cause: ErrHubRoom}
}

func staleEpochError(current int64) error {
	return &Error{status: http.StatusPreconditionFailed, cause: ErrStaleMembershipEpoch,
		msg: fmt.Sprintf("%s: the room is at membership epoch %d and this event was not placed; sync its control log and re-sign", protocol.StaleMembershipEpochCode, current)}
}

func (s *Service) hubRooms(ctx context.Context) ([]store.HubRoom, error) {
	if s.hubs == nil {
		return nil, nil
	}
	return s.hubs.HubRoomsContext(ctx)
}

func (s *Service) hubRoomByGlobal(ctx context.Context, globalRoomID string) (store.HubRoom, bool, error) {
	rooms, err := s.hubRooms(ctx)
	if err != nil {
		return store.HubRoom{}, false, err
	}
	for _, room := range rooms {
		if room.GlobalRoomID == globalRoomID {
			return room, true, nil
		}
	}
	return store.HubRoom{}, false, nil
}

func (s *Service) hubRoomByLocal(ctx context.Context, localRoomID string) (store.HubRoom, bool, error) {
	rooms, err := s.hubRooms(ctx)
	if err != nil {
		return store.HubRoom{}, false, err
	}
	for _, room := range rooms {
		if room.LocalRoomID == localRoomID {
			return room, true, nil
		}
	}
	return store.HubRoom{}, false, nil
}

// hubRoster replays the stored control log (upto 0 = current epoch).
func (s *Service) hubRoster(ctx context.Context, room store.HubRoom, upto int64) (hubRoster, []store.HubControlRecord, error) {
	records, err := s.hubs.HubControlLogContext(ctx, room.GlobalRoomID, 0)
	if err != nil {
		return hubRoster{}, nil, err
	}
	roster, err := replayHubRoster(records, upto)
	if err != nil {
		return hubRoster{}, nil, fmt.Errorf("hub room %s control log: %w", room.GlobalRoomID, err)
	}
	return roster, records, nil
}

// pairingNetworkID is the network a pairing speaks for: config's remote
// network id, else the single network pinned through it.
func (s *Service) pairingNetworkID(pairing protocol.Pairing) (string, error) {
	if remote := strings.TrimSpace(pairing.RemoteNetworkID); remote != "" {
		return remote, nil
	}
	networks, err := s.keyPins.PairingNetworks(pairing.ID)
	if err != nil {
		return "", err
	}
	if len(networks) != 1 {
		return "", fmt.Errorf("pairing %q has no single known network yet (it learns one at its peer's first signed message)", pairing.ID)
	}
	return networks[0], nil
}

// pairingForHubMember finds the one live pairing that reaches network.
func (s *Service) pairingForHubMember(network string) (protocol.Pairing, bool) {
	var found []protocol.Pairing
	for _, pairing := range s.snapshotRelayPairings() {
		if id, err := s.pairingNetworkID(pairing); err == nil && id == network {
			found = append(found, pairing)
		}
	}
	if len(found) != 1 {
		return protocol.Pairing{}, false
	}
	return found[0], true
}

// hubFanoutPairings lists the member links a placed message goes to: every
// active member but the home itself and the message's origin.
func (s *Service) hubFanoutPairings(ctx context.Context, roster hubRoster, origin string) []string {
	var ids []string
	for _, network := range roster.networks() {
		if network == roster.HomeNetworkID || network == origin {
			continue
		}
		pairing, ok := s.pairingForHubMember(network)
		if !ok {
			logHub(ctx, roster.GlobalRoomID).Warn("hub member has no unique pairing; it will not receive this message", "network_id", network)
			continue
		}
		ids = append(ids, pairing.ID)
	}
	slices.Sort(ids)
	return ids
}

// hubMessageID namespaces stored ids of messages placed in a hub room by
// their event id, so fifty origins' local ids can never collide (a 409).
func hubMessageID(eventID string) string {
	return "fed-" + eventID
}
