package rooms_test

import (
	"strings"
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/hubinvites"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// A pending join records why it has not finished, and once its invite has
// expired it stops retrying and ends in a terminal status, even if a grant
// would now let it in.
func TestPendingJoinRecordsFailuresAndExpires(t *testing.T) {
	h := newHubTopology(t, memoryBackend)
	if err := h.beta.invites.AddJoin(hubinvites.PendingJoin{PairingID: "link-home", GlobalRoomID: h.global, Exp: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	h.beta.start(t)
	waitFor(t, func() bool {
		joins, _ := h.beta.invites.Joins()
		return len(joins) == 1 && strings.Contains(joins[0].LastError, "403")
	})
	joins, _ := h.beta.invites.Joins()
	if !joins[0].Pending() {
		t.Fatalf("a refused join went terminal before its invite expired: %#v", joins[0])
	}

	joins[0].Exp = time.Now().Add(-time.Second)
	if err := h.beta.invites.UpdateJoin(joins[0]); err != nil {
		t.Fatal(err)
	}
	if err := h.home.invites.AddGrant(hubinvites.Grant{PairingID: "link-beta", GlobalRoomID: h.global, Role: protocol.RoomRoleWriter, Exp: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	h.beta.start(t)
	waitFor(t, func() bool {
		joins, _ := h.beta.invites.Joins()
		return len(joins) == 1 && joins[0].Status == hubinvites.JoinExpired
	})
	h.beta.start(t)
	time.Sleep(100 * time.Millisecond)
	if h.beta.joined(t, h.global) {
		t.Fatal("an expired pending join was still redeemed")
	}
	if _, live, _ := h.home.invites.Grant("link-beta", h.global, time.Now()); !live {
		t.Fatal("the expired join was attempted (the grant was consumed)")
	}
}
