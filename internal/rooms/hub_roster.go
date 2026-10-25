package rooms

import (
	"crypto/ed25519"
	"fmt"
	"sort"

	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// hubMember is one network's standing in a hub room at some epoch.
type hubMember struct {
	NetworkID   string
	Fingerprint string
	PublicKey   string
	Role        string
}

// hubRoster is a hub room's membership at Epoch, rebuilt by replaying the
// home-signed control log. Every replay re-verifies every signature and the
// hash chain, so a stored log can never be trusted further than it verifies.
type hubRoster struct {
	GlobalRoomID    string
	RoomID          string // the home's local room id: the canonical wire id
	HomeNetworkID   string
	HomeFingerprint string
	HomeKey         ed25519.PublicKey
	History         string
	Epoch           int64
	HeadDigest      string
	Members         map[string]hubMember
	// Ever lists every network the log ever admitted, removed or not.
	Ever map[string]bool
}

// canWrite reports whether network may post at this epoch.
func (r hubRoster) canWrite(network string) (hubMember, bool) {
	member, ok := r.Members[network]
	return member, ok && protocol.RoomRoleCanWrite(member.Role)
}

func (r hubRoster) networks() []string {
	networks := make([]string, 0, len(r.Members))
	for network := range r.Members {
		networks = append(networks, network)
	}
	sort.Strings(networks)
	return networks
}

func hubControlError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", protocol.ErrRoomControlMalformed, fmt.Sprintf(format, args...))
}

// replayHubRoster verifies records (which must start at genesis) and
// returns the roster after the event whose epoch is upto (0 means all).
func replayHubRoster(records []store.HubControlRecord, upto int64) (hubRoster, error) {
	if len(records) == 0 {
		return hubRoster{}, hubControlError("empty control log")
	}
	var roster hubRoster
	for index, record := range records {
		if record.Seq != int64(index)+1 {
			return hubRoster{}, hubControlError("control log seq %d at position %d", record.Seq, index+1)
		}
		if upto > 0 && record.Seq > upto {
			break
		}
		next, err := applyHubControl(roster, record)
		if err != nil {
			return hubRoster{}, err
		}
		roster = next
	}
	if upto > 0 && roster.Epoch != upto {
		return hubRoster{}, hubControlError("control log does not reach epoch %d", upto)
	}
	return roster, nil
}

func applyHubControl(roster hubRoster, record store.HubControlRecord) (hubRoster, error) {
	key := roster.HomeKey
	if record.Seq == 1 {
		// Genesis is self-signed by the key it names; the caller decides
		// whether that key is the home it expects (pins, global room id).
		event, _, err := protocol.ParseRoomControlUnverified(record.Control)
		if err != nil {
			return hubRoster{}, err
		}
		if key, err = protocol.ParseNetworkPublicKey(event.PublicKey); err != nil {
			return hubRoster{}, hubControlError("genesis key: %v", err)
		}
	}
	event, digest, err := protocol.VerifyRoomControl(record.Control, key)
	if err != nil {
		return hubRoster{}, err
	}
	if digest != record.Digest || event.Seq != record.Seq || (roster.GlobalRoomID != "" && event.GlobalRoomID != roster.GlobalRoomID) {
		return hubRoster{}, hubControlError("control event %d does not match its stored record", record.Seq)
	}
	if event.PrevHash != roster.HeadDigest {
		return hubRoster{}, hubControlError("control event %d does not chain to the previous event", event.Seq)
	}
	next := roster
	next.Members = make(map[string]hubMember, len(roster.Members)+1)
	for network, member := range roster.Members {
		next.Members[network] = member
	}
	next.Ever = make(map[string]bool, len(roster.Ever)+1)
	for network := range roster.Ever {
		next.Ever[network] = true
	}
	next.Epoch, next.HeadDigest = event.Epoch, digest
	subject, active := next.Members[event.NetworkID]
	switch event.Kind {
	case protocol.RoomControlGenesis:
		fingerprint, roomID, _ := protocol.ParseGlobalRoomID(event.GlobalRoomID)
		if event.Seq != 1 || fingerprint != event.Fingerprint {
			return hubRoster{}, hubControlError("genesis must be first and name the home key in the global room id")
		}
		next.GlobalRoomID, next.RoomID, next.HomeNetworkID = event.GlobalRoomID, roomID, event.NetworkID
		next.HomeFingerprint, next.HomeKey, next.History = event.Fingerprint, key, event.History
		next.Members[event.NetworkID] = hubMember{NetworkID: event.NetworkID, Fingerprint: event.Fingerprint, PublicKey: event.PublicKey, Role: protocol.RoomRoleOwner}
	case protocol.RoomControlAdd:
		if active || event.NetworkID == next.HomeNetworkID {
			return hubRoster{}, hubControlError("cannot add %s: already a member", event.NetworkID)
		}
		next.Members[event.NetworkID] = hubMember{NetworkID: event.NetworkID, Fingerprint: event.Fingerprint, PublicKey: event.PublicKey, Role: event.Role}
	case protocol.RoomControlRemove, protocol.RoomControlLeave:
		if !active || event.NetworkID == next.HomeNetworkID {
			return hubRoster{}, hubControlError("cannot %s %s: not a removable member", event.Kind, event.NetworkID)
		}
		delete(next.Members, event.NetworkID)
	case protocol.RoomControlSetRole:
		if !active || event.NetworkID == next.HomeNetworkID {
			return hubRoster{}, hubControlError("cannot set the role of %s", event.NetworkID)
		}
		subject.Role = event.Role
		next.Members[event.NetworkID] = subject
	case protocol.RoomControlSetPolicy:
		next.History = event.History
	default:
		return hubRoster{}, hubControlError("unknown control kind %q", event.Kind)
	}
	if event.NetworkID != "" {
		next.Ever[event.NetworkID] = true
	}
	return next, nil
}
