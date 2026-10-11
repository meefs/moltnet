package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/bridge/daimon"
	"github.com/noopolis/moltnet/internal/bridge/loop"
	"github.com/noopolis/moltnet/pkg/bridgeconfig"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestClockLowerBoundaryMessageCausalWake(t *testing.T) {
	t.Setenv("NOOPOLIS_RUN_ID", "run-clock-boundary")
	for _, kind := range []string{"memory", "json", "sqlite"} {
		t.Run(kind, func(t *testing.T) {
			const origin = "0001-01-01T00:00:00.001Z"
			real := time.UnixMilli(0).UTC()
			want := time.Date(1, 1, 1, 0, 0, 0, int(time.Millisecond), time.UTC)
			causalPath := filepath.Join(t.TempDir(), "causal.jsonl")
			clock := &bridgeconfig.ClockConfig{Version: bridgeconfig.TaskClockVersion, Origin: origin}
			server, err := newWithRealClock(Config{NetworkID: "local", Clock: clock, CausalEventsPath: causalPath,
				Storage: StorageConfig{Kind: kind,
					JSON:   JSONStorageConfig{Path: filepath.Join(t.TempDir(), "store.json")},
					SQLite: SQLiteStorageConfig{Path: filepath.Join(t.TempDir(), "store.db")},
				}}, func() time.Time { return real })
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			handler := server.Handler()
			clockRequest(t, handler, "POST", "/v1/rooms", protocol.CreateRoomRequest{ID: "room", Members: []string{"writer"}}, nil, http.StatusCreated)
			clockRequest(t, handler, "POST", "/v1/messages", protocol.SendMessageRequest{ID: "boundary",
				Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "room"}, From: protocol.Actor{Type: "agent", ID: "writer"},
				Parts: []protocol.Part{{Kind: protocol.PartKindText, Text: "hello"}},
			}, nil, http.StatusAccepted)
			var page protocol.MessagePage
			clockRequest(t, handler, "GET", "/v1/rooms/room/messages", nil, &page, http.StatusOK)
			if len(page.Messages) != 1 {
				t.Fatalf("stored messages: %#v", page)
			}
			message := page.Messages[0]
			assertClockTime(t, "message", message.CreatedAt, want)

			data, err := os.ReadFile(causalPath)
			if err != nil {
				t.Fatal(err)
			}
			var causal protocol.CausalEvent
			if err := json.Unmarshal(bytes.TrimSpace(data), &causal); err != nil {
				t.Fatalf("missing or invalid causal record: %q, %v", data, err)
			}
			if err := causal.Validate(); err != nil {
				t.Fatal(err)
			}
			if causal.EventID != protocol.MessageEventID(message.ID) || causal.Type != protocol.EventTypeMessageAccepted {
				t.Fatalf("causal record does not describe the stored message: %#v", causal)
			}
			assertClockTime(t, "causal", causal.RecordedAt, want)

			// Feed the persisted timestamp through the actual Daimon wake encoder.
			config := bridgeconfig.Config{Clock: clock, Agent: bridgeconfig.AgentConfig{ID: "reader"},
				Runtime: bridgeconfig.RuntimeConfig{ControlURL: "http://control.invalid"}}
			request, err := daimon.NewCodec("").EncodeRequest(t.Context(), config, loop.ControlDelivery{
				Target: message.Target, EventID: protocol.MessageEventID(message.ID),
				Message: message.Parts[0].Text, OccurredAt: message.CreatedAt,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer request.Body.Close()
			var wake struct {
				DeliveryID string `json:"delivery_id"`
				Event      struct {
					OccurredAt string `json:"occurred_at"`
				} `json:"event"`
			}
			if err := json.NewDecoder(request.Body).Decode(&wake); err != nil {
				t.Fatal(err)
			}
			if wake.DeliveryID != causal.EventID || wake.Event.OccurredAt != "1970-01-01T00:00:00.000Z" {
				t.Fatalf("boundary wake did not preserve identity and invert once: %#v", wake)
			}
		})
	}
}
