package bridgeconfig

import (
	"strings"
	"testing"
	"time"
)

func TestClockRuntimeBounds(t *testing.T) {
	for _, test := range []struct {
		origin string
		step   time.Duration
	}{
		{"9999-12-31T23:59:59.999Z", time.Millisecond},
		{"0001-01-01T00:00:00Z", -time.Millisecond},
	} {
		t.Run(test.origin, func(t *testing.T) {
			real := time.UnixMilli(0)
			config := ClockConfig{Version: TaskClockVersion, Origin: test.origin, AnchorEpochMS: 0}
			now, err := config.Now(func() time.Time { return real })
			if err != nil {
				t.Fatal(err)
			}
			if _, err := now(); err != nil {
				t.Fatalf("boundary itself must be valid: %v", err)
			}
			real = real.Add(test.step)
			value, err := now()
			if err == nil || !strings.Contains(err.Error(), "clock result is outside") || !value.IsZero() {
				t.Fatalf("exhausted clock emitted %v, %v", value, err)
			}
		})
	}
}
