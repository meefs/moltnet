package transport

import (
	"context"
	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
	"net/http"
	"strings"
	"sync"
)

type fakeStatusError struct {
	status int
	msg    string
}

func (e fakeStatusError) Error() string {
	return e.msg
}

func (e fakeStatusError) StatusCode() int {
	return e.status
}

type fakeService struct {
	mu                 sync.Mutex
	network            protocol.Network
	pairNetwork        protocol.Network
	agents             []protocol.AgentSummary
	pairAgents         []protocol.AgentSummary
	pairings           []protocol.Pairing
	pairingsPage       protocol.PairingPage
	pairListErr        error
	rooms              []protocol.Room
	pairRooms          []protocol.Room
	roomPage           protocol.MessagePage
	appliedConfig      protocol.ApplyConfigRequest
	threads            []protocol.Thread
	threadPage         protocol.MessagePage
	threadsPage        protocol.ThreadPage
	dms                []protocol.DirectConversation
	dmListPage         protocol.DirectConversationPage
	dmPage             protocol.MessagePage
	artifactPage       protocol.ArtifactPage
	createdRoom        protocol.CreateRoomRequest
	registeredAgent    protocol.RegisterAgentRequest
	removedAgentID     string
	removedRoomID      string
	connectedAgents    []protocol.Actor
	disconnectedAgents []protocol.Actor
	disconnectReasons  []string
	disconnectErrors   []string
	deliveredWakes     []protocol.Event
	failedWakes        []protocol.Event
	failedWakeAgents   []protocol.Actor
	failedWakeDetails  []protocol.WakeFailureDetails
	updatedRoom        protocol.UpdateRoomMembersRequest
	joinRoom           protocol.JoinRoomRequest
	sentMessage        protocol.SendMessageRequest
	roomID             string
	roomBefore         string
	roomAfter          string
	threadID           string
	threadBefore       string
	threadAfter        string
	dmID               string
	dmBefore           string
	dmAfter            string
	agentBefore        string
	agentAfter         string
	pairBefore         string
	pairAfter          string
	pairRoomPage       protocol.RoomPage
	pairAgentPage      protocol.AgentPage
	limit              int
	createErr          error
	pairingErr         error
	roomError          error
	threadError        error
	dmError            error
	artifactErr        error
	sendErr            error
	healthErr          error
	stream             chan protocol.Event
	replayStream       chan protocol.Event
	agentTokenClaims   map[string]authn.Claims
}

func (f *fakeService) Health(ctx context.Context) error { return f.healthErr }
func (f *fakeService) Network() protocol.Network        { return f.network }
func (f *fakeService) GetAgent(agentID string) (protocol.AgentSummary, error) {
	return f.GetAgentContext(context.Background(), agentID)
}
func (f *fakeService) GetAgentContext(ctx context.Context, agentID string) (protocol.AgentSummary, error) {
	for _, agent := range f.agents {
		if agent.ID == agentID {
			return agent, nil
		}
	}
	return protocol.AgentSummary{}, fakeStatusError{status: http.StatusNotFound, msg: "unknown agent"}
}
func (f *fakeService) GetDirectConversation(dmID string) (protocol.DirectConversation, error) {
	for _, dm := range f.dms {
		if dm.ID == dmID {
			return dm, nil
		}
	}
	return protocol.DirectConversation{}, fakeStatusError{status: http.StatusNotFound, msg: "unknown dm"}
}
func (f *fakeService) GetRoom(roomID string) (protocol.Room, error) {
	return f.GetRoomContext(context.Background(), roomID)
}
func (f *fakeService) GetRoomContext(ctx context.Context, roomID string) (protocol.Room, error) {
	for _, room := range f.rooms {
		if room.ID == roomID {
			return room, nil
		}
	}
	return protocol.Room{}, fakeStatusError{status: http.StatusNotFound, msg: "unknown room"}
}
func (f *fakeService) GetThread(threadID string) (protocol.Thread, error) {
	return f.GetThreadContext(context.Background(), threadID)
}
func (f *fakeService) GetThreadContext(ctx context.Context, threadID string) (protocol.Thread, error) {
	for _, thread := range f.threads {
		if thread.ID == threadID {
			return thread, nil
		}
	}
	return protocol.Thread{}, fakeStatusError{status: http.StatusNotFound, msg: "unknown thread"}
}
func (f *fakeService) ListAgentsContext(ctx context.Context, page protocol.PageRequest) (protocol.AgentPage, error) {
	f.agentBefore = page.Before
	f.agentAfter = page.After
	f.limit = page.Limit
	return protocol.AgentPage{Agents: f.agents}, nil
}
func (f *fakeService) AgentConnected(ctx context.Context, agent protocol.Actor) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connectedAgents = append(f.connectedAgents, agent)
}
func (f *fakeService) AgentDisconnected(ctx context.Context, agent protocol.Actor, reason string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disconnectedAgents = append(f.disconnectedAgents, agent)
	f.disconnectReasons = append(f.disconnectReasons, reason)
	if err != nil {
		f.disconnectErrors = append(f.disconnectErrors, err.Error())
		return
	}
	f.disconnectErrors = append(f.disconnectErrors, "")
}
func (f *fakeService) AgentWakeDelivered(ctx context.Context, agent protocol.Actor, event protocol.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deliveredWakes = append(f.deliveredWakes, event)
}
func (f *fakeService) AgentWakeFailed(ctx context.Context, agent protocol.Actor, event protocol.Event, err error, details protocol.WakeFailureDetails) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failedWakes = append(f.failedWakes, event)
	f.failedWakeAgents = append(f.failedWakeAgents, agent)
	f.failedWakeDetails = append(f.failedWakeDetails, details)
}
func (f *fakeService) ListPairingsContext(ctx context.Context, page protocol.PageRequest) (protocol.PairingPage, error) {
	f.pairBefore = page.Before
	f.pairAfter = page.After
	f.limit = page.Limit
	if f.pairListErr != nil {
		return protocol.PairingPage{}, f.pairListErr
	}
	if len(f.pairingsPage.Pairings) > 0 || f.pairingsPage.Page != (protocol.PageInfo{}) {
		return f.pairingsPage, nil
	}
	return protocol.PairingPage{Pairings: f.pairings}, nil
}

// PairingForPairScopedContext is a minimal stand-in for
// rooms.Service.PairingForPairScopedContext (internal/rooms/federation_access.go):
// token-id-first against f.pairings, falling back to claims.Network(),
// mirroring the one real resolution strategy filterPairScopedRoomDiscovery
// (federation_discovery.go) now delegates to via the Service interface,
// rather than a second, independent one of fakeService's own.
func (f *fakeService) PairingForPairScopedContext(ctx context.Context) (protocol.Pairing, bool) {
	claims, ok := authn.ClaimsFromContext(ctx)
	if !ok {
		return protocol.Pairing{}, false
	}
	for _, pairing := range f.pairings {
		if pairing.ID == claims.TokenID {
			return pairing, true
		}
	}
	remoteNetworkID := claims.Network()
	if remoteNetworkID == "" {
		return protocol.Pairing{}, false
	}
	for _, pairing := range f.pairings {
		if pairing.RemoteNetworkID == remoteNetworkID {
			return pairing, true
		}
	}
	return protocol.Pairing{}, false
}
func (f *fakeService) PairingNetwork(ctx context.Context, pairingID string) (protocol.Network, error) {
	if f.pairingErr != nil {
		return protocol.Network{}, f.pairingErr
	}
	return f.pairNetwork, nil
}
func (f *fakeService) PairingRoomsContext(ctx context.Context, pairingID string, page protocol.PageRequest) (protocol.RoomPage, error) {
	f.roomBefore = page.Before
	f.roomAfter = page.After
	f.limit = page.Limit
	if f.pairingErr != nil {
		return protocol.RoomPage{}, f.pairingErr
	}
	if len(f.pairRoomPage.Rooms) > 0 || f.pairRoomPage.Page != (protocol.PageInfo{}) {
		return f.pairRoomPage, nil
	}
	return protocol.RoomPage{Rooms: f.pairRooms}, nil
}
func (f *fakeService) PairingAgentsContext(ctx context.Context, pairingID string, page protocol.PageRequest) (protocol.AgentPage, error) {
	f.agentBefore = page.Before
	f.agentAfter = page.After
	f.limit = page.Limit
	if f.pairingErr != nil {
		return protocol.AgentPage{}, f.pairingErr
	}
	if len(f.pairAgentPage.Agents) > 0 || f.pairAgentPage.Page != (protocol.PageInfo{}) {
		return f.pairAgentPage, nil
	}
	return protocol.AgentPage{Agents: f.pairAgents}, nil
}
func (f *fakeService) ListRoomsContext(ctx context.Context, page protocol.PageRequest) (protocol.RoomPage, error) {
	f.roomBefore = page.Before
	f.roomAfter = page.After
	f.limit = page.Limit
	return protocol.RoomPage{Rooms: f.rooms}, nil
}
func (f *fakeService) ApplyConfigContext(ctx context.Context, request protocol.ApplyConfigRequest) (protocol.ApplyConfigResult, error) {
	f.appliedConfig = request
	return protocol.ApplyConfigResult{Applied: true}, nil
}
func (f *fakeService) CreateRoomContext(ctx context.Context, request protocol.CreateRoomRequest) (protocol.Room, error) {
	f.createdRoom = request
	if f.createErr != nil {
		return protocol.Room{}, f.createErr
	}
	return protocol.Room{ID: request.ID, Name: request.Name}, nil
}
func (f *fakeService) UpdateRoomMembers(ctx context.Context, roomID string, request protocol.UpdateRoomMembersRequest) (protocol.Room, error) {
	f.roomID = roomID
	f.updatedRoom = request
	return protocol.Room{ID: roomID, Members: append(append([]string(nil), request.Add...), request.Remove...)}, nil
}
func (f *fakeService) JoinRoomContext(ctx context.Context, roomID string, request protocol.JoinRoomRequest) (protocol.Room, error) {
	f.roomID = roomID
	f.joinRoom = request
	return protocol.Room{ID: roomID, Members: []string{request.From.ID}}, nil
}
func (f *fakeService) RegisterAgentContext(ctx context.Context, request protocol.RegisterAgentRequest) (store.RegistrationResult, error) {
	f.registeredAgent = request
	agentID := strings.TrimSpace(request.RequestedAgentID)
	if agentID == "" {
		agentID = "agent"
	}
	return store.RegistrationResult{Created: true, AgentRegistration: protocol.AgentRegistration{
		NetworkID:   "local",
		AgentID:     agentID,
		ActorUID:    "actor_1",
		ActorURI:    protocol.AgentFQID("local", agentID),
		DisplayName: request.Name,
	}}, nil
}
func (f *fakeService) RemoveAgentContext(ctx context.Context, agentID string) (protocol.RemoveResult, error) {
	f.removedAgentID = agentID
	return protocol.RemoveResult{Removed: true, Kind: "agent", ID: agentID, Mode: "soft"}, nil
}
func (f *fakeService) RemoveRoomContext(ctx context.Context, roomID string) (protocol.RemoveResult, error) {
	f.removedRoomID = roomID
	return protocol.RemoveResult{Removed: true, Kind: "room", ID: roomID, Mode: "soft"}, nil
}
func (f *fakeService) ListRoomMessagesContext(ctx context.Context, roomID string, page protocol.PageRequest) (protocol.MessagePage, error) {
	f.roomID = roomID
	f.roomBefore = page.Before
	f.limit = page.Limit
	if f.roomError != nil {
		return protocol.MessagePage{}, f.roomError
	}
	return f.roomPage, nil
}
func (f *fakeService) ListThreadsContext(ctx context.Context, roomID string, page protocol.PageRequest) (protocol.ThreadPage, error) {
	f.roomID = roomID
	f.threadBefore = page.Before
	f.threadAfter = page.After
	f.limit = page.Limit
	if f.threadError != nil {
		return protocol.ThreadPage{}, f.threadError
	}
	if len(f.threadsPage.Threads) > 0 || f.threadsPage.Page != (protocol.PageInfo{}) {
		return f.threadsPage, nil
	}
	return protocol.ThreadPage{Threads: f.threads}, nil
}
func (f *fakeService) ListThreadMessagesContext(ctx context.Context, threadID string, page protocol.PageRequest) (protocol.MessagePage, error) {
	f.threadID = threadID
	f.threadBefore = page.Before
	f.limit = page.Limit
	if f.threadError != nil {
		return protocol.MessagePage{}, f.threadError
	}
	return f.threadPage, nil
}
func (f *fakeService) ListDirectConversationsContext(ctx context.Context, page protocol.PageRequest) (protocol.DirectConversationPage, error) {
	f.dmBefore = page.Before
	f.dmAfter = page.After
	f.limit = page.Limit
	if len(f.dmListPage.DMs) > 0 || f.dmListPage.Page != (protocol.PageInfo{}) {
		return f.dmListPage, nil
	}
	return protocol.DirectConversationPage{DMs: f.dms}, nil
}
func (f *fakeService) ListDMMessagesContext(ctx context.Context, dmID string, page protocol.PageRequest) (protocol.MessagePage, error) {
	f.dmID = dmID
	f.dmBefore = page.Before
	f.limit = page.Limit
	if f.dmError != nil {
		return protocol.MessagePage{}, f.dmError
	}
	return f.dmPage, nil
}
func (f *fakeService) ListArtifactsContext(ctx context.Context, filter protocol.ArtifactFilter, page protocol.PageRequest) (protocol.ArtifactPage, error) {
	f.roomID = filter.RoomID
	f.threadID = filter.ThreadID
	f.dmID = filter.DMID
	f.roomBefore = page.Before
	f.limit = page.Limit
	if f.artifactErr != nil {
		return protocol.ArtifactPage{}, f.artifactErr
	}
	return f.artifactPage, nil
}
func (f *fakeService) SendMessageContext(ctx context.Context, request protocol.SendMessageRequest) (protocol.MessageAccepted, error) {
	f.sentMessage = request
	if err := protocol.ValidateSendMessageRequest(request); err != nil {
		return protocol.MessageAccepted{}, fakeStatusError{
			status: http.StatusUnprocessableEntity,
			msg:    err.Error(),
		}
	}
	if f.sendErr != nil {
		return protocol.MessageAccepted{}, f.sendErr
	}
	return protocol.MessageAccepted{MessageID: "msg_1", EventID: "evt_1", Accepted: true}, nil
}
func (f *fakeService) Subscribe(ctx context.Context) <-chan protocol.Event {
	if f.stream == nil {
		ch := make(chan protocol.Event)
		close(ch)
		return ch
	}
	return f.stream
}
func (f *fakeService) SubscribeFrom(ctx context.Context, lastEventID string) <-chan protocol.Event {
	if f.replayStream != nil {
		return f.replayStream
	}
	return f.Subscribe(ctx)
}
