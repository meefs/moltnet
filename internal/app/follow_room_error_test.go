package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestFollowPrivateSelectedRoomDoesNotStallHealthyRooms(t *testing.T) {
	origin := newFollowApp(t, "origin", true, nil)
	healthy := protocol.CreateRoomRequest{ID: "healthy", Visibility: "public", WritePolicy: protocol.RoomWritePolicyOperators}
	response := followRequest(origin.Handler(), http.MethodPost, "/v1/rooms", "operator", healthy)
	if response.Code != http.StatusCreated {
		t.Fatal(response.Body.String())
	}
	remote := httptest.NewServer(origin.Handler())
	defer remote.Close()
	follower := newFollowApp(t, "follower", false, []protocol.Follow{{ID: "news", RemoteBaseURL: remote.URL, Rooms: []string{"public", "healthy"}, PollIntervalSeconds: 1}})
	followWait(t, func() bool { return followStatus(t, follower.Handler()).Status == "live" })
	private := protocol.CreateRoomRequest{ID: "public", Visibility: "private", WritePolicy: protocol.RoomWritePolicyOperators}
	response = followRequest(origin.Handler(), http.MethodPost, "/v1/apply", "operator", protocol.ApplyConfigRequest{Rooms: []protocol.CreateRoomRequest{private}})
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	followSend(t, origin.Handler(), "healthy-live", "healthy", protocol.TargetKindRoom, "")
	followWait(t, func() bool {
		status := followStatus(t, follower.Handler())
		if status.Status != "live" || followMessageCount(follower.Handler(), "news.healthy") != 1 {
			return false
		}
		for _, room := range status.Rooms {
			if room.RemoteRoomID == "public" {
				return room.LastError != ""
			}
		}
		return false
	})
	private.Visibility = "public"
	response = followRequest(origin.Handler(), http.MethodPost, "/v1/apply", "operator", protocol.ApplyConfigRequest{Rooms: []protocol.CreateRoomRequest{private}})
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	followSend(t, origin.Handler(), "restored", "public", protocol.TargetKindRoom, "")
	followWait(t, func() bool {
		if followMessageCount(follower.Handler(), "news.public") != 1 {
			return false
		}
		for _, room := range followStatus(t, follower.Handler()).Rooms {
			if room.RemoteRoomID == "public" {
				return room.LastError == ""
			}
		}
		return false
	})
}
