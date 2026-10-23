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
	sessionKey := w.config.ID + "/" + w.config.RemoteBaseURL + "/" + networkID + "/initialized"
	initialized, err := w.sink.GetFollowCheckpoint(ctx, sessionKey)
	if err != nil {
		return err
	}
	backfill := w.config.BackfillLimit()
	if initialized.Initialized {
		backfill = math.MaxInt
	}
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
			count, err := w.syncHistory(ctx, networkID, room.ID, "", backfill)
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
					n, err := w.syncHistory(ctx, networkID, room.ID, thread.ID, backfill)
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
	if err := w.sink.SetFollowCheckpoint(ctx, sessionKey, Checkpoint{Initialized: true}); err != nil {
		return err
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
	key := w.config.ID + "/" + w.config.RemoteBaseURL + "/" + networkID + "/room/" + roomID + "/thread/" + threadID
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
	ingest := func(messages []protocol.Message) error {
		for _, message := range messages {
			if message.Target.RoomID != roomID || threadID != "" && message.Target.ThreadID != threadID {
				return fmt.Errorf("origin returned message outside requested history")
			}
			added, err := w.sink.ImportFollowMessage(ctx, w.config, networkID, message)
			if err != nil {
				return err
			}
			if added {
				checkpoint.Count++
			}
			checkpoint.Cursor = message.ID
			checkpoint.Initialized = true
			if err = w.sink.SetFollowCheckpoint(ctx, key, checkpoint); err != nil {
				return err
			}
		}
		return nil
	}
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
	for {
		page, err := read("", checkpoint.Cursor, pageSize)
		if err != nil {
			return 0, err
		}
		previous := checkpoint.Cursor
		if err = ingest(page.Messages); err != nil {
			return 0, err
		}
		if !page.Page.HasMore {
			break
		}
		if checkpoint.Cursor == previous {
			return 0, fmt.Errorf("origin returned non-advancing history cursor")
		}
	}
	return checkpoint.Count, nil
}
