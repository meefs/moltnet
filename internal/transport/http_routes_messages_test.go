package transport

import (
	"github.com/noopolis/moltnet/pkg/protocol"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testHTTPMessageRoutes(t *testing.T, service *fakeService, handler http.Handler) {
	t.Run("room messages", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/rooms/research/messages?limit=12&before=msg_room_older", nil)
		handler.ServeHTTP(response, request)

		if service.roomID != "research" || service.roomBefore != "msg_room_older" || service.limit != 12 {
			t.Fatalf("unexpected room lookup %q before %q limit %d", service.roomID, service.roomBefore, service.limit)
		}
		if !strings.Contains(response.Body.String(), "msg_room") {
			t.Fatalf("unexpected body %s", response.Body.String())
		}
		if !strings.Contains(response.Body.String(), "\"page\"") {
			t.Fatalf("expected page metadata in body %s", response.Body.String())
		}
	})

	t.Run("threads list", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/rooms/research/threads?limit=2&after=thread_prev", nil)
		handler.ServeHTTP(response, request)

		if service.roomID != "research" || service.threadAfter != "thread_prev" || service.limit != 2 {
			t.Fatalf("unexpected thread lookup %q after %q limit %d", service.roomID, service.threadAfter, service.limit)
		}
		if !strings.Contains(response.Body.String(), "thread_1") {
			t.Fatalf("unexpected body %s", response.Body.String())
		}
		if !strings.Contains(response.Body.String(), "\"page\"") {
			t.Fatalf("expected page metadata in body %s", response.Body.String())
		}
	})

	t.Run("thread messages", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/threads/thread_1/messages?limit=4&before=msg_thread_prev", nil)
		handler.ServeHTTP(response, request)

		if service.threadID != "thread_1" || service.threadBefore != "msg_thread_prev" || service.limit != 4 {
			t.Fatalf("unexpected thread lookup %q before %q limit %d", service.threadID, service.threadBefore, service.limit)
		}
		if !strings.Contains(response.Body.String(), "msg_thread") {
			t.Fatalf("unexpected body %s", response.Body.String())
		}
	})

	t.Run("thread get", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/threads/thread_1", nil)
		handler.ServeHTTP(response, request)

		if !strings.Contains(response.Body.String(), `"id":"thread_1"`) {
			t.Fatalf("unexpected body %s", response.Body.String())
		}
	})

	t.Run("dms list", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/dms?limit=2&after=dm_prev", nil)
		handler.ServeHTTP(response, request)

		if service.dmAfter != "dm_prev" || service.limit != 2 {
			t.Fatalf("unexpected dm list after %q limit %d", service.dmAfter, service.limit)
		}
		if !strings.Contains(response.Body.String(), "dm_1") {
			t.Fatalf("unexpected body %s", response.Body.String())
		}
		if !strings.Contains(response.Body.String(), "\"page\"") {
			t.Fatalf("expected page metadata in body %s", response.Body.String())
		}
	})

	t.Run("dm get", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/dms/dm_1", nil)
		handler.ServeHTTP(response, request)

		if !strings.Contains(response.Body.String(), `"id":"dm_1"`) {
			t.Fatalf("unexpected body %s", response.Body.String())
		}
	})

	t.Run("dm messages", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/dms/dm_1/messages?limit=20&before=msg_dm_older", nil)
		handler.ServeHTTP(response, request)

		if service.dmID != "dm_1" || service.dmBefore != "msg_dm_older" || service.limit != 20 {
			t.Fatalf("unexpected dm lookup %q before %q limit %d", service.dmID, service.dmBefore, service.limit)
		}
		if !strings.Contains(response.Body.String(), "msg_dm") {
			t.Fatalf("unexpected body %s", response.Body.String())
		}
	})

	t.Run("artifacts", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/artifacts?thread_id=thread_1&before=art_prev&limit=10", nil)
		handler.ServeHTTP(response, request)

		if service.threadID != "thread_1" || service.roomBefore != "art_prev" || service.limit != 10 {
			t.Fatalf("unexpected artifacts lookup thread=%q before=%q limit=%d", service.threadID, service.roomBefore, service.limit)
		}
		if !strings.Contains(response.Body.String(), "art_1") {
			t.Fatalf("unexpected body %s", response.Body.String())
		}
	})

	t.Run("send message", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{
			"target":{"kind":"room","room_id":"research"},
			"from":{"type":"agent","id":"orchestrator"},
			"parts":[{"kind":"text","text":"hello"}]
		}`))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusAccepted {
			t.Fatalf("unexpected status %d", response.Code)
		}

		if service.sentMessage.Target.RoomID != "research" {
			t.Fatalf("unexpected sent message %#v", service.sentMessage)
		}
	})

	t.Run("report wake failed", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/agents/wake-failed", strings.NewReader(`{
			"agent_id":"orchestrator",
			"event":{"id":"evt_1","type":"message.created","network_id":"local","message":{"id":"msg_1"}},
			"error":"control url returned 400 Bad Request",
			"attempts":5,
			"classification":"permanent"
		}`))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusAccepted {
			t.Fatalf("unexpected status %d, body %s", response.Code, response.Body.String())
		}
		if len(service.failedWakes) != 1 || service.failedWakes[0].ID != "evt_1" {
			t.Fatalf("unexpected recorded wake failure %#v", service.failedWakes)
		}
		if len(service.failedWakeAgents) != 1 || service.failedWakeAgents[0].ID != "orchestrator" {
			t.Fatalf("unexpected wake failure agent %#v", service.failedWakeAgents)
		}
		if len(service.failedWakeDetails) != 1 ||
			service.failedWakeDetails[0].Attempts != 5 ||
			service.failedWakeDetails[0].Classification != protocol.WakeFailureClassificationPermanent {
			t.Fatalf("unexpected wake failure details %#v", service.failedWakeDetails)
		}
	})

	t.Run("stream events", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/events/stream", nil)
		handler.ServeHTTP(response, request)

		body := response.Body.String()
		if !strings.Contains(body, ": stream-open") || !strings.Contains(body, "id: evt_1") || !strings.Contains(body, "event: message.created") || !strings.Contains(body, "\"id\":\"evt_1\"") {
			t.Fatalf("unexpected stream body %s", body)
		}
	})

	t.Run("stream replay from last event id", func(t *testing.T) {
		replay := make(chan protocol.Event, 1)
		replay <- protocol.Event{
			ID:   "evt_2",
			Type: protocol.EventTypeMessageCreated,
			Message: &protocol.Message{
				ID: "msg_2",
			},
		}
		close(replay)
		service.replayStream = replay
		defer func() { service.replayStream = nil }()

		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/events/stream", nil)
		request.Header.Set("Last-Event-ID", "evt_1")
		handler.ServeHTTP(response, request)

		body := response.Body.String()
		if !strings.Contains(body, "id: evt_2") || !strings.Contains(body, "\"id\":\"evt_2\"") {
			t.Fatalf("unexpected replay stream body %s", body)
		}
	})

	t.Run("stream skips unsafe event type", func(t *testing.T) {
		stream := make(chan protocol.Event, 1)
		stream <- protocol.Event{ID: "evt_bad", Type: "bad\nevent"}
		close(stream)
		service.stream = stream

		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/events/stream", nil)
		handler.ServeHTTP(response, request)

		body := response.Body.String()
		if strings.Contains(body, "event: bad") || strings.Contains(body, "id: evt_bad") {
			t.Fatalf("expected unsafe event to be skipped, got %s", body)
		}
	})

	t.Run("console redirects and serves", func(t *testing.T) {
		redirect := httptest.NewRecorder()
		redirectRequest := httptest.NewRequest(http.MethodGet, "/", nil)
		handler.ServeHTTP(redirect, redirectRequest)
		if redirect.Code != http.StatusTemporaryRedirect {
			t.Fatalf("unexpected redirect status %d", redirect.Code)
		}
		if location := redirect.Header().Get("Location"); location != "/console/" {
			t.Fatalf("unexpected redirect location %q", location)
		}

		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/console/", nil)
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("unexpected console status %d", response.Code)
		}
		if !strings.Contains(response.Body.String(), "Moltnet Console") {
			t.Fatalf("unexpected console body %s", response.Body.String())
		}
	})

	t.Run("metrics", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("unexpected metrics status %d", response.Code)
		}
		if !strings.Contains(response.Body.String(), "moltnet_http_requests_total") {
			t.Fatalf("unexpected metrics body %s", response.Body.String())
		}
	})
}
