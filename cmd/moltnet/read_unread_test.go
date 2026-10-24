package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/noopolis/moltnet/pkg/bridgeconfig"
	"github.com/noopolis/moltnet/pkg/clientconfig"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// unreadFixture serves two rooms and one DM. Each target returns its
// messages after the requested cursor, so cursors advancing is observable.
func unreadFixture(t *testing.T, dmsEnabled bool) (string, func(target string, message protocol.Message)) {
	t.Helper()
	workspace := t.TempDir()
	writeClientConfigFixture(t, workspace, clientconfig.Config{
		Version: clientconfig.VersionV1,
		Attachments: []clientconfig.AttachmentConfig{{
			Auth:      clientconfig.AuthConfig{Mode: "none"},
			BaseURL:   "http://127.0.0.1:8787",
			MemberID:  "alpha",
			NetworkID: "local_lab",
			Rooms:     []bridgeconfig.RoomBinding{{ID: "research"}, {ID: "general"}},
			DMs:       &bridgeconfig.DMConfig{Enabled: dmsEnabled},
		}},
	})

	var mu sync.Mutex
	messages := map[string][]protocol.Message{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if request.URL.Path == "/v1/dms" {
			_ = json.NewEncoder(response).Encode(protocol.DirectConversationPage{DMs: []protocol.DirectConversation{{ID: "dm_1"}}})
			return
		}
		list, ok := messages[request.URL.Path]
		if !ok && !strings.HasSuffix(request.URL.Path, "/messages") {
			t.Errorf("unexpected path %s", request.URL.Path)
		}
		after := request.URL.Query().Get("after")
		var page []protocol.Message
		seen := after == ""
		for _, message := range list {
			if seen {
				page = append(page, message)
			}
			if message.ID == after {
				seen = true
			}
		}
		_ = json.NewEncoder(response).Encode(protocol.MessagePage{Messages: page})
	}))
	t.Cleanup(server.Close)
	rewriteClientConfigBaseURL(t, workspace, server.URL)

	add := func(path string, message protocol.Message) {
		mu.Lock()
		defer mu.Unlock()
		messages[path] = append(messages[path], message)
	}
	return workspace, add
}

func textMessage(id string, from string, text string) protocol.Message {
	return protocol.Message{ID: id, From: protocol.Actor{ID: from}, Parts: []protocol.Part{{Kind: "text", Text: text}}}
}

func runReadArgs(t *testing.T, args ...string) string {
	t.Helper()
	return captureStdout(t, func() {
		if err := run(context.Background(), append([]string{"read"}, args...), "test"); err != nil {
			t.Fatalf("run(read %v) error = %v", args, err)
		}
	})
}

func TestRunReadSinceLastWithoutTargetReadsAllUnreadThenIsSilent(t *testing.T) {
	workspace, add := unreadFixture(t, true)
	add("/v1/rooms/research/messages", textMessage("m1", "bob", "hello research"))
	add("/v1/dms/dm_1/messages", textMessage("d1", "carol", "psst"))

	cwd := mustGetwd(t)
	defer func() { _ = os.Chdir(cwd) }()
	if err := os.Chdir(workspace); err != nil {
		t.Fatal(err)
	}

	first := runReadArgs(t, "--since-last")
	want := "room:research (1 new)\n  bob: hello research\ndm:dm_1 (1 new)\n  carol: psst\n"
	if first != want {
		t.Fatalf("first unread output = %q, want %q", first, want)
	}

	if second := runReadArgs(t, "--since-last"); second != "" {
		t.Fatalf("second unread output = %q, want silence", second)
	}

	// Only what arrived since is shown, and the targeted read shares the cursor.
	add("/v1/rooms/general/messages", textMessage("g1", "dave", "line one\nline two"))
	if third := runReadArgs(t, "--since-last"); third != "room:general (1 new)\n  dave: line one\n    line two\n" {
		t.Fatalf("third unread output = %q", third)
	}
	targeted := runReadArgs(t, "--since-last", "room:general")
	if strings.Contains(targeted, `"g1"`) {
		t.Fatalf("targeted read after unread read = %q, want the cursor already advanced", targeted)
	}
}

func TestRunReadSinceLastStopHook(t *testing.T) {
	workspace, add := unreadFixture(t, false)

	cwd := mustGetwd(t)
	defer func() { _ = os.Chdir(cwd) }()
	if err := os.Chdir(workspace); err != nil {
		t.Fatal(err)
	}

	if empty := runReadArgs(t, "--since-last", "--stop-hook"); empty != "" {
		t.Fatalf("stop hook with nothing new = %q, want silence", empty)
	}

	add("/v1/rooms/research/messages", textMessage("m1", "bob", `say "hi"`))
	add("/v1/dms/dm_1/messages", textMessage("d1", "carol", "DMs are disabled, never shown"))
	output := runReadArgs(t, "--since-last", "--stop-hook")
	var payload map[string]string
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("stop hook output %q is not JSON: %v", output, err)
	}
	if payload["decision"] != "block" || !strings.Contains(payload["reason"], "room:research (1 new)\n  bob: say \"hi\"") || strings.Contains(payload["reason"], "carol") {
		t.Fatalf("stop hook payload = %#v", payload)
	}
	if len(payload) != 2 {
		t.Fatalf("stop hook payload has extra keys: %#v", payload)
	}
}

func TestRunReadStopHookRequiresUntargetedSinceLast(t *testing.T) {
	for _, args := range [][]string{{"--stop-hook", "room:general"}, {"--stop-hook", "--since-last", "room:general"}} {
		err := run(context.Background(), append([]string{"read"}, args...), "test")
		if err == nil || !strings.Contains(err.Error(), "--stop-hook requires --since-last with no target") {
			t.Fatalf("read %v error = %v", args, err)
		}
	}
}
