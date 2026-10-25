package pairings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/noopolis/moltnet/pkg/protocol"
)

// Hub room requests a member makes to its room's home: redeeming a room
// invite (POST /v1/hub/admit) and leaving (POST /v1/hub/leave).
const (
	RoomAdmitPath = "/v1/hub/admit"
	RoomLeavePath = "/v1/hub/leave"
)

// RequestRoomAdmission redeems a room invite over its pairing.
func (c *Client) RequestRoomAdmission(ctx context.Context, pairing protocol.Pairing, admission protocol.SignedRoomAdmission) (protocol.RoomControlPage, error) {
	return c.postHub(ctx, pairing, RoomAdmitPath, admission)
}

// RequestRoomLeave asks the home to record this network leaving a room.
func (c *Client) RequestRoomLeave(ctx context.Context, pairing protocol.Pairing, request protocol.RoomLeaveRequest) (protocol.RoomControlPage, error) {
	return c.postHub(ctx, pairing, RoomLeavePath, request)
}

func (c *Client) postHub(ctx context.Context, pairing protocol.Pairing, path string, payload any) (protocol.RoomControlPage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return protocol.RoomControlPage{}, fmt.Errorf("encode %s: %w", path, err)
	}
	var page protocol.RoomControlPage
	if pairing.Relay != nil {
		err = c.relay.callJSON(ctx, pairing, http.MethodPost, path, body, &page)
		return page, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(pairing, path), bytes.NewReader(body))
	if err != nil {
		return protocol.RoomControlPage{}, fmt.Errorf("build %s request: %w", path, err)
	}
	request.Header.Set("Content-Type", "application/json")
	authorizeRequest(request, pairing)
	if err := c.http.doJSON(request, &page); err != nil {
		return protocol.RoomControlPage{}, err
	}
	return page, nil
}
