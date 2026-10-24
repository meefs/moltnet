package follow

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"sync"

	"github.com/noopolis/moltnet/pkg/protocol"
)

type scanTarget struct{ room, thread string }

const maxPendingScans = 256

// Overflow is recovered by the periodic poll; remote event bursts cannot grow
// memory or spawn unbounded HTTP requests.
type pendingScans struct {
	mu      sync.Mutex
	targets map[scanTarget]struct{}
	wake    chan struct{}
}

func newPendingScans() *pendingScans {
	return &pendingScans{targets: map[scanTarget]struct{}{}, wake: make(chan struct{}, 1)}
}
func (p *pendingScans) add(target scanTarget) {
	if target.room == "" {
		return
	}
	p.mu.Lock()
	if len(p.targets) < maxPendingScans {
		p.targets[target] = struct{}{}
	}
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}
func (p *pendingScans) take() []scanTarget {
	p.mu.Lock()
	defer p.mu.Unlock()
	targets := make([]scanTarget, 0, len(p.targets))
	for target := range p.targets {
		targets = append(targets, target)
	}
	clear(p.targets)
	return targets
}
func readEvents(reader io.Reader, pending *pendingScans) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	data := ""
	dispatch := func() {
		var event protocol.Event
		if json.Unmarshal([]byte(data), &event) != nil {
			return
		}
		switch {
		case event.Message != nil:
			target := event.Message.Target
			if target.Kind == protocol.TargetKindRoom {
				pending.add(scanTarget{room: target.RoomID})
			}
			if target.Kind == protocol.TargetKindThread {
				pending.add(scanTarget{room: target.RoomID, thread: target.ThreadID})
			}
		case event.Thread != nil:
			pending.add(scanTarget{room: event.Thread.RoomID, thread: event.Thread.ID})
		case event.Room != nil:
			pending.add(scanTarget{room: event.Room.ID})
		}
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			dispatch()
			data = ""
			continue
		}
		if value, ok := strings.CutPrefix(line, "data:"); ok {
			if len(data)+len(value) > 1<<20 {
				return io.ErrShortBuffer
			}
			data += strings.TrimPrefix(value, " ") + "\n"
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return io.EOF
}
