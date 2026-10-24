package app

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestFollowSQLiteOriginEqualTimestampGap(t *testing.T) {
	for _, tc := range []struct {
		name              string
		backfill, initial int
	}{{"all", 200, 3}, {"limited", 1, 1}, {"none", 0, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			config := followTestConfig("origin", true)
			config.Storage = StorageConfig{Kind: storageKindSQLite, SQLite: SQLiteStorageConfig{Path: filepath.Join(t.TempDir(), "origin.db")}}
			// Task clocks have millisecond precision. Equal timestamps are normal;
			// message IDs have no monotonic ordering guarantee.
			at := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
			origin, err := newWithRealClock(config, func() time.Time { return at })
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = origin.Close() })
			for _, id := range []string{"x_old", "y_old", "z_baseline"} {
				followSend(t, origin.Handler(), id, "public", protocol.TargetKindRoom, "")
			}
			gate := &followOriginGate{handler: origin.Handler(), streams: make(map[int]context.CancelFunc)}
			remote := httptest.NewServer(gate)
			t.Cleanup(remote.Close)
			n := tc.backfill
			follower := newFollowApp(t, "follower", false, []protocol.Follow{{ID: "news", RemoteBaseURL: remote.URL, Backfill: &n, PollIntervalSeconds: 3600}})
			followWait(t, func() bool { return followStatus(t, follower.Handler()).Status == "live" })
			if got := followMessageCount(follower.Handler(), "news.public"); got != tc.initial {
				t.Fatalf("initial count=%d want%d", got, tc.initial)
			}
			followSend(t, origin.Handler(), "m_live", "public", protocol.TargetKindRoom, "")
			followWait(t, func() bool { return followMessageCount(follower.Handler(), "news.public") == tc.initial+1 })
			gate.disconnect()
			followSend(t, origin.Handler(), "a_gap", "public", protocol.TargetKindRoom, "")
			gate.reconnect()
			followWait(t, func() bool { return followMessageCount(follower.Handler(), "news.public") == tc.initial+2 })
		})
	}
}
