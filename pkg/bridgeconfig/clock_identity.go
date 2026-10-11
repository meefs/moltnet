package bridgeconfig

import (
	"crypto/sha256"
	"fmt"
	"time"
)

// TaskClockHeader carries the server-authored clock identity in HTTP responses.
// An absent header means the server uses real time.
const TaskClockHeader = "Moltnet-Clock"

// Identity compares both clock coordinates, not just their difference. Normalize
// origins to epoch milliseconds so equivalent RFC3339 spellings agree.
func (c *ClockConfig) Identity() (string, error) {
	if c == nil {
		return "", nil
	}
	if err := c.Validate(); err != nil {
		return "", err
	}
	origin, _ := time.Parse(time.RFC3339Nano, c.Origin)
	canonical := fmt.Sprintf("%s\n%d\n%d", c.Version, origin.UnixMilli(), c.AnchorEpochMS)
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(canonical))), nil
}
