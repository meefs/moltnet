package rooms

import (
	"sync"

	"github.com/noopolis/moltnet/internal/store"
)

// hubRosterCache remembers the verified roster after every epoch of each
// hub room's control log, keyed by the record digests it verified, so a
// message no longer re-verifies the whole log two or three times. A stored
// log that no longer extends the cached prefix is replayed from scratch.
type hubRosterCache struct {
	mu    sync.Mutex
	rooms map[string]*verifiedControlLog
}

type verifiedControlLog struct {
	digests []string
	rosters []hubRoster // rosters[i] is the roster at epoch i+1
}

// roster returns the verified roster at upto (0: the head) for records.
func (c *hubRosterCache) roster(globalRoomID string, records []store.HubControlRecord, upto int64) (hubRoster, error) {
	if len(records) == 0 {
		return hubRoster{}, hubControlError("empty control log")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rooms == nil {
		c.rooms = map[string]*verifiedControlLog{}
	}
	cached := c.rooms[globalRoomID]
	if cached == nil || !cached.prefixOf(records) {
		cached = &verifiedControlLog{}
	}
	for index := len(cached.digests); index < len(records); index++ {
		record := records[index]
		if record.Seq != int64(index)+1 {
			return hubRoster{}, hubControlError("control log seq %d at position %d", record.Seq, index+1)
		}
		var previous hubRoster
		if index > 0 {
			previous = cached.rosters[index-1]
		}
		next, err := applyHubControl(previous, record)
		if err != nil {
			return hubRoster{}, err
		}
		cached.digests = append(cached.digests, record.Digest)
		cached.rosters = append(cached.rosters, next)
	}
	c.rooms[globalRoomID] = cached
	switch {
	case upto == 0:
		return cached.rosters[len(records)-1], nil
	case upto > int64(len(records)):
		return hubRoster{}, hubControlError("control log does not reach epoch %d", upto)
	}
	return cached.rosters[upto-1], nil
}

func (l *verifiedControlLog) prefixOf(records []store.HubControlRecord) bool {
	if len(l.digests) > len(records) {
		return false
	}
	for index, digest := range l.digests {
		if records[index].Digest != digest {
			return false
		}
	}
	return true
}
