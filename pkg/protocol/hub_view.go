package protocol

// HubRoomInfo is a shared room as one network sees it (GET
// /v1/rooms/{id}/hub): its home, the roster from its control log with this
// network's trust in each member's key, and the control events themselves,
// for display. It is an HTTP view, never part of the machine v1 contract.
type HubRoomInfo struct {
	GlobalRoomID    string           `json:"global_room_id"`
	LocalRoomID     string           `json:"local_room_id"`
	HomeNetworkID   string           `json:"home_network_id"`
	HomePairingID   string           `json:"home_pairing_id,omitempty"`
	IsHome          bool             `json:"is_home"`
	MembershipEpoch int64            `json:"membership_epoch"`
	History         string           `json:"history"`
	Members         []HubMemberInfo  `json:"members"`
	Events          []HubControlInfo `json:"events"`
}

// HubMemberInfo is one member network. Trust is this network's trust in
// its key: verified, introduced, unconfirmed, or self.
type HubMemberInfo struct {
	NetworkID   string `json:"network_id"`
	Role        string `json:"role"`
	Fingerprint string `json:"fingerprint"`
	Trust       string `json:"trust"`
}

// HubControlInfo is one verified control event, for display.
type HubControlInfo struct {
	Seq       int64  `json:"seq"`
	Epoch     int64  `json:"epoch"`
	Kind      string `json:"kind"`
	NetworkID string `json:"network_id,omitempty"`
	Role      string `json:"role,omitempty"`
	History   string `json:"history,omitempty"`
	CreatedAt string `json:"created_at"`
}

// HubTrustSelf labels this network's own entry in a roster.
const HubTrustSelf = "self"

// HubRoomSummary is the operator view the hub administration endpoints
// return (POST /v1/hub/rooms, /v1/hub/rooms/control, ...): a hub room with
// its current roster as network id -> role.
type HubRoomSummary struct {
	GlobalRoomID    string            `json:"global_room_id"`
	LocalRoomID     string            `json:"local_room_id"`
	HomeNetworkID   string            `json:"home_network_id"`
	HomePairingID   string            `json:"home_pairing_id,omitempty"`
	MembershipEpoch int64             `json:"membership_epoch"`
	History         string            `json:"history"`
	Members         map[string]string `json:"members"`
}
