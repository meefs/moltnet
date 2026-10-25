package rooms_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/rooms"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func memoryBackend(*testing.T) backendStore { return store.NewMemoryStore() }

func joinedTopology(t *testing.T, open func(t *testing.T) backendStore) hubTopology {
	t.Helper()
	topology := newHubTopology(t, open)
	topology.add(t, topology.alpha)
	topology.add(t, topology.beta)
	topology.join(t, topology.alpha)
	topology.join(t, topology.beta)
	return topology
}

func TestHubRoomFansOutAcrossThreeNetworks(t *testing.T) {
	for _, backend := range envelopeTestBackends(t) {
		t.Run(backend.name, func(t *testing.T) {
			h := joinedTopology(t, backend.open)
			h.alpha.post(t, "hello from alpha")
			h.home.waitTexts(t, "hello from alpha")
			h.beta.waitTexts(t, "hello from alpha")
			h.home.post(t, "home speaks")
			h.alpha.waitTexts(t, "hello from alpha", "home speaks")
			h.beta.waitTexts(t, "hello from alpha", "home speaks")
			h.beta.post(t, "beta answers")
			h.alpha.waitTexts(t, "hello from alpha", "home speaks", "beta answers")
			h.home.waitTexts(t, "hello from alpha", "home speaks", "beta answers")
			// Members never echo their own messages back, and never forward.
			if got := h.beta.texts(t); !equalStrings(got, []string{"hello from alpha", "home speaks", "beta answers"}) {
				t.Fatalf("beta = %q", got)
			}
			for _, network := range []*hubNet{h.alpha, h.beta} {
				if state := network.delivery(t, "link-home"); state.Pending != 0 || state.Rejected != 0 {
					t.Fatalf("%s outbox = %#v", network.id, state)
				}
			}
		})
	}
}

func TestHubRoomLabelsKeysMetOnlyThroughTheRoomAsIntroduced(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	h.alpha.post(t, "from alpha")
	h.home.waitTexts(t, "from alpha")
	h.home.post(t, "from home")
	h.beta.waitTexts(t, "from alpha", "from home")
	labels := map[string]string{}
	for _, message := range h.beta.wire(t) {
		labels[message.Parts[0].Text] = message.Origin.Signature
	}
	// Beta met alpha only through the room: introduced, never verified.
	// Beta pinned home itself from the invite: verified.
	if labels["from alpha"] != protocol.SignatureStateIntroduced || labels["from home"] != protocol.SignatureStateVerified {
		t.Fatalf("beta signature labels = %#v", labels)
	}
	pin, ok, err := h.beta.pins.Lookup("alpha")
	if err != nil || !ok || pin.Trust != protocol.KeyTrustIntroduced || pin.IntroducedBy != "home" || pin.Fingerprint != h.alpha.identity.Fingerprint() {
		t.Fatalf("beta's pin for alpha = %#v, %v, %v", pin, ok, err)
	}
}

func TestHubHomeCannotForgeAnOriginTheMemberPinnedItself(t *testing.T) {
	h := newHubTopology(t, memoryBackend)
	// Beta verified alpha's real key out of band before ever meeting it.
	pinKey(t, h.beta.pins, verifiedPin("alpha", h.alpha.identity))
	// A malicious home pins and introduces its own key under alpha's name.
	impostor := newTestIdentity(t)
	if _, err := h.home.pins.Forget("alpha"); err != nil {
		t.Fatal(err)
	}
	pinKey(t, h.home.pins, unconfirmedPin("alpha", impostor, "link-alpha"))
	h.add(t, h.alpha)
	h.add(t, h.beta)
	h.join(t, h.beta)
	canonical := canonicalRoomOf(h.global)
	forged := hubEnvelope(t, impostor, "alpha", h.global, canonical, "forged-1", "alpha says: wire the money", 3, 1)
	forged.Receipt = homeReceipt(t, h.home.identity, h.global, 3, 1, eventID(t, forged), "")
	status, body := hubPost(t, h.beta, "beta-link-secret", forged)
	if status != http.StatusForbidden || !strings.Contains(body, "pinned to a different key") {
		t.Fatalf("forged origin accepted by a member that pinned alpha: %d %s", status, body)
	}
	if got := h.beta.texts(t); len(got) != 0 {
		t.Fatalf("forged message stored: %q", got)
	}
}

func TestHubRemovedMemberIsRefusedEverywhere(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	h.control(t, protocol.RoomControlRemove, "beta")
	// Beta still believes it is a member; the home refuses definitively.
	h.beta.post(t, "still here?")
	waitFor(t, func() bool { return h.beta.delivery(t, "link-home").Rejected == 1 })
	if got := h.home.texts(t); len(got) != 0 {
		t.Fatalf("home stored a removed member's message: %q", got)
	}
	// A home releasing a removed network's message is refused by members:
	// beta may not write at the epoch the receipt places it in.
	canonical := canonicalRoomOf(h.global)
	held := hubEnvelope(t, h.beta.identity, "beta", h.global, canonical, "held-1", "late", 4, 9)
	held.Receipt = homeReceipt(t, h.home.identity, h.global, 4, 1, eventID(t, held), "")
	if status, body := hubPost(t, h.alpha, "alpha-link-secret", held); status != http.StatusForbidden {
		t.Fatalf("member accepted a removed origin's message: %d %s", status, body)
	}
	// Home stops fanning out to beta.
	h.alpha.post(t, "after removal")
	h.home.waitTexts(t, "after removal")
	if got := h.beta.texts(t); len(got) != 1 || got[0] != "still here?" {
		t.Fatalf("removed beta received: %q", got)
	}
}

func TestHubStaleEpochSyncsResignsAndRetries(t *testing.T) {
	h := newHubTopology(t, memoryBackend)
	h.add(t, h.alpha)
	h.join(t, h.alpha) // alpha's log stops at epoch 2
	h.add(t, h.beta)   // the room moves to epoch 3 without telling alpha
	h.join(t, h.beta)
	notices := h.alpha.service.Subscribe(t.Context())
	h.alpha.post(t, "signed under epoch 2")
	// The home answers stale_membership_epoch; alpha learns that beta joined,
	// re-signs under epoch 3 and resends. Nothing is rejected or dropped.
	h.beta.waitTexts(t, "signed under epoch 2")
	if state := h.alpha.delivery(t, "link-home"); state.Rejected != 0 {
		t.Fatalf("stale epoch was treated as a rejection: %#v", state)
	}
	sawNotice := false
	for !sawNotice {
		select {
		case event := <-notices:
			sawNotice = event.Type == protocol.EventTypeRoomMembersUpdated && event.Room != nil && event.Room.ID == localRoom(h.alpha)
		case <-time.After(time.Second):
			t.Fatal("alpha recorded no membership notice for beta's join")
		}
	}
	view, err := h.alpha.service.HubRoomsContext(contextBackground())
	if err != nil || len(view) != 1 || view[0].Epoch != 3 || view[0].Members["beta"] != protocol.RoomRoleWriter {
		t.Fatalf("alpha did not sync the join first: %#v, %v", view, err)
	}
}

func TestHubCrossRoomReplayIsRefused(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	if _, err := h.home.service.CreateRoom(protocol.CreateRoomRequest{ID: "other", Members: []string{"lead"}}); err != nil {
		t.Fatal(err)
	}
	otherView, err := h.home.service.CreateHubRoomContext(contextBackground(), "other", "")
	if err != nil {
		t.Fatal(err)
	}
	// Alpha's valid message for the research room, retargeted at "other".
	valid := hubEnvelope(t, h.alpha.identity, "alpha", h.global, canonicalRoomOf(h.global), "replay-1", "research only", 3, 1)
	retargeted := valid
	retargeted.Target.RoomID = canonicalRoomOf(otherView.GlobalRoomID)
	if status, body := hubPost(t, h.home, "alpha-link-secret", retargeted); status != http.StatusForbidden {
		t.Fatalf("home accepted a cross-room replay: %d %s", status, body)
	}
	// A receipt from another room never places this room's event.
	valid.Receipt = homeReceipt(t, h.home.identity, otherView.GlobalRoomID, 3, 1, eventID(t, valid), "")
	if status, body := hubPost(t, h.beta, "beta-link-secret", valid); status != http.StatusForbidden {
		t.Fatalf("member accepted a receipt from another room: %d %s", status, body)
	}
}

func TestHubConflictingReceiptIsRefusedNotAcknowledged(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	h.alpha.post(t, "first")
	h.beta.waitTexts(t, "first")
	canonical := canonicalRoomOf(h.global)
	// A malicious home places a different event at the seq beta already has.
	other := hubEnvelope(t, h.home.identity, "home", h.global, canonical, "rewrite-1", "rewritten history", 3, 1)
	other.Receipt = homeReceipt(t, h.home.identity, h.global, 3, 1, eventID(t, other), "")
	if status, body := hubPost(t, h.beta, "beta-link-secret", other); status != http.StatusConflict {
		t.Fatalf("conflicting receipt for an occupied seq: %d %s", status, body)
	}
	// And places the same event again at a new seq.
	again := hubEnvelope(t, h.home.identity, "home", h.global, canonical, "dup-1", "placed twice", 3, 2)
	again.Receipt = homeReceipt(t, h.home.identity, h.global, 3, 5, eventID(t, again), strings.Repeat("0", 64))
	if status, _ := hubPost(t, h.beta, "beta-link-secret", again); status != http.StatusAccepted {
		t.Fatalf("first placement refused: %d", status)
	}
	_, firstPlacement, err := protocol.VerifyRoomReceipt(*again.Receipt, h.home.identity.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	// Chained correctly to the first placement, so only the one-placement-
	// per-event rule can refuse it.
	again.Receipt = homeReceipt(t, h.home.identity, h.global, 3, 6, eventID(t, again), firstPlacement)
	if status, body := hubPost(t, h.beta, "beta-link-secret", again); status != http.StatusConflict {
		t.Fatalf("second placement of one event acknowledged: %d %s", status, body)
	}
	if got := h.beta.texts(t); !equalStrings(got, []string{"first", "placed twice"}) {
		t.Fatalf("beta history = %q", got)
	}
}

func TestHubForwardsAreAcceptedOnlyFromTheRoomsHome(t *testing.T) {
	h := newHubTopology(t, memoryBackend)
	h.add(t, h.alpha)
	h.add(t, h.beta)
	// Beta also has a direct, unbound pairing that someone else holds.
	h.beta.pairings = append(h.beta.pairings, protocol.Pairing{ID: "side-door", RemoteBaseURL: h.alpha.url, Token: "side-secret"})
	h.beta.tokens = append(h.beta.tokens, authnPairToken("side-door", "side-secret"))
	h.beta.start(t)
	h.join(t, h.beta)
	forward := hubEnvelope(t, h.alpha.identity, "alpha", h.global, canonicalRoomOf(h.global), "side-1", "via the side door", 3, 1)
	forward.Receipt = homeReceipt(t, h.home.identity, h.global, 3, 1, eventID(t, forward), "")
	if status, body := hubPost(t, h.beta, "side-secret", forward); status != http.StatusForbidden || !strings.Contains(body, "home") {
		t.Fatalf("forward over a non-home pairing: %d %s", status, body)
	}
	if status, body := hubPost(t, h.beta, "beta-link-secret", forward); status != http.StatusAccepted {
		t.Fatalf("the same forward from the home: %d %s", status, body)
	}
}

func TestHubReceiptMustPlaceTheSignedEpoch(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	forward := hubEnvelope(t, h.alpha.identity, "alpha", h.global, canonicalRoomOf(h.global), "epoch-1", "which epoch?", 3, 1)
	forward.Receipt = homeReceipt(t, h.home.identity, h.global, 2, 1, eventID(t, forward), "")
	if status, body := hubPost(t, h.beta, "beta-link-secret", forward); status != http.StatusForbidden {
		t.Fatalf("receipt epoch differing from the signed epoch: %d %s", status, body)
	}
	unsignedReceipt := forward
	unsignedReceipt.Receipt = homeReceipt(t, newTestIdentity(t), h.global, 3, 1, eventID(t, forward), "")
	if status, body := hubPost(t, h.beta, "beta-link-secret", unsignedReceipt); status != http.StatusForbidden {
		t.Fatalf("receipt not signed by the home: %d %s", status, body)
	}
}

func TestHubSubmissionMustComeFromTheOriginsOwnLink(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	// Beta's link presents a message signed by alpha: refused, even though
	// alpha may write and the signature is valid.
	relayed := hubEnvelope(t, h.alpha.identity, "alpha", h.global, canonicalRoomOf(h.global), "proxy-1", "beta relays alpha", 3, 1)
	if status, body := hubPost(t, h.home, "beta-link-secret", relayed); status != http.StatusForbidden {
		t.Fatalf("home accepted a submission from another member's link: %d %s", status, body)
	}
	// A member's submission must not carry a receipt of its own.
	own := hubEnvelope(t, h.alpha.identity, "alpha", h.global, canonicalRoomOf(h.global), "own-1", "with receipt", 3, 1)
	own.Receipt = homeReceipt(t, h.alpha.identity, h.global, 3, 1, eventID(t, own), "")
	if status, _ := hubPost(t, h.home, "alpha-link-secret", own); status != http.StatusForbidden {
		t.Fatalf("home accepted a member-issued receipt: %d", status)
	}
	if got := h.home.texts(t); len(got) != 0 {
		t.Fatalf("home stored %q", got)
	}
}

func TestRoomLinkGrantsOnlyTheHubRoom(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	if _, err := h.home.service.CreateRoom(protocol.CreateRoomRequest{ID: "open-plan", Members: []string{"lead", "alpha:lead"},
		Federation: &protocol.RoomFederation{Mode: protocol.RoomFederationAll}}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/rooms", "/v1/agents"} {
		body := pairGet(t, h.home.url+path, "alpha-link-secret")
		if strings.Contains(body, "open-plan") || strings.Contains(body, "research") || strings.Contains(body, `"lead"`) {
			t.Fatalf("room link discovered %s: %s", path, body)
		}
	}
	plain := protocol.SendMessageRequest{ID: "plain-1", Origin: protocol.MessageOrigin{NetworkID: "alpha", MessageID: "plain-1"},
		Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "open-plan"},
		From:   protocol.Actor{Type: "agent", ID: "lead", NetworkID: "alpha"}, Parts: []protocol.Part{{Kind: "text", Text: "not a hub room"}}}
	plain.Envelope = signEnvelopeFor(t, h.alpha.identity, plain)
	if status, body := hubPost(t, h.home, "alpha-link-secret", plain); status != http.StatusForbidden {
		t.Fatalf("room link wrote an ordinary room: %d %s", status, body)
	}
	dm := protocol.SendMessageRequest{ID: "dm-1", Origin: protocol.MessageOrigin{NetworkID: "alpha", MessageID: "dm-1"},
		Target: protocol.Target{Kind: protocol.TargetKindDM, DMID: "dm-1", ParticipantIDs: []string{"alpha:lead", "lead"}},
		From:   protocol.Actor{Type: "agent", ID: "lead", NetworkID: "alpha"}, Parts: []protocol.Part{{Kind: "text", Text: "psst"}}}
	dm.Envelope = signEnvelopeFor(t, h.alpha.identity, dm)
	if status, body := hubPost(t, h.home, "alpha-link-secret", dm); status != http.StatusForbidden || !strings.Contains(body, "room link") {
		t.Fatalf("room link carried a DM: %d %s", status, body)
	}
	// Nothing ordinary flows out over a room link either.
	if _, err := h.home.service.SendMessage(protocol.SendMessageRequest{Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "open-plan"},
		From: protocol.Actor{Type: "agent", ID: "lead"}, Parts: []protocol.Part{{Kind: "text", Text: "outbound"}}}); err != nil {
		t.Fatal(err)
	}
	if state := h.home.delivery(t, "link-alpha"); state.Pending != 0 {
		t.Fatalf("ordinary room traffic queued on a room link: %#v", state)
	}
}

func TestHubPinBindingSurvivesHomeRestart(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	h.alpha.post(t, "before restart")
	h.home.waitTexts(t, "before restart")
	h.home.start(t) // process-local learned bindings are gone
	// Alpha's link credential now claims a network that was never pinned.
	gamma := newTestIdentity(t)
	claimed := hubEnvelope(t, gamma, "gamma", h.global, canonicalRoomOf(h.global), "gamma-1", "I am gamma", 3, 1)
	if status, body := hubPost(t, h.home, "alpha-link-secret", claimed); status != http.StatusForbidden {
		t.Fatalf("restarted home let a link speak for a new network: %d %s", status, body)
	}
	if _, ok, _ := h.home.pins.Lookup("gamma"); ok {
		t.Fatal("restart hole pinned a new identity")
	}
	h.alpha.post(t, "after restart")
	h.beta.waitTexts(t, "before restart", "after restart")
}

func TestHubReaderMayNotWrite(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	if _, err := h.home.service.AppendHubControlContext(contextBackground(), rooms.HubControlRequest{
		GlobalRoomID: h.global, Kind: protocol.RoomControlSetRole, NetworkID: "beta", Role: protocol.RoomRoleReader}); err != nil {
		t.Fatal(err)
	}
	canonical := canonicalRoomOf(h.global)
	// At the home: a reader's correctly signed, current-epoch submission.
	submission := hubEnvelope(t, h.beta.identity, "beta", h.global, canonical, "reader-1", "may I?", 4, 1)
	if status, body := hubPost(t, h.home, "beta-link-secret", submission); status != http.StatusForbidden {
		t.Fatalf("home accepted a reader's message: %d %s", status, body)
	}
	// At a member: the home releasing a reader's message at epoch 4.
	forward := hubEnvelope(t, h.beta.identity, "beta", h.global, canonical, "reader-2", "released", 4, 2)
	forward.Receipt = homeReceipt(t, h.home.identity, h.global, 4, 1, eventID(t, forward), "")
	if status, body := hubPost(t, h.alpha, "alpha-link-secret", forward); status != http.StatusForbidden {
		t.Fatalf("member accepted a reader's message: %d %s", status, body)
	}
	// Once beta has synced, its own agents cannot post locally either.
	if _, err := h.beta.service.SyncHubRoomContext(contextBackground(), h.global); err != nil {
		t.Fatal(err)
	}
	if _, err := h.beta.service.SendMessage(protocol.SendMessageRequest{Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: localRoom(h.beta)},
		From: protocol.Actor{Type: "agent", ID: "lead"}, Parts: []protocol.Part{{Kind: "text", Text: "local"}}}); !errors.Is(err, rooms.ErrWriteForbidden) {
		t.Fatalf("a reader network's local agent posted: %v", err)
	}
}

func TestHubQueuedFanoutToAMemberRemovedMeanwhileIsDropped(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	beta := h.beta.handler.Load()
	h.beta.handler.Store(http.Handler(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "down", http.StatusServiceUnavailable)
	})))
	h.home.post(t, "while beta is down")
	waitFor(t, func() bool { return h.home.delivery(t, "link-beta").Pending == 1 })
	h.control(t, protocol.RoomControlRemove, "beta")
	h.beta.handler.Store(beta)
	waitFor(t, func() bool { return h.home.delivery(t, "link-beta").Rejected == 1 })
	if got := h.beta.texts(t); len(got) != 0 {
		t.Fatalf("removed member received a message queued before its removal: %q", got)
	}
}
