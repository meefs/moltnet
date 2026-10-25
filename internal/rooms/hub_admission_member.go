package rooms

import (
	"context"

	"github.com/noopolis/moltnet/pkg/protocol"
)

type roomAdmissionClient interface {
	RequestRoomAdmission(ctx context.Context, pairing protocol.Pairing, admission protocol.SignedRoomAdmission) (protocol.RoomControlPage, error)
	RequestRoomLeave(ctx context.Context, pairing protocol.Pairing, request protocol.RoomLeaveRequest) (protocol.RoomControlPage, error)
}

func (s *Service) admissionClient() (roomAdmissionClient, error) {
	client, ok := s.pairingClient.(roomAdmissionClient)
	if !ok {
		return nil, hubUnavailableError("pairing client cannot redeem room invites")
	}
	return client, nil
}

// requestAdmission signs this network's admission and redeems the room
// invite over its pairing; the home answers with the control log.
func (s *Service) requestAdmission(ctx context.Context, pairing protocol.Pairing, globalRoomID string) ([]protocol.SignedRoomControl, error) {
	client, err := s.admissionClient()
	if err != nil {
		return nil, err
	}
	createdAt, err := s.hubTimestamp()
	if err != nil {
		return nil, err
	}
	public := s.identity.Public()
	signed, err := protocol.SignRoomAdmission(protocol.RoomAdmission{V: protocol.HubAdmissionVersionV1, GlobalRoomID: globalRoomID,
		NetworkID: s.networkID, Fingerprint: public.Fingerprint, PublicKey: public.PublicKey, CreatedAt: createdAt}, s.identity.Sign)
	if err != nil {
		return nil, hubInvalidError(err.Error())
	}
	page, err := client.RequestRoomAdmission(ctx, pairing, signed)
	if err != nil {
		return nil, err
	}
	if page.GlobalRoomID != globalRoomID {
		return nil, hubInvalidError("home answered for a different hub room")
	}
	return page.Events, nil
}

// LeaveHubRoomContext makes this member network leave a hub room: the home
// appends the leave, and this network syncs it. The local room and its
// history stay; nothing more is sent or received for the room.
func (s *Service) LeaveHubRoomContext(ctx context.Context, globalRoomID string) (HubRoomView, error) {
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
	if hub.IsHome() {
		return HubRoomView{}, hubInvalidError("the home cannot leave its own room")
	}
	client, err := s.admissionClient()
	if err != nil {
		return HubRoomView{}, err
	}
	pairing, err := s.findPairingByID(hub.HomePairingID)
	if err != nil {
		return HubRoomView{}, err
	}
	if _, err := client.RequestRoomLeave(ctx, pairing, protocol.RoomLeaveRequest{GlobalRoomID: globalRoomID}); err != nil {
		return HubRoomView{}, err
	}
	if _, err := s.syncHubControl(ctx, hub); err != nil {
		return HubRoomView{}, err
	}
	return s.hubRoomView(ctx, hub)
}

// finishPendingJoins redeems room invites `moltnet room join` recorded,
// once their pairing is loaded; a finished or already joined room drops
// its entry, a failure stays for the next pass.
func (s *Service) finishPendingJoins(ctx context.Context) {
	joins, err := s.hubInvites.Joins()
	if err != nil {
		logHub(ctx, "").Warn("could not read pending hub room joins", "error", err)
		return
	}
	for _, join := range joins {
		if _, joined, err := s.hubRoomByGlobal(ctx, join.GlobalRoomID); err == nil && joined {
			_ = s.hubInvites.RemoveJoin(join.GlobalRoomID)
			continue
		}
		if _, err := s.findPairingByID(join.PairingID); err != nil {
			continue // the pairing is not loaded yet (restart pending)
		}
		view, err := s.joinHubRoom(ctx, HubJoinRequest{PairingID: join.PairingID, GlobalRoomID: join.GlobalRoomID, LocalRoomID: join.LocalRoomID, Admit: true})
		if err != nil {
			logHub(ctx, join.GlobalRoomID).Warn("pending hub room join not finished yet; will retry", "pairing_id", join.PairingID, "error", err)
			continue
		}
		if err := s.hubInvites.RemoveJoin(join.GlobalRoomID); err != nil {
			logHub(ctx, join.GlobalRoomID).Error("joined a hub room but could not clear its pending join", "error", err)
		}
		logHub(ctx, join.GlobalRoomID).Info("finished a pending hub room join", "local_room_id", view.LocalRoomID, "epoch", view.Epoch)
	}
}
