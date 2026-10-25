package rooms_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func hubHTTP(t *testing.T, method, url, token string, payload any) (int, string) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		raw, _ := json.Marshal(payload)
		body = bytes.NewReader(raw)
	}
	request, _ := http.NewRequest(method, url, body)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(raw)
}

func TestHubAdministrationIsOperatorOnly(t *testing.T) {
	h := newHubTopology(t, memoryBackend)
	add := map[string]string{"global_room_id": h.global, "kind": protocol.RoomControlAdd, "pairing_id": "link-alpha"}
	// A member's link credential can neither administer nor list hub rooms.
	if status, body := hubHTTP(t, http.MethodPost, h.home.url+"/v1/hub/rooms/control", "alpha-link-secret", add); status != http.StatusForbidden {
		t.Fatalf("pair credential administered a hub room: %d %s", status, body)
	}
	if status, _ := hubHTTP(t, http.MethodGet, h.home.url+"/v1/hub/rooms", "alpha-link-secret", nil); status != http.StatusForbidden {
		t.Fatalf("pair credential listed hub rooms: %d", status)
	}
	// Before it is added, alpha cannot read the roster either.
	controlURL := h.home.url + "/v1/hub/control?room=" + urlQuery(h.global)
	if status, _ := hubHTTP(t, http.MethodGet, controlURL, "alpha-link-secret", nil); status != http.StatusNotFound {
		t.Fatalf("non-member read the control log: %d", status)
	}
	if status, body := hubHTTP(t, http.MethodPost, h.home.url+"/v1/hub/rooms/control", hubOperatorToken, add); status != http.StatusOK || !strings.Contains(body, `"alpha":"writer"`) {
		t.Fatalf("operator add: %d %s", status, body)
	}
	status, body := hubHTTP(t, http.MethodGet, controlURL, "alpha-link-secret", nil)
	var page protocol.RoomControlPage
	if status != http.StatusOK || json.Unmarshal([]byte(body), &page) != nil || len(page.Events) != 2 {
		t.Fatalf("member control pull: %d %s", status, body)
	}
	// Beta was never added: still nothing.
	if status, _ := hubHTTP(t, http.MethodGet, controlURL, "beta-link-secret", nil); status != http.StatusNotFound {
		t.Fatalf("non-member read the control log after an add: %d", status)
	}
}

func TestHubStaleSubmissionAnswersRetryClassCode(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	stale := hubEnvelope(t, h.alpha.identity, "alpha", h.global, canonicalRoomOf(h.global), "stale-1", "old epoch", 2, 1)
	status, body := hubPost(t, h.home, "alpha-link-secret", stale)
	if status != http.StatusPreconditionFailed || !strings.Contains(body, `"code":"`+protocol.StaleMembershipEpochCode+`"`) {
		t.Fatalf("stale submission: %d %s", status, body)
	}
}

func TestHubRoomRefusesOrdinaryPairRelays(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	// A full pairing to the home relays an ordinary, unsigned-for-hub
	// message straight into the hub room's local id.
	h.home.pairings = append(h.home.pairings, protocol.Pairing{ID: "full-peer", RemoteNetworkID: "gamma", RemoteBaseURL: h.alpha.url, Token: "full-secret"})
	h.home.tokens = append(h.home.tokens, authnPairToken("full-peer", "full-secret"))
	h.home.start(t)
	plain := protocol.SendMessageRequest{ID: "plain-1", Origin: protocol.MessageOrigin{NetworkID: "gamma", MessageID: "plain-1"},
		Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "research"},
		From:   protocol.Actor{Type: "agent", ID: "lead", NetworkID: "gamma"}, Parts: []protocol.Part{{Kind: "text", Text: "side entrance"}}}
	if status, body := hubPost(t, h.home, "full-secret", plain); status != http.StatusForbidden {
		t.Fatalf("ordinary relay into a hub room: %d %s", status, body)
	}
	if got := h.home.texts(t); len(got) != 0 {
		t.Fatalf("hub room stored %q", got)
	}
}
