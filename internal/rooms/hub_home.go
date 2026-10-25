package rooms

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/internal/netkeys"
	"github.com/noopolis/moltnet/internal/observability"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// HubControlRequest asks the home to append one control event. PairingID
// names the member link for add (the home learns the network and key from
// it); NetworkID names an existing member for remove, leave and set_role.
type HubControlRequest struct {
	GlobalRoomID string `json:"global_room_id"`
	Kind         string `json:"kind"`
	PairingID    string `json:"pairing_id,omitempty"`
	NetworkID    string `json:"network_id,omitempty"`
	Role         string `json:"role,omitempty"`
	History      string `json:"history,omitempty"`
}

// HubRoomView is a hub room with its current roster, for operators.
type HubRoomView struct {
	store.HubRoom
	Epoch   int64             `json:"membership_epoch"`
	History string            `json:"history"`
	Members map[string]string `json:"members"` // network id -> role
}

func logHub(ctx context.Context, globalRoomID string) *slog.Logger {
	return observability.Logger(ctx, "rooms.hub", "global_room_id", globalRoomID)
}

// requireHubOperator admits this network's own operator (or an open,
// unauthenticated local server), never a peer credential.
func requireHubOperator(ctx context.Context) error {
	claims, ok := authn.ClaimsFromContext(ctx)
	if !ok {
		if authn.ModeFromContext(ctx) == authn.ModeNone {
			return nil
		}
		return hubForbiddenError("hub room administration requires an operator credential")
	}
	if claims.Operator() || (claims.Allows(authn.ScopeAdmin) && !claims.Allows(authn.ScopePair)) {
		return nil
	}
	return hubForbiddenError("hub room administration requires an operator credential")
}

func (s *Service) requireHubs() error {
	if s.hubs == nil {
		return hubUnavailableError("this message store does not support hub rooms")
	}
	if s.identity == nil {
		return hubUnavailableError("hub rooms need a network signing key (a durable keys directory)")
	}
	return nil
}

func (s *Service) hubTimestamp() (string, error) {
	now, err := s.now()
	if err != nil {
		return "", err
	}
	return now.UTC().Format(time.RFC3339Nano), nil
}

// CreateHubRoomContext makes an existing local room a hub room homed here:
// it signs the genesis that fixes the global room id for good.
func (s *Service) CreateHubRoomContext(ctx context.Context, roomID, history string) (HubRoomView, error) {
	if err := requireHubOperator(ctx); err != nil {
		return HubRoomView{}, err
	}
	if err := s.requireHubs(); err != nil {
		return HubRoomView{}, err
	}
	if history == "" {
		history = protocol.RoomHistoryFromJoin
	}
	if !protocol.ValidRoomHistory(history) {
		return HubRoomView{}, hubInvalidError(fmt.Sprintf("unknown history policy %q", history))
	}
	room, ok, err := s.getRoom(ctx, strings.TrimSpace(roomID))
	if err != nil {
		return HubRoomView{}, err
	}
	if !ok {
		return HubRoomView{}, unknownRoomError(roomID)
	}
	if room.Follow != nil || protocol.NormalizeRoomFederation(room.Federation).Mode != protocol.RoomFederationNone {
		return HubRoomView{}, hubInvalidError("a hub room must be a local room with federation none; its members come from the control log")
	}
	createdAt, err := s.hubTimestamp()
	if err != nil {
		return HubRoomView{}, err
	}
	public := s.identity.Public()
	genesis := protocol.RoomControlEvent{
		V: protocol.RoomControlVersionV1, GlobalRoomID: protocol.GlobalRoomID(public.Fingerprint, room.ID), Seq: 1, Epoch: 1,
		Kind: protocol.RoomControlGenesis, NetworkID: s.networkID, Fingerprint: public.Fingerprint, PublicKey: public.PublicKey,
		Role: protocol.RoomRoleOwner, History: history, CreatedAt: createdAt,
	}
	signed, digest, err := protocol.SignRoomControl(genesis, s.identity.Sign)
	if err != nil {
		return HubRoomView{}, hubInvalidError(err.Error())
	}
	hub := store.HubRoom{GlobalRoomID: genesis.GlobalRoomID, LocalRoomID: room.ID, HomeNetworkID: s.networkID, CreatedAt: time.Now().UTC()}
	err = s.hubs.CreateHubRoomContext(ctx, hub, store.HubControlRecord{GlobalRoomID: hub.GlobalRoomID, Seq: 1, Digest: digest, Control: signed})
	s.invalidateHubRooms()
	if err != nil {
		if errors.Is(err, store.ErrHubRoomExists) {
			return HubRoomView{}, hubConflictError(err)
		}
		return HubRoomView{}, err
	}
	logHub(ctx, hub.GlobalRoomID).Info("created hub room", "room_id", room.ID, "history", history)
	return s.hubRoomView(ctx, hub)
}

// AppendHubControlContext appends one home-signed control event.
func (s *Service) AppendHubControlContext(ctx context.Context, request HubControlRequest) (HubRoomView, error) {
	if err := requireHubOperator(ctx); err != nil {
		return HubRoomView{}, err
	}
	return s.appendHubControl(ctx, request)
}

// appendHubControl signs and appends a control event on the home; callers
// have authorized the request (an operator, an admission, a leave).
func (s *Service) appendHubControl(ctx context.Context, request HubControlRequest) (HubRoomView, error) {
	if err := s.requireHubs(); err != nil {
		return HubRoomView{}, err
	}
	hub, ok, err := s.hubRoomByGlobal(ctx, request.GlobalRoomID)
	if err != nil {
		return HubRoomView{}, err
	}
	if !ok || !hub.IsHome() {
		return HubRoomView{}, hubForbiddenError("only a room's home can change its control log")
	}
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	roster, records, err := s.hubRoster(ctx, hub, 0)
	if err != nil {
		return HubRoomView{}, err
	}
	createdAt, err := s.hubTimestamp()
	if err != nil {
		return HubRoomView{}, err
	}
	event := protocol.RoomControlEvent{
		V: protocol.RoomControlVersionV1, GlobalRoomID: hub.GlobalRoomID, Seq: roster.Epoch + 1, Epoch: roster.Epoch + 1,
		PrevHash: roster.HeadDigest, Kind: request.Kind, NetworkID: strings.TrimSpace(request.NetworkID),
		Role: request.Role, History: request.History, CreatedAt: createdAt,
	}
	if request.Kind == protocol.RoomControlAdd {
		if err := s.fillHubAddSubject(&event, request.PairingID); err != nil {
			return HubRoomView{}, err
		}
	}
	signed, digest, err := protocol.SignRoomControl(event, s.identity.Sign)
	if err != nil {
		return HubRoomView{}, hubInvalidError(err.Error())
	}
	record := store.HubControlRecord{GlobalRoomID: hub.GlobalRoomID, Seq: event.Seq, Digest: digest, Control: signed}
	// Validate before touching any link: a refused event must leave every
	// member's link where it was.
	if _, err := replayHubRoster(append(records, record), 0); err != nil {
		return HubRoomView{}, hubInvalidError(err.Error())
	}
	if request.Kind == protocol.RoomControlAdd {
		// The link is written before the add it accompanies (a link without
		// membership authorizes nothing; membership without its link would
		// strand the member), and never moves a current member's link.
		if _, member := roster.Members[event.NetworkID]; member {
			return HubRoomView{}, hubInvalidError(fmt.Sprintf("network %q is a current member; its member link stays", event.NetworkID))
		}
		if err := s.recordHubLink(hub.GlobalRoomID, request.PairingID, event.NetworkID); err != nil {
			return HubRoomView{}, err
		}
	}
	if err := s.hubs.AppendHubControlContext(ctx, []store.HubControlRecord{record}); err != nil {
		return HubRoomView{}, err
	}
	logHub(ctx, hub.GlobalRoomID).Info("hub room control event appended", "kind", event.Kind, "network_id", event.NetworkID, "epoch", event.Epoch)
	return s.hubRoomView(ctx, hub)
}

// fillHubAddSubject takes the new member's network id and key from the
// pairing that links it and from this network's own pin for it.
func (s *Service) fillHubAddSubject(event *protocol.RoomControlEvent, pairingID string) error {
	pairing, err := s.findPairingByID(pairingID)
	if err != nil {
		return err
	}
	network, err := s.pairingNetworkID(pairing)
	if err != nil {
		return hubInvalidError(err.Error())
	}
	if event.NetworkID != "" && event.NetworkID != network {
		return hubInvalidError(fmt.Sprintf("pairing %q speaks for %q, not %q", pairing.ID, network, event.NetworkID))
	}
	pin, ok, err := s.keyPins.Lookup(network)
	if err != nil {
		return trustStoreUnavailableError(err)
	}
	if !ok || pin.PublicKey == "" || pin.Trust == protocol.KeyTrustIntroduced {
		return hubInvalidError(fmt.Sprintf("network %q has no key pinned over a pairing yet; it must send one signed message first, or run `moltnet keys trust`", network))
	}
	event.NetworkID, event.Fingerprint, event.PublicKey = network, pin.Fingerprint, pin.PublicKey
	if event.Role == "" {
		event.Role = protocol.RoomRoleWriter
	}
	return nil
}

// HubRoomsContext lists hub rooms with their rosters, for operators.
func (s *Service) HubRoomsContext(ctx context.Context) ([]HubRoomView, error) {
	if err := requireHubOperator(ctx); err != nil {
		return nil, err
	}
	rooms, err := s.hubRooms(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]HubRoomView, 0, len(rooms))
	for _, room := range rooms {
		view, err := s.hubRoomView(ctx, room)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

func (s *Service) hubRoomView(ctx context.Context, room store.HubRoom) (HubRoomView, error) {
	roster, _, err := s.hubRoster(ctx, room, 0)
	if err != nil {
		return HubRoomView{}, err
	}
	members := make(map[string]string, len(roster.Members))
	for network, member := range roster.Members {
		members[network] = member.Role
	}
	return HubRoomView{HubRoom: room, Epoch: roster.Epoch, History: roster.History, Members: members}, nil
}

// HubControlPageContext serves a hub room's control log. A peer may read it
// only if its network was ever admitted, and a removed member only up to
// its removal: it learns of that, and nobody else learns the roster.
func (s *Service) HubControlPageContext(ctx context.Context, globalRoomID string, afterSeq int64) (protocol.RoomControlPage, error) {
	if s.hubs == nil {
		return protocol.RoomControlPage{}, hubNotFoundError(globalRoomID)
	}
	_, ok, err := s.hubRoomByGlobal(ctx, globalRoomID)
	if err != nil {
		return protocol.RoomControlPage{}, err
	}
	if !ok {
		return protocol.RoomControlPage{}, hubNotFoundError(globalRoomID)
	}
	records, err := s.hubs.HubControlLogContext(ctx, globalRoomID, 0)
	if err != nil {
		return protocol.RoomControlPage{}, err
	}
	limit := int64(len(records))
	if pairing, peer := s.pairingForPairScopedContext(ctx); peer {
		network, linked, err := s.hubPeerNetwork(ctx, globalRoomID, pairing)
		if err != nil {
			return protocol.RoomControlPage{}, err
		}
		if !linked || network == s.networkID {
			return protocol.RoomControlPage{}, hubNotFoundError(globalRoomID)
		}
		access, err := hubAccessFor(records, network)
		if err != nil {
			return protocol.RoomControlPage{}, err
		}
		// A network never admitted learns nothing; a removed one learns of
		// its removal and nothing after it.
		if limit = access.controlLimit(); limit == 0 {
			return protocol.RoomControlPage{}, hubNotFoundError(globalRoomID)
		}
	}
	page := protocol.RoomControlPage{GlobalRoomID: globalRoomID, Events: []protocol.SignedRoomControl{}}
	size := syncEncodedSize(page)
	for _, record := range records {
		if record.Seq <= max(afterSeq, 0) || record.Seq > limit {
			continue
		}
		next := syncEncodedSize(record.Control) + 1
		if len(page.Events) > 0 && size+next > syncPageBytes {
			page.More = true
			break
		}
		page.Events, size = append(page.Events, record.Control), size+next
	}
	return page, nil
}

// introducedPin is the pin a member records for a network it met only
// through a room: signature-checked, identity vouched for by the home only.
func introducedPin(roster hubRoster, member hubMember) netkeys.Pin {
	return netkeys.Pin{
		NetworkID: member.NetworkID, Fingerprint: member.Fingerprint, PublicKey: member.PublicKey,
		Trust: protocol.KeyTrustIntroduced, Source: netkeys.PinSourceRoom, SourceID: roster.GlobalRoomID,
		IntroducedBy: roster.HomeNetworkID,
	}
}
