package rooms

import (
	"errors"
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/bridgeconfig"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestClockExhaustionStopsBackgroundEvents(t *testing.T) {
	real := time.UnixMilli(0)
	clock := bridgeconfig.ClockConfig{Version: bridgeconfig.TaskClockVersion, Origin: "9999-12-31T23:59:59.999Z"}
	now, err := clock.Now(func() time.Time { return real })
	if err != nil {
		t.Fatal(err)
	}
	memory := store.NewMemoryStore()
	broker := events.NewBroker()
	pairing := protocol.Pairing{ID: "peer"}
	service := NewService(ServiceConfig{Now: now, NetworkID: "local", Store: memory, Messages: memory, Broker: broker, Pairings: []protocol.Pairing{pairing}})
	defer service.Close()
	stream := service.Subscribe(t.Context())
	real = real.Add(time.Millisecond)
	service.AgentConnected(t.Context(), protocol.Actor{ID: "writer"})
	service.AgentDisconnected(t.Context(), protocol.Actor{ID: "writer"}, "closed", nil)
	service.setPairingError("peer", "unreachable")
	service.setPairingStatus("peer", protocol.PairingStatusConnected)
	if _, _, err := service.refreshPairingDiagnostics(t.Context(), pairing, pairingCheckNetwork); !errors.Is(err, bridgeconfig.ErrClockRange) {
		t.Fatalf("diagnostics: %v", err)
	}
	select {
	case event := <-stream:
		t.Fatalf("exhausted clock published a background event: %#v", event)
	default:
	}
	if service.pairingStatuses["peer"].value != protocol.PairingStatusUnknown {
		t.Fatal("failed pairing update changed status")
	}
}
