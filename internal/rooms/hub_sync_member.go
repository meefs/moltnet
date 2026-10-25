package rooms

import (
	"context"
	"errors"
	"fmt"

	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

type roomSyncClient interface {
	FetchRoomSync(ctx context.Context, pairing protocol.Pairing, globalRoomID string, afterSeq, controlAfter int64) (protocol.RoomSyncPage, error)
}

// checkCursorSuccessor refuses a receipt for the seq right after the
// verified cursor that does not chain to the cursor's receipt: the home
// rewriting order this member already verified.
func (s *Service) checkCursorSuccessor(ctx context.Context, hub store.HubRoom, receipt protocol.RoomReceipt) error {
	cursor, err := s.hubs.HubSyncCursorContext(ctx, hub.GlobalRoomID)
	if err != nil {
		return err
	}
	if cursor.Seq > 0 && receipt.RoomSeq == cursor.Seq+1 && receipt.PrevReceiptHash != cursor.Digest {
		return hubConflictError(fmt.Errorf("%w: seq %d does not chain to verified seq %d", store.ErrHubReceiptConflict, receipt.RoomSeq, cursor.Seq))
	}
	return nil
}

// notePlacement advances the verified cursor over a receipt that extends it
// contiguously, and schedules catch-up when a receipt arrives past a gap.
func (s *Service) notePlacement(ctx context.Context, hub store.HubRoom, placed store.HubReceiptRecord) {
	cursor, err := s.hubs.HubSyncCursorContext(ctx, hub.GlobalRoomID)
	if err != nil {
		return
	}
	switch {
	case placed.RoomSeq == cursor.Seq+1 && (cursor.Seq == 0 || placed.PrevDigest == cursor.Digest):
		if err := s.hubs.AdvanceHubSyncCursorContext(ctx, store.HubSyncCursor{GlobalRoomID: hub.GlobalRoomID, Seq: placed.RoomSeq, Digest: placed.Digest}); err != nil {
			logHub(ctx, hub.GlobalRoomID).Warn("could not advance the hub sync cursor", "error", err)
		}
	case placed.RoomSeq > cursor.Seq+1:
		logHub(ctx, hub.GlobalRoomID).Info("hub room receipt arrived past a gap; catching up", "cursor_seq", cursor.Seq, "room_seq", placed.RoomSeq)
		s.requestHubSync()
	}
}

// syncHubRoom pulls everything after this member's verified cursor from the
// room's home, page by page: control events first, then every receipt in
// order. The whole receipt chain is verified from the cursor before
// anything is stored; a shorter head than the cursor, a broken chain, an
// epoch going backwards, or a receipt contradicting a stored one aborts the
// sync and is logged as a rollback attempt.
func (s *Service) syncHubRoom(ctx context.Context, hub store.HubRoom) error {
	if hub.IsHome() {
		return nil
	}
	client, ok := s.pairingClient.(roomSyncClient)
	if !ok {
		return hubUnavailableError("pairing client cannot sync hub rooms")
	}
	pairing, err := s.findPairingByID(hub.HomePairingID)
	if err != nil {
		return err
	}
	for {
		roster, _, err := s.hubRoster(ctx, hub, 0)
		if err != nil {
			return err
		}
		cursor, err := s.hubs.HubSyncCursorContext(ctx, hub.GlobalRoomID)
		if err != nil {
			return err
		}
		page, err := client.FetchRoomSync(ctx, pairing, hub.GlobalRoomID, cursor.Seq, roster.Epoch)
		if err != nil {
			return err
		}
		if page.GlobalRoomID != hub.GlobalRoomID {
			return hubInvalidError("home answered for a different hub room")
		}
		if _, err := s.applyPulledControl(ctx, hub, page.Control); err != nil {
			return err
		}
		next, err := s.verifySyncChain(ctx, hub, cursor, page)
		if err != nil {
			logHub(ctx, hub.GlobalRoomID).Error("refusing a hub sync page that rewrites verified history", "cursor_seq", cursor.Seq, "error", err)
			return err
		}
		if err := s.storeSyncEntries(ctx, hub, pairing, page.Entries); err != nil {
			return err
		}
		if next.Seq > cursor.Seq {
			if err := s.hubs.AdvanceHubSyncCursorContext(ctx, next); err != nil {
				return err
			}
			logHub(ctx, hub.GlobalRoomID).Info("hub room caught up", "from_seq", cursor.Seq, "to_seq", next.Seq, "entries", len(page.Entries))
		}
		if !page.More || next.Seq == cursor.Seq {
			return nil
		}
	}
}

// verifySyncChain checks a page's receipts continue the cursor's chain
// seq by seq, and returns the cursor they advance to.
func (s *Service) verifySyncChain(ctx context.Context, hub store.HubRoom, cursor store.HubSyncCursor, page protocol.RoomSyncPage) (store.HubSyncCursor, error) {
	if page.ThroughSeq < cursor.Seq {
		return cursor, fmt.Errorf("%w: home reports head %d below verified seq %d", store.ErrHubSyncRollback, page.ThroughSeq, cursor.Seq)
	}
	roster, _, err := s.hubRoster(ctx, hub, 0)
	if err != nil {
		return cursor, err
	}
	next := cursor
	var epoch int64
	if cursor.Seq > 0 {
		// The receipt at the cursor is stored when this member could see
		// its message; epochs after it may not go backwards.
		if stored, err := s.hubs.HubReceiptsAfterContext(ctx, hub.GlobalRoomID, protocol.HubEpochV1, cursor.Seq-1, 1); err != nil {
			return cursor, err
		} else if len(stored) == 1 && stored[0].RoomSeq == cursor.Seq {
			epoch = stored[0].MembershipEpoch
		}
	}
	for _, entry := range page.Entries {
		receipt, digest, err := protocol.VerifyRoomReceipt(entry.Receipt, roster.HomeKey)
		if err != nil {
			return cursor, err
		}
		switch {
		case receipt.GlobalRoomID != hub.GlobalRoomID || receipt.HubEpoch != protocol.HubEpochV1:
			return cursor, fmt.Errorf("%w: receipt for another room or hub epoch", store.ErrHubReceiptConflict)
		case receipt.RoomSeq != next.Seq+1:
			return cursor, fmt.Errorf("%w: expected seq %d, got %d", store.ErrHubReceiptConflict, next.Seq+1, receipt.RoomSeq)
		case next.Seq > 0 && receipt.PrevReceiptHash != next.Digest:
			return cursor, fmt.Errorf("%w: seq %d does not chain to seq %d", store.ErrHubSyncRollback, receipt.RoomSeq, next.Seq)
		case receipt.MembershipEpoch < epoch || receipt.MembershipEpoch > roster.Epoch:
			return cursor, fmt.Errorf("%w: seq %d at epoch %d out of order", store.ErrHubReceiptConflict, receipt.RoomSeq, receipt.MembershipEpoch)
		}
		if entry.Message != nil && (entry.Message.Receipt == nil || *entry.Message.Receipt != entry.Receipt) {
			return cursor, fmt.Errorf("%w: entry message carries a different receipt", store.ErrHubReceiptConflict)
		}
		epoch, next = receipt.MembershipEpoch, store.HubSyncCursor{GlobalRoomID: hub.GlobalRoomID, Seq: receipt.RoomSeq, Digest: digest}
	}
	if next.Seq != page.ThroughSeq {
		return cursor, fmt.Errorf("%w: page accounts for seqs up to %d but claims %d", store.ErrHubReceiptConflict, next.Seq, page.ThroughSeq)
	}
	return next, nil
}

// storeSyncEntries ingests each visible entry through the same acceptance
// rule as a live forward; this network's own messages attach their
// receipt to the local copy instead.
func (s *Service) storeSyncEntries(ctx context.Context, hub store.HubRoom, pairing protocol.Pairing, entries []protocol.RoomSyncEntry) error {
	for _, entry := range entries {
		if entry.Message == nil {
			continue
		}
		request := stripPeerTestimony(*entry.Message)
		request.Origin = canonicalOriginIDs(request.Origin)
		if request.Origin.NetworkID == s.networkID {
			if err := s.attachOwnReceipt(ctx, hub, request.Origin.MessageID, entry.Receipt); err != nil {
				return err
			}
			continue
		}
		in, err := s.checkHubIngressFrom(ctx, pairing, request)
		if err != nil {
			return err
		}
		if _, err := s.acceptHubForward(ctx, in, false); err != nil && !errors.Is(err, store.ErrDuplicateMessage) {
			return err
		}
	}
	return nil
}

// attachOwnReceipt gives this network's own copy of a message it sent to
// the home the room seq the home placed it at, once the receipt verifies
// and places exactly the envelope stored here.
func (s *Service) attachOwnReceipt(ctx context.Context, hub store.HubRoom, messageID string, signed protocol.SignedRoomReceipt) error {
	roster, _, err := s.hubRoster(ctx, hub, 0)
	if err != nil {
		return err
	}
	receipt, digest, err := protocol.VerifyRoomReceipt(signed, roster.HomeKey)
	if err != nil {
		return signatureRejectedError("invalid room receipt: " + err.Error())
	}
	envelope, err := s.storedOutboundEnvelope(ctx, messageID)
	if err != nil {
		return err
	}
	if envelope == nil {
		return hubInvalidError(fmt.Sprintf("no signed envelope for own message %s", messageID))
	}
	_, eventID, err := protocol.ParseSignedEnvelope(*envelope)
	if err != nil || receipt.EventID != eventID || receipt.GlobalRoomID != hub.GlobalRoomID {
		return hubConflictError(fmt.Errorf("%w: receipt does not place own message %s as stored", store.ErrHubReceiptConflict, messageID))
	}
	placed := store.HubReceiptRecord{GlobalRoomID: receipt.GlobalRoomID, HubEpoch: receipt.HubEpoch, MembershipEpoch: receipt.MembershipEpoch,
		RoomSeq: receipt.RoomSeq, EventID: receipt.EventID, Digest: digest, PrevDigest: receipt.PrevReceiptHash, MessageID: messageID, Receipt: signed}
	return s.hubs.AttachHubReceiptContext(ctx, placed)
}
