package follow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

type incompatibleError struct{ reason string }

func (e *incompatibleError) Error() string { return e.reason }

func (w *worker) get(ctx context.Context, path string, target any) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(w.config.RemoteBaseURL, "/")+path, nil)
	if err != nil {
		return err
	}
	response, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if path == "/v1/network" && (response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden) {
			return &incompatibleError{"origin does not expose anonymous public discovery"}
		}
		return fmt.Errorf("GET %s: HTTP %d", path, response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(target)
}

func (w *worker) session(ctx context.Context) error {
	w.setState("connecting", nil)
	var network protocol.Network
	if err := w.get(ctx, "/v1/network", &network); err != nil {
		return err
	}
	compatible := false
	for _, version := range network.Protocols.HTTP {
		if version == protocol.HTTPProtocolV1 {
			compatible = true
		}
	}
	if !network.Capabilities.PublicRead || !compatible || network.Capabilities.MessagePagination != "cursor" || network.Capabilities.EventStream != "sse" {
		return &incompatibleError{"origin requires public_read, compatible HTTP, cursor pagination and SSE"}
	}
	if network.ID == "" || w.config.RemoteNetworkID != "" && network.ID != w.config.RemoteNetworkID {
		return &incompatibleError{"remote_network_id does not match origin"}
	}
	// Pin even unconfigured remote identity across reconnects and process restarts.
	identityKey := w.config.ID + "/" + w.config.RemoteBaseURL + "/network"
	identity, err := w.sink.GetFollowCheckpoint(ctx, identityKey)
	if err != nil {
		return err
	}
	if identity.Initialized && identity.Cursor != network.ID {
		return &incompatibleError{"origin network identity changed"}
	}
	if !identity.Initialized {
		if err = w.sink.SetFollowCheckpoint(ctx, identityKey, Checkpoint{Initialized: true, Cursor: network.ID}); err != nil {
			return err
		}
	}
	w.mu.Lock()
	w.status.RemoteNetworkID = network.ID
	w.mu.Unlock()
	// Establish the first-sync cutoff before subscribing. Once these durable
	// cursors exist, a second history scan after subscription covers every gap,
	// including messages arriving while the SSE handshake is in progress.
	w.setState("backfilling", nil)
	if err = w.sync(ctx, network.ID); err != nil {
		return err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, strings.TrimRight(w.config.RemoteBaseURL, "/")+"/v1/events/stream", nil)
	if err != nil {
		return err
	}
	response, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("SSE HTTP %d", response.StatusCode)
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		return fmt.Errorf("origin did not return SSE")
	}
	pending := newPendingScans()
	ended := make(chan error, 1)
	go func() { ended <- readEvents(response.Body, pending) }()
	defer func() { cancel(); response.Body.Close(); <-ended }()
	// The baseline cursors precede this subscription; scan the gap now.
	w.setState("backfilling", nil)
	if err = w.sync(ctx, network.ID); err != nil {
		return err
	}
	w.setState("live", nil)
	ticker := time.NewTicker(time.Duration(w.config.PollIntervalSeconds) * time.Second)
	defer ticker.Stop()
	// One scan runs at a time per follow. A fixed window coalesces bursts
	// without letting a continuous event stream postpone work indefinitely.
	var debounce <-chan time.Time
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-ended:
			ended <- err
			return err
		case <-pending.wake:
			if debounce == nil {
				timer = time.NewTimer(50 * time.Millisecond)
				debounce = timer.C
			}
		case <-debounce:
			debounce = nil
			for _, target := range pending.take() {
				w.syncTarget(ctx, network.ID, target)
			}
		case <-ticker.C:
			if err = w.sync(ctx, network.ID); err != nil {
				return err
			}
		}
	}
}

func escaped(value string) string { return url.PathEscape(value) }
