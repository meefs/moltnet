package follow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/noopolis/moltnet/pkg/protocol"
)

type historyReader func(before, after string, limit int) (protocol.MessagePage, error)

// suppressBaseline snapshots the existing newest timestamp group. Excluded
// IDs share one durable record, so old imported markers need not accumulate.
func (w *worker) suppressBaseline(ctx context.Context, key string, read historyReader) error {
	baselineKey := key + "/baseline"
	baseline, err := w.sink.GetFollowCheckpoint(ctx, baselineKey)
	if err != nil {
		return err
	}
	if baseline.Initialized {
		return nil
	}
	newest, err := read("", "", 1)
	if err != nil {
		return err
	}
	if len(newest.Messages) == 0 {
		return nil
	}
	latest := newest.Messages[len(newest.Messages)-1]
	excluded := map[string]bool{latest.ID: true}
	save := func(complete bool) error {
		encoded, err := json.Marshal(excluded)
		if err != nil {
			return err
		}
		return w.sink.SetFollowCheckpoints(ctx, map[string]Checkpoint{baselineKey: {Initialized: complete, Cursor: string(encoded), LatestAt: latest.CreatedAt}}, []string{baselineKey + "/"})
	}
	before := latest.ID
	for {
		page, err := read(before, "", pageSize)
		if err != nil {
			return err
		}
		complete := !page.Page.HasMore
		for i := len(page.Messages) - 1; i >= 0; i-- {
			message := page.Messages[i]
			if !message.CreatedAt.Equal(latest.CreatedAt) {
				complete = true
				break
			}
			excluded[message.ID] = true
		}
		if err = save(complete); err != nil {
			return err
		}
		if complete {
			return nil
		}
		if page.Page.NextBefore == "" || page.Page.NextBefore == before {
			return fmt.Errorf("origin returned non-advancing baseline cursor")
		}
		before = page.Page.NextBefore
	}
}

// overlapStart finds an anchor strictly older than the durable high watermark.
// Reading forward from it revisits every equal-time ID, including IDs inserted
// below the previous cursor. Each page is discarded before fetching another.
func overlapStart(checkpoint Checkpoint, read historyReader) (string, []protocol.Message, error) {
	before := checkpoint.Cursor
	for {
		page, err := read(before, "", pageSize)
		if err != nil {
			return "", nil, err
		}
		for i := len(page.Messages) - 1; i >= 0; i-- {
			if page.Messages[i].CreatedAt.Before(checkpoint.LatestAt) {
				return page.Messages[i].ID, nil, nil
			}
		}
		if !page.Page.HasMore {
			return "", page.Messages, nil
		}
		if page.Page.NextBefore == "" || page.Page.NextBefore == before {
			return "", nil, fmt.Errorf("origin returned non-advancing overlap cursor")
		}
		before = page.Page.NextBefore
	}
}
