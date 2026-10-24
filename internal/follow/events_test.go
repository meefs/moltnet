package follow

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func await(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("follow did not reach expected state")
}

func TestSSEScansOnlyEventTargetAndCoalescesBurst(t *testing.T) {
	for _, size := range []int{1, 25} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			events := make(chan protocol.Event, 100)
			var active atomic.Bool
			var mu sync.Mutex
			paths := []string{}
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := r.URL.Path
				if active.Load() {
					mu.Lock()
					paths = append(paths, path)
					mu.Unlock()
				}
				switch {
				case path == "/v1/network":
					json.NewEncoder(w).Encode(testNetwork())
				case path == "/v1/events/stream":
					w.Header().Set("Content-Type", "text/event-stream")
					w.(http.Flusher).Flush()
					for {
						select {
						case <-r.Context().Done():
							return
						case event := <-events:
							data, _ := json.Marshal(event)
							fmt.Fprintf(w, "data: %s\n\n", data)
							w.(http.Flusher).Flush()
						}
					}
				case path == "/v1/rooms":
					page := protocol.RoomPage{}
					for i := 0; i < size; i++ {
						page.Rooms = append(page.Rooms, protocol.Room{ID: fmt.Sprintf("room%d", i), Visibility: "public"})
					}
					json.NewEncoder(w).Encode(page)
				case strings.HasSuffix(path, "/threads"):
					page := protocol.ThreadPage{}
					for i := 0; i < size; i++ {
						page.Threads = append(page.Threads, protocol.Thread{ID: fmt.Sprintf("thread%d", i)})
					}
					json.NewEncoder(w).Encode(page)
				case strings.HasSuffix(path, "/messages"):
					json.NewEncoder(w).Encode(protocol.MessagePage{})
				default:
					json.NewEncoder(w).Encode(protocol.Room{ID: "room0", Visibility: "public"})
				}
			}))
			defer origin.Close()
			manager := New(&checkpointSink{values: map[string]Checkpoint{}})
			manager.Apply(t.Context(), []protocol.Follow{{ID: "news", RemoteBaseURL: origin.URL, PollIntervalSeconds: 3600}})
			defer manager.Close()
			await(t, func() bool { return manager.Status()[0].Status == "live" })
			active.Store(true)
			for i := 0; i < 20; i++ {
				events <- protocol.Event{Message: &protocol.Message{Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "room0"}}}
				events <- protocol.Event{Message: &protocol.Message{Target: protocol.Target{Kind: protocol.TargetKindThread, RoomID: "room0", ThreadID: "thread0"}}}
			}
			await(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(paths) >= 4 })
			time.Sleep(150 * time.Millisecond)
			manager.Close()
			mu.Lock()
			defer mu.Unlock()
			if len(paths) != 4 {
				t.Fatalf("%d rooms/threads produced %d requests: %v", size, len(paths), paths)
			}
			for _, path := range paths {
				if path != "/v1/rooms/room0" && path != "/v1/rooms/room0/messages" && path != "/v1/threads/thread0/messages" {
					t.Fatalf("unrelated scan %s", path)
				}
			}
		})
	}
}

func TestPendingScansAreBounded(t *testing.T) {
	pending := newPendingScans()
	for i := 0; i < maxPendingScans*2; i++ {
		pending.add(scanTarget{room: fmt.Sprint(i)})
	}
	if len(pending.take()) != maxPendingScans {
		t.Fatal("pending scans exceeded bound")
	}
	if len(pending.take()) != 0 {
		t.Fatal("pending scans not drained")
	}
}
