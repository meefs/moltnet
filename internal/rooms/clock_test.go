package rooms

import (
	"bytes"
	"context"
	"testing"
	"time"

	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/observability"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestClockLifecycleAndRealtimeRetries(t *testing.T) {
	task := time.Date(2025, 10, 1, 8, 0, 0, 0, time.UTC)
	memory := store.NewMemoryStore()
	broker := events.NewBroker(memory)
	pairing := protocol.Pairing{ID: "remote", Status: protocol.PairingStatusConnected}
	service := NewService(ServiceConfig{Now: func() time.Time { return task }, NetworkID: "local", Store: memory, Messages: memory, Broker: broker, Pairings: []protocol.Pairing{pairing}})
	defer service.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := broker.Subscribe(ctx)
	checkEvent := func(kind string) {
		t.Helper()
		select {
		case event := <-stream:
			if event.Type != kind || !event.CreatedAt.Equal(task) {
				t.Fatalf("event got %#v, want %s at %s", event, kind, task)
			}
		case <-time.After(time.Second):
			t.Fatal("missing " + kind)
		}
	}
	agent := protocol.Actor{Type: "agent", ID: "writer"}
	service.AgentConnected(ctx, agent)
	checkEvent(protocol.EventTypeAgentConnected)
	service.AgentWakeDelivered(ctx, agent, protocol.Event{Message: &protocol.Message{ID: "m", Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "r"}}})
	checkEvent(protocol.EventTypeAgentWakeDelivered)
	service.AgentDisconnected(ctx, agent, "closed", nil)
	checkEvent(protocol.EventTypeAgentDisconnected)
	room, err := service.CreateRoom(protocol.CreateRoomRequest{ID: "r", Members: []string{"writer"}})
	if err != nil {
		t.Fatal(err)
	}
	checkEvent(protocol.EventTypeRoomCreated)
	task = task.Add(time.Minute)
	if _, err := service.UpdateRoomMembers(ctx, "r", protocol.UpdateRoomMembersRequest{Add: []string{"reader"}}); err != nil {
		t.Fatal(err)
	}
	checkEvent(protocol.EventTypeRoomMembersUpdated)
	saved, ok, err := memory.GetRoom("r")
	if err != nil || !ok || !saved.CreatedAt.Equal(room.CreatedAt) {
		t.Fatalf("room creation rewritten: %#v %v", saved, err)
	}
	// CheckedAt is client-visible task time. updatedAt drives real cache/retry age.
	before := time.Now()
	service.setPairingError("remote", "unavailable")
	checkEvent(protocol.EventTypePairingUpdated)
	status := service.currentPairingStatus(pairing)
	if !status.diagnostics.CheckedAt.Equal(task) || status.updatedAt.Before(before) || status.updatedAt.After(time.Now()) {
		t.Fatalf("time domains mixed: %#v", status)
	}
	// Force known-peer error (the initial peer may still be pending).
	status.value = protocol.PairingStatusError
	service.pairingStatuses[pairing.ID] = status
	if service.pairingRelayAttemptReady(pairing, protocol.Message{}) {
		t.Fatal("retry did not wait real duration")
	}
	status.updatedAt = time.Now().Add(-pairingRelayErrorRetryAfter - time.Second)
	service.pairingStatuses[pairing.ID] = status
	if !service.pairingRelayAttemptReady(pairing, protocol.Message{}) {
		t.Fatal("past task clock delayed an elapsed real retry")
	}
	status.value = protocol.PairingStatusConnected
	status.checked = true
	status.updatedAt = time.Now()
	if service.pairingDiagnosticsNeedRefresh(status) {
		t.Fatal("fresh real cache expired")
	}
	status.updatedAt = time.Now().Add(-pairingDiagnosticsTTL - time.Second)
	if !service.pairingDiagnosticsNeedRefresh(status) {
		t.Fatal("task clock prevented real cache expiry")
	}
}

func TestClockDeniedCausalTimestamp(t *testing.T) {
	t.Setenv(causalRunIDEnv, "run-clock-denied")
	task := time.Date(2025, 10, 1, 8, 0, 0, 0, time.UTC)
	memory := store.NewMemoryStore()
	var output bytes.Buffer
	service := NewService(ServiceConfig{Now: func() time.Time { return task }, NetworkID: "local", Store: memory, Messages: memory, Broker: events.NewBroker(), CausalWriter: observability.NewCausalWriter(&output)})
	defer service.Close()
	if _, err := service.CreateRoom(protocol.CreateRoomRequest{ID: "r", Members: []string{"writer"}}); err != nil {
		t.Fatal(err)
	}
	ctx := authn.WithClaims(t.Context(), staticClaims("intruder", authn.ScopeWrite))
	if _, err := service.SendMessageContext(ctx, protocol.SendMessageRequest{Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "r"}, From: protocol.Actor{Type: "agent", ID: "intruder"}, Parts: []protocol.Part{{Kind: protocol.PartKindText, Text: "denied"}}}); err == nil {
		t.Fatal("expected policy denial")
	}
	recorded := causalLines(t, &output)
	if len(recorded) != 1 || recorded[0].Type != protocol.EventTypeMessageDenied || !recorded[0].RecordedAt.Equal(task) {
		t.Fatalf("denial timestamps: %#v", recorded)
	}
}

func TestClockRoomJoinEvent(t *testing.T) {
	task := time.Date(2025, 10, 1, 8, 0, 0, 0, time.UTC)
	memory := store.NewMemoryStore()
	broker := events.NewBroker(memory)
	service := NewService(ServiceConfig{Now: func() time.Time { return task }, NetworkID: "local", Store: memory, Messages: memory, Broker: broker})
	defer service.Close()
	if _, err := service.CreateRoomContext(credentialModeContext(), protocol.CreateRoomRequest{ID: "r", Credential: protocol.NewSecretString(joinTestCredential)}); err != nil {
		t.Fatal(err)
	}
	claims := registerPolicyAgent(t, service, "writer")
	ctx := bearerClaimsContext(claims)
	stream := broker.Subscribe(t.Context())
	task = task.Add(time.Minute)
	if _, err := service.JoinRoomContext(ctx, "r", protocol.JoinRoomRequest{From: protocol.Actor{Type: "agent", ID: "writer"}, Credential: protocol.NewSecretString(joinTestCredential)}); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-stream:
		if event.Type != protocol.EventTypeRoomMembersUpdated || !event.CreatedAt.Equal(task) {
			t.Fatalf("join event timestamp: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("missing join event")
	}
}
