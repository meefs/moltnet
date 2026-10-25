package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

var (
	// ErrHubRoomExists means a global room id or its local room is taken.
	ErrHubRoomExists = errors.New("hub room already exists")
	// ErrHubControlConflict means a control event does not extend the
	// stored log: a gap, or a different event at an already stored seq.
	ErrHubControlConflict = errors.New("room control event conflicts with the stored log")
	// ErrHubReceiptConflict means a receipt contradicts a stored one: a
	// different receipt at the same (room, hub epoch, room seq), a different
	// placement of the same event, or a broken receipt chain. It is never
	// acknowledged as a duplicate.
	ErrHubReceiptConflict = errors.New("room receipt conflicts with a stored receipt")
)

// HubRoom maps a global room id onto a local room. HomePairingID is empty
// on the home itself and names the pairing that reaches the home elsewhere.
type HubRoom struct {
	GlobalRoomID  string    `json:"global_room_id"`
	LocalRoomID   string    `json:"local_room_id"`
	HomeNetworkID string    `json:"home_network_id"`
	HomePairingID string    `json:"home_pairing_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// IsHome reports whether this network is the room's home.
func (r HubRoom) IsHome() bool { return r.HomePairingID == "" }

// HubControlRecord is one stored, already verified control event.
type HubControlRecord struct {
	GlobalRoomID string                     `json:"global_room_id"`
	Seq          int64                      `json:"seq"`
	Digest       string                     `json:"digest"`
	Control      protocol.SignedRoomControl `json:"control"`
}

// HubReceiptRecord is one stored, already verified receipt, bound to the
// local message it placed.
type HubReceiptRecord struct {
	GlobalRoomID string                     `json:"global_room_id"`
	HubEpoch     int64                      `json:"hub_epoch"`
	RoomSeq      int64                      `json:"room_seq"`
	EventID      string                     `json:"event_id"`
	Digest       string                     `json:"digest"`
	PrevDigest   string                     `json:"prev_digest,omitempty"`
	MessageID    string                     `json:"message_id"`
	Receipt      protocol.SignedRoomReceipt `json:"receipt"`
}

// HubStore persists hub rooms, their control logs and message receipts.
// Receipts are written inside the message append (WithHubReceipt), so seq,
// receipt, history and fanout commit together.
type HubStore interface {
	CreateHubRoomContext(ctx context.Context, room HubRoom, genesis HubControlRecord) error
	HubRoomsContext(ctx context.Context) ([]HubRoom, error)
	// AppendHubControlContext appends records that must continue the stored
	// log contiguously; re-appending an identical stored record is a no-op.
	AppendHubControlContext(ctx context.Context, records []HubControlRecord) error
	HubControlLogContext(ctx context.Context, globalRoomID string, afterSeq int64) ([]HubControlRecord, error)
	HubReceiptHeadContext(ctx context.Context, globalRoomID string, hubEpoch int64) (HubReceiptRecord, bool, error)
	HubReceiptForEventContext(ctx context.Context, globalRoomID, eventID string) (HubReceiptRecord, bool, error)
	HubReceiptsForMessagesContext(ctx context.Context, messageIDs []string) (map[string]HubReceiptRecord, error)
	// NextHubOriginSeqContext reserves this network's next per-room origin
	// sequence number. Gaps are harmless; reuse never happens.
	NextHubOriginSeqContext(ctx context.Context, globalRoomID string) (int64, error)
	// ReplaceOutboundEnvelopeContext re-signs a local, not yet placed
	// message after a membership epoch change.
	ReplaceOutboundEnvelopeContext(ctx context.Context, record MessageEnvelopeRecord) error
}

type hubReceiptKey struct{}

// WithHubReceipt attaches the receipt to persist atomically with the next
// message append on ctx.
func WithHubReceipt(ctx context.Context, record HubReceiptRecord) context.Context {
	return context.WithValue(ctx, hubReceiptKey{}, &record)
}

func hubReceiptForMessage(ctx context.Context, message protocol.Message) *HubReceiptRecord {
	if ctx == nil {
		return nil
	}
	record, _ := ctx.Value(hubReceiptKey{}).(*HubReceiptRecord)
	if record == nil {
		return nil
	}
	bound := *record
	bound.MessageID = message.ID
	return &bound
}

func hubReceiptConflict(record HubReceiptRecord, reason string) error {
	return fmt.Errorf("%w: room %s hub epoch %d seq %d event %s: %s", ErrHubReceiptConflict,
		record.GlobalRoomID, record.HubEpoch, record.RoomSeq, record.EventID, reason)
}

// checkHubReceipt applies the conflict rules against the stored receipts
// at the same seq, for the same event, and at the neighbouring seqs.
// duplicate means this exact receipt is already stored.
func checkHubReceipt(record HubReceiptRecord, atSeq, forEvent, previous, next *HubReceiptRecord) (duplicate bool, err error) {
	if atSeq != nil && atSeq.Digest != record.Digest {
		return false, hubReceiptConflict(record, "a different receipt holds this room seq")
	}
	if forEvent != nil && forEvent.Digest != record.Digest {
		return false, hubReceiptConflict(record, "this event was already placed by a different receipt")
	}
	if previous != nil && previous.Digest != record.PrevDigest {
		return false, hubReceiptConflict(record, "prev_receipt_hash does not chain to the stored previous receipt")
	}
	if next != nil && next.PrevDigest != record.Digest {
		return false, hubReceiptConflict(record, "the stored next receipt does not chain to this one")
	}
	return atSeq != nil, nil
}

func controlConflict(globalRoomID string, seq int64, reason string) error {
	return fmt.Errorf("%w: room %s seq %d: %s", ErrHubControlConflict, globalRoomID, seq, reason)
}

var (
	_ HubStore = (*MemoryStore)(nil)
	_ HubStore = (*FileStore)(nil)
	_ HubStore = (*SQLStore)(nil)
)
