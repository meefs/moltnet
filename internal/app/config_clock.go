package app

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/noopolis/moltnet/pkg/bridgeconfig"
)

func mergeEnvClock(config *Config) error {
	// An explicitly set but empty/malformed value must not silently disable time.
	value, present := os.LookupEnv("MOLTNET_CLOCK")
	if !present {
		return nil
	}
	var clock bridgeconfig.ClockConfig
	if err := json.Unmarshal([]byte(value), &clock); err != nil {
		return fmt.Errorf("MOLTNET_CLOCK: %w", err)
	}
	config.Clock = &clock
	return nil
}
