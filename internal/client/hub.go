package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/noopolis/moltnet/pkg/protocol"
)

// ShareRoom makes a local room a shared room homed by this network.
func (c *Client) ShareRoom(ctx context.Context, roomID, history string) (protocol.HubRoomSummary, error) {
	var view protocol.HubRoomSummary
	err := c.doJSON(ctx, http.MethodPost, "/v1/hub/rooms", map[string]string{"room_id": roomID, "history": history}, &view)
	return view, err
}

// RoomHub describes the shared room a local room maps to.
func (c *Client) RoomHub(ctx context.Context, roomID string) (protocol.HubRoomInfo, error) {
	var info protocol.HubRoomInfo
	err := c.doJSON(ctx, http.MethodGet, "/v1/rooms/"+url.PathEscape(roomID)+"/hub", nil, &info)
	return info, err
}

// AppendHubControl asks the home to append one control event.
func (c *Client) AppendHubControl(ctx context.Context, globalRoomID, kind, networkID, role string) (protocol.HubRoomSummary, error) {
	var view protocol.HubRoomSummary
	body := map[string]string{"global_room_id": globalRoomID, "kind": kind, "network_id": networkID, "role": role}
	err := c.doJSON(ctx, http.MethodPost, "/v1/hub/rooms/control", body, &view)
	return view, err
}

// LeaveHubRoom makes this member network leave a shared room.
func (c *Client) LeaveHubRoom(ctx context.Context, globalRoomID string) (protocol.HubRoomSummary, error) {
	var view protocol.HubRoomSummary
	err := c.doJSON(ctx, http.MethodPost, "/v1/hub/rooms/leave", protocol.RoomLeaveRequest{GlobalRoomID: globalRoomID}, &view)
	return view, err
}

// HubRooms lists the shared rooms this network homes or joined.
func (c *Client) HubRooms(ctx context.Context) ([]protocol.HubRoomSummary, error) {
	var payload struct {
		Rooms []protocol.HubRoomSummary `json:"rooms"`
	}
	err := c.doJSON(ctx, http.MethodGet, "/v1/hub/rooms", nil, &payload)
	return payload.Rooms, err
}
