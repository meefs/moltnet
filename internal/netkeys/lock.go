package netkeys

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

const lockPoll = 10 * time.Millisecond

// lockWait is a variable so tests can bound how long contention blocks.
var lockWait = 5 * time.Second

// acquireFileLock serializes pin writes between the server and the CLI with
// an advisory flock(2) on a lock file. The kernel releases the lock when the
// holder exits, so a crashed writer can never leave it stuck, and no writer
// ever deletes a lock another process may hold: the lock file itself is
// permanent and only its lock state changes.
func acquireFileLock(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, fileMode)
	if err != nil {
		return nil, fmt.Errorf("lock key pins: %w", err)
	}
	deadline := time.Now().Add(lockWait)
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			_ = file.Close()
			return nil, fmt.Errorf("lock key pins: %w", err)
		}
		if time.Now().After(deadline) {
			_ = file.Close()
			return nil, fmt.Errorf("lock key pins: %s is held by another writer", path)
		}
		time.Sleep(lockPoll)
	}
}
