package core

import (
	"fmt"
	"net/http"

	bridgeutil "github.com/noopolis/moltnet/internal/bridge"
)

func checkClockIdentity(headers http.Header, expected string) error {
	if err := bridgeutil.CheckClockIdentity(headers, expected); err != nil {
		return fmt.Errorf("moltnet compatibility preflight: %w", err)
	}
	return nil
}
