package transport

import (
	"encoding/json"
	"github.com/noopolis/moltnet/pkg/protocol"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testHTTPAdminRoutes(t *testing.T, service *fakeService, handler http.Handler) {
	t.Run("healthz", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("unexpected status %d", response.Code)
		}
	})

	t.Run("readyz", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("unexpected status %d", response.Code)
		}
		if !strings.Contains(response.Body.String(), `"ready"`) {
			t.Fatalf("unexpected body %s", response.Body.String())
		}
	})

	t.Run("network", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/network", nil)
		handler.ServeHTTP(response, request)

		var payload protocol.Network
		if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.ID != "local" {
			t.Fatalf("unexpected network %#v", payload)
		}
		if payload.Capabilities.MessagePagination != "cursor" || payload.Capabilities.AttachmentProtocol != "websocket" {
			t.Fatalf("unexpected capabilities %#v", payload.Capabilities)
		}
	})

	t.Run("rooms list", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/rooms?limit=3&after=room_prev", nil)
		handler.ServeHTTP(response, request)

		if service.roomAfter != "room_prev" || service.limit != 3 {
			t.Fatalf("unexpected rooms list after %q limit %d", service.roomAfter, service.limit)
		}
		if !strings.Contains(response.Body.String(), "research") {
			t.Fatalf("unexpected body %s", response.Body.String())
		}
	})

	t.Run("room get", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/rooms/research", nil)
		handler.ServeHTTP(response, request)

		if !strings.Contains(response.Body.String(), `"id":"research"`) {
			t.Fatalf("unexpected body %s", response.Body.String())
		}
	})

	t.Run("agents list", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/agents?limit=4&after=agent_prev", nil)
		handler.ServeHTTP(response, request)

		if service.agentAfter != "agent_prev" || service.limit != 4 {
			t.Fatalf("unexpected agents list after %q limit %d", service.agentAfter, service.limit)
		}
		if !strings.Contains(response.Body.String(), "orchestrator") {
			t.Fatalf("unexpected body %s", response.Body.String())
		}
	})

	t.Run("agent get", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/agents/orchestrator", nil)
		handler.ServeHTTP(response, request)

		if !strings.Contains(response.Body.String(), `"id":"orchestrator"`) {
			t.Fatalf("unexpected body %s", response.Body.String())
		}
	})

	t.Run("pairings list", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/pairings?limit=2&after=pair_prev", nil)
		handler.ServeHTTP(response, request)

		if service.pairAfter != "pair_prev" || service.limit != 2 {
			t.Fatalf("unexpected pairings list after %q limit %d", service.pairAfter, service.limit)
		}
		if !strings.Contains(response.Body.String(), "Remote Lab") {
			t.Fatalf("unexpected body %s", response.Body.String())
		}
	})

	t.Run("pairings invalid cursor error", func(t *testing.T) {
		service.pairListErr = fakeStatusError{status: http.StatusUnprocessableEntity, msg: `invalid cursor "missing"`}
		defer func() { service.pairListErr = nil }()

		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/pairings?after=missing", nil)
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("unexpected invalid cursor status %d", response.Code)
		}
	})

	t.Run("pairing network", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/pairings/pair_1/network", nil)
		handler.ServeHTTP(response, request)

		if !strings.Contains(response.Body.String(), "\"id\":\"remote\"") {
			t.Fatalf("unexpected body %s", response.Body.String())
		}
	})

	t.Run("pairing rooms", func(t *testing.T) {
		service.pairRoomPage = protocol.RoomPage{
			Rooms: []protocol.Room{
				{ID: "remote-research", NetworkID: "remote", FQID: "molt://remote/rooms/remote-research", Name: "Remote Research"},
			},
			Page: protocol.PageInfo{HasMore: true, NextAfter: "remote-research"},
		}
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/pairings/pair_1/rooms?after=remote-seed&limit=2", nil)
		handler.ServeHTTP(response, request)

		if !strings.Contains(response.Body.String(), "remote-research") {
			t.Fatalf("unexpected body %s", response.Body.String())
		}
		if service.roomAfter != "remote-seed" || service.limit != 2 {
			t.Fatalf("unexpected room pagination capture after=%q limit=%d", service.roomAfter, service.limit)
		}
	})

	t.Run("pairing agents", func(t *testing.T) {
		service.pairAgentPage = protocol.AgentPage{
			Agents: []protocol.AgentSummary{
				{ID: "remote-writer", NetworkID: "remote", FQID: "molt://remote/agents/remote-writer"},
			},
			Page: protocol.PageInfo{HasMore: true, NextBefore: "remote-writer"},
		}
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/pairings/pair_1/agents?before=remote-zzz&limit=3", nil)
		handler.ServeHTTP(response, request)

		if !strings.Contains(response.Body.String(), "remote-writer") {
			t.Fatalf("unexpected body %s", response.Body.String())
		}
		if service.agentBefore != "remote-zzz" || service.limit != 3 {
			t.Fatalf("unexpected agent pagination capture before=%q limit=%d", service.agentBefore, service.limit)
		}
	})

	t.Run("pairing rooms invalid cursor", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/v1/pairings/pair_1/rooms?before=room_1&after=room_2", nil)
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("unexpected invalid cursor status %d", response.Code)
		}
	})

	t.Run("create room", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/rooms", strings.NewReader(`{"id":"review","name":"Review"}`))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusCreated {
			t.Fatalf("unexpected status %d", response.Code)
		}

		if service.createdRoom.ID != "review" {
			t.Fatalf("unexpected room request %#v", service.createdRoom)
		}
	})

	t.Run("update room members", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPatch, "/v1/rooms/research/members", strings.NewReader(`{"add":["writer"],"remove":["reviewer"]}`))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("unexpected status %d", response.Code)
		}
		if service.roomID != "research" || len(service.updatedRoom.Add) != 1 || service.updatedRoom.Add[0] != "writer" {
			t.Fatalf("unexpected room member update %#v", service.updatedRoom)
		}
		if !strings.Contains(response.Body.String(), "writer") || !strings.Contains(response.Body.String(), "reviewer") {
			t.Fatalf("unexpected body %s", response.Body.String())
		}
	})

}
