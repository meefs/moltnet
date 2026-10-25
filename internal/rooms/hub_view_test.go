package rooms_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestHubRoomViewShowsHomeRosterTrustAndControlEvents(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	h.alpha.post(t, "from alpha")
	h.home.waitTexts(t, "from alpha")
	h.beta.waitTexts(t, "from alpha")
	for _, message := range h.beta.wire(t) {
		if message.Origin.Signature != protocol.SignatureStateIntroduced || message.Origin.IntroducedBy != "home" {
			t.Fatalf("beta's label for alpha = %+v", message.Origin)
		}
	}
	var info protocol.HubRoomInfo
	body := pairGet(t, h.beta.url+"/v1/rooms/"+localRoom(h.beta)+"/hub", hubOperatorToken)
	if err := json.Unmarshal([]byte(body), &info); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	trust := map[string]string{}
	for _, member := range info.Members {
		trust[member.NetworkID] = member.Trust
	}
	if info.HomeNetworkID != "home" || info.IsHome || info.MembershipEpoch != 3 || info.Members[0].NetworkID != "home" ||
		trust["home"] != protocol.KeyTrustVerified || trust["alpha"] != protocol.KeyTrustIntroduced || trust["beta"] != protocol.HubTrustSelf {
		t.Fatalf("beta's view = %s", body)
	}
	if len(info.Events) != 3 || info.Events[0].Kind != protocol.RoomControlGenesis || info.Events[2].NetworkID != "beta" || info.Events[2].CreatedAt == "" {
		t.Fatalf("events = %+v", info.Events)
	}
	if status := pairGetStatus(t, h.beta.url+"/v1/rooms/nope/hub", hubOperatorToken); status != http.StatusNotFound {
		t.Fatalf("unknown room: %d", status)
	}
}

func TestHubRoomViewLabelsAConflictingPinAsConflict(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	pinKey(t, h.beta.pins, verifiedPin("alpha", newTestIdentity(t)))
	var info protocol.HubRoomInfo
	body := pairGet(t, h.beta.url+"/v1/rooms/"+localRoom(h.beta)+"/hub", hubOperatorToken)
	if err := json.Unmarshal([]byte(body), &info); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	for _, member := range info.Members {
		if member.NetworkID == "alpha" && member.Trust != protocol.HubTrustConflict {
			t.Fatalf("alpha's trust with a conflicting pin = %q", member.Trust)
		}
	}
}
