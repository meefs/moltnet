package protocol

// Hub room catch-up. A member asks the home for everything after the last
// room seq it holds contiguously; the home answers with every receipt in
// order (so the member can verify the whole receipt chain) and, beside the
// receipts placed while the member may see them, the exact hub request the
// home fans out (signed envelope included). The home clamps the page to the
// member's membership intervals, never to the member's cursor.

// MaxRoomSyncPage bounds one GET /v1/hub/sync page.
const MaxRoomSyncPage = 200

// MaxRoomSyncPageBytes is the encoded size a sync page aims to stay under,
// control events included: pairing transports (HTTP and relay) accept
// responses up to 1 MiB. A page always carries at least one control event or
// entry, so it always advances.
const MaxRoomSyncPageBytes = 640 << 10

// MaxRoomSyncEntryBytes caps one entry. A message too large to ever fit a
// page travels receipt-only, so the chain still advances; such a message
// could not cross a pairing transport live either.
const MaxRoomSyncEntryBytes = 900 << 10

// RoomSyncEntry is one placed room seq. Message is nil for a seq the
// caller may not see (placed while it was not a member, or before its join
// in a from_join room): the receipt alone keeps the chain verifiable.
type RoomSyncEntry struct {
	Receipt SignedRoomReceipt   `json:"receipt"`
	Message *SendMessageRequest `json:"message,omitempty"`
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
