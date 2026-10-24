package protocol

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// FollowNamespace is reserved for identities imported by a locally configured
// follow. The hash of the follow ID fits identifier limits even for long IDs,
// contains no URI/scoped-ID delimiters, and never depends on origin testimony.
// Real configured network IDs must not use the case-insensitive "follow-" prefix.
func FollowNamespace(id string) string {
	return fmt.Sprintf("follow-%x", sha256.Sum256([]byte(id)))
}

// ValidateNetworkNamespace reserves follow identities without restricting the
// existing grammar of real network IDs. It is for configuration, not claims.
func ValidateNetworkNamespace(id string) error {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(id)), "follow-") {
		return fmt.Errorf("network id prefix follow- is reserved for local follow namespaces")
	}
	return nil
}
