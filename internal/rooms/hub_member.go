package rooms

import (
	"context"
	"errors"
	"fmt"
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
}

type roomControlClient interface {
	FetchRoomControl(ctx context.Context, pairing protocol.Pairing, globalRoomID string, afterSeq int64) (protocol.RoomControlPage, error)
}

func (s *Service) fetchRoomControl(ctx context.Context, pairing protocol.Pairing, globalRoomID string, afterSeq int64) ([]protocol.SignedRoomControl, error) {
	client, ok := s.pairingClient.(roomControlClient)
	if !ok {
		return nil, hubUnavailableError("pairing client cannot fetch hub room control logs")
	}
	page, err := client.FetchRoomControl(ctx, pairing, globalRoomID, afterSeq)
	if err != nil {
		return nil, err
	}
	if page.GlobalRoomID != globalRoomID {
		return nil, hubInvalidError("home answered for a different hub room")
	}
	return page.Events, nil
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
// over that pairing, and this network must be a member), then provisions
// the local room and stores the log. Local agents write under this
// network's role; the room's write policy is registered agents.
func (s *Service) JoinHubRoomContext(ctx context.Context, request HubJoinRequest) (HubRoomView, error) {
	if err := requireHubOperator(ctx); err != nil {
		return HubRoomView{}, err
	}
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
	events, err := s.fetchRoomControl(ctx, pairing, request.GlobalRoomID, 0)
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
	if _, member := roster.Members[s.networkID]; !member {
		return HubRoomView{}, hubForbiddenError(fmt.Sprintf("network %q is not a member of hub room %s", s.networkID, request.GlobalRoomID))
	}
	localRoomID := strings.TrimSpace(request.LocalRoomID)
	if localRoomID == "" {
		localRoomID = roomID
	}
	if _, err := s.CreateRoomContext(ctx, protocol.CreateRoomRequest{
		ID: localRoomID, Name: roomID, Visibility: protocol.RoomVisibilityPrivate,
		WritePolicy: protocol.RoomWritePolicyRegisteredAgents, Federation: protocol.DefaultRoomFederation(),
	}); err != nil {
		return HubRoomView{}, err
	}
	hub := store.HubRoom{GlobalRoomID: request.GlobalRoomID, LocalRoomID: localRoomID, HomeNetworkID: home, HomePairingID: pairing.ID, CreatedAt: time.Now().UTC()}
	if err := s.hubs.CreateHubRoomContext(ctx, hub, records[0]); err != nil {
		if errors.Is(err, store.ErrHubRoomExists) {
			return HubRoomView{}, hubConflictError(err)
		}
		return HubRoomView{}, err
	}
	if err := s.hubs.AppendHubControlContext(ctx, records[1:]); err != nil {
		return HubRoomView{}, err
	}
	logHub(ctx, hub.GlobalRoomID).Info("joined hub room", "local_room_id", localRoomID, "home_network_id", home, "epoch", roster.Epoch)
	return s.hubRoomView(ctx, hub)
}

// syncHubControl pulls control events after the stored head from the home,
// verifies the extended log, stores it, and logs who joined or left: the
// notice a stale sender sees before its message reaches a new member.
func (s *Service) syncHubControl(ctx context.Context, hub store.HubRoom) (hubRoster, error) {
	before, records, err := s.hubRoster(ctx, hub, 0)
	if err != nil {
		return hubRoster{}, err
	}
	if hub.IsHome() {
		return before, nil
	}
	pairing, err := s.findPairingByID(hub.HomePairingID)
	if err != nil {
		return hubRoster{}, err
	}
	events, err := s.fetchRoomControl(ctx, pairing, hub.GlobalRoomID, before.Epoch)
	if err != nil {
		return hubRoster{}, err
	}
	pulled, err := controlRecords(hub.GlobalRoomID, before.Epoch+1, events)
	if err != nil {
		return hubRoster{}, hubInvalidError(err.Error())
	}
	after, err := replayHubRoster(append(records, pulled...), 0)
	if err != nil {
		logHub(ctx, hub.GlobalRoomID).Error("refusing a control log from the home that does not verify", "error", err)
		return hubRoster{}, hubInvalidError(err.Error())
	}
	if len(pulled) == 0 {
		return after, nil
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
	return after, nil
}
