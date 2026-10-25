package rooms_test

import (
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/hubinvites"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func (n *hubNet) joined(t *testing.T, global string) bool {
	t.Helper()
	rooms, err := n.store.(store.HubStore).HubRoomsContext(contextBackground())
	if err != nil {
		t.Fatal(err)
	}
	for _, room := range rooms {
		if room.GlobalRoomID == global {
			return true
		}
	}
	return false
}

func TestRoomInviteAdmitsTheJoinerOnceAndPinsItsKeyOverThePairing(t *testing.T) {
	h := newHubTopology(t, memoryBackend)
	h.add(t, h.alpha)
	h.join(t, h.alpha)
	// The home has never seen beta's key: admission is what pins it.
	if _, err := h.home.pins.Forget("beta"); err != nil {
		t.Fatal(err)
	}
	if err := h.home.invites.AddGrant(hubinvites.Grant{PairingID: "link-beta", GlobalRoomID: h.global, Role: protocol.RoomRoleWriter, Exp: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := h.beta.invites.AddJoin(hubinvites.PendingJoin{PairingID: "link-home", GlobalRoomID: h.global, LocalRoomID: localRoom(h.beta)}); err != nil {
		t.Fatal(err)
	}
	h.beta.start(t) // the server finishes the pending join
	waitFor(t, func() bool { return h.beta.joined(t, h.global) })
	waitFor(t, func() bool { joins, _ := h.beta.invites.Joins(); return len(joins) == 0 })
	if _, ok, _ := h.home.invites.Grant("link-beta", h.global, time.Now()); ok {
		t.Fatal("the grant was not consumed")
	}
	pin, ok, _ := h.home.pins.Lookup("beta")
	if !ok || pin.Fingerprint != h.beta.identity.Fingerprint() || pin.Trust != protocol.KeyTrustUnconfirmed || pin.SourceID != "link-beta" {
		t.Fatalf("home's pin for beta = %#v", pin)
	}
	view, err := h.home.service.HubRoomsContext(contextBackground())
	if err != nil || view[0].Members["beta"] != protocol.RoomRoleWriter {
		t.Fatalf("home roster = %#v, %v", view, err)
	}
	h.beta.post(t, "beta joined by invite")
	h.alpha.waitTexts(t, "beta joined by invite")
}

func TestRoomAdmissionWithoutALiveGrantIsRefusedAndRetried(t *testing.T) {
	h := newHubTopology(t, memoryBackend)
	if err := h.beta.invites.AddJoin(hubinvites.PendingJoin{PairingID: "link-home", GlobalRoomID: h.global}); err != nil {
		t.Fatal(err)
	}
	h.beta.start(t)
	time.Sleep(100 * time.Millisecond)
	if h.beta.joined(t, h.global) {
		t.Fatal("joined without a grant")
	}
	if joins, _ := h.beta.invites.Joins(); len(joins) != 1 {
		t.Fatal("a refused pending join was dropped instead of retried")
	}
	expired := hubinvites.Grant{PairingID: "link-beta", GlobalRoomID: h.global, Role: protocol.RoomRoleWriter, Exp: time.Now().Add(-time.Minute)}
	if err := h.home.invites.AddGrant(expired); err != nil {
		t.Fatal(err)
	}
	h.beta.start(t)
	time.Sleep(100 * time.Millisecond)
	if h.beta.joined(t, h.global) {
		t.Fatal("joined on an expired grant")
	}
	// The grant is per pairing: alpha's link cannot redeem beta's invite.
	if err := h.home.invites.AddGrant(hubinvites.Grant{PairingID: "link-beta", GlobalRoomID: h.global, Role: protocol.RoomRoleReader, Exp: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := h.alpha.invites.AddJoin(hubinvites.PendingJoin{PairingID: "link-home", GlobalRoomID: h.global}); err != nil {
		t.Fatal(err)
	}
	h.alpha.start(t)
	time.Sleep(100 * time.Millisecond)
	if h.alpha.joined(t, h.global) {
		t.Fatal("alpha redeemed an invite minted for beta's pairing")
	}
}

func TestHubMemberLeavesAndCanNoLongerWrite(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	if _, err := h.beta.service.LeaveHubRoomContext(contextBackground(), h.global); err != nil {
		t.Fatal(err)
	}
	view, err := h.home.service.HubRoomsContext(contextBackground())
	if err != nil {
		t.Fatal(err)
	}
	if _, still := view[0].Members["beta"]; still || view[0].Epoch != 4 {
		t.Fatalf("home roster after leave = %#v", view[0])
	}
	if _, err := h.beta.service.SendMessage(protocol.SendMessageRequest{Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: localRoom(h.beta)},
		From: protocol.Actor{Type: "agent", ID: "lead"}, Parts: []protocol.Part{{Kind: "text", Text: "after leaving"}}}); err == nil {
		t.Fatal("a network that left still writes")
	}
	h.home.post(t, "after beta left")
	h.alpha.waitTexts(t, "after beta left")
	if got := h.beta.texts(t); len(got) != 0 {
		t.Fatalf("beta received after leaving: %q", got)
	}
}
