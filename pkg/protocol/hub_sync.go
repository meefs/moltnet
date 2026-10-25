package protocol

// Hub room catch-up. A member asks the home for everything after the last
// room seq it holds contiguously; the home answers with every receipt in
// order (so the member can verify the whole receipt chain) and, beside the
// receipts placed while the member may see them, the exact hub request the
// home fans out (signed envelope included). The home clamps the page to the
// member's membership intervals, never to the member's cursor.

// MaxRoomSyncPage bounds one GET /v1/hub/sync page.
const MaxRoomSyncPage = 200

// MaxPairingPayloadBytes is the largest JSON body a pairing transport
// carries in either direction: an HTTP request or response, or a relay
// frame's payload. Live hub delivery and catch-up share it.
const MaxPairingPayloadBytes = 1 << 20

// MaxRoomSyncPageBytes is the encoded size a sync page of several entries
// aims to stay under, control events included. A page always carries at
// least one control event or entry, so it always advances; a page with one
// entry may grow to the transport limit (MaxPairingPayloadBytes).
const MaxRoomSyncPageBytes = 640 << 10

// RoomSyncWithheldTooLarge marks a sync entry whose message the home
// accepted but cannot fit in any page, even alone (it came within the page's
// own few hundred bytes of the transport limit). The member stores the
// receipt as withheld and shows it; it is never treated as delivered.
const RoomSyncWithheldTooLarge = "too_large"

// RoomSyncEntry is one placed room seq. Message is nil for a seq the
// caller may not see (placed while it was not a member, or before its join
// in a from_join room): the receipt alone keeps the chain verifiable.
//
// The message carries no receipt of its own in a page (Receipt is the one):
// that keeps an entry no larger than the live delivery of the same message.
// Withheld names why a visible message is missing (RoomSyncWithheldTooLarge).
type RoomSyncEntry struct {
	Receipt  SignedRoomReceipt   `json:"receipt"`
	Message  *SendMessageRequest `json:"message,omitempty"`
	Withheld string              `json:"withheld,omitempty"`
}

// RoomSyncPage is GET /v1/hub/sync's response. ThroughSeq is the last room
// seq this page accounts for (Entries cover every seq after the request's
// cursor up to it); More means a further page exists. Control carries the
// control events after the request's control cursor that the caller may
// read, so one round trip brings the roster up to the entries' epochs.
type RoomSyncPage struct {
	GlobalRoomID string              `json:"global_room_id"`
	Control      []SignedRoomControl `json:"control"`
	Entries      []RoomSyncEntry     `json:"entries"`
	ThroughSeq   int64               `json:"through_seq"`
	More         bool                `json:"more"`
}

// HubAdmissionVersionV1 versions the statement a joining member signs.
const HubAdmissionVersionV1 = "moltnet.room-admission.v1"

// RoomLeaveRequest is POST /v1/hub/leave: the caller's network leaves.
type RoomLeaveRequest struct {
	GlobalRoomID string `json:"global_room_id"`
}
