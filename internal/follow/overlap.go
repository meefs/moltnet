package follow

import (
	"context"
	"fmt"

	"github.com/noopolis/moltnet/pkg/protocol"
)

type historyReader func(before, after string, limit int) (protocol.MessagePage, error)

func knownMessageKey(key, id string) string { return key + "/baseline/" + id }

// suppressBaseline records the existing newest timestamp group. A later insert
// can sort anywhere within that group; the durable IDs distinguish it from
// history deliberately excluded by the configured backfill limit.
func (w *worker) suppressBaseline(ctx context.Context, key string, read historyReader) error {
	newest, err := read("", "", 1)
	if err != nil {
		return err
	}
	if len(newest.Messages) == 0 {
		return nil
	}
	latest := newest.Messages[len(newest.Messages)-1]
	mark := func(message protocol.Message) error {
		return w.sink.SetFollowCheckpoint(ctx, knownMessageKey(key, message.ID), Checkpoint{Initialized: true, Cursor: "suppressed"})
	}
	if err = mark(latest); err != nil {
		return err
	}
	before := latest.ID
	for {
		page, err := read(before, "", pageSize)
		if err != nil {
			return err
		}
		for i := len(page.Messages) - 1; i >= 0; i-- {
			message := page.Messages[i]
			if !message.CreatedAt.Equal(latest.CreatedAt) {
				return nil
			}
			if err = mark(message); err != nil {
				return err
			}
		}
		if !page.Page.HasMore {
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
