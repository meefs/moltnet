package follow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

func (s *checkpointSink) SetFollowCheckpoints(_ context.Context, values map[string]Checkpoint, prefixes []string) error {
	for key := range s.values {
		for _, prefix := range prefixes {
			if strings.HasPrefix(key, prefix) {
				delete(s.values, key)
				break
			}
		}
	}
	for key, value := range values {
		s.values[key] = value
	}
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
	var excluded map[string]bool
	if err := json.Unmarshal([]byte(sink.values["stream/baseline"].Cursor), &excluded); err != nil {
		t.Fatal(err)
	}
	if len(excluded) != 301 || len(sink.values) != 1 {
		t.Fatalf("baseline has %d IDs in %d records", len(excluded), len(sink.values))
	}
	if excluded["older"] {
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
