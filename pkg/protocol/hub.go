package protocol

import (
	"errors"
	"fmt"
	"strings"
)

// Hub rooms: one room spanning several networks. The network that creates a
// room is its home; it keeps a linear, home-signed, hash-chained control log
// (who is a member, with which role and key), orders messages with signed
// receipts, and fans them out. Origins sign their own messages
// (moltnet.envelope.v1), so the home can delay or refuse a message but never
// forge or alter one.
const (
	RoomControlVersionV1 = "moltnet.room-control.v1"
	RoomReceiptVersionV1 = "moltnet.room-receipt.v1"
	// HubEpochV1 is the only home incarnation in v1 (no MoveHome yet).
	HubEpochV1 int64 = 1
)

// Control event kinds.
const (
	RoomControlGenesis   = "genesis"
	RoomControlAdd       = "add"
	RoomControlRemove    = "remove"
	RoomControlLeave     = "leave"
	RoomControlSetRole   = "set_role"
	RoomControlSetPolicy = "set_policy"
)

// Network roles in a hub room. Agents and humans act under their network's
// role, so the roster grows by network, never by agent.
const (
	RoomRoleOwner  = "owner"
	RoomRoleAdmin  = "admin"
	RoomRoleWriter = "writer"
	RoomRoleReader = "reader"
)

// History policies, fixed at genesis so writers know their audience up front.
const (
	RoomHistoryFromJoin    = "from_join"
	RoomHistoryFullHistory = "full_history"
)

// StaleMembershipEpochCode is the error code a home answers (HTTP 412) when a
// submission was signed under an older membership epoch. It is retry-class:
// the sender syncs the control log, re-signs under the new epoch, resends.
const StaleMembershipEpochCode = "stale_membership_epoch"

var (
	ErrRoomControlMalformed = errors.New("malformed room control event")
	ErrRoomControlSignature = errors.New("room control signature is invalid")
	ErrRoomReceiptMalformed = errors.New("malformed room receipt")
	ErrRoomReceiptSignature = errors.New("room receipt signature is invalid")
)

// RoomControlEvent is one entry of a hub room's control log. Seq starts at 1
// (genesis) and every event opens a new membership epoch equal to its seq.
//
// NetworkID names the subject network (for genesis: the home itself).
// Fingerprint and PublicKey are the subject's key (genesis, add); the home
// introduces member keys to every other member this way.
type RoomControlEvent struct {
	V            string `json:"v"`
	GlobalRoomID string `json:"global_room_id"`
	Seq          int64  `json:"seq"`
	Epoch        int64  `json:"epoch"`
	PrevHash     string `json:"prev_hash,omitempty"`
	Kind         string `json:"kind"`
	NetworkID    string `json:"network_id,omitempty"`
	Fingerprint  string `json:"fingerprint,omitempty"`
	PublicKey    string `json:"public_key,omitempty"`
	Role         string `json:"role,omitempty"`
	History      string `json:"history,omitempty"`
	CreatedAt    string `json:"created_at"`
}

// SignedRoomControl carries the exact canonical bytes the home signed.
type SignedRoomControl struct {
	Control   string `json:"control"`
	Signature string `json:"signature"`
}

// RoomReceipt is the home's signed statement that it placed event EventID at
// RoomSeq under the given epochs. PrevReceiptHash chains receipts so a home
// cannot silently rewrite order a member has already seen.
type RoomReceipt struct {
	V               string `json:"v"`
	GlobalRoomID    string `json:"global_room_id"`
	HubEpoch        int64  `json:"hub_epoch"`
	MembershipEpoch int64  `json:"membership_epoch"`
	RoomSeq         int64  `json:"room_seq"`
	EventID         string `json:"event_id"`
	PrevReceiptHash string `json:"prev_receipt_hash,omitempty"`
}

// SignedRoomReceipt travels beside a home-forwarded message.
type SignedRoomReceipt struct {
	Receipt   string `json:"receipt"`
	Signature string `json:"signature"`
}

// RoomControlPage is GET /v1/hub/control's response.
type RoomControlPage struct {
	GlobalRoomID string              `json:"global_room_id"`
	Events       []SignedRoomControl `json:"events"`
	// More means the log continues after the last event (pages are bounded
	// by MaxRoomSyncPageBytes); ask again after its seq.
	More bool `json:"more,omitempty"`
}

// GlobalRoomID is "<home fingerprint>/<home-local room id>": fixed at genesis
// and never reused, so two homes' "research" rooms can never be confused.
func GlobalRoomID(homeFingerprint, roomID string) string {
	return homeFingerprint + "/" + roomID
}

// ParseGlobalRoomID splits and validates a global room id.
func ParseGlobalRoomID(value string) (fingerprint string, roomID string, err error) {
	fingerprint, roomID, ok := strings.Cut(value, "/")
	if !ok {
		return "", "", fmt.Errorf("global room id %q has no room part", value)
	}
	if normalized, err := NormalizeFingerprint(fingerprint); err != nil || normalized != fingerprint {
		return "", "", fmt.Errorf("global room id %q has a non-canonical home fingerprint", value)
	}
	if err := ValidateRoomID(roomID); err != nil {
		return "", "", fmt.Errorf("global room id %q: %w", value, err)
	}
	return fingerprint, roomID, nil
}

// RoomRoleCanWrite reports whether a network role may post messages.
func RoomRoleCanWrite(role string) bool {
	switch role {
	case RoomRoleOwner, RoomRoleAdmin, RoomRoleWriter:
		return true
	}
	return false
}

// ValidMemberRole is a role the home may grant a member (never owner).
func ValidMemberRole(role string) bool {
	switch role {
	case RoomRoleAdmin, RoomRoleWriter, RoomRoleReader:
		return true
	}
	return false
}

// ValidRoomHistory reports whether policy is a known history policy.
func ValidRoomHistory(policy string) bool {
	return policy == RoomHistoryFromJoin || policy == RoomHistoryFullHistory
}
