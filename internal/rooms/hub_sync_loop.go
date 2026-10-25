package rooms

import (
	"context"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

const defaultHubSyncInterval = 30 * time.Second

func (s *Service) configureHubSync(config ServiceConfig) {
	s.hubSyncInterval = positiveDuration(config.HubSyncInterval, defaultHubSyncInterval)
	s.hubSyncWake = make(chan struct{}, 1)
}

func pairingStatusUp(status string) bool {
	return status == protocol.PairingStatusConnected || status == protocol.PairingStatusDegraded
}

// requestHubSync wakes the catch-up loop (a gap, a link coming up).
func (s *Service) requestHubSync() {
	select {
	case s.hubSyncWake <- struct{}{}:
	default:
	}
}

// runHubSyncLoop catches member hub rooms up at start, on every wake, and
// on a slow timer, and finishes pending joins.
func (s *Service) runHubSyncLoop() {
	defer s.relayWG.Done()
	ticker := time.NewTicker(s.hubSyncInterval)
	defer ticker.Stop()
	for {
		s.syncAllHubRooms(s.lifecycleCtx)
		select {
		case <-s.lifecycleCtx.Done():
			return
		case <-ticker.C:
		case <-s.hubSyncWake:
		}
	}
}

func (s *Service) syncAllHubRooms(ctx context.Context) {
	if s.hubs == nil || s.identity == nil {
		return
	}
	s.recoverHubJoins(ctx)
	rooms, err := s.hubRooms(ctx)
	if err != nil {
		return
	}
	for _, hub := range rooms {
		if hub.IsHome() || ctx.Err() != nil {
			continue
		}
		attempt, cancel := context.WithTimeout(ctx, 4*s.deliveryTimeout)
		if err := s.syncHubRoom(attempt, hub); err != nil {
			logHub(ctx, hub.GlobalRoomID).Warn("hub room catch-up failed; will retry", "error", err)
		}
		cancel()
	}
}
