package netkeys

import (
	"os"
	"syscall"
	"time"
)

// pinCache holds the last decoded pins.json, keyed by the file's stamp.
// Writers replace the file by rename, so a new inode, size or mtime means a
// re-read; `moltnet keys trust/forget` still take effect without a restart.
type pinCache struct {
	valid bool
	stamp fileStamp
	pins  map[string]Pin
}

type fileStamp struct {
	modified time.Time
	size     int64
	inode    uint64
}

func stampOf(info os.FileInfo) fileStamp {
	stamp := fileStamp{modified: info.ModTime(), size: info.Size()}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		stamp.inode = uint64(stat.Ino)
	}
	return stamp
}

func copyPins(pins map[string]Pin) map[string]Pin {
	copied := make(map[string]Pin, len(pins))
	for id, pin := range pins {
		copied[id] = pin
	}
	return copied
}
