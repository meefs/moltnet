package protocol

import "time"

// Follow describes an anonymous, pull-only subscription to a public network.
type Follow struct {
	ID                  string            `json:"id" yaml:"id"`
	RemoteBaseURL       string            `json:"remote_base_url" yaml:"remote_base_url"`
	RemoteNetworkID     string            `json:"remote_network_id,omitempty" yaml:"remote_network_id,omitempty"`
	Rooms               []string          `json:"rooms,omitempty" yaml:"rooms,omitempty"`
	RoomMapping         map[string]string `json:"room_mapping,omitempty" yaml:"room_mapping,omitempty"`
	Backfill            *int              `json:"backfill,omitempty" yaml:"backfill,omitempty"`
	Visibility          string            `json:"visibility,omitempty" yaml:"visibility,omitempty"`
	PollIntervalSeconds int               `json:"poll_interval_seconds,omitempty" yaml:"poll_interval_seconds,omitempty"`
	ReconnectMinSeconds int               `json:"reconnect_min_seconds,omitempty" yaml:"reconnect_min_seconds,omitempty"`
	ReconnectMaxSeconds int               `json:"reconnect_max_seconds,omitempty" yaml:"reconnect_max_seconds,omitempty"`
}

func (f Follow) LocalRoomID(remote string) string {
	if local, ok := f.RoomMapping[remote]; ok {
		return local
	}
	return f.ID + "." + remote
}

func (f Follow) BackfillLimit() int {
	if f.Backfill == nil {
		return 200
	}
	return *f.Backfill
}

func (f Follow) WithDefaults() Follow {
	if f.Visibility == "" {
		f.Visibility = RoomVisibilityPrivate
	}
	if f.PollIntervalSeconds == 0 {
		f.PollIntervalSeconds = 30
	}
	if f.ReconnectMinSeconds == 0 {
		f.ReconnectMinSeconds = 1
	}
	if f.ReconnectMaxSeconds == 0 {
		f.ReconnectMaxSeconds = 60
	}
	return f
}

type RoomFollow struct {
	ID            string `json:"id"`
	RemoteBaseURL string `json:"remote_base_url"`
	RemoteRoomID  string `json:"remote_room_id"`
}

type FollowStatus struct {
	ID              string             `json:"id"`
	RemoteBaseURL   string             `json:"remote_base_url"`
	RemoteNetworkID string             `json:"remote_network_id,omitempty"`
	Status          string             `json:"status"`
	LastError       string             `json:"last_error,omitempty"`
	LastSynced      *time.Time         `json:"last_synced,omitempty"`
	Rooms           []FollowRoomStatus `json:"rooms"`
}

type FollowRoomStatus struct {
	RemoteRoomID string `json:"remote_room_id"`
	LocalRoomID  string `json:"local_room_id"`
	MessageCount int    `json:"message_count"`
}

type FollowPage struct {
	Follows []FollowStatus `json:"follows"`
}
