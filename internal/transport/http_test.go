package transport

import (
	"errors"
	"fmt"
	"github.com/noopolis/moltnet/pkg/protocol"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewHTTPHandlerRoutes(t *testing.T) {
	t.Parallel()

	stream := make(chan protocol.Event, 1)
	stream <- protocol.Event{
		ID:   "evt_1",
		Type: protocol.EventTypeMessageCreated,
		Message: &protocol.Message{
			ID: "msg_1",
		},
	}
	close(stream)

	service := &fakeService{
		network: protocol.Network{
			ID:   "local",
			Name: "Local",
			Capabilities: protocol.NetworkCapabilities{
				EventStream:        "sse",
				AttachmentProtocol: "websocket",
				HumanIngress:       true,
				MessagePagination:  "cursor",
				Pairings:           true,
			},
		},
		agents: []protocol.AgentSummary{
			{ID: "orchestrator", FQID: "molt://local/agents/orchestrator", NetworkID: "local", Rooms: []string{"research"}},
		},
		pairings: []protocol.Pairing{
			{ID: "pair_1", RemoteNetworkID: "remote", RemoteNetworkName: "Remote Lab", Status: "connected"},
		},
		pairNetwork: protocol.Network{
			ID:   "remote",
			Name: "Remote Lab",
		},
		pairRooms: []protocol.Room{
			{ID: "remote-research", NetworkID: "remote", FQID: "molt://remote/rooms/remote-research", Name: "Remote Research"},
		},
		pairAgents: []protocol.AgentSummary{
			{ID: "remote-writer", NetworkID: "remote", FQID: "molt://remote/agents/remote-writer"},
		},
		rooms: []protocol.Room{
			{ID: "research", NetworkID: "local", FQID: "molt://local/rooms/research", Name: "Research"},
		},
		roomPage: protocol.MessagePage{
			Messages: []protocol.Message{
				{ID: "msg_room"},
			},
			Page: protocol.PageInfo{HasMore: true, NextBefore: "msg_room"},
		},
		threads: []protocol.Thread{
			{ID: "thread_1", NetworkID: "local", FQID: "molt://local/threads/thread_1", RoomID: "research", ParentMessageID: "msg_room", MessageCount: 1},
		},
		threadPage: protocol.MessagePage{
			Messages: []protocol.Message{
				{ID: "msg_thread"},
			},
			Page: protocol.PageInfo{HasMore: false},
		},
		dms: []protocol.DirectConversation{
			{ID: "dm_1", NetworkID: "local", FQID: "molt://local/dms/dm_1", MessageCount: 1},
		},
		dmPage: protocol.MessagePage{
			Messages: []protocol.Message{
				{ID: "msg_dm"},
			},
			Page: protocol.PageInfo{HasMore: false},
		},
		artifactPage: protocol.ArtifactPage{
			Artifacts: []protocol.Artifact{
				{ID: "art_1", NetworkID: "local", FQID: "molt://local/artifacts/art_1", MessageID: "msg_thread", PartIndex: 0, Kind: "image"},
			},
			Page: protocol.PageInfo{HasMore: false},
		},
		stream: stream,
	}

	handler := NewHTTPHandler(service, nil)

	testHTTPAdminRoutes(t, service, handler)
	testHTTPMessageRoutes(t, service, handler)
}

func TestNewHTTPHandlerErrorsAndHelpers(t *testing.T) {
	t.Parallel()

	service := &fakeService{
		network:     protocol.Network{ID: "local", Name: "Local"},
		createErr:   fakeStatusError{status: http.StatusConflict, msg: "room already exists"},
		pairingErr:  fakeStatusError{status: http.StatusBadGateway, msg: "paired network request failed"},
		roomError:   fakeStatusError{status: http.StatusNotFound, msg: fmt.Sprintf("unknown room %q", "x")},
		threadError: fakeStatusError{status: http.StatusNotFound, msg: fmt.Sprintf("unknown thread %q", "x")},
		dmError:     fakeStatusError{status: http.StatusUnprocessableEntity, msg: "dm id is required"},
		artifactErr: fakeStatusError{status: http.StatusNotFound, msg: fmt.Sprintf("unknown room %q", "x")},
		sendErr:     errors.New("bad send"),
	}
	handler := NewHTTPHandler(service, nil)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{name: "invalid room json", method: http.MethodPost, path: "/v1/rooms", body: `{"id":1}`, status: http.StatusBadRequest},
		{name: "room create error", method: http.MethodPost, path: "/v1/rooms", body: `{"id":"x"}`, status: http.StatusConflict},
		{name: "invalid room members json", method: http.MethodPatch, path: "/v1/rooms/x/members", body: `{"add":1}`, status: http.StatusBadRequest},
		{name: "room list error", method: http.MethodGet, path: "/v1/rooms/x/messages", status: http.StatusNotFound},
		{name: "pairing network error", method: http.MethodGet, path: "/v1/pairings/pair_1/network", status: http.StatusBadGateway},
		{name: "pairing rooms error", method: http.MethodGet, path: "/v1/pairings/pair_1/rooms", status: http.StatusBadGateway},
		{name: "pairing agents error", method: http.MethodGet, path: "/v1/pairings/pair_1/agents", status: http.StatusBadGateway},
		{name: "thread list error", method: http.MethodGet, path: "/v1/rooms/x/threads", status: http.StatusNotFound},
		{name: "thread messages error", method: http.MethodGet, path: "/v1/threads/x/messages", status: http.StatusNotFound},
		{name: "dm list error", method: http.MethodGet, path: "/v1/dms/x/messages", status: http.StatusUnprocessableEntity},
		{name: "artifacts error", method: http.MethodGet, path: "/v1/artifacts?room_id=x", status: http.StatusNotFound},
		{name: "invalid message json", method: http.MethodPost, path: "/v1/messages", body: `{"target":{}}`, status: http.StatusUnprocessableEntity},
		{name: "invalid wake failed json", method: http.MethodPost, path: "/v1/agents/wake-failed", body: `{"agent_id":1}`, status: http.StatusBadRequest},
		{name: "wake failed missing agent id", method: http.MethodPost, path: "/v1/agents/wake-failed", body: `{"event":{"id":"evt_1","message":{"id":"msg_1"}}}`, status: http.StatusBadRequest},
		{name: "wake failed missing event message", method: http.MethodPost, path: "/v1/agents/wake-failed", body: `{"agent_id":"orchestrator","event":{"id":"evt_1"}}`, status: http.StatusBadRequest},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			handler.ServeHTTP(response, request)

			if response.Code != test.status {
				t.Fatalf("unexpected status %d", response.Code)
			}
		})
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{
		"target":{"kind":"room","room_id":"research"},
		"from":{"type":"agent","id":"orchestrator"},
		"parts":[{"kind":"text","text":"hello"}]
	}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected status %d", response.Code)
	}

	if got := readLimit(httptest.NewRequest(http.MethodGet, "/v1/rooms/x/messages?limit=0", nil)); got != 100 {
		t.Fatalf("unexpected default limit %d", got)
	}
	if got := readLimit(httptest.NewRequest(http.MethodGet, "/v1/rooms/x/messages?limit=wat", nil)); got != 100 {
		t.Fatalf("unexpected invalid limit %d", got)
	}
	if got := readLimit(httptest.NewRequest(http.MethodGet, "/v1/rooms/x/messages?limit=600", nil)); got != 500 {
		t.Fatalf("unexpected capped limit %d", got)
	}
	if got := readLimit(httptest.NewRequest(http.MethodGet, "/v1/rooms/x/messages?limit=7", nil)); got != 7 {
		t.Fatalf("unexpected limit %d", got)
	}
	page, err := readPageRequest(httptest.NewRequest(http.MethodGet, "/v1/rooms/x/messages?before=msg_7", nil))
	if err != nil {
		t.Fatalf("readPageRequest() error = %v", err)
	}
	if got := page.Before; got != "msg_7" {
		t.Fatalf("unexpected before %q", got)
	}

}
