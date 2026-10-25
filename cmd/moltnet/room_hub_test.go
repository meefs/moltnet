package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/app"
	"github.com/noopolis/moltnet/internal/hubinvites"
	"github.com/noopolis/moltnet/internal/netkeys"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestRoomInviteAndJoinWriteRoomLinksGrantsAndPendingJoins(t *testing.T) {
	t.Setenv("TEST_RELAY_TOKEN", "relay-secret-value")
	homePath := writeMoltnetConfig(t, t.TempDir(), "home-net", "Home")
	identity, _, err := netkeys.LoadOrCreateIdentity(netkeys.DirForConfig(homePath))
	if err != nil {
		t.Fatal(err)
	}
	global := protocol.GlobalRoomID(identity.Fingerprint(), "research")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/rooms/research/hub" {
			http.NotFound(response, request)
			return
		}
		_ = json.NewEncoder(response).Encode(protocol.HubRoomInfo{GlobalRoomID: global, LocalRoomID: "research", HomeNetworkID: "home-net", IsHome: true})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"room", "invite", "research", "--role", "reader", "--id", "room-lab",
			"--relay-url", "wss://relay.example", "--relay-token-env", "TEST_RELAY_TOKEN", "--config", homePath, "--base-url", server.URL}, "test"); err != nil {
			t.Fatalf("room invite: %v", err)
		}
	})
	code := extractInviteCode(t, output)
	invite, err := protocol.DecodeInvite(code)
	if err != nil || invite.HubRoom == nil || invite.HubRoom.GlobalRoomID != global || invite.HubRoom.Role != protocol.RoomRoleReader {
		t.Fatalf("invite = %#v, %v", invite, err)
	}
	if !strings.Contains(output, protocol.DisplayFingerprint(identity.Fingerprint())) {
		t.Fatalf("invite output does not show the home fingerprint:\n%s", output)
	}
	homeConfig, err := app.LoadConfigForPath(homePath, "")
	if err != nil || len(homeConfig.Pairings) != 1 || !homeConfig.Pairings[0].IsRoomLink() {
		t.Fatalf("home pairings = %#v, %v", homeConfig.Pairings, err)
	}
	if grant, ok, _ := hubinvites.Open(netkeys.DirForConfig(homePath)).Grant("room-lab", global, time.Now()); !ok || grant.Role != protocol.RoomRoleReader {
		t.Fatalf("grant = %#v, %v", grant, ok)
	}

	labPath := writeMoltnetConfig(t, t.TempDir(), "lab-net", "Lab")
	captureStdout(t, func() {
		if err := run(context.Background(), []string{"room", "join", code, "--local-room", "home-research", "--config", labPath}, "test"); err != nil {
			t.Fatalf("room join: %v", err)
		}
	})
	labConfig, err := app.LoadConfigForPath(labPath, "")
	if err != nil || len(labConfig.Pairings) != 1 || !labConfig.Pairings[0].IsRoomLink() || labConfig.Pairings[0].RemoteNetworkID != "home-net" {
		t.Fatalf("lab pairings = %#v, %v", labConfig.Pairings, err)
	}
	pin, ok, _ := netkeys.OpenPinStore(netkeys.DirForConfig(labPath)).Lookup("home-net")
	if !ok || pin.Trust != protocol.KeyTrustVerified || pin.Fingerprint != identity.Fingerprint() {
		t.Fatalf("lab's pin for the home = %#v", pin)
	}
	joins, _ := hubinvites.Open(netkeys.DirForConfig(labPath)).Joins()
	if len(joins) != 1 || joins[0].GlobalRoomID != global || joins[0].LocalRoomID != "home-research" || joins[0].PairingID != "room-lab" ||
		!joins[0].Exp.Equal(invite.Exp) {
		t.Fatalf("pending joins = %#v", joins)
	}
}

func TestRoomJoinRefusesAPairingInvite(t *testing.T) {
	t.Setenv("TEST_RELAY_TOKEN", "relay-secret-value")
	path := writeMoltnetConfig(t, t.TempDir(), "alice-net", "Alice")
	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"pair", "invite", "--relay-url", "wss://relay.example", "--relay-token-env", "TEST_RELAY_TOKEN",
			"--id", "friend", "--config", path, "--print-only"}, "test"); err != nil {
			t.Fatal(err)
		}
	})
	other := writeMoltnetConfig(t, t.TempDir(), "bob-net", "Bob")
	err := run(context.Background(), []string{"room", "join", extractInviteCode(t, output), "--config", other}, "test")
	if err == nil || !strings.Contains(err.Error(), "moltnet pair") {
		t.Fatalf("room join with a pairing invite: %v", err)
	}
}

func TestRoomMembersShowsAPendingOrExpiredJoin(t *testing.T) {
	labPath := writeMoltnetConfig(t, t.TempDir(), "lab-net", "Lab")
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	invites := hubinvites.Open(app.KeysDirForConfigPath(labPath))
	global := "sha256:" + strings.Repeat("ab", 32) + "/research"
	if err := invites.AddJoin(hubinvites.PendingJoin{PairingID: "room-lab", GlobalRoomID: global, Exp: time.Now().Add(time.Hour), LastError: "paired network returned HTTP 403 Forbidden"}); err != nil {
		t.Fatal(err)
	}
	members := func() string {
		return captureStdout(t, func() {
			if err := run(context.Background(), []string{"room", "members", "research", "--config", labPath, "--base-url", server.URL}, "test"); err != nil {
				t.Fatalf("room members: %v", err)
			}
		})
	}
	if output := members(); !strings.Contains(output, "is pending") || !strings.Contains(output, "HTTP 403") {
		t.Fatalf("pending join output:\n%s", output)
	}
	joins, _ := invites.Joins()
	joins[0].Status = hubinvites.JoinExpired
	if err := invites.UpdateJoin(joins[0]); err != nil {
		t.Fatal(err)
	}
	if output := members(); !strings.Contains(output, "expired") {
		t.Fatalf("expired join output:\n%s", output)
	}
}
