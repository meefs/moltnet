package follow

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func (w *worker) selected(id string) bool {
	if len(w.config.Rooms) == 0 {
		return true
	}
	for _, selected := range w.config.Rooms {
		if selected == id {
			return true
		}
	}
	return false
}

// Full discovery is reserved for subscription/reconnect and periodic polling.
// Room failures are diagnostics, not a reason to stop healthy room streams.
func (w *worker) sync(ctx context.Context, networkID string) error {
	before := ""
	found := map[string]bool{}
	for {
		var page protocol.RoomPage
		if err := w.get(ctx, "/v1/rooms?limit=200&before="+url.QueryEscape(before), &page); err != nil {
			return err
		}
		for _, room := range page.Rooms {
			if !w.selected(room.ID) || protocol.NormalizeRoomVisibility(room.Visibility) != protocol.RoomVisibilityPublic || room.Follow != nil {
				continue
			}
			found[room.ID] = true
			w.rooms[room.ID] = room
			count, err := w.syncRoom(ctx, networkID, room)
			w.roomStatus(room.ID, count, err)
		}
		if !page.Page.HasMore {
			break
		}
		if page.Page.NextBefore == "" || page.Page.NextBefore == before {
			return fmt.Errorf("origin returned non-advancing room cursor")
		}
		before = page.Page.NextBefore
	}
	for id := range w.rooms {
		if !found[id] {
			delete(w.rooms, id)
			w.roomStatus(id, -1, fmt.Errorf("room %q is not public-readable", id))
		}
	}
	for _, id := range w.config.Rooms {
		if !found[id] {
			w.roomStatus(id, -1, fmt.Errorf("configured room %q is not public-readable", id))
		}
	}
	w.synced()
	return ctx.Err()
}

func (w *worker) syncRoom(ctx context.Context, networkID string, room protocol.Room) (int, error) {
	if err := w.sink.EnsureFollowRoom(ctx, w.config, room); err != nil {
		return -1, err
	}
	threadKey := w.historyKey(networkID, room.ID, "") + "/threads-baselined"
	baseline, err := w.sink.GetFollowCheckpoint(ctx, threadKey)
	if err != nil {
		return -1, err
	}
	count, err := w.syncHistory(ctx, networkID, room.ID, "", w.config.BackfillLimit())
	if err != nil {
		return -1, err
	}
	backfill := w.config.BackfillLimit()
	if baseline.Initialized {
		backfill = math.MaxInt
	}
	before := ""
	for {
		var page protocol.ThreadPage
		if err := w.get(ctx, "/v1/rooms/"+escaped(room.ID)+"/threads?limit=200&before="+url.QueryEscape(before), &page); err != nil {
			return -1, err
		}
		for _, thread := range page.Threads {
			n, err := w.syncHistory(ctx, networkID, room.ID, thread.ID, backfill)
			if err != nil {
				return -1, err
			}
			count += n
		}
		if !page.Page.HasMore {
			break
		}
		if page.Page.NextBefore == "" || page.Page.NextBefore == before {
			return -1, fmt.Errorf("origin returned non-advancing thread cursor")
		}
		before = page.Page.NextBefore
	}
	if !baseline.Initialized {
		err = w.sink.SetFollowCheckpoint(ctx, threadKey, Checkpoint{Initialized: true})
	}
	return count, err
}

func (w *worker) roomStatus(id string, count int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	index := -1
	for i := range w.status.Rooms {
		if w.status.Rooms[i].RemoteRoomID == id {
			index = i
			break
		}
	}
	if index < 0 {
		w.status.Rooms = append(w.status.Rooms, protocol.FollowRoomStatus{RemoteRoomID: id, LocalRoomID: w.config.LocalRoomID(id)})
		index = len(w.status.Rooms) - 1
	}
	value := &w.status.Rooms[index]
	if count >= 0 {
		value.MessageCount = count
	}
	value.LastError = ""
	if err != nil {
		value.LastError = err.Error()
	}
}
func (w *worker) synced() {
	now := time.Now().UTC()
	w.mu.Lock()
	w.status.LastSynced = &now
	w.mu.Unlock()
}

func (w *worker) syncTarget(ctx context.Context, networkID string, target scanTarget) {
	if !w.selected(target.room) {
		return
	}
	// Re-check anonymous visibility for just this room; event payloads are hints.
	var room protocol.Room
	if err := w.get(ctx, "/v1/rooms/"+escaped(target.room), &room); err != nil {
		w.roomStatus(target.room, -1, err)
		return
	}
	if room.ID != target.room || protocol.NormalizeRoomVisibility(room.Visibility) != protocol.RoomVisibilityPublic || room.Follow != nil {
		w.roomStatus(target.room, -1, fmt.Errorf("room %q is not public-readable", target.room))
		return
	}
	if _, known := w.rooms[room.ID]; !known {
		w.rooms[room.ID] = room
		count, err := w.syncRoom(ctx, networkID, room)
		w.roomStatus(room.ID, count, err)
		return
	}
	if err := w.sink.EnsureFollowRoom(ctx, w.config, room); err != nil {
		w.roomStatus(room.ID, -1, err)
		return
	}
	key := w.historyKey(networkID, room.ID, target.thread)
	previous, err := w.sink.GetFollowCheckpoint(ctx, key)
	if err != nil {
		w.roomStatus(room.ID, -1, err)
		return
	}
	backfill := w.config.BackfillLimit()
	if target.thread != "" {
		baseline, err := w.sink.GetFollowCheckpoint(ctx, w.historyKey(networkID, room.ID, "")+"/threads-baselined")
		if err != nil {
			w.roomStatus(room.ID, -1, err)
			return
		}
		if baseline.Initialized {
			backfill = math.MaxInt
		}
	}
	count, err := w.syncHistory(ctx, networkID, room.ID, target.thread, backfill)
	if err != nil {
		w.roomStatus(room.ID, -1, err)
		return
	}
	w.mu.RLock()
	total := 0
	for _, status := range w.status.Rooms {
		if status.RemoteRoomID == room.ID {
			total = status.MessageCount
			break
		}
	}
	w.mu.RUnlock()
	w.roomStatus(room.ID, total+count-previous.Count, nil)
	w.synced()
}
