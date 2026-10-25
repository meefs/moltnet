package rooms_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/internal/hubinvites"
	"github.com/noopolis/moltnet/internal/rooms"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// linkAs adds a second room link between home and member under explicit ids
// (another room invite to the same peer). Keys are pinned already.
func linkAs(home, member *hubNet, homeLink, memberLink, secret string) {
	home.pairings = append(home.pairings, protocol.Pairing{ID: homeLink, RemoteBaseURL: member.url, Token: protocol.SecretString(secret), Scope: protocol.PairingScopeRooms})
	home.tokens = append(home.tokens, authn.TokenConfig{ID: homeLink, Value: secret, Scopes: []authn.Scope{authn.ScopePair}})
	member.pairings = append(member.pairings, protocol.Pairing{ID: memberLink, RemoteNetworkID: home.id, RemoteBaseURL: home.url, Token: protocol.SecretString(secret), Scope: protocol.PairingScopeRooms})
	member.tokens = append(member.tokens, authn.TokenConfig{ID: memberLink, Value: secret, Network: home.id, Scopes: []authn.Scope{authn.ScopePair}})
}

// admit redeems a room invite: the home's grant for homeLink, the member's
// signed admission over memberLink.
func admit(t *testing.T, home, member *hubNet, global, homeLink, memberLink, localRoom string) {
	t.Helper()
	if err := home.invites.AddGrant(hubinvites.Grant{PairingID: homeLink, GlobalRoomID: global, Role: protocol.RoomRoleWriter, Exp: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := member.service.JoinHubRoomContext(contextBackground(), rooms.HubJoinRequest{PairingID: memberLink, GlobalRoomID: global, LocalRoomID: localRoom, Admit: true}); err != nil {
		t.Fatalf("%s admission over %s: %v", member.id, memberLink, err)
	}
}

func (n *hubNet) roomTexts(t *testing.T, roomID string) []string {
	t.Helper()
	page, err := n.service.ListRoomMessages(roomID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	texts := []string{}
	for _, message := range page.Messages {
		texts = append(texts, message.Parts[0].Text)
	}
	return texts
}

func (n *hubNet) waitRoomTexts(t *testing.T, roomID string, want ...string) {
	t.Helper()
	waitFor(t, func() bool { return equalStrings(n.roomTexts(t, roomID), want) })
}

// A network the home is already paired with directly also joins a shared
// room over a room link: admission, submissions and live fanout all use
// the room link, in both directions of the direct pairing.
func TestHubMemberWithADirectPairingUsesItsRoomLink(t *testing.T) {
	for name, configBound := range map[string]bool{"home invited the peer directly": false, "home joined the peer's direct invite": true} {
		t.Run(name, func(t *testing.T) {
			h := newHubTopology(t, memoryBackend)
			secret := "alpha-friend-secret"
			friend := protocol.Pairing{ID: "friend-alpha", RemoteBaseURL: h.alpha.url, Token: protocol.SecretString(secret)}
			token := authn.TokenConfig{ID: "friend-alpha", Value: secret, Scopes: []authn.Scope{authn.ScopePair}}
			if configBound {
				friend.RemoteNetworkID, token.Network = "alpha", "alpha"
			} else if err := h.home.pins.BindPairing("friend-alpha", "alpha"); err != nil {
				t.Fatal(err)
			}
			h.home.pairings, h.home.tokens = append(h.home.pairings, friend), append(h.home.tokens, token)
			h.home.start(t)

			admit(t, h.home, h.alpha, h.global, "link-alpha", "link-home", localRoom(h.alpha))
			h.home.post(t, "hello alpha")
			h.alpha.waitTexts(t, "hello alpha") // live: no catch-up runs in this window
			if state := h.home.delivery(t, "link-alpha"); state.Rejected != 0 {
				t.Fatalf("home -> alpha over the room link: %#v", state)
			}
			h.alpha.post(t, "alpha speaks")
			h.home.waitTexts(t, "hello alpha", "alpha speaks")
			if state := h.alpha.delivery(t, "link-home"); state.Rejected != 0 {
				t.Fatalf("alpha -> home over the room link was refused: %#v", state)
			}
		})
	}
}

// twoRooms shares a second room with alpha over a second room link.
func twoRooms(t *testing.T) (hubTopology, string) {
	t.Helper()
	h := newHubTopology(t, memoryBackend)
	linkAs(h.home, h.alpha, "link2-alpha", "link2-home", "alpha-link2-secret")
	h.home.start(t)
	h.alpha.start(t)
	if _, err := h.home.service.CreateRoom(protocol.CreateRoomRequest{ID: "ops", Members: []string{"lead"}}); err != nil {
		t.Fatal(err)
	}
	view, err := h.home.service.CreateHubRoomContext(context.Background(), "ops", "")
	if err != nil {
		t.Fatal(err)
	}
	admit(t, h.home, h.alpha, h.global, "link-alpha", "link-home", localRoom(h.alpha))
	admit(t, h.home, h.alpha, view.GlobalRoomID, "link2-alpha", "link2-home", "shared2-alpha")
	return h, view.GlobalRoomID
}

func TestHubTwoRoomsWithOnePeerFanOutOverEachLink(t *testing.T) {
	h, _ := twoRooms(t)
	h.home.post(t, "in research")
	if _, err := h.home.service.SendMessage(protocol.SendMessageRequest{Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "ops"},
		From: protocol.Actor{Type: "agent", ID: "lead"}, Parts: []protocol.Part{{Kind: "text", Text: "in ops"}}}); err != nil {
		t.Fatal(err)
	}
	h.alpha.waitTexts(t, "in research")
	h.alpha.waitRoomTexts(t, "shared2-alpha", "in ops")
}

// A room link is a credential for its own room only: alpha's link for
// research may not read ops' sync or control log, leave ops, or submit to
// ops, although alpha is an ops member and its signature is valid.
func TestHubRoomLinkCredentialIsScopedToItsRoom(t *testing.T) {
	h, ops := twoRooms(t)
	researchLink := "alpha-link-secret"
	for _, path := range []string{"/v1/hub/sync?after=0&room=" + urlQuery(ops), "/v1/hub/control?after=0&room=" + urlQuery(ops)} {
		if status, body := hubHTTP(t, http.MethodGet, h.home.url+path, researchLink, nil); status != http.StatusNotFound {
			t.Fatalf("research link read %s: %d %s", path, status, body)
		}
	}
	if status, body := hubHTTP(t, http.MethodPost, h.home.url+"/v1/hub/leave", researchLink, protocol.RoomLeaveRequest{GlobalRoomID: ops}); status != http.StatusNotFound {
		t.Fatalf("research link left ops: %d %s", status, body)
	}
	views, err := h.home.service.HubRoomsContext(contextBackground())
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		if _, member := view.Members["alpha"]; !member {
			t.Fatalf("alpha is no longer a member of %s", view.GlobalRoomID)
		}
	}
	submission := hubEnvelope(t, h.alpha.identity, "alpha", ops, "ops", "cross-1", "into ops over the research link", 2, 1)
	if status, body := hubPost(t, h.home, researchLink, submission); status != http.StatusForbidden {
		t.Fatalf("research link submitted to ops: %d %s", status, body)
	}
	// Its own link still works for each room.
	if status, body := hubHTTP(t, http.MethodGet, h.home.url+"/v1/hub/sync?after=0&room="+urlQuery(ops), "alpha-link2-secret", nil); status != http.StatusOK {
		t.Fatalf("ops link read ops sync: %d %s", status, body)
	}
}

// Strict pair-network binding: the room link recorded at admission is a
// durable binding, so a strict home admits by invite and accepts the
// member's submissions.
func TestHubStrictBindingHomeAdmitsAndAcceptsRoomLinkMembers(t *testing.T) {
	h := newHubTopology(t, memoryBackend)
	h.home.strict = true
	h.home.start(t)
	admit(t, h.home, h.alpha, h.global, "link-alpha", "link-home", localRoom(h.alpha))
	h.alpha.post(t, "alpha speaks")
	h.home.waitTexts(t, "alpha speaks")
	if state := h.alpha.delivery(t, "link-home"); state.Rejected != 0 {
		t.Fatalf("strict home refused the member: %#v", state)
	}
}

// A network the home is paired with directly (config-bound) but whose key
// it never pinned cannot be claimed first through a room invite: whoever
// holds the invite would pin its own key under that network's id.
func TestHubAdmissionCannotClaimADirectlyPairedNetworkWithNoPin(t *testing.T) {
	h := newHubTopology(t, memoryBackend)
	h.home.pairings = append(h.home.pairings, protocol.Pairing{ID: "friend-alpha", RemoteNetworkID: "alpha", RemoteBaseURL: h.alpha.url, Token: "friend-secret"})
	h.home.tokens = append(h.home.tokens, authn.TokenConfig{ID: "friend-alpha", Value: "friend-secret", Network: "alpha", Scopes: []authn.Scope{authn.ScopePair}})
	if _, err := h.home.pins.Forget("alpha"); err != nil {
		t.Fatal(err)
	}
	h.home.start(t)
	if err := h.home.invites.AddGrant(hubinvites.Grant{PairingID: "link-alpha", GlobalRoomID: h.global, Role: protocol.RoomRoleWriter, Exp: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.alpha.service.JoinHubRoomContext(contextBackground(), rooms.HubJoinRequest{PairingID: "link-home", GlobalRoomID: h.global, Admit: true}); err == nil {
		t.Fatal("a room invite claimed a directly paired network with no pinned key")
	}
	if _, pinned, _ := h.home.pins.Lookup("alpha"); pinned {
		t.Fatal("the refused admission pinned a key")
	}
}
