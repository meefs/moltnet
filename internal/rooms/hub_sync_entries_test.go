package rooms_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/noopolis/moltnet/internal/netkeys"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// serveForgedSyncPages answers GET /v1/hub/sync by its after parameter.
func serveForgedSyncPages(n *hubNet, pages map[string]protocol.RoomSyncPage) {
	real := n.handler.Load().(http.Handler)
	n.handler.Store(http.Handler(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/hub/sync" {
			page, ok := pages[request.URL.Query().Get("after")]
			if !ok {
				http.NotFound(response, request)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(response).Encode(page)
			return
		}
		real.ServeHTTP(response, request)
	})))
}

func receiptDigest(t *testing.T, signed *protocol.SignedRoomReceipt, home *netkeys.Identity) string {
	t.Helper()
	_, digest, err := protocol.VerifyRoomReceipt(*signed, home.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

// placedEntry is a sync entry the home placed: the message as fanned out,
// with its receipt at seq after prev.
func placedEntry(t *testing.T, h hubTopology, signer *netkeys.Identity, origin, id, text string, epoch, seq int64, prev string) (protocol.RoomSyncEntry, string) {
	t.Helper()
	request := hubEnvelope(t, signer, origin, h.global, canonicalRoomOf(h.global), id, text, epoch, seq)
	receipt := homeReceipt(t, h.home.identity, h.global, epoch, seq, eventID(t, request), prev)
	request.Receipt = receipt
	return protocol.RoomSyncEntry{Receipt: *receipt, Message: &request}, receiptDigest(t, receipt, h.home.identity)
}

// stripAckReceipts drops the receipt from the home's acknowledgments.
func stripAckReceipts(n *hubNet) {
	real := n.handler.Load().(http.Handler)
	n.handler.Store(http.Handler(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/messages" {
			real.ServeHTTP(response, request)
			return
		}
		recorder := httptest.NewRecorder()
		real.ServeHTTP(recorder, request)
		var body map[string]any
		if json.Unmarshal(recorder.Body.Bytes(), &body) == nil {
			delete(body, "receipt")
		}
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(recorder.Code)
		_ = json.NewEncoder(response).Encode(body)
	})))
}

// A member's own message posted with a caller-chosen origin.message_id is
// found by origin tuple in its sync pages, so it neither aborts catch-up
// nor blocks later gaps from being filled.
func TestHubOwnMessageWithCustomOriginIDDoesNotPoisonSync(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	real := h.alpha.handler.Load().(http.Handler)
	var refused atomic.Bool
	h.alpha.handler.Store(http.Handler(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost && refused.CompareAndSwap(false, true) {
			http.Error(response, "no", http.StatusForbidden)
			return
		}
		real.ServeHTTP(response, request)
	})))
	h.home.post(t, "lost to alpha")
	waitFor(t, func() bool { return h.home.delivery(t, "link-alpha").Rejected == 1 })
	stripAckReceipts(h.home) // only sync can tell alpha's own copy its seq
	own, err := h.alpha.service.SendMessage(protocol.SendMessageRequest{
		Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: localRoom(h.alpha)},
		From:   protocol.Actor{Type: "agent", ID: "lead"}, Parts: []protocol.Part{{Kind: "text", Text: "custom origin id"}},
		Origin: protocol.MessageOrigin{MessageID: "idem-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.beta.waitTexts(t, "lost to alpha", "custom origin id")
	if err := h.alpha.catchUp(t, h.global); err != nil {
		t.Fatalf("catch-up over an own message with a custom origin id: %v", err)
	}
	if cursor := h.alpha.cursor(t, h.global); cursor.Seq != 2 {
		t.Fatalf("alpha cursor = %#v", cursor)
	}
	// The own copy learned its room seq from sync (the home's ack may
	// carry it too; sync must agree either way).
	placed, err := h.alpha.store.(store.HubStore).HubReceiptsForMessagesContext(contextBackground(), []string{own.MessageID})
	if err != nil || placed[own.MessageID].RoomSeq != 2 {
		t.Fatalf("own copy's receipt = %#v, %v", placed, err)
	}
	refused.Store(false)
	h.home.post(t, "lost again")
	waitFor(t, func() bool { return h.home.delivery(t, "link-alpha").Rejected == 2 })
	h.home.post(t, "live again")
	h.beta.waitTexts(t, "lost to alpha", "custom origin id", "lost again", "live again")
	if err := h.alpha.catchUp(t, h.global); err != nil {
		t.Fatal(err)
	}
	if cursor := h.alpha.cursor(t, h.global); cursor.Seq != 4 {
		t.Fatalf("the second gap was not filled: cursor %#v, texts %q", cursor, h.alpha.texts(t))
	}
}

// An entry refused for its content (here: signed by a key that is not its
// origin's) is skipped and logged; its receipt is kept and the cursor moves
// past it, so later entries still arrive.
func TestHubSyncSkipsAnEntryRefusedForItsContent(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	stranger := newTestIdentity(t)
	bad, badDigest := placedEntry(t, h, stranger, "alpha", "bad-1", "forged author", 3, 1, "")
	good, goodDigest := placedEntry(t, h, h.home.identity, "home", "good-1", "after the bad one", 3, 2, badDigest)
	serveForgedSyncPages(h.home, map[string]protocol.RoomSyncPage{
		"0": {GlobalRoomID: h.global, ThroughSeq: 2, Entries: []protocol.RoomSyncEntry{bad, good}},
		"2": {GlobalRoomID: h.global, ThroughSeq: 2},
	})
	if err := h.beta.catchUp(t, h.global); err != nil {
		t.Fatalf("one refused entry aborted catch-up: %v", err)
	}
	if cursor := h.beta.cursor(t, h.global); cursor.Seq != 2 || cursor.Digest != goodDigest {
		t.Fatalf("cursor = %#v", cursor)
	}
	if got := h.beta.texts(t); !equalStrings(got, []string{"after the bad one"}) {
		t.Fatalf("beta = %q", got)
	}
	receipts, err := h.beta.store.(store.HubStore).HubReceiptsAfterContext(contextBackground(), h.global, protocol.HubEpochV1, 0, 0)
	if err != nil || len(receipts) != 2 || receipts[0].MessageID != "" {
		t.Fatalf("stored receipts = %#v, %v", receipts, err)
	}
}

// Live seq 2 arrived before seq 1. A sync chain that is internally valid but
// places a different seq 2 without its message must still meet the stored
// seq 2, and be refused.
func TestHubSyncReceiptOnlyEntryMeetsStoredPlacements(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	live, _ := placedEntry(t, h, h.home.identity, "home", "live-2", "live seq 2", 3, 2, strings.Repeat("1", 64))
	if status, body := hubPost(t, h.beta, "beta-link-secret", *live.Message); status != http.StatusAccepted {
		t.Fatalf("live seq 2: %d %s", status, body)
	}
	first := homeReceipt(t, h.home.identity, h.global, 3, 1, strings.Repeat("a", 64), "")
	other := homeReceipt(t, h.home.identity, h.global, 3, 2, strings.Repeat("b", 64), receiptDigest(t, first, h.home.identity))
	serveForgedSyncPages(h.home, map[string]protocol.RoomSyncPage{
		"0": {GlobalRoomID: h.global, ThroughSeq: 2, Entries: []protocol.RoomSyncEntry{{Receipt: *first}, {Receipt: *other}}},
	})
	if err := h.beta.catchUp(t, h.global); err == nil {
		t.Fatal("a receipt-only seq 2 contradicting the stored seq 2 was accepted")
	}
	if cursor := h.beta.cursor(t, h.global); cursor.Seq != 0 {
		t.Fatalf("cursor moved to %#v", cursor)
	}
}

// The epoch floor survives a page boundary even when the page ended on a
// receipt-only entry.
func TestHubSyncKeepsTheEpochFloorAcrossPages(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	first := homeReceipt(t, h.home.identity, h.global, 3, 1, strings.Repeat("a", 64), "")
	back := homeReceipt(t, h.home.identity, h.global, 2, 2, strings.Repeat("b", 64), receiptDigest(t, first, h.home.identity))
	serveForgedSyncPages(h.home, map[string]protocol.RoomSyncPage{
		"0": {GlobalRoomID: h.global, ThroughSeq: 1, More: true, Entries: []protocol.RoomSyncEntry{{Receipt: *first}}},
		"1": {GlobalRoomID: h.global, ThroughSeq: 2, Entries: []protocol.RoomSyncEntry{{Receipt: *back}}},
	})
	if err := h.beta.catchUp(t, h.global); err == nil {
		t.Fatal("an epoch going backwards across a page boundary was accepted")
	}
	if cursor := h.beta.cursor(t, h.global); cursor.Seq != 1 || cursor.MembershipEpoch != 3 {
		t.Fatalf("cursor = %#v", cursor)
	}
}
