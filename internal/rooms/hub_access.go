package rooms

import (
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// hubAccess is one network's standing over a hub room's whole control log:
// the epochs at which it was a member, and the history policy each epoch
// ran under. Visibility of history and of the log itself derives from it,
// never from a caller's cursor.
type hubAccess struct {
	// member[e-1] reports membership at epoch e (epoch == control seq).
	member []bool
	// fullHistory[e-1] reports whether epoch e ran under full_history.
	fullHistory []bool
}

// hubAccessFor replays the verified control log for network.
func hubAccessFor(records []store.HubControlRecord, network string) (hubAccess, error) {
	var access hubAccess
	var roster hubRoster
	for index, record := range records {
		if record.Seq != int64(index)+1 {
			return hubAccess{}, hubControlError("control log seq %d at position %d", record.Seq, index+1)
		}
		next, err := applyHubControl(roster, record)
		if err != nil {
			return hubAccess{}, err
		}
		roster = next
		_, member := roster.Members[network]
		access.member = append(access.member, member)
		access.fullHistory = append(access.fullHistory, roster.History == protocol.RoomHistoryFullHistory)
	}
	return access, nil
}

func (a hubAccess) memberAt(epoch int64) bool {
	return epoch >= 1 && epoch <= int64(len(a.member)) && a.member[epoch-1]
}

// lastMemberEpoch is the last epoch at which the network was a member (0:
// never).
func (a hubAccess) lastMemberEpoch() int64 {
	for epoch := int64(len(a.member)); epoch >= 1; epoch-- {
		if a.member[epoch-1] {
			return epoch
		}
	}
	return 0
}

// current reports membership at the head epoch.
func (a hubAccess) current() bool {
	return len(a.member) > 0 && a.member[len(a.member)-1]
}

// controlLimit is the last control seq the network may read: everything
// while it is a member; after removal or leaving, up to and including the
// event that ended its membership, so it learns of that and nothing later.
func (a hubAccess) controlLimit() int64 {
	if a.current() {
		return int64(len(a.member))
	}
	last := a.lastMemberEpoch()
	if last == 0 {
		return 0
	}
	return last + 1
}

// messageVisible reports whether a message placed at epoch may be served
// to the network: placed while it was a member (from_join), or, for an
// epoch that ran under full_history, placed before a later membership of
// it. A network is never served what was placed after its last membership.
func (a hubAccess) messageVisible(epoch int64) bool {
	if a.memberAt(epoch) {
		return true
	}
	if epoch < 1 || epoch > int64(len(a.fullHistory)) || !a.fullHistory[epoch-1] {
		return false
	}
	return a.lastMemberEpoch() > epoch
}
