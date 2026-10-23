package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestFollowWritebackPreservesJSONSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "moltnet.json")
	body := `{"network":{"id":"local"},"storage":{"kind":"memory"},"auth":{"mode":"bearer","tokens":[{"id":"operator","value":"preserved-secret","scopes":["admin"]}]}}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	follow := protocol.Follow{ID: "news", RemoteBaseURL: "https://news.example", RoomMapping: map[string]string{"general": "archive"}}
	if err := WriteFollow(path, follow); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfigForPath(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if config.Follows[0].LocalRoomID("general") != "archive" || config.Auth.Tokens[0].Value != "preserved-secret" {
		t.Fatal("writeback corrupted options or secret")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFollow(path, follow); err == nil {
		t.Fatal("duplicate follow accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("failed write changed config")
	}
	link := filepath.Join(t.TempDir(), "Moltnet")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := RemoveFollow(link, "news"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink write result = %v", err)
	}
	if err := RemoveFollow(path, "news"); err != nil {
		t.Fatal(err)
	}
	config, err = LoadConfigForPath(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Follows) != 0 || config.Auth.Tokens[0].Value != "preserved-secret" {
		t.Fatal("remove corrupted config")
	}
}
