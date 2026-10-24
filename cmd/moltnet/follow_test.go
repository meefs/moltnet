package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/noopolis/moltnet/internal/app"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestFollowAddRemoveRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "Moltnet")
	body := roomCreateTestConfigBody("follower", "127.0.0.1:1", "keep-secret", "  []")
	writeRoomCreateTestConfig(t, path, body)
	oldOut, oldErr := stdout, stderr
	stdout, stderr = io.Discard, io.Discard
	defer func() { stdout, stderr = oldOut, oldErr }()
	if err := run(context.Background(), []string{"follow", "add", "https://news.example", "--id", "news", "--room", "general", "--backfill", "0", "--config", path}, ""); err != nil {
		t.Fatal(err)
	}
	config, err := app.LoadConfigForPath(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Follows) != 1 || config.Follows[0].ID != "news" || config.Follows[0].BackfillLimit() != 0 || config.Follows[0].Rooms[0] != "general" {
		t.Fatalf("follows = %#v", config.Follows)
	}
	if config.Auth.Tokens[0].Value != "keep-secret" {
		t.Fatal("existing secret changed")
	}
	if err := run(context.Background(), []string{"follow", "remove", "news", "--config", path}, ""); err != nil {
		t.Fatal(err)
	}
	config, err = app.LoadConfigForPath(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Follows) != 0 {
		t.Fatal("follow not removed")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"follow", "add", "https://news.example", "--backfill", "-1", "--config", path}, ""); err == nil {
		t.Fatal("negative backfill accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(contents) {
		t.Fatal("invalid edit changed config")
	}
}

func TestFollowCLIApplyRoundTrip(t *testing.T) {
	var requests []protocol.ApplyConfigRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/v1/apply" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer keep-secret" {
			t.Errorf("operator token missing")
		}
		var request protocol.ApplyConfigRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		requests = append(requests, request)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"applied":true}`))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "Moltnet")
	writeRoomCreateTestConfig(t, path, roomCreateTestConfigBody("follower", "127.0.0.1:1", "keep-secret", "  []"))
	oldOut, oldErr := stdout, stderr
	stdout, stderr = io.Discard, io.Discard
	defer func() { stdout, stderr = oldOut, oldErr }()
	for _, args := range [][]string{
		{"follow", "add", "https://news.example", "--id", "news", "--config", path, "--base-url", server.URL},
		{"follow", "remove", "news", "--config", path, "--base-url", server.URL},
	} {
		// Explicit URL uses an explicit token override, as other operator commands do.
		t.Setenv("FOLLOW_TEST_TOKEN", "keep-secret")
		args = append(args, "--token-env", "FOLLOW_TEST_TOKEN")
		if err := run(context.Background(), args, ""); err != nil {
			t.Fatal(err)
		}
	}
	if len(requests) != 2 || requests[0].Follows == nil || len(*requests[0].Follows) != 1 || requests[1].Follows == nil || len(*requests[1].Follows) != 0 {
		t.Fatalf("apply requests = %#v", requests)
	}
	for _, request := range requests {
		if len(request.Rooms) != 0 || len(request.Agents) != 0 {
			t.Fatal("follow edit reapplied unrelated config")
		}
	}
}

func TestFollowCLIRefusesEnvironmentOverride(t *testing.T) {
	for _, key := range []string{"MOLTNET_FOLLOWS_JSON", "MOLTNET_FOLLOWS"} {
		t.Run(key, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "Moltnet")
			body := roomCreateTestConfigBody("follower", "127.0.0.1:1", "keep-secret", "  []")
			writeRoomCreateTestConfig(t, path, body)
			t.Setenv(key, `[]`)
			if err := run(context.Background(), []string{"follow", "add", "https://news.example", "--config", path}, ""); err == nil {
				t.Fatal("env override accepted")
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(contents) != body {
				t.Fatal("env override changed config")
			}
		})
	}
}
