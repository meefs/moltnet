package follow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

type countingSink struct {
	checkpointSink
	batches, singles, imports int
}

func (s *countingSink) SetFollowCheckpoint(ctx context.Context, key string, value Checkpoint) error {
	s.singles++
	return s.checkpointSink.SetFollowCheckpoint(ctx, key, value)
}
func (s *countingSink) SetFollowCheckpoints(ctx context.Context, values map[string]Checkpoint, prefixes []string) error {
	s.batches++
	return s.checkpointSink.SetFollowCheckpoints(ctx, values, prefixes)
}
func (s *countingSink) ImportFollowMessage(context.Context, protocol.Follow, string, protocol.Message) (bool, error) {
	s.imports++
	return true, nil
}

func TestHistoryBatchesCheckpointsAndPrunesBaseline(t *testing.T) {
	messages := make([]protocol.Message, 100)
	for i := range messages {
		messages[i] = protocol.Message{ID: fmt.Sprintf("msg_%03d", i), CreatedAt: time.Unix(100, 0), Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "public"}}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		end := len(messages)
		before := r.URL.Query().Get("before")
		after := r.URL.Query().Get("after")
		if before != "" {
			for i, msg := range messages {
				if msg.ID == before {
					end = i
					break
				}
			}
		}
		start := 0
		if after != "" {
			for i, msg := range messages {
				if msg.ID == after {
					start = i + 1
					break
				}
			}
		}
		if r.URL.Query().Get("limit") == "1" && start < end {
			start = end - 1
		}
		json.NewEncoder(w).Encode(protocol.MessagePage{Messages: messages[start:end]})
	}))
	defer server.Close()
	sink := &countingSink{checkpointSink: checkpointSink{values: map[string]Checkpoint{}}}
	worker := &worker{config: protocol.Follow{ID: "news", RemoteBaseURL: server.URL}.WithDefaults(), client: newHTTPClient(), sink: sink}
	defer worker.client.CloseIdleConnections()
	count, err := worker.syncHistory(t.Context(), "origin", "public", "", 100)
	if err != nil || count != 100 || sink.imports != 100 {
		t.Fatalf("history count %d imports %d error %v", count, sink.imports, err)
	}
	if sink.singles != 0 || sink.batches > 4 {
		t.Fatalf("per-message checkpoint writes: %d singles, %d batches", sink.singles, sink.batches)
	}
	if len(sink.values) != 1 {
		t.Fatalf("baseline records leaked: %+v", sink.values)
	}
}
