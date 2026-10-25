package rooms

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// HubJoinRequest provisions a local room for a hub room this network was
// added to. LocalRoomID defaults to the home's room id.
type HubJoinRequest struct {
	PairingID    string `json:"pairing_id"`
	GlobalRoomID string `json:"global_room_id"`
	LocalRoomID  string `json:"local_room_id,omitempty"`
	// Admit redeems a room invite first: the home admits this network over
	// the invite's pairing (a pending join from `moltnet room join`).
	Admit bool `json:"admit,omitempty"`
}

type roomControlClient interface {
	FetchRoomControl(ctx context.Context, pairing protocol.Pairing, globalRoomID string, afterSeq int64) (protocol.RoomControlPage, error)
}

func (s *Service) fetchRoomControl(ctx context.Context, pairing protocol.Pairing, globalRoomID string, afterSeq int64) ([]protocol.SignedRoomControl, error) {
	client, ok := s.pairingClient.(roomControlClient)
	if !ok {
		return nil, hubUnavailableError("pairing client cannot fetch hub room control logs")
	}
	var events []protocol.SignedRoomControl
	for {
		page, err := client.FetchRoomControl(ctx, pairing, globalRoomID, afterSeq)
		if err != nil {
			return nil, err
		}
		if page.GlobalRoomID != globalRoomID {
			return nil, hubInvalidError("home answered for a different hub room")
		}
		events = append(events, page.Events...)
		if !page.More || len(page.Events) == 0 {
			return events, nil
		}
		last, _, err := protocol.ParseRoomControlUnverified(page.Events[len(page.Events)-1])
		if err != nil || last.Seq <= afterSeq {
			return nil, hubInvalidError("home sent a control page that does not advance")
		}
		afterSeq = last.Seq
	}
}

// continueRoomControl completes a control log page the home answered with
// (an admission), pulling the rest when it said there is more.
func (s *Service) continueRoomControl(ctx context.Context, pairing protocol.Pairing, page protocol.RoomControlPage) ([]protocol.SignedRoomControl, error) {
	if !page.More || len(page.Events) == 0 {
		return page.Events, nil
	}
	last, _, err := protocol.ParseRoomControlUnverified(page.Events[len(page.Events)-1])
	if err != nil {
		return nil, hubInvalidError("home sent an unreadable control page")
	}
	rest, err := s.fetchRoomControl(ctx, pairing, page.GlobalRoomID, last.Seq)
	if err != nil {
		return nil, err
	}
	return append(page.Events, rest...), nil
}

func controlRecords(globalRoomID string, start int64, events []protocol.SignedRoomControl) ([]store.HubControlRecord, error) {
	records := make([]store.HubControlRecord, 0, len(events))
	for index, signed := range events {
		event, digest, err := protocol.ParseRoomControlUnverified(signed)
		if err != nil {
			return nil, err
		}
		if event.Seq != start+int64(index) {
			return nil, hubControlError("home sent control event %d, expected %d", event.Seq, start+int64(index))
		}
		records = append(records, store.HubControlRecord{GlobalRoomID: globalRoomID, Seq: event.Seq, Digest: digest, Control: signed})
	}
	return records, nil
}

// JoinHubRoomContext pulls a hub room's control log from its home over the
// pairing, verifies it (the genesis key must be the key pinned for the home
// over that pairing, and this network must be a member), stores the log,
// provisions the local room, and catches up. It is idempotent per pairing.
func (s *Service) JoinHubRoomContext(ctx context.Context, request HubJoinRequest) (HubRoomView, error) {
	if err := requireHubOperator(ctx); err != nil {
		return HubRoomView{}, err
	}
	return s.joinHubRoom(ctx, request)
}

func (s *Service) joinHubRoom(ctx context.Context, request HubJoinRequest) (HubRoomView, error) {
	if err := s.requireHubs(); err != nil {
		return HubRoomView{}, err
	}
	pairing, err := s.findPairingByID(request.PairingID)
	if err != nil {
		return HubRoomView{}, err
	}
	home, err := s.pairingNetworkID(pairing)
	if err != nil {
		return HubRoomView{}, hubInvalidError(err.Error())
	}
	homeFingerprint, roomID, err := protocol.ParseGlobalRoomID(request.GlobalRoomID)
	if err != nil {
		return HubRoomView{}, hubInvalidError(err.Error())
	}
	pin, pinned, err := s.keyPins.Lookup(home)
	if err != nil {
		return HubRoomView{}, trustStoreUnavailableError(err)
	}
	if !pinned || pin.Fingerprint != homeFingerprint || pin.Trust == protocol.KeyTrustIntroduced {
		return HubRoomView{}, hubForbiddenError(fmt.Sprintf("hub room %s is not homed by the key pinned for %q over this pairing", request.GlobalRoomID, home))
	}
	var events []protocol.SignedRoomControl
	if request.Admit {
		events, err = s.requestAdmission(ctx, pairing, request.GlobalRoomID)
	} else {
		events, err = s.fetchRoomControl(ctx, pairing, request.GlobalRoomID, 0)
	}
	if err != nil {
		return HubRoomView{}, err
	}
	records, err := controlRecords(request.GlobalRoomID, 1, events)
	if err != nil {
		return HubRoomView{}, hubInvalidError(err.Error())
	}
	roster, err := replayHubRoster(records, 0)
	if err != nil {
		return HubRoomView{}, hubInvalidError(err.Error())
	}
	if roster.GlobalRoomID != request.GlobalRoomID || roster.HomeNetworkID != home {
		return HubRoomView{}, hubForbiddenError("hub room genesis names a different home")
	}
	if self, member := roster.Members[s.networkID]; !member {
		return HubRoomView{}, hubForbiddenError(fmt.Sprintf("network %q is not a member of hub room %s", s.networkID, request.GlobalRoomID))
	} else if self.Fingerprint != s.identity.Fingerprint() {
		return HubRoomView{}, hubForbiddenError(fmt.Sprintf("hub room %s admits %q under a key that is not this network's", request.GlobalRoomID, s.networkID))
	}
	localRoomID := strings.TrimSpace(request.LocalRoomID)
	if localRoomID == "" {
		localRoomID = roomID
	}
	hub := store.HubRoom{GlobalRoomID: request.GlobalRoomID, LocalRoomID: localRoomID, HomeNetworkID: home, HomePairingID: pairing.ID, CreatedAt: time.Now().UTC()}
	if existing, ok, err := s.hubRoomByGlobal(ctx, request.GlobalRoomID); err != nil {
		return HubRoomView{}, err
	} else if ok {
		// A repeated join (an operator retry, or a pending join replayed
		// after a crash) finishes the first one instead of failing.
		if existing.HomePairingID != pairing.ID {
			return HubRoomView{}, hubConflictError(fmt.Errorf("%w: %s is already joined over pairing %q", store.ErrHubRoomExists, request.GlobalRoomID, existing.HomePairingID))
		}
		hub = existing
	} else {
		if _, taken, err := s.getRoom(ctx, localRoomID); err != nil {
			return HubRoomView{}, err
		} else if taken {
			return HubRoomView{}, hubConflictError(fmt.Errorf("%w: local room %s already exists; pass another local room id", store.ErrHubRoomExists, localRoomID))
		}
		// The hub record (with the verified log) is written first and the
		// local room second, so a crash in between leaves a hub room whose
		// local room recovery recreates, never an orphaned local room.
		err := s.hubs.CreateHubRoomContext(ctx, hub, records[0])
		s.invalidateHubRooms()
		if err != nil {
			if errors.Is(err, store.ErrHubRoomExists) {
				return HubRoomView{}, hubConflictError(err)
			}
			return HubRoomView{}, err
		}
	}
	if err := s.hubs.AppendHubControlContext(ctx, records[1:]); err != nil {
		return HubRoomView{}, err
	}
	if err := s.ensureHubLocalRoom(ctx, hub); err != nil {
		return HubRoomView{}, err
	}
	logHub(ctx, hub.GlobalRoomID).Info("joined hub room", "local_room_id", hub.LocalRoomID, "home_network_id", home, "epoch", roster.Epoch)
	if err := s.syncHubRoom(ctx, hub); err != nil {
		logHub(ctx, hub.GlobalRoomID).Warn("first catch-up after joining failed; it will retry", "error", err)
	}
	return s.hubRoomView(ctx, hub)
}

// ensureHubLocalRoom provisions the local room a joined hub room maps to,
// if it is missing. Local agents write under this network's role; the
// room's write policy is registered agents.
func (s *Service) ensureHubLocalRoom(ctx context.Context, hub store.HubRoom) error {
	if _, ok, err := s.getRoom(ctx, hub.LocalRoomID); err != nil || ok {
		return err
	}
	_, canonical, err := protocol.ParseGlobalRoomID(hub.GlobalRoomID)
	if err != nil {
		return err
	}
	_, err = s.CreateRoomContext(ctx, protocol.CreateRoomRequest{
		ID: hub.LocalRoomID, Name: canonical, Visibility: protocol.RoomVisibilityPrivate,
		WritePolicy: protocol.RoomWritePolicyRegisteredAgents, Federation: protocol.DefaultRoomFederation(),
	})
	var exists *Error
	if errors.As(err, &exists) && exists.status == http.StatusConflict {
		return nil
	}
	return err
}

// recoverHubJoins finishes joins a crash interrupted: every joined hub
// room gets its local room back.
func (s *Service) recoverHubJoins(ctx context.Context) {
	rooms, err := s.hubRooms(ctx)
	if err != nil {
		return
	}
	for _, hub := range rooms {
		if hub.IsHome() {
			continue
		}
		if err := s.ensureHubLocalRoom(ctx, hub); err != nil {
			logHub(ctx, hub.GlobalRoomID).Warn("could not recover a joined hub room's local room", "error", err)
		}
	}
}

// syncHubControl pulls control events after the stored head from the home
// and applies them (applyPulledControl).
func (s *Service) syncHubControl(ctx context.Context, hub store.HubRoom) (hubRoster, error) {
	before, _, err := s.hubRoster(ctx, hub, 0)
	if err != nil || hub.IsHome() {
		return before, err
	}
	pairing, err := s.findPairingByID(hub.HomePairingID)
	if err != nil {
		return hubRoster{}, err
	}
	events, err := s.fetchRoomControl(ctx, pairing, hub.GlobalRoomID, before.Epoch)
	if err != nil {
		return hubRoster{}, err
	}
	return s.applyPulledControl(ctx, hub, events)
}

// applyPulledControl verifies control events the home sent after the
// stored head, stores the extended log, and logs who joined or left: the
// notice a stale sender sees before its message reaches a new member.
func (s *Service) applyPulledControl(ctx context.Context, hub store.HubRoom, events []protocol.SignedRoomControl) (hubRoster, error) {
	before, records, err := s.hubRoster(ctx, hub, 0)
	if err != nil {
		return hubRoster{}, err
	}
	// A concurrent sync may have stored some of these already: events up to
	// the stored head must be exactly the stored ones (the store refuses a
	// forked log), and only the rest extends it.
	start := before.Epoch + 1
	if len(events) > 0 {
		if first, _, err := protocol.ParseRoomControlUnverified(events[0]); err == nil && first.Seq < start && first.Seq >= 1 {
			start = first.Seq
		}
	}
	pulled, err := controlRecords(hub.GlobalRoomID, start, events)
	if err != nil {
		return hubRoster{}, hubInvalidError(err.Error())
	}
	if overlap := min(int64(len(pulled)), before.Epoch-start+1); overlap > 0 {
		if err := s.hubs.AppendHubControlContext(ctx, pulled[:overlap]); err != nil {
			return hubRoster{}, err
		}
		pulled = pulled[overlap:]
	}
	if len(pulled) == 0 {
		return before, nil
	}
	after, err := replayHubRoster(append(records, pulled...), 0)
	if err != nil {
		logHub(ctx, hub.GlobalRoomID).Error("refusing a control log from the home that does not verify", "error", err)
		return hubRoster{}, hubInvalidError(err.Error())
	}
	if err := s.hubs.AppendHubControlContext(ctx, pulled); err != nil {
		return hubRoster{}, err
	}
	var joined, left []string
	for network := range after.Members {
		if _, was := before.Members[network]; !was {
			joined = append(joined, network)
		}
	}
	for network := range before.Members {
		if _, still := after.Members[network]; !still {
			left = append(left, network)
		}
	}
	logHub(ctx, hub.GlobalRoomID).Info("hub room membership changed", "from_epoch", before.Epoch, "to_epoch", after.Epoch, "joined", joined, "left", left)
	s.publishHubMembershipNotice(ctx, hub)
	return after, nil
}

// SyncHubRoomContext catches a member's copy of a hub room up to the home's
// head: control log and messages (operators; the sync loop does the same
// on a timer, on gaps and on reconnect).
func (s *Service) SyncHubRoomContext(ctx context.Context, globalRoomID string) (HubRoomView, error) {
	if err := requireHubOperator(ctx); err != nil {
		return HubRoomView{}, err
	}
	hub, ok, err := s.hubRoomByGlobal(ctx, globalRoomID)
	if err != nil {
		return HubRoomView{}, err
	}
	if !ok {
		return HubRoomView{}, hubNotFoundError(globalRoomID)
	}
	if _, err := s.syncHubControl(ctx, hub); err != nil {
		return HubRoomView{}, err
	}
	if err := s.syncHubRoom(ctx, hub); err != nil {
		return HubRoomView{}, err
	}
	return s.hubRoomView(ctx, hub)
}

// publishHubMembershipNotice tells local subscribers (the console) that the
// room's audience changed, before any re-signed message goes out to it.
// This is notification, not consent: the sender's network re-signs and
// resends automatically.
func (s *Service) publishHubMembershipNotice(ctx context.Context, hub store.HubRoom) {
	room, ok, err := s.getRoom(ctx, hub.LocalRoomID)
	if err != nil || !ok {
		return
	}
	now, err := s.now()
	if err != nil {
		return
	}
	s.publishEvent(protocol.Event{ID: newPrefixedID("evt"), Type: protocol.EventTypeRoomMembersUpdated, NetworkID: s.networkID, Room: &room, CreatedAt: now})
}
