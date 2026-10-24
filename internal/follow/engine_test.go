package follow

import (
	"testing"
	"time"
)

func TestBackoffBoundsAndCap(t *testing.T) {
	for attempt := 0; attempt < 100; attempt++ {
		minimum, maximum := time.Second, 8*time.Second
		delay := backoff(attempt, minimum, maximum)
		ceiling := minimum
		for i := 0; i < attempt && ceiling < maximum; i++ {
			ceiling *= 2
		}
		if delay < ceiling/2 || delay > ceiling {
			t.Fatalf("attempt %d delay %s outside [%s,%s]", attempt, delay, ceiling/2, ceiling)
		}
	}
}
