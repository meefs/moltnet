package follow

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"

	"github.com/noopolis/moltnet/pkg/protocol"
)

const pageSize = 200

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
	baselineKey := key + "/baseline"
	baseline, err := w.sink.GetFollowCheckpoint(ctx, baselineKey)
	if err != nil {
		return 0, err
	}
	excluded := map[string]bool{}
	if baseline.Cursor != "" {
		if err = json.Unmarshal([]byte(baseline.Cursor), &excluded); err != nil {
			return 0, err
		}
	}
	save := func() error {
		values := map[string]Checkpoint{key: checkpoint}
		// Only exclusions in the current timestamp group are needed for overlap.
		// Collapse them into one record; drop imported/obsolete per-message keys.
		if len(excluded) > 0 && !checkpoint.LatestAt.After(baseline.LatestAt) {
			encoded, err := json.Marshal(excluded)
			if err != nil {
				return err
			}
			baseline.Cursor = string(encoded)
			values[baselineKey] = baseline
		}
		return w.sink.SetFollowCheckpoints(ctx, values, []string{baselineKey})
	}
	ingest := func(messages []protocol.Message) error {
		for _, message := range messages {
			if message.Target.RoomID != roomID || threadID != "" && message.Target.ThreadID != threadID {
				return fmt.Errorf("origin returned message outside requested history")
			}
			if !initial && excluded[message.ID] {
				continue
			}
			added, err := w.sink.ImportFollowMessage(ctx, w.config, networkID, message)
			if err != nil {
				return err
			}
			if added {
				checkpoint.Count++
			}
			delete(excluded, message.ID)
			if checkpoint.Cursor == "" || message.CreatedAt.After(checkpoint.LatestAt) || message.CreatedAt.Equal(checkpoint.LatestAt) && message.ID > checkpoint.Cursor {
				checkpoint.Cursor = message.ID
				checkpoint.LatestAt = message.CreatedAt
			}
			checkpoint.Initialized = true
		}
		return save()
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
			if err = save(); err != nil {
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
				if initial && len(page.Messages) > 0 {
					// The backward walk fixed the requested window. Only IDs
					// strictly before its first message were deliberately skipped.
					// Persist that boundary with the first imported page, so a
					// reconnect cannot suppress the remainder of the backfill.
					first := page.Messages[0]
					for id := range excluded {
						if first.CreatedAt.Before(baseline.LatestAt) || id >= first.ID {
							delete(excluded, id)
						}
					}
				}
				if err = ingest(page.Messages); err != nil {
					return 0, err
				}
				if len(page.Messages) > 0 {
					initialAfter = page.Messages[len(page.Messages)-1].ID
				}
				if len(page.Messages) == 0 {
					checkpoint.Initialized = true
					if err = save(); err != nil {
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
