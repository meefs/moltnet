package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFollowsConfigValidation(t *testing.T) {
	for _, test := range []struct{ name, body, want string }{
		{"valid", "follows:\n  - id: news\n    remote_base_url: https://news.example\n    backfill: 0\n", ""},
		{"unknown", "follows:\n  - id: news\n    remote_base_url: https://news.example\n    typo: true\n", "typo"},
		{"scheme", "follows:\n  - id: news\n    remote_base_url: file:///tmp/news\n", "http/https"},
		{"duplicate", "follows:\n  - {id: news, remote_base_url: https://news.example}\n  - {id: news, remote_base_url: https://other.example}\n", "duplicate follow"},
		{"negative", "follows:\n  - {id: news, remote_base_url: https://news.example, backfill: -1}\n", "backfill"},
		{"collision", "rooms:\n  - id: news.general\nfollows:\n  - {id: news, remote_base_url: https://news.example}\n", "collides"},
		{"mapped collision", "rooms:\n  - id: general\nfollows:\n  - id: news\n    remote_base_url: https://news.example\n    rooms: [general]\n    room_mapping: {general: general}\n", "collides"},
		{"tuning", "follows:\n  - {id: news, remote_base_url: https://news.example, reconnect_min_seconds: 4, reconnect_max_seconds: 2}\n", "intervals"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "Moltnet")
			if err := os.WriteFile(path, []byte("storage: {kind: memory}\n"+test.body), 0600); err != nil {
				t.Fatal(err)
			}
			config, err := LoadFile(path, "")
			if test.want != "" {
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("error = %v, want %q", err, test.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(config.Follows) != 1 || config.Follows[0].BackfillLimit() != 0 {
				t.Fatalf("follows = %#v", config.Follows)
			}
		})
	}
}

func TestFollowsEnvStrictAndOverrides(t *testing.T) {
	t.Setenv("MOLTNET_FOLLOWS_JSON", `[{"id":"news","remote_base_url":"https://example.com"}]`)
	t.Setenv("MOLTNET_BASE_URL", "https://local.example")
	config, err := ConfigFromEnv("")
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Follows) != 1 || config.PublicBaseURL != "https://local.example" {
		t.Fatalf("config = %#v", config)
	}
	t.Setenv("MOLTNET_FOLLOWS_JSON", `[{"id":"news","remote_base_url":"https://example.com","unknown":true}]`)
	if _, err := ConfigFromEnv(""); err == nil {
		t.Fatal("unknown env follow field accepted")
	}
	t.Setenv("MOLTNET_FOLLOWS_JSON", `[] []`)
	if _, err := ConfigFromEnv(""); err == nil {
		t.Fatal("trailing JSON accepted")
	}
}

func TestPublicBaseURLValidation(t *testing.T) {
	for _, value := range []string{"https://user:pass@example.com", "https://example.com?q=1", "https://example.com#fragment", "ftp://example.com"} {
		t.Run(value, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "Moltnet")
			writeConfigFile(t, path, "server:\n  base_url: "+value+"\n")
			if _, err := LoadFile(path, ""); err == nil {
				t.Fatal("unsafe base URL accepted")
			}
		})
	}
}
