package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestFollowDuplicateDiscoveryClaimsCannotImpersonateOrSuppress(t *testing.T) {
	// Both real HTTP origins self-declare "good" and serve the exact same ID.
	good := newFollowApp(t, "good", true, nil)
	evil := newFollowApp(t, "good", true, nil)
	for _, origin := range []*App{evil, good} {
		r := followRequest(origin.Handler(), http.MethodPost, "/v1/messages", "operator", protocol.SendMessageRequest{ID: "g1", Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "public"}, From: protocol.Actor{Type: "agent", ID: "writer"}, Parts: []protocol.Part{{Kind: "text", Text: "one"}}})
		if r.Code != http.StatusAccepted {
			t.Fatal(r.Body.String())
		}
	}
	a, b := httptest.NewServer(evil.Handler()), httptest.NewServer(good.Handler())
	t.Cleanup(a.Close)
	t.Cleanup(b.Close)
	follower := newFollowApp(t, "local", false, []protocol.Follow{{ID: "evil-follow", RemoteBaseURL: a.URL}, {ID: "good-follow", RemoteBaseURL: b.URL}})
	followWait(t, func() bool {
		r := followRequest(follower.Handler(), http.MethodGet, "/v1/follows", "operator", nil)
		var p protocol.FollowPage
		_ = json.Unmarshal(r.Body.Bytes(), &p)
		return len(p.Follows) == 2 && p.Follows[0].Status == "live" && p.Follows[1].Status == "live"
	})
	var imported []protocol.Message
	for _, id := range []string{"evil-follow", "good-follow"} {
		page := followMessages(t, follower.Handler(), id+".public")
		if len(page.Messages) != 1 {
			t.Fatalf("%s mirror suppressed: %+v", id, page)
		}
		m := page.Messages[0]
		if !strings.HasPrefix(m.From.NetworkID, "follow-") || strings.Contains(m.From.FQID, "molt://good/") || m.From.CredentialBound {
			t.Fatalf("origin impersonation: %+v", m.From)
		}
		if m.Origin.NetworkID != m.From.NetworkID || m.Origin.MessageID != "g1" || m.Origin.ReceivedVia != "follow:"+id {
			t.Fatalf("untrusted provenance: %+v", m.Origin)
		}
		var origin map[string]any
		raw, _ := json.Marshal(m.Origin)
		_ = json.Unmarshal(raw, &origin)
		if origin["claimed_network_id"] != "good" || origin["claimed_message_id"] != "g1" {
			t.Fatalf("lost claims: %s", raw)
		}
		imported = append(imported, m)
	}
	if imported[0].ID == imported[1].ID || imported[0].From.FQID == imported[1].From.FQID {
		t.Fatal("follows share message or author identity")
	}
	r := followRequest(follower.Handler(), http.MethodGet, "/v1/follows", "operator", nil)
	var status struct {
		Follows []struct {
			Claimed   string `json:"claimed_network_id"`
			Warning   string `json:"warning"`
			LastError string `json:"last_error"`
		} `json:"follows"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	for _, s := range status.Follows {
		if s.Claimed != "good" || !strings.Contains(s.Warning, "claimed network id") || s.LastError != "" {
			t.Fatalf("duplicate claim diagnostics: %+v", s)
		}
	}
}

func TestFollowNamespaceCannotBeConfiguredAsRealNetwork(t *testing.T) {
	cfg := followTestConfig(protocol.FollowNamespace("news"), false)
	if instance, err := New(cfg); err == nil {
		instance.Close()
		t.Fatal("reserved network accepted at startup")
	}
	raw := rawConfigFile{}
	raw.Network.ID = cfg.NetworkID
	if err := validateConfigFile(raw); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("reserved file network accepted: %v", err)
	}
	if err := mergeEnvFollows(&cfg); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("reserved env network accepted: %v", err)
	}
}
