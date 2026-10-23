// Package follow pulls public histories into read-only local mirror rooms.
package follow

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

type Checkpoint struct {
	Cursor      string `json:"cursor"`
	Initialized bool   `json:"initialized"`
	Count       int    `json:"count"`
}

type Sink interface {
	EnsureFollowRoom(context.Context, protocol.Follow, protocol.Room) error
	ImportFollowMessage(context.Context, protocol.Follow, string, protocol.Message) (bool, error)
	GetFollowCheckpoint(context.Context, string) (Checkpoint, error)
	SetFollowCheckpoint(context.Context, string, Checkpoint) error
}

type Manager struct {
	sink    Sink
	mu      sync.RWMutex
	workers map[string]*worker
}

type worker struct {
	config protocol.Follow
	sink   Sink
	client *http.Client
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.RWMutex
	status protocol.FollowStatus
}

func New(sink Sink) *Manager { return &Manager{sink: sink, workers: make(map[string]*worker)} }

func (m *Manager) Apply(ctx context.Context, configs []protocol.Follow) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, w := range m.workers {
		w.cancel()
	}
	for _, w := range m.workers {
		<-w.done
	}
	m.workers = make(map[string]*worker, len(configs))
	for _, cfg := range configs {
		cfg = cfg.WithDefaults()
		runCtx, cancel := context.WithCancel(ctx)
		w := &worker{config: cfg, sink: m.sink, client: &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 15 * time.Second}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, cancel: cancel, done: make(chan struct{}), status: protocol.FollowStatus{ID: cfg.ID, RemoteBaseURL: cfg.RemoteBaseURL, RemoteNetworkID: cfg.RemoteNetworkID, Status: "connecting", Rooms: []protocol.FollowRoomStatus{}}}
		m.workers[cfg.ID] = w
		go w.run(runCtx)
	}
}

func (m *Manager) Close() { m.Apply(context.Background(), nil) }

func (m *Manager) Status() []protocol.FollowStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	values := make([]protocol.FollowStatus, 0, len(m.workers))
	for _, w := range m.workers {
		w.mu.RLock()
		status := w.status
		status.Rooms = append([]protocol.FollowRoomStatus(nil), status.Rooms...)
		w.mu.RUnlock()
		values = append(values, status)
	}
	return values
}

func (w *worker) setState(state string, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status.Status = state
	w.status.LastError = ""
	if err != nil {
		w.status.LastError = err.Error()
	}
}

func backoff(attempt int, min, max time.Duration) time.Duration {
	delay := min
	for i := 0; i < attempt && delay < max; i++ {
		delay *= 2
		if delay > max {
			delay = max
		}
	}
	// Full positive jitter within [delay/2, delay].
	return delay/2 + time.Duration(rand.Int64N(int64(delay/2)+1))
}

func (w *worker) run(ctx context.Context) {
	defer close(w.done)
	defer w.client.CloseIdleConnections()
	for attempt := 0; ctx.Err() == nil; attempt++ {
		err := w.session(ctx)
		if ctx.Err() != nil {
			return
		}
		w.mu.RLock()
		wasLive := w.status.Status == "live"
		w.mu.RUnlock()
		if wasLive {
			attempt = 0
		}
		var incompatible *incompatibleError
		if errors.As(err, &incompatible) {
			w.setState("incompatible", err)
		} else {
			w.setState("error", err)
		}
		delay := backoff(attempt, time.Duration(w.config.ReconnectMinSeconds)*time.Second, time.Duration(w.config.ReconnectMaxSeconds)*time.Second)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
