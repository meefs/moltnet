package rooms_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/noopolis/moltnet/pkg/protocol"
)

// serveForgedSync makes the home answer GET /v1/hub/sync with page, signed
// by the real home key: a home rewriting what a member already verified.
func serveForgedSync(n *hubNet, page protocol.RoomSyncPage) {
	real := n.handler.Load().(http.Handler)
	n.handler.Store(http.Handler(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/hub/sync" {
			response.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(response).Encode(page)
			return
		}
		real.ServeHTTP(response, request)
	})))
}

func TestHubMemberRejectsSyncPagesThatRollBackVerifiedHistory(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	h.home.post(t, "one")
	h.home.post(t, "two")
	h.beta.waitTexts(t, "one", "two")
	if err := h.beta.catchUp(t, h.global); err != nil {
		t.Fatal(err)
	}
	verified := h.beta.cursor(t, h.global)
	if verified.Seq != 2 {
		t.Fatalf("cursor = %#v", verified)
	}
	event := strings.Repeat("e", 64)
	forgeries := map[string]protocol.RoomSyncPage{
		"head below the cursor": {GlobalRoomID: h.global, ThroughSeq: 1},
		"seq 3 not chained to the verified seq 2": {GlobalRoomID: h.global, ThroughSeq: 3, Entries: []protocol.RoomSyncEntry{
			{Receipt: *homeReceipt(t, h.home.identity, h.global, 3, 3, event, strings.Repeat("0", 64))}}},
		"a rewritten seq 2 replayed after the cursor": {GlobalRoomID: h.global, ThroughSeq: 2, Entries: []protocol.RoomSyncEntry{
			{Receipt: *homeReceipt(t, h.home.identity, h.global, 3, 2, event, verified.Digest)}}},
		"an epoch going backwards": {GlobalRoomID: h.global, ThroughSeq: 3, Entries: []protocol.RoomSyncEntry{
			{Receipt: *homeReceipt(t, h.home.identity, h.global, 2, 3, event, verified.Digest)}}},
		"a page claiming more than it accounts for": {GlobalRoomID: h.global, ThroughSeq: 9},
	}
	for label, page := range forgeries {
		serveForgedSync(h.home, page)
		if err := h.beta.catchUp(t, h.global); err == nil {
			t.Fatalf("%s: accepted", label)
		}
		if cursor := h.beta.cursor(t, h.global); cursor != verified {
			t.Fatalf("%s: cursor moved to %#v", label, cursor)
		}
	}
	if got := h.beta.texts(t); !equalStrings(got, []string{"one", "two"}) {
		t.Fatalf("beta = %q", got)
	}
}
