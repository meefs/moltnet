package follow

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

type checkpointSink struct{ values map[string]Checkpoint }

func (s *checkpointSink) EnsureFollowRoom(context.Context, protocol.Follow, protocol.Room) error {
	return nil
}
func (s *checkpointSink) ImportFollowMessage(context.Context, protocol.Follow, string, protocol.Message) (bool, error) {
	return true, nil
}
func (s *checkpointSink) GetFollowCheckpoint(_ context.Context, key string) (Checkpoint, error) {
	return s.values[key], nil
}
func (s *checkpointSink) SetFollowCheckpoint(_ context.Context, key string, value Checkpoint) error {
	s.values[key] = value
	return nil
}

func TestOverlapTraversesLargeTimestampGroup(t *testing.T) {
	latest := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	messages := []protocol.Message{{ID: "older", CreatedAt: latest.Add(-time.Second)}}
	for i := 0; i < 301; i++ {
		messages = append(messages, protocol.Message{ID: fmt.Sprintf("same_%03d", i), CreatedAt: latest})
	}
	requests := 0
	read := func(before, after string, limit int) (protocol.MessagePage, error) {
		requests++
		if limit > pageSize {
			t.Fatalf("unbounded page size %d", limit)
		}
		if after != "" {
			t.Fatal("backward helper requested after cursor")
		}
		end := len(messages)
		if before != "" {
			for i, message := range messages {
				if message.ID == before {
					end = i
					break
				}
			}
		}
		start := max(0, end-limit)
		page := protocol.MessagePage{Messages: messages[start:end], Page: protocol.PageInfo{HasMore: start > 0}}
		if start > 0 && start < end {
			page.Page.NextBefore = messages[start].ID
		}
		return page, nil
	}
	sink := &checkpointSink{values: map[string]Checkpoint{}}
	worker := &worker{sink: sink}
	if err := worker.suppressBaseline(t.Context(), "stream", read); err != nil {
		t.Fatal(err)
	}
	if len(sink.values) != 301 {
		t.Fatalf("baseline contains %d IDs, want301", len(sink.values))
	}
	if _, exists := sink.values[knownMessageKey("stream", "older")]; exists {
		t.Fatal("older timestamp suppressed")
	}
	anchor, seed, err := overlapStart(Checkpoint{Cursor: "same_300", LatestAt: latest}, read)
	if err != nil || anchor != "older" || len(seed) != 0 {
		t.Fatalf("overlap anchor %q seed%d error%v", anchor, len(seed), err)
	}
	if requests < 5 {
		t.Fatalf("large group was not paginated: %d requests", requests)
	}
}
