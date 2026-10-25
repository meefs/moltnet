package rooms_test

import (
	"encoding/json"
	"testing"

	"github.com/noopolis/moltnet/internal/rooms"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// A removed member learns of its removal from the control log, and
// nothing that happened after it.
func TestHubRemovedMemberReadsTheControlLogOnlyUpToItsRemoval(t *testing.T) {
	h := joinedTopology(t, memoryBackend)            // epoch 3
	h.control(t, protocol.RoomControlRemove, "beta") // epoch 4
	if _, err := h.home.service.AppendHubControlContext(contextBackground(), rooms.HubControlRequest{
		GlobalRoomID: h.global, Kind: protocol.RoomControlSetRole, NetworkID: "alpha", Role: protocol.RoomRoleAdmin}); err != nil {
		t.Fatal(err)
	}
	var page protocol.RoomControlPage
	if err := json.Unmarshal([]byte(pairGet(t, h.home.url+"/v1/hub/control?after=0&room="+urlQuery(h.global), "beta-link-secret")), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 4 {
		t.Fatalf("removed member read %d control events, want the 4 up to its removal", len(page.Events))
	}
	if err := json.Unmarshal([]byte(pairGet(t, h.home.url+"/v1/hub/control?after=0&room="+urlQuery(h.global), "alpha-link-secret")), &page); err != nil || len(page.Events) != 5 {
		t.Fatalf("current member read %d events, %v", len(page.Events), err)
	}
}
