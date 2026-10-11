package bridge

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/noopolis/moltnet/pkg/bridgeconfig"
)

// ErrClockMismatch is terminal: reconnecting cannot repair incompatible clocks.
var ErrClockMismatch = errors.New("clock mismatch")

// CheckClockIdentity validates the server-authored identity on each connection.
func CheckClockIdentity(headers http.Header, expected string) error {
	values := headers.Values(bridgeconfig.TaskClockHeader)
	reported := headers.Get(bridgeconfig.TaskClockHeader)
	if len(values) > 1 || len(values) == 1 && reported == "" {
		return fmt.Errorf("%w: invalid server clock identity header", ErrClockMismatch)
	}
	if reported == expected {
		return nil
	}
	if expected == "" {
		expected = "unclocked (real time)"
	}
	if reported == "" {
		reported = "unclocked (real time)"
	}
	return fmt.Errorf("%w: bridge expects %s, server reports %s; configure identical origin and anchor on both", ErrClockMismatch, expected, reported)
}
