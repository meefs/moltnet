package transport

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/internal/rooms"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// hubService is optional so test fakes of Service keep working.
type hubService interface {
	CreateHubRoomContext(ctx context.Context, roomID, history string) (rooms.HubRoomView, error)
	AppendHubControlContext(ctx context.Context, request rooms.HubControlRequest) (rooms.HubRoomView, error)
	JoinHubRoomContext(ctx context.Context, request rooms.HubJoinRequest) (rooms.HubRoomView, error)
	SyncHubRoomContext(ctx context.Context, globalRoomID string) (rooms.HubRoomView, error)
	HubRoomsContext(ctx context.Context) ([]rooms.HubRoomView, error)
	HubControlPageContext(ctx context.Context, globalRoomID string, afterSeq int64) (protocol.RoomControlPage, error)
	HubSyncPageContext(ctx context.Context, globalRoomID string, afterSeq, controlAfter int64) (protocol.RoomSyncPage, error)
	RoomHubContext(ctx context.Context, roomID string) (protocol.HubRoomInfo, error)
	AdmitHubMemberContext(ctx context.Context, admission protocol.SignedRoomAdmission) (protocol.RoomControlPage, error)
	LeaveHubPeerContext(ctx context.Context, request protocol.RoomLeaveRequest) (protocol.RoomControlPage, error)
	LeaveHubRoomContext(ctx context.Context, globalRoomID string) (rooms.HubRoomView, error)
}

var hubControlScopes = []authn.Scope{authn.ScopeObserve, authn.ScopeAdmin, authn.ScopePair}

// registerHubRoutes serves hub rooms: the control log any admitted member
// network may pull from the home (GET /v1/hub/control, also reachable over
// relay pairings), and operator-only administration.
func registerHubRoutes(mux *http.ServeMux, policy *authn.Policy, service Service) {
	hubs, ok := service.(hubService)
	if !ok {
		return
	}
	mux.HandleFunc("GET /v1/hub/control", authorizedAnyWithVerifier(policy, service, hubControlScopes, func(response http.ResponseWriter, request *http.Request) {
		after, err := strconv.ParseInt(defaultString(request.URL.Query().Get("after"), "0"), 10, 64)
		if err != nil || after < 0 {
			writeError(response, http.StatusUnprocessableEntity, err)
			return
		}
		page, err := hubs.HubControlPageContext(request.Context(), request.URL.Query().Get("room"), after)
		respondHub(response, page, err, http.StatusOK)
	}))
	mux.HandleFunc("GET /v1/hub/sync", authorizedAnyWithVerifier(policy, service, hubControlScopes, func(response http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		after, errAfter := strconv.ParseInt(defaultString(query.Get("after"), "0"), 10, 64)
		controlAfter, errControl := strconv.ParseInt(defaultString(query.Get("control_after"), "0"), 10, 64)
		if errAfter != nil || errControl != nil || after < 0 || controlAfter < 0 {
			writeError(response, http.StatusUnprocessableEntity, errors.New("after and control_after must be non-negative integers"))
			return
		}
		page, err := hubs.HubSyncPageContext(request.Context(), query.Get("room"), after, controlAfter)
		respondHub(response, page, err, http.StatusOK)
	}))
	mux.HandleFunc("GET /v1/rooms/{roomID}/hub", publicInOpen(policy, service, readScopes, func(response http.ResponseWriter, request *http.Request) {
		info, err := hubs.RoomHubContext(request.Context(), request.PathValue("roomID"))
		respondHub(response, info, err, http.StatusOK)
	}))
	mux.HandleFunc("POST /v1/hub/admit", authorizedWithVerifier(policy, service, authn.ScopePair, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var payload protocol.SignedRoomAdmission
		if err := decodeJSON(response, request, &payload); err != nil {
			writeError(response, http.StatusBadRequest, err)
			return
		}
		page, err := hubs.AdmitHubMemberContext(request.Context(), payload)
		respondHub(response, page, err, http.StatusOK)
	})))
	mux.HandleFunc("POST /v1/hub/leave", authorizedWithVerifier(policy, service, authn.ScopePair, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var payload protocol.RoomLeaveRequest
		if err := decodeJSON(response, request, &payload); err != nil {
			writeError(response, http.StatusBadRequest, err)
			return
		}
		page, err := hubs.LeaveHubPeerContext(request.Context(), payload)
		respondHub(response, page, err, http.StatusOK)
	})))
	mux.HandleFunc("POST /v1/hub/rooms/leave", authorizedWithVerifier(policy, service, authn.ScopeAdmin, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var payload protocol.RoomLeaveRequest
		if err := decodeJSON(response, request, &payload); err != nil {
			writeError(response, http.StatusBadRequest, err)
			return
		}
		view, err := hubs.LeaveHubRoomContext(request.Context(), payload.GlobalRoomID)
		respondHub(response, view, err, http.StatusOK)
	})))
	mux.HandleFunc("GET /v1/hub/rooms", authorizedWithVerifier(policy, service, authn.ScopeAdmin, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		views, err := hubs.HubRoomsContext(request.Context())
		respondHub(response, map[string]any{"rooms": views}, err, http.StatusOK)
	})))
	mux.HandleFunc("POST /v1/hub/rooms", authorizedWithVerifier(policy, service, authn.ScopeAdmin, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var payload struct {
			RoomID  string `json:"room_id"`
			History string `json:"history,omitempty"`
		}
		if err := decodeJSON(response, request, &payload); err != nil {
			writeError(response, http.StatusBadRequest, err)
			return
		}
		view, err := hubs.CreateHubRoomContext(request.Context(), payload.RoomID, payload.History)
		respondHub(response, view, err, http.StatusCreated)
	})))
	mux.HandleFunc("POST /v1/hub/rooms/control", authorizedWithVerifier(policy, service, authn.ScopeAdmin, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var payload rooms.HubControlRequest
		if err := decodeJSON(response, request, &payload); err != nil {
			writeError(response, http.StatusBadRequest, err)
			return
		}
		view, err := hubs.AppendHubControlContext(request.Context(), payload)
		respondHub(response, view, err, http.StatusOK)
	})))
	mux.HandleFunc("POST /v1/hub/rooms/sync", authorizedWithVerifier(policy, service, authn.ScopeAdmin, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var payload struct {
			GlobalRoomID string `json:"global_room_id"`
		}
		if err := decodeJSON(response, request, &payload); err != nil {
			writeError(response, http.StatusBadRequest, err)
			return
		}
		view, err := hubs.SyncHubRoomContext(request.Context(), payload.GlobalRoomID)
		respondHub(response, view, err, http.StatusOK)
	})))
	mux.HandleFunc("POST /v1/hub/rooms/join", authorizedWithVerifier(policy, service, authn.ScopeAdmin, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var payload rooms.HubJoinRequest
		if err := decodeJSON(response, request, &payload); err != nil {
			writeError(response, http.StatusBadRequest, err)
			return
		}
		view, err := hubs.JoinHubRoomContext(request.Context(), payload)
		respondHub(response, view, err, http.StatusCreated)
	})))
}

func respondHub(response http.ResponseWriter, value any, err error, status int) {
	if err != nil {
		writeError(response, statusForError(err), err)
		return
	}
	writeJSON(response, status, value)
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
