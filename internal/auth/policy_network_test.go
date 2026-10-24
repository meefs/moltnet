package auth

import (
	"strings"
	"testing"
)

func TestNewPolicyRejectsFollowNetworkBinding(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{ModeBearer, ModeOpen} {
		for _, network := range []string{"follow-peer", "FOLLOW-peer", " follow-peer "} {
			_, err := NewPolicy(Config{Mode: mode, Tokens: []TokenConfig{{Value: "secret", Network: network, Scopes: []Scope{ScopePair}}}})
			if err == nil || !strings.Contains(err.Error(), "auth.tokens[0].network") || !strings.Contains(err.Error(), "reserved") {
				t.Fatalf("mode %q, network %q: expected reserved namespace error, got %v", mode, network, err)
			}
		}
	}
}

func TestNewPolicyAllowsExistingNetworkBindings(t *testing.T) {
	t.Parallel()
	for _, network := range []string{"", "remote", " follow_peer "} {
		if _, err := NewPolicy(Config{Mode: ModeBearer, Tokens: []TokenConfig{{Value: "secret", Network: network, Scopes: []Scope{ScopePair}}}}); err != nil {
			t.Fatalf("network %q: %v", network, err)
		}
	}
}
