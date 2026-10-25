package rooms

import (
	"context"
	"encoding/json"
	"sync/atomic"

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
		network, linked, err := s.hubPeerNetwork(ctx, globalRoomID, pairing)
		if err != nil {
			return protocol.RoomSyncPage{}, err
		}
		if !linked || network == s.networkID {
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
	size := syncEncodedSize(page)
	for _, record := range records {
		if record.Seq <= max(controlAfter, 0) || record.Seq > controlLimit {
			continue
		}
		next := syncEncodedSize(record.Control) + 1
		if len(page.Control) > 0 && size+next > syncPageBytes() {
			// The rest of the control log comes first on the next page;
			// entries wait for the roster that authorizes them.
			page.More = true
			return page, nil
		}
		page.Control, size = append(page.Control, record.Control), size+next
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
		next := syncEncodedSize(entry) + 1
		if len(page.Entries) > 0 || len(page.Control) > 0 {
			if size+next > syncPageBytes() {
				page.More = true
				break
			}
		} else if entry.Message != nil && !fitsTransportAlone(page, entry, record.RoomSeq) {
			// Alone on its page it may use the whole transport limit; only a
			// message within the page's own bytes of it cannot, and the
			// member is told so instead of it vanishing.
			logHub(ctx, globalRoomID).Warn("hub sync withholds a message too large for a page even alone", "room_seq", record.RoomSeq, "bytes", next)
			entry.Message, entry.Withheld = nil, protocol.RoomSyncWithheldTooLarge
			next = syncEncodedSize(entry) + 1
		}
		page.Entries, size = append(page.Entries, entry), size+next
		page.ThroughSeq = record.RoomSeq
	}
	return page, nil
}

// fitsTransportAlone reports whether page carrying entry alone (at seq, the
// longer "more":false) encodes within the pairing transport limit, less
// the newline the JSON encoder appends to every response.
func fitsTransportAlone(page protocol.RoomSyncPage, entry protocol.RoomSyncEntry, seq int64) bool {
	page.Entries, page.ThroughSeq, page.More = []protocol.RoomSyncEntry{entry}, seq, false
	return syncEncodedSize(page) <= syncTransportBytes-1
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
		// The entry carries the receipt; the message does not repeat it, so an
		// entry is no larger than the live delivery of the same message.
		signed := envelope.Envelope
		request.Envelope, request.Receipt = &signed, nil
		result[id] = request
	}
	return result, nil
}

// syncPageLimit holds the sync page budget: protocol.MaxRoomSyncPageBytes.
// Tests lower it while background sync requests may still be reading it, so
// it is atomic.
var syncPageLimit atomic.Int64

func init() { syncPageLimit.Store(protocol.MaxRoomSyncPageBytes) }

// syncPageBytes is the current sync page budget.
func syncPageBytes() int { return int(syncPageLimit.Load()) }

// syncTransportBytes is the pairing transport limit a single-entry page may
// use: protocol.MaxPairingPayloadBytes, the limit HTTP and relay share.
const syncTransportBytes = protocol.MaxPairingPayloadBytes

// syncEncodedSize is value's JSON size (the page's wire size, near enough).
func syncEncodedSize(value any) int {
	encoded, err := json.Marshal(value)
	if err != nil {
		return syncPageBytes()
	}
	return len(encoded)
}
