package rooms_test

import (
	"net/http"
	"strings"
	"testing"

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
	h.alpha.post(t, "signed under epoch 2")
	// The home answers stale_membership_epoch; alpha learns that beta joined,
	// re-signs under epoch 3 and resends. Nothing is rejected or dropped.
	h.beta.waitTexts(t, "signed under epoch 2")
	if state := h.alpha.delivery(t, "link-home"); state.Rejected != 0 {
		t.Fatalf("stale epoch was treated as a rejection: %#v", state)
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
	again.Receipt = homeReceipt(t, h.home.identity, h.global, 3, 6, eventID(t, again), strings.Repeat("1", 64))
	if status, body := hubPost(t, h.beta, "beta-link-secret", again); status != http.StatusConflict {
		t.Fatalf("second placement of one event acknowledged: %d %s", status, body)
	}
	if got := h.beta.texts(t); !equalStrings(got, []string{"first", "placed twice"}) {
		t.Fatalf("beta history = %q", got)
	}
}
