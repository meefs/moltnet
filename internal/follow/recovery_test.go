package follow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

type recoverySink struct {
	checkpointSink
	imported map[string]bool
}

func (s *recoverySink) ImportFollowMessage(_ context.Context, _ protocol.Follow, _ string, message protocol.Message) (bool, error) {
	added := !s.imported[message.ID]
	s.imported[message.ID] = true
	return added, nil
}

func TestInterruptedBackfillPreservesItsWindow(t *testing.T) {
	for _, equalTime := range []bool{false, true} {
		t.Run(fmt.Sprint(equalTime), func(t *testing.T) {
			messages := make([]protocol.Message, 1000)
			for i := range messages {
				stamp := int64(i + 1)
				if equalTime {
					stamp = 1
				}
				messages[i] = protocol.Message{ID: fmt.Sprintf("msg_%04d", i), CreatedAt: time.Unix(stamp, 0), Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "public"}}
			}
			failForward := true
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				after, before := query.Get("after"), query.Get("before")
				if after != "" && failForward {
					failForward = false
					http.Error(w, "interrupted backfill", http.StatusServiceUnavailable)
					return
				}
				start, end := 0, len(messages)
				for i, message := range messages {
					if message.ID == after {
						start = i + 1
					}
					if message.ID == before {
						end = i
					}
				}
				limit, _ := strconv.Atoi(query.Get("limit"))
				page := protocol.MessagePage{}
				if after != "" {
					end = min(end, start+limit)
					page.Page.HasMore = end < len(messages)
				} else {
					start = max(0, end-limit)
					page.Page.HasMore = start > 0
					if start > 0 {
						page.Page.NextBefore = messages[start].ID
					}
				}
				page.Messages = messages[start:end]
				json.NewEncoder(w).Encode(page)
			}))
			defer server.Close()
			sink := &recoverySink{checkpointSink: checkpointSink{values: map[string]Checkpoint{}}, imported: map[string]bool{}}
			worker := &worker{config: protocol.Follow{ID: "news", RemoteBaseURL: server.URL}.WithDefaults(), client: newHTTPClient(), sink: sink}
			defer worker.client.CloseIdleConnections()
			if _, err := worker.syncHistory(t.Context(), "origin", "public", "", 500); err == nil {
				t.Fatal("failure was not injected")
			}
			if sink.imported["msg_0999"] || len(sink.imported) == 0 {
				t.Fatal("failure did not interrupt a partially imported window")
			}
			count, err := worker.syncHistory(t.Context(), "origin", "public", "", 500)
			if err != nil || count != 500 || len(sink.imported) != 500 || !sink.imported["msg_0999"] || sink.imported["msg_0499"] {
				t.Fatalf("recovery lost backfill window: count=%d imported=%d newest=%v skipped=%v error=%v", count, len(sink.imported), sink.imported["msg_0999"], sink.imported["msg_0499"], err)
			}
		})
	}
}
