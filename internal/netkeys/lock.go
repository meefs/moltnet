package netkeys

import (
	"errors"
	"fmt"
	"os"
	"time"
)

const (
	lockWait  = 5 * time.Second
	lockStale = 30 * time.Second
	lockPoll  = 10 * time.Millisecond
)

// acquireFileLock serializes pin writes between the server and the CLI with
// an exclusive-create lock file (portable, stdlib only). A lock older than
// lockStale is treated as left behind by a crashed writer and removed.
func acquireFileLock(path string) (func(), error) {
	deadline := time.Now().Add(lockWait)
	for {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
		if err == nil {
			_ = file.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("lock key pins: %w", err)
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > lockStale {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("lock key pins: %s is held by another writer", path)
		}
		time.Sleep(lockPoll)
	}
}
