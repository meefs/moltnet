package rooms_test

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// A member network's agents read the shared room this network joined:
// history, discovery and the live event stream, with an agent-restricted
// credential that is no explicit member of the provisioned local room.
func TestHubMemberAgentsReadTheJoinedRoom(t *testing.T) {
	h := newHubTopology(t, memoryBackend)
	h.alpha.tokens = append(h.alpha.tokens, authn.TokenConfig{ID: "agent-scout", Value: "scout-secret", Scopes: []authn.Scope{authn.ScopeObserve, authn.ScopeWrite}, Agents: []string{"scout"}})
	h.alpha.start(t)
	h.add(t, h.alpha)
	h.join(t, h.alpha)
	h.home.post(t, "hello agents")
	h.alpha.waitTexts(t, "hello agents")

	if status, body := hubHTTP(t, http.MethodGet, h.alpha.url+"/v1/rooms/shared-alpha/messages", "scout-secret", nil); status != http.StatusOK || !strings.Contains(body, "hello agents") {
		t.Fatalf("agent history read: %d %s", status, body)
	}
	if body := pairGet(t, h.alpha.url+"/v1/rooms", "scout-secret"); !strings.Contains(body, `"shared-alpha"`) {
		t.Fatalf("agent room list lacks the joined room: %s", body)
	}
	// Ordinary private rooms stay closed to it.
	if _, err := h.alpha.service.CreateRoom(protocol.CreateRoomRequest{ID: "private-ops", Members: []string{"lead"}}); err != nil {
		t.Fatal(err)
	}
	if status, _ := hubHTTP(t, http.MethodGet, h.alpha.url+"/v1/rooms/private-ops/messages", "scout-secret", nil); status != http.StatusForbidden {
		t.Fatalf("agent read an ordinary private room: %d", status)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, h.alpha.url+"/v1/events/stream", nil)
	request.Header.Set("Authorization", "Bearer scout-secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("agent event stream: %v %v", response, err)
	}
	defer response.Body.Close()
	go func() {
		time.Sleep(100 * time.Millisecond)
		h.home.post(t, "streamed to agents")
	}()
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "streamed to agents") {
			return
		}
	}
	t.Fatal("the agent's event stream never carried the joined room's message")
}
