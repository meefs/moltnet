package app

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/bridgeconfig"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestClockRegistrationSameMillisecond(t *testing.T) {
	for _, kind := range []string{"memory", "json", "sqlite"} {
		t.Run(kind, func(t *testing.T) {
			real := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
			config := Config{NetworkID: "local", Clock: &bridgeconfig.ClockConfig{
				Version: bridgeconfig.TaskClockVersion, Origin: "2025-10-01T08:00:00Z", AnchorEpochMS: real.UnixMilli(),
			}, Storage: StorageConfig{Kind: kind,
				JSON:   JSONStorageConfig{Path: filepath.Join(t.TempDir(), "store.json")},
				SQLite: SQLiteStorageConfig{Path: filepath.Join(t.TempDir(), "store.db")},
			}}
			server, err := newWithRealClock(config, func() time.Time { return real })
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			var first, updated protocol.AgentRegistration
			request := protocol.RegisterAgentRequest{RequestedAgentID: "writer"}
			clockRequest(t, server.Handler(), "POST", "/v1/agents/register", request, &first, http.StatusCreated)
			request.Name = "Renamed"
			clockRequest(t, server.Handler(), "POST", "/v1/agents/register", request, &updated, http.StatusOK)
			if !updated.CreatedAt.Equal(updated.UpdatedAt) || !updated.CreatedAt.Equal(first.CreatedAt) || updated.ActorUID != first.ActorUID || updated.DisplayName != "Renamed" {
				t.Fatalf("same-millisecond update changed registration identity or time: %#v -> %#v", first, updated)
			}
		})
	}
}
