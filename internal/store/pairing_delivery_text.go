package store

import (
	"strings"
	"unicode"
)

// Delivery diagnostics are bounded, valid text on every backend. In particular,
// PostgreSQL TEXT rejects NUL and invalid UTF-8; one diagnostic must not poison
// a batch containing results for unrelated pairings.
func pairingDeliveryText(value string) string {
	const maxRunes = 1024
	count := 0
	clean := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		count++
		if count > maxRunes {
			return -1
		}
		return r
	}, strings.ToValidUTF8(value, "\uFFFD"))
	if clean == "" && value != "" {
		return "delivery failed (no printable diagnostic)"
	}
	return clean
}
