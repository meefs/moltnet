package rooms

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/noopolis/moltnet/pkg/protocol"
)

const removeModeSoft = "soft"

func (s *Service) RemoveAgentContext(ctx context.Context, agentID string) (protocol.RemoveResult, error) {
	id := strings.TrimSpace(agentID)
	if err := protocol.ValidateMemberID(id); err != nil {
		return protocol.RemoveResult{}, unknownAgentError(id)
	}

	agent, err := s.GetAgent(id)
	if err != nil {
		return protocol.RemoveResult{}, err
	}
	now, err := s.now()
	if err != nil {
		return protocol.RemoveResult{}, err
	}
	if err := s.removeAgent(ctx, id); err != nil {
		return protocol.RemoveResult{}, err
	}
	if err := s.removeRegisteredAgent(ctx, id); err != nil {
		return protocol.RemoveResult{}, err
	}

	s.agentPresenceMu.Lock()
	delete(s.connectedAgents, id)
	s.agentPresenceMu.Unlock()

	s.publishEvent(protocol.Event{
		ID:        newPrefixedID("evt"),
		Type:      protocol.EventTypeAgentRemoved,
		NetworkID: s.networkID,
		Agent: &protocol.AgentEvent{
			AgentID:   id,
			NetworkID: s.networkID,
			FQID:      agent.FQID,
			Name:      agent.Name,
			Reason:    "removed",
		},
		CreatedAt: now,
	})

	return protocol.RemoveResult{Removed: true, Kind: "agent", ID: id, Mode: removeModeSoft}, nil
}

func (s *Service) RemoveRoomContext(ctx context.Context, roomID string) (protocol.RemoveResult, error) {
	s.followApplyMu.Lock()
	defer s.followApplyMu.Unlock()
	id := strings.TrimSpace(roomID)
	if err := protocol.ValidateRoomID(id); err != nil {
		return protocol.RemoveResult{}, invalidRoomIDError()
	}

	room, ok, err := s.getRoom(ctx, id)
	if err != nil {
		return protocol.RemoveResult{}, err
	}
	if !ok {
		return protocol.RemoveResult{}, unknownRoomError(id)
	}
	if room.Follow != nil {
		for _, configured := range s.follows.Status() {
			if configured.ID == room.Follow.ID {
				return protocol.RemoveResult{}, &Error{status: http.StatusConflict, msg: fmt.Sprintf("room %q is a follow mirror; remove follow %q first", id, room.Follow.ID)}
			}
		}
	}
	now, err := s.now()
	if err != nil {
		return protocol.RemoveResult{}, err
	}
	if err := s.removeRoom(ctx, id); err != nil {
		return protocol.RemoveResult{}, err
	}
	s.clearRoomCredential(id)

	s.publishEvent(protocol.Event{
		ID:        newPrefixedID("evt"),
		Type:      protocol.EventTypeRoomRemoved,
		NetworkID: s.networkID,
		Room:      &room,
		CreatedAt: now,
	})

	return protocol.RemoveResult{Removed: true, Kind: "room", ID: id, Mode: removeModeSoft}, nil
}
