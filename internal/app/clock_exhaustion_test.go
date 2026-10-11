package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/bridgeconfig"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestClockHTTPRuntimeExhaustion(t *testing.T) {
	for _, kind := range []string{"memory", "json", "sqlite"} {
		for _, bound := range []struct {
			origin string
			step   time.Duration
		}{
			{"9999-12-31T23:59:59.999Z", time.Millisecond},
			{"0001-01-01T00:00:00.001Z", -time.Millisecond},
		} {
			t.Run(kind+"/"+bound.origin, func(t *testing.T) {
				real := time.UnixMilli(0)
				config := Config{NetworkID: "local", Clock: &bridgeconfig.ClockConfig{
					Version: bridgeconfig.TaskClockVersion, Origin: bound.origin, AnchorEpochMS: 0,
				}, Storage: StorageConfig{Kind: kind,
					JSON:   JSONStorageConfig{Path: filepath.Join(t.TempDir(), "store.json")},
					SQLite: SQLiteStorageConfig{Path: filepath.Join(t.TempDir(), "store.db")},
				}}
				server, err := newWithRealClock(config, func() time.Time { return real })
				if err != nil {
					t.Fatal(err)
				}
				defer server.Close()
				handler := server.Handler()
				clockRequest(t, handler, "POST", "/v1/rooms", protocol.CreateRoomRequest{ID: "r", Members: []string{"writer"}}, nil, http.StatusCreated)
				clockRequest(t, handler, "POST", "/v1/agents/register", protocol.RegisterAgentRequest{RequestedAgentID: "writer"}, nil, http.StatusCreated)
				real = real.Add(bound.step)
				for _, request := range []struct {
					method, path string
					body         any
				}{
					{"POST", "/v1/rooms", protocol.CreateRoomRequest{ID: "new"}},
					{"POST", "/v1/agents/register", protocol.RegisterAgentRequest{RequestedAgentID: "new"}},
					{"POST", "/v1/messages", protocol.SendMessageRequest{Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "r"}, From: protocol.Actor{Type: "agent", ID: "writer"}, Parts: []protocol.Part{{Kind: "text", Text: "late"}}}},
					{"PATCH", "/v1/rooms/r/members", protocol.UpdateRoomMembersRequest{Add: []string{"new"}}},
					{"DELETE", "/v1/rooms/r", nil},
					{"DELETE", "/v1/agents/writer", nil},
					{"GET", "/v1/events/stream", nil},
				} {
					body, err := json.Marshal(request.body)
					if err != nil {
						t.Fatal(err)
					}
					req := httptest.NewRequest(request.method, request.path, bytes.NewReader(body))
					req.Header.Set("Last-Event-ID", "missing")
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, req)
					if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), bridgeconfig.ErrClockRange.Error()) {
						t.Fatalf("%s %s: %d %s", request.method, request.path, response.Code, response.Body)
					}
				}
				var room protocol.Room
				clockRequest(t, handler, "GET", "/v1/rooms/r", nil, &room, http.StatusOK)
				if len(room.Members) != 1 || room.Members[0] != "writer" {
					t.Fatalf("failed operation changed members: %#v", room)
				}
				var page protocol.MessagePage
				clockRequest(t, handler, "GET", "/v1/rooms/r/messages", nil, &page, http.StatusOK)
				if len(page.Messages) != 0 {
					t.Fatal("failed operation persisted a message")
				}
				clockRequest(t, handler, "GET", "/v1/agents/writer", nil, nil, http.StatusOK)
				clockRequest(t, handler, "GET", "/v1/rooms/new", nil, nil, http.StatusNotFound)
			})
		}
	}
}
