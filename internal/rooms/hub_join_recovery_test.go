package rooms_test

import (
	"errors"
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/rooms"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// A crash after the hub record was written but before the local room was
// created is finished by a repeated join, or by recovery at start.
func TestHubJoinInterruptedBeforeItsLocalRoomIsRecovered(t *testing.T) {
	h := newHubTopology(t, memoryBackend)
	h.add(t, h.alpha)
	h.add(t, h.beta)
	h.home.post(t, "already there")
	genesis, err := h.home.store.(store.HubStore).HubControlLogContext(contextBackground(), h.global, 0)
	if err != nil {
		t.Fatal(err)
	}
	crashed := func(n *hubNet) {
		hub := store.HubRoom{GlobalRoomID: h.global, LocalRoomID: localRoom(n), HomeNetworkID: "home", HomePairingID: "link-home", CreatedAt: time.Now().UTC()}
		if err := n.store.(store.HubStore).CreateHubRoomContext(contextBackground(), hub, genesis[0]); err != nil {
			t.Fatal(err)
		}
	}
	// Alpha restarts and retries the join: it finishes instead of failing.
	crashed(h.alpha)
	h.alpha.start(t)
	h.join(t, h.alpha)
	if _, ok, _ := h.alpha.store.GetRoom(localRoom(h.alpha)); !ok {
		t.Fatal("repeated join did not provision the local room")
	}
	// Beta restarts: recovery provisions the room and catches up the log.
	crashed(h.beta)
	h.beta.start(t)
	waitFor(t, func() bool {
		_, ok, _ := h.beta.store.GetRoom(localRoom(h.beta))
		return ok
	})
	if _, err := h.beta.service.SyncHubRoomContext(contextBackground(), h.global); err != nil {
		t.Fatal(err)
	}
	view, err := h.beta.service.HubRoomsContext(contextBackground())
	if err != nil || len(view) != 1 || view[0].Epoch != 3 {
		t.Fatalf("recovered view = %#v, %v", view, err)
	}
	h.home.post(t, "after recovery")
	h.beta.waitTexts(t, "already there", "after recovery") // placed after beta was added
}

func TestHubJoinRefusesATakenLocalRoomBeforeWritingAnything(t *testing.T) {
	h := newHubTopology(t, memoryBackend)
	h.add(t, h.beta)
	if _, err := h.beta.service.CreateRoom(protocol.CreateRoomRequest{ID: localRoom(h.beta), Members: []string{"lead"}}); err != nil {
		t.Fatal(err)
	}
	_, err := h.beta.service.JoinHubRoomContext(contextBackground(), rooms.HubJoinRequest{PairingID: "link-home", GlobalRoomID: h.global, LocalRoomID: localRoom(h.beta)})
	if !errors.Is(err, store.ErrHubRoomExists) {
		t.Fatalf("join over a taken local room: %v", err)
	}
	if hubs, _ := h.beta.store.(store.HubStore).HubRoomsContext(contextBackground()); len(hubs) != 0 {
		t.Fatalf("a refused join left a hub record: %#v", hubs)
	}
}

// Concurrent catch-ups of one room (the loop, a gap, an operator) agree:
// none fails on events the other just stored.
func TestHubConcurrentSyncsOfOneRoomBothSucceed(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	for round := 0; round < 5; round++ {
		role := []string{protocol.RoomRoleAdmin, protocol.RoomRoleWriter}[round%2]
		if _, err := h.home.service.AppendHubControlContext(contextBackground(), rooms.HubControlRequest{
			GlobalRoomID: h.global, Kind: protocol.RoomControlSetRole, NetworkID: "alpha", Role: role}); err != nil {
			t.Fatal(err)
		}
		errs := make(chan error, 4)
		for range 4 {
			go func() {
				_, err := h.beta.service.SyncHubRoomContext(contextBackground(), h.global)
				errs <- err
			}()
		}
		for range 4 {
			if err := <-errs; err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
		}
	}
}
