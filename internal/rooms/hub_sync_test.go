package rooms_test

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func (n *hubNet) catchUp(t *testing.T, global string) error {
	t.Helper()
	_, err := n.service.SyncHubRoomContext(contextBackground(), global)
	return err
}

func (n *hubNet) cursor(t *testing.T, global string) store.HubSyncCursor {
	t.Helper()
	cursor, err := n.store.(store.HubStore).HubSyncCursorContext(contextBackground(), global)
	if err != nil {
		t.Fatal(err)
	}
	return cursor
}

// down makes the network unreachable for inbound traffic; it returns the
// function that brings it back.
func (n *hubNet) down() func() {
	real := n.handler.Load()
	n.handler.Store(http.Handler(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "down", http.StatusServiceUnavailable)
	})))
	return func() { n.handler.Store(real) }
}

func TestHubMemberCatchesUpWhatItMissedWhileDown(t *testing.T) {
	for _, backend := range envelopeTestBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			h := joinedTopology(t, backend.open)
			up := h.beta.down()
			h.home.post(t, "one")
			h.alpha.post(t, "two")
			h.home.waitTexts(t, "one", "two")
			// Beta pulls from the home while the home still cannot reach it.
			if err := h.beta.catchUp(t, h.global); err != nil {
				t.Fatal(err)
			}
			h.beta.waitTexts(t, "one", "two")
			if cursor := h.beta.cursor(t, h.global); cursor.Seq != 2 {
				t.Fatalf("beta cursor = %#v", cursor)
			}
			up()
			// The outbox redelivers the same placements: duplicates, nothing new.
			waitFor(t, func() bool { return h.home.delivery(t, "link-beta").Pending == 0 })
			if state := h.home.delivery(t, "link-beta"); state.Rejected != 0 {
				t.Fatalf("the live copy of a message beta already synced was refused: %#v", state)
			}
			h.beta.waitTexts(t, "one", "two")
		})
	}
}

func TestHubFromJoinRoomServesNoHistoryBeforeTheJoin(t *testing.T) {
	h := newHubTopology(t, memoryBackend)
	h.add(t, h.alpha)
	h.join(t, h.alpha)
	h.home.post(t, "before beta")
	h.alpha.waitTexts(t, "before beta")
	h.add(t, h.beta)
	h.join(t, h.beta) // joining catches up
	if got := h.beta.texts(t); len(got) != 0 {
		t.Fatalf("from_join room served history from before the join: %q", got)
	}
	if cursor := h.beta.cursor(t, h.global); cursor.Seq != 1 {
		t.Fatalf("beta did not verify the receipt chain it may not read: %#v", cursor)
	}
	h.home.post(t, "after beta")
	h.beta.waitTexts(t, "after beta")
	waitFor(t, func() bool { return h.beta.cursor(t, h.global).Seq == 2 })
}

func TestHubFullHistoryRoomServesHistoryToNewMembers(t *testing.T) {
	h := newHubTopologyWith(t, memoryBackend, protocol.RoomHistoryFullHistory)
	h.home.post(t, "from genesis")
	h.add(t, h.beta)
	h.join(t, h.beta)
	h.beta.waitTexts(t, "from genesis")
}

func TestHubSyncServesARemovedMemberNothingPlacedAfterItsRemoval(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	h.home.post(t, "while a member")
	h.beta.waitTexts(t, "while a member")
	h.control(t, protocol.RoomControlRemove, "beta")
	h.home.post(t, "after removal")
	h.alpha.waitTexts(t, "while a member", "after removal")
	var page protocol.RoomSyncPage
	raw := pairGet(t, h.home.url+"/v1/hub/sync?after=0&room="+urlQuery(h.global), "beta-link-secret")
	if err := json.Unmarshal([]byte(raw), &page); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	if page.ThroughSeq != 1 || len(page.Entries) != 1 || page.Entries[0].Message == nil || page.More {
		t.Fatalf("removed member's sync page = %s", raw)
	}
	if err := h.beta.catchUp(t, h.global); err != nil {
		t.Fatal(err)
	}
	if got := h.beta.texts(t); !equalStrings(got, []string{"while a member"}) {
		t.Fatalf("removed beta = %q", got)
	}
	// A network never admitted learns nothing at all.
	if status := pairGetStatus(t, h.home.url+"/v1/hub/sync?after=0&room="+urlQuery(h.global), "nobody"); status == http.StatusOK {
		t.Fatal("an unknown credential read the sync log")
	}
}

func TestHubOriginsOwnCopyLearnsItsRoomSeq(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	h.home.post(t, "home first")
	h.alpha.waitTexts(t, "home first")
	h.alpha.post(t, "alpha second")
	h.beta.waitTexts(t, "home first", "alpha second")
	page, err := h.alpha.service.ListRoomMessages(localRoom(h.alpha), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	var own protocol.Message
	for _, message := range page.Messages {
		if message.Parts[0].Text == "alpha second" {
			own = message
		}
	}
	waitFor(t, func() bool {
		receipts, err := h.alpha.store.(store.HubStore).HubReceiptsForMessagesContext(contextBackground(), []string{own.ID})
		return err == nil && receipts[own.ID].RoomSeq == 2
	})
	waitFor(t, func() bool { return h.alpha.cursor(t, h.global).Seq == 2 })
}

func TestHubLiveForwardPastAGapTriggersCatchUp(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	// Beta refuses the first fanout outright, so the home drops that row.
	real := h.beta.handler.Load().(http.Handler)
	var refused atomic.Bool
	h.beta.handler.Store(http.Handler(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost && refused.CompareAndSwap(false, true) {
			http.Error(response, "no", http.StatusForbidden)
			return
		}
		real.ServeHTTP(response, request)
	})))
	h.home.post(t, "lost to beta")
	// The refused row is settled as rejected, never resent: only catch-up
	// can bring this message to beta now.
	waitFor(t, func() bool { return h.home.delivery(t, "link-beta").Rejected == 1 })
	h.home.post(t, "arrives past the gap")
	// Storage lists by arrival (the gap fill lands after the live copy); the
	// HTTP view carries each message's room seq and epoch, and room order
	// (what the console shows) is by room seq.
	h.beta.waitTexts(t, "arrives past the gap", "lost to beta")
	waitFor(t, func() bool { return h.beta.cursor(t, h.global).Seq == 2 })
	bySeq := map[int64]string{}
	for _, message := range h.beta.wire(t) {
		if message.MembershipEpoch != 3 {
			t.Fatalf("%q placed under epoch %d on the wire", message.Parts[0].Text, message.MembershipEpoch)
		}
		bySeq[message.RoomSeq] = message.Parts[0].Text
	}
	if bySeq[1] != "lost to beta" || bySeq[2] != "arrives past the gap" {
		t.Fatalf("room order on the wire = %q", bySeq)
	}
}

func pairGetStatus(t *testing.T, url, secret string) int {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, url, nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	return response.StatusCode
}
