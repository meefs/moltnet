package protocol

import (
	"strings"
	"testing"
)

func TestFollowNamespaceFitsIdentityGrammarAndIsReserved(t *testing.T) {
	for _, id := range []string{"news", "a:b", strings.Repeat("x", 128)} {
		namespace := FollowNamespace(id)
		fqid := AgentFQID(namespace, "writer")
		if err := ValidateMemberID(fqid); err != nil {
			t.Fatal(err)
		}
		if err := ValidateMessageID(namespace); err != nil {
			t.Fatal(err)
		}
		n, a, ok := ParseAgentFQID(fqid)
		if !ok || n != namespace || a != "writer" {
			t.Fatalf("fqid roundtrip: %s", fqid)
		}
		n, a, ok = ParseScopedAgentID(ScopedAgentID(namespace, "writer"))
		if !ok || n != namespace || a != "writer" {
			t.Fatal("scoped roundtrip")
		}
		if ValidateNetworkNamespace(namespace) == nil || ValidateNetworkNamespace(strings.ToUpper(namespace)) == nil {
			t.Fatal("real network can use reserved namespace")
		}
	}
	if FollowNamespace("news") == FollowNamespace("other") {
		t.Fatal("follow namespaces collide")
	}
	if err := ValidateNetworkNamespace("real"); err != nil {
		t.Fatal(err)
	}
}
