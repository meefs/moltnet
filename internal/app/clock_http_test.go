package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/bridgeconfig"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestClockHTTPDomainTimestamps(t *testing.T) {
	t.Setenv("NOOPOLIS_RUN_ID", "run-clock")
	for _, clocked := range []bool{true, false} {
		name := "real"
		if clocked {
			name = "task"
		}
		t.Run(name, func(t *testing.T) {
			real := time.Date(2026, 10, 10, 8, 0, 1, 250123456, time.UTC)
			clock := &bridgeconfig.ClockConfig{Version: bridgeconfig.TaskClockVersion, Origin: "2025-10-01T08:00:00Z", AnchorEpochMS: real.Add(-1250 * time.Millisecond).UnixMilli()}
			want := time.Date(2025, 10, 1, 8, 0, 1, 250000000, time.UTC)
			if !clocked {
				clock = nil
				want = real
			}
			causalPath := filepath.Join(t.TempDir(), "causal.jsonl")
			config := Config{NetworkID: "local", Clock: clock, CausalEventsPath: causalPath}
			app, err := newWithRealClock(config, func() time.Time { return real })
			if err != nil {
				t.Fatal(err)
			}
			defer app.Close()
			handler := app.Handler()
			var room protocol.Room
			clockRequest(t, handler, "POST", "/v1/rooms", protocol.CreateRoomRequest{ID: "research", Members: []string{"writer", "reader"}}, &room, http.StatusCreated)
			assertClockTime(t, "room", room.CreatedAt, want)
			var registration protocol.AgentRegistration
			clockRequest(t, handler, "POST", "/v1/agents/register", protocol.RegisterAgentRequest{RequestedAgentID: "writer"}, &registration, http.StatusCreated)
			assertClockTime(t, "registration created", registration.CreatedAt, want)
			assertClockTime(t, "registration updated", registration.UpdatedAt, want)
			var last protocol.MessageAccepted
			for _, target := range []protocol.Target{
				{Kind: protocol.TargetKindRoom, RoomID: "research"},
				{Kind: protocol.TargetKindThread, RoomID: "research", ThreadID: "thread", ParentMessageID: "msg-room"},
				{Kind: protocol.TargetKindDM, DMID: "dm", ParticipantIDs: []string{"writer", "reader"}},
			} {
				clockRequest(t, handler, "POST", "/v1/messages", protocol.SendMessageRequest{ID: "msg-" + target.Kind, Target: target, From: protocol.Actor{Type: "agent", ID: "writer"}, Parts: []protocol.Part{{Kind: protocol.PartKindText, Text: "hello"}}}, &last, http.StatusAccepted)
			}
			for _, endpoint := range []string{"/v1/rooms/research/messages", "/v1/threads/thread/messages", "/v1/dms/dm/messages"} {
				var page protocol.MessagePage
				clockRequest(t, handler, "GET", endpoint, nil, &page, http.StatusOK)
				if len(page.Messages) != 1 {
					t.Fatalf("%s: %#v", endpoint, page)
				}
				assertClockTime(t, endpoint, page.Messages[0].CreatedAt, want)
			}
			var thread protocol.Thread
			clockRequest(t, handler, "GET", "/v1/threads/thread", nil, &thread, http.StatusOK)
			assertClockTime(t, "thread updated", thread.LastMessageAt, want)
			var dm protocol.DirectConversation
			clockRequest(t, handler, "GET", "/v1/dms/dm", nil, &dm, http.StatusOK)
			assertClockTime(t, "DM updated", dm.LastMessageAt, want)
			// The real HTTP SSE handler replays the stamped events and synthesizes a
			// gap through the same broker. No listener or special test service needed.
			events := clockHTTPReplay(t, handler, last.EventID)
			if len(events) != 7 {
				t.Fatalf("events: got %d, want gap + room + 3 messages + thread + DM", len(events))
			}
			for _, event := range events {
				assertClockTime(t, event.Type, event.CreatedAt, want)
				if event.Message != nil {
					assertClockTime(t, "event.message", event.Message.CreatedAt, want)
				}
			}
			// A later registration update retains its recorded creation instant.
			real = real.Add(2 * time.Second)
			clockRequest(t, handler, "POST", "/v1/agents/register", protocol.RegisterAgentRequest{RequestedAgentID: "writer", Name: "renamed"}, &registration, http.StatusOK)
			assertClockTime(t, "registration creation retained", registration.CreatedAt, want)
			assertClockTime(t, "registration update advanced", registration.UpdatedAt, want.Add(2*time.Second))
			data, err := os.ReadFile(causalPath)
			if err != nil {
				t.Fatal(err)
			}
			lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
			if len(lines) != 3 {
				t.Fatalf("causal events: %s", data)
			}
			for _, line := range lines {
				var event protocol.CausalEvent
				if err := json.Unmarshal(line, &event); err != nil {
					t.Fatal(err)
				}
				assertClockTime(t, "causal", event.RecordedAt, want)
			}
		})
	}
}

func TestClockHTTPDefaultUsesRealNow(t *testing.T) {
	before := time.Now()
	app, err := New(Config{NetworkID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	var registration protocol.AgentRegistration
	clockRequest(t, app.Handler(), "POST", "/v1/agents/register", protocol.RegisterAgentRequest{RequestedAgentID: "writer"}, &registration, http.StatusCreated)
	if registration.CreatedAt.Before(before) || registration.CreatedAt.After(time.Now()) {
		t.Fatalf("default registration timestamp %s is not real now", registration.CreatedAt)
	}
	var room protocol.Room
	clockRequest(t, app.Handler(), "POST", "/v1/rooms", protocol.CreateRoomRequest{ID: "r", Members: []string{"writer"}}, &room, http.StatusCreated)
	clockRequest(t, app.Handler(), "POST", "/v1/messages", protocol.SendMessageRequest{Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "r"}, From: protocol.Actor{Type: "agent", ID: "writer"}, Parts: []protocol.Part{{Kind: protocol.PartKindText, Text: "hello"}}}, nil, http.StatusAccepted)
	var page protocol.MessagePage
	clockRequest(t, app.Handler(), "GET", "/v1/rooms/r/messages", nil, &page, http.StatusOK)
	if len(page.Messages) != 1 || page.Messages[0].CreatedAt.Before(before) || page.Messages[0].CreatedAt.After(time.Now()) {
		t.Fatalf("default message timestamp: %#v", page)
	}
}

func clockRequest(t *testing.T, handler http.Handler, method, path string, body, out any, status int) {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != status {
		t.Fatalf("%s %s: %d want %d: %s", method, path, response.Code, status, response.Body)
	}
	if out != nil {
		if err := json.Unmarshal(response.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
}

func assertClockTime(t *testing.T, label string, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("%s: got %s, want %s", label, got, want)
	}
}

type clockReplayRecorder struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
	lastID string
}

func (r *clockReplayRecorder) Flush() {
	r.ResponseRecorder.Flush()
	if strings.Contains(r.Body.String(), "id: "+r.lastID+"\n") {
		r.cancel()
	}
}

func clockHTTPReplay(t *testing.T, handler http.Handler, lastID string) []protocol.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request := httptest.NewRequest("GET", "/v1/events/stream", nil).WithContext(ctx)
	request.Header.Set("Last-Event-ID", "missing-before-trial")
	response := &clockReplayRecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel, lastID: lastID}
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("SSE status %d: %s", response.Code, response.Body)
	}
	var events []protocol.Event
	for _, line := range strings.Split(response.Body.String(), "\n") {
		if strings.HasPrefix(line, "data: ") {
			var event protocol.Event
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
				t.Fatal(err)
			}
			events = append(events, event)
		}
	}
	if len(events) == 0 || events[len(events)-1].ID != lastID {
		t.Fatalf("incomplete SSE replay: %s", response.Body)
	}
	return events
}
