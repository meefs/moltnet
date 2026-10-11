package core

import (
	"fmt"
	"net/http"

	"github.com/noopolis/moltnet/pkg/bridgeconfig"
)

func checkClockIdentity(headers http.Header, expected string) error {
	values := headers.Values(bridgeconfig.TaskClockHeader)
	reported := headers.Get(bridgeconfig.TaskClockHeader)
	if len(values) > 1 || len(values) == 1 && reported == "" {
		return fmt.Errorf("moltnet compatibility preflight: invalid server clock identity header")
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
	return fmt.Errorf("moltnet compatibility preflight: clock mismatch: bridge expects %s, server reports %s; configure identical origin and anchor on both", expected, reported)
}
