package follow

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

const pageSize = 200

func (w *worker) sync(ctx context.Context, networkID string) error {
	before := ""
	statuses := []protocol.FollowRoomStatus{}
	for {
		var page protocol.RoomPage
		if err := w.get(ctx, "/v1/rooms?limit=200&before="+url.QueryEscape(before), &page); err != nil {
			return err
		}
		for _, room := range page.Rooms {
			if protocol.NormalizeRoomVisibility(room.Visibility) != protocol.RoomVisibilityPublic || room.Follow != nil {
				continue
			}
			selected := len(w.config.Rooms) == 0
			for _, id := range w.config.Rooms {
				if id == room.ID {
					selected = true
				}
			}
			if !selected {
				continue
			}
			if err := w.sink.EnsureFollowRoom(ctx, w.config, room); err != nil {
				return err
			}
			roomState, err := w.sink.GetFollowCheckpoint(ctx, w.historyKey(networkID, room.ID, ""))
			if err != nil {
				return err
			}
			count, err := w.syncHistory(ctx, networkID, room.ID, "", w.config.BackfillLimit())
			threadBackfill := w.config.BackfillLimit()
			if roomState.Initialized {
				threadBackfill = math.MaxInt
			}
			if err != nil {
				return err
			}
			threadBefore := ""
			for {
				var threads protocol.ThreadPage
				if err := w.get(ctx, "/v1/rooms/"+escaped(room.ID)+"/threads?limit=200&before="+url.QueryEscape(threadBefore), &threads); err != nil {
					return err
				}
				for _, thread := range threads.Threads {
					n, err := w.syncHistory(ctx, networkID, room.ID, thread.ID, threadBackfill)
					if err != nil {
						return err
					}
					count += n
				}
				if !threads.Page.HasMore {
					break
				}
				if threads.Page.NextBefore == "" || threads.Page.NextBefore == threadBefore {
					return fmt.Errorf("origin returned non-advancing thread cursor")
				}
				threadBefore = threads.Page.NextBefore
			}
			statuses = append(statuses, protocol.FollowRoomStatus{RemoteRoomID: room.ID, LocalRoomID: w.config.LocalRoomID(room.ID), MessageCount: count})
		}
		if !page.Page.HasMore {
			break
		}
		if page.Page.NextBefore == "" || page.Page.NextBefore == before {
			return fmt.Errorf("origin returned non-advancing room cursor")
		}
		before = page.Page.NextBefore
	}
	for _, id := range w.config.Rooms {
		found := false
		for _, status := range statuses {
			if status.RemoteRoomID == id {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("configured room %q is not public-readable", id)
		}
	}
	now := time.Now().UTC()
	w.mu.Lock()
	w.status.Rooms = statuses
	w.status.LastSynced = &now
	w.mu.Unlock()
	return nil
}

func (w *worker) syncHistory(ctx context.Context, networkID, roomID, threadID string, backfill int) (int, error) {
	key := w.historyKey(networkID, roomID, threadID)
	checkpoint, err := w.sink.GetFollowCheckpoint(ctx, key)
	if err != nil {
		return 0, err
	}
	path := "/v1/rooms/" + escaped(roomID) + "/messages"
	if threadID != "" {
		path = "/v1/threads/" + escaped(threadID) + "/messages"
	}
	read := func(before, after string, limit int) (protocol.MessagePage, error) {
		var page protocol.MessagePage
		err := w.get(ctx, fmt.Sprintf("%s?limit=%d&before=%s&after=%s", path, limit, url.QueryEscape(before), url.QueryEscape(after)), &page)
		return page, err
	}
	initial := !checkpoint.Initialized
	if initial {
		if err = w.suppressBaseline(ctx, key, read); err != nil {
			return 0, err
		}
	}
	ingest := func(messages []protocol.Message) error {
		for _, message := range messages {
			if message.Target.RoomID != roomID || threadID != "" && message.Target.ThreadID != threadID {
				return fmt.Errorf("origin returned message outside requested history")
			}
			known, err := w.sink.GetFollowCheckpoint(ctx, knownMessageKey(key, message.ID))
			if err != nil {
				return err
			}
			if !initial && known.Initialized && known.Cursor == "suppressed" {
				continue
			}
			added, err := w.sink.ImportFollowMessage(ctx, w.config, networkID, message)
			if err != nil {
				return err
			}
			if added {
				checkpoint.Count++
			}
			if err = w.sink.SetFollowCheckpoint(ctx, knownMessageKey(key, message.ID), Checkpoint{Initialized: true, Cursor: "imported"}); err != nil {
				return err
			}
			if checkpoint.Cursor == "" || message.CreatedAt.After(checkpoint.LatestAt) || message.CreatedAt.Equal(checkpoint.LatestAt) && message.ID > checkpoint.Cursor {
				checkpoint.Cursor = message.ID
				checkpoint.LatestAt = message.CreatedAt
			}
			checkpoint.Initialized = true
			if err = w.sink.SetFollowCheckpoint(ctx, key, checkpoint); err != nil {
				return err
			}
		}
		return nil
	}
	initialAfter := ""
	if checkpoint.Cursor == "" {
		remaining := backfill
		if checkpoint.Initialized {
			remaining = math.MaxInt
		}
		if remaining == 0 {
			page, err := read("", "", 1)
			if err != nil {
				return 0, err
			}
			if len(page.Messages) > 0 {
				checkpoint.Cursor = page.Messages[len(page.Messages)-1].ID
				checkpoint.LatestAt = page.Messages[len(page.Messages)-1].CreatedAt
			}
			checkpoint.Initialized = true
			if err = w.sink.SetFollowCheckpoint(ctx, key, checkpoint); err != nil {
				return 0, err
			}
			return checkpoint.Count, nil
		}
		before := ""
		for {
			limit := min(remaining, pageSize)
			page, err := read(before, "", limit)
			if err != nil {
				return 0, err
			}
			remaining -= len(page.Messages)
			if remaining == 0 || !page.Page.HasMore || len(page.Messages) == 0 {
				if err = ingest(page.Messages); err != nil {
					return 0, err
				}
				if len(page.Messages) > 0 {
					initialAfter = page.Messages[len(page.Messages)-1].ID
				}
				if len(page.Messages) == 0 {
					checkpoint.Initialized = true
					if err = w.sink.SetFollowCheckpoint(ctx, key, checkpoint); err != nil {
						return 0, err
					}
					return checkpoint.Count, nil
				}
				break
			}
			if page.Page.NextBefore == "" || page.Page.NextBefore == before {
				return 0, fmt.Errorf("origin returned non-advancing history cursor")
			}
			before = page.Page.NextBefore
		}
	}
	after := checkpoint.Cursor
	if initialAfter != "" {
		after = initialAfter
	}
	if !initial && !checkpoint.LatestAt.IsZero() {
		anchor, seed, err := overlapStart(checkpoint, read)
		if err != nil {
			return 0, err
		}
		if len(seed) > 0 {
			if err = ingest(seed); err != nil {
				return 0, err
			}
			after = seed[len(seed)-1].ID
		} else if anchor != "" {
			after = anchor
		} else {
			after = checkpoint.Cursor
		}
	}
	for {
		page, err := read("", after, pageSize)
		if err != nil {
			return 0, err
		}
		if err = ingest(page.Messages); err != nil {
			return 0, err
		}
		if !page.Page.HasMore {
			break
		}
		if len(page.Messages) == 0 || page.Messages[len(page.Messages)-1].ID == after {
			return 0, fmt.Errorf("origin returned non-advancing history cursor")
		}
		after = page.Messages[len(page.Messages)-1].ID
	}
	return checkpoint.Count, nil
}

func (w *worker) historyKey(networkID, roomID, threadID string) string {
	return w.config.ID + "/" + w.config.RemoteBaseURL + "/" + networkID + "/room/" + roomID + "/thread/" + threadID
}
