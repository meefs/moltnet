package rooms

import (
	"context"

	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// HubSyncPageContext is the home serving catch-up (GET /v1/hub/sync). The
// page is clamped to the caller's membership intervals: control events up
// to its removal, receipts up to the last epoch it was a member at, and a
// message only beside a receipt it may see (placed while it was a member,
// or under full_history before a later membership of it). Receipts it may
// not see travel alone, so its receipt chain stays verifiable without
// revealing what they placed.
func (s *Service) HubSyncPageContext(ctx context.Context, globalRoomID string, afterSeq, controlAfter int64) (protocol.RoomSyncPage, error) {
	if s.hubs == nil {
		return protocol.RoomSyncPage{}, hubNotFoundError(globalRoomID)
	}
	hub, ok, err := s.hubRoomByGlobal(ctx, globalRoomID)
	if err != nil {
		return protocol.RoomSyncPage{}, err
	}
	if !ok || !hub.IsHome() {
		return protocol.RoomSyncPage{}, hubNotFoundError(globalRoomID)
	}
	pairing, peer := s.pairingForPairScopedContext(ctx)
	if !peer {
		if err := requireHubOperator(ctx); err != nil {
			return protocol.RoomSyncPage{}, err
		}
	}
	records, err := s.hubs.HubControlLogContext(ctx, globalRoomID, 0)
	if err != nil {
		return protocol.RoomSyncPage{}, err
	}
	if _, err := s.hubRosters.roster(globalRoomID, records, 0); err != nil {
		return protocol.RoomSyncPage{}, err
	}
	access := hubAccess{}
	controlLimit := int64(len(records))
	if peer {
		network, err := s.pairingNetworkID(pairing)
		if err != nil || network == s.networkID {
			return protocol.RoomSyncPage{}, hubNotFoundError(globalRoomID)
		}
		if access, err = hubAccessFor(records, network); err != nil {
			return protocol.RoomSyncPage{}, err
		}
		if controlLimit = access.controlLimit(); controlLimit == 0 {
			return protocol.RoomSyncPage{}, hubNotFoundError(globalRoomID)
		}
	}
	page := protocol.RoomSyncPage{GlobalRoomID: globalRoomID, Control: []protocol.SignedRoomControl{}, Entries: []protocol.RoomSyncEntry{}, ThroughSeq: max(afterSeq, 0)}
	for _, record := range records {
		if record.Seq > max(controlAfter, 0) && record.Seq <= controlLimit {
			page.Control = append(page.Control, record.Control)
		}
	}
	receipts, err := s.hubs.HubReceiptsAfterContext(ctx, globalRoomID, protocol.HubEpochV1, page.ThroughSeq, protocol.MaxRoomSyncPage+1)
	if err != nil {
		return protocol.RoomSyncPage{}, err
	}
	if len(receipts) > protocol.MaxRoomSyncPage {
		receipts, page.More = receipts[:protocol.MaxRoomSyncPage], true
	}
	visible := func(record store.HubReceiptRecord) bool {
		return !peer || access.messageVisible(record.MembershipEpoch)
	}
	var served []store.HubReceiptRecord
	for _, record := range receipts {
		// Epochs never decrease along room seqs, so a removed member's view
		// ends at the first receipt placed after its last membership.
		if peer && !access.current() && record.MembershipEpoch > access.lastMemberEpoch() {
			page.More = false
			break
		}
		served = append(served, record)
	}
	messages, err := s.hubSyncMessages(ctx, hub, served, visible)
	if err != nil {
		return protocol.RoomSyncPage{}, err
	}
	for _, record := range served {
		entry := protocol.RoomSyncEntry{Receipt: record.Receipt}
		if request, ok := messages[record.MessageID]; ok {
			entry.Message = &request
		}
		page.Entries = append(page.Entries, entry)
		page.ThroughSeq = record.RoomSeq
	}
	return page, nil
}

// hubSyncMessages rebuilds, for the visible receipts, the exact hub request
// the home fans out: the stored message and its signed envelope.
func (s *Service) hubSyncMessages(ctx context.Context, hub store.HubRoom, receipts []store.HubReceiptRecord, visible func(store.HubReceiptRecord) bool) (map[string]protocol.SendMessageRequest, error) {
	ids := make([]string, 0, len(receipts))
	byMessage := map[string]store.HubReceiptRecord{}
	for _, record := range receipts {
		if visible(record) {
			ids = append(ids, record.MessageID)
			byMessage[record.MessageID] = record
		}
	}
	result := map[string]protocol.SendMessageRequest{}
	if len(ids) == 0 {
		return result, nil
	}
	messages, err := s.hubs.HubMessagesContext(ctx, ids)
	if err != nil {
		return nil, err
	}
	envelopes, err := s.envelopes.MessageEnvelopesContext(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		message, okMessage := messages[id]
		envelope, okEnvelope := envelopes[id]
		if !okMessage || !okEnvelope || envelope.Digest != byMessage[id].EventID {
			logHub(ctx, hub.GlobalRoomID).Warn("hub sync skips a placed message whose stored copy is incomplete", "message_id", id)
			continue
		}
		request, err := s.hubRelayRequest(message, hub)
		if err != nil {
			return nil, err
		}
		signed, receipt := envelope.Envelope, byMessage[id].Receipt
		request.Envelope, request.Receipt = &signed, &receipt
		result[id] = request
	}
	return result, nil
}
