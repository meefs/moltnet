package follow

import (
	"bufio"
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
	wake := make(chan struct{}, 1)
	ended := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "data:") {
				select {
				case wake <- struct{}{}:
				default:
				}
			}
		}
		err := scanner.Err()
		if err == nil {
			err = io.EOF
		}
		ended <- err
	}()
	defer func() { cancel(); response.Body.Close(); <-ended }()
	// Subscribe first: messages arriving during backfill cause another scan.
	w.setState("backfilling", nil)
	if err = w.sync(ctx, network.ID); err != nil {
		return err
	}
	w.setState("live", nil)
	ticker := time.NewTicker(time.Duration(w.config.PollIntervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-ended:
			ended <- err
			return err
		case <-wake:
		case <-ticker.C:
		}
		if err = w.sync(ctx, network.ID); err != nil {
			return err
		}
	}
}

func escaped(value string) string { return url.PathEscape(value) }
