package rooms

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func (s *Service) ListFollowsContext(context.Context) (protocol.FollowPage, error) {
	values := s.follows.Status()
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	return protocol.FollowPage{Follows: values}, nil
}

func (s *Service) EnsureFollowRoom(ctx context.Context, config protocol.Follow, remote protocol.Room) error {
	id := config.LocalRoomID(remote.ID)
	if err := protocol.ValidateRoomID(id); err != nil {
		return err
	}
	room, exists, err := s.getRoom(ctx, id)
	if err != nil {
		return err
	}
	if exists && (room.Follow == nil || room.Follow.ID != config.ID || room.Follow.RemoteBaseURL != config.RemoteBaseURL || room.Follow.RemoteRoomID != remote.ID) {
		return fmt.Errorf("follow room %q collides with an existing room", id)
	}
	if !exists {
		now, err := s.now()
		if err != nil {
			return err
		}
		room = protocol.Room{ID: id, NetworkID: s.networkID, FQID: protocol.RoomFQID(s.networkID, id), CreatedAt: now}
	}
	room.Name = remote.Name
	room.Visibility = config.WithDefaults().Visibility
	room.WritePolicy = protocol.RoomWritePolicyOperators
	room.Federation = protocol.DefaultRoomFederation()
	room.Follow = &protocol.RoomFollow{ID: config.ID, RemoteBaseURL: config.RemoteBaseURL, RemoteRoomID: remote.ID}
	if exists {
		_, err = s.reconcileRoom(ctx, room)
	} else {
		err = s.createRoom(ctx, room)
		if err == nil {
			s.publishEvent(protocol.Event{
				ID: newPrefixedID("evt"), Type: protocol.EventTypeRoomCreated,
				NetworkID: s.networkID, Room: &room, CreatedAt: room.CreatedAt,
			})
		}
	}
	return err
}

func followMessageID(networkID, messageID string) string {
	return fmt.Sprintf("msg_%x", sha256.Sum256([]byte(networkID+"\x00"+messageID)))
}

func (s *Service) ImportFollowMessage(ctx context.Context, config protocol.Follow, networkID string, remote protocol.Message) (bool, error) {
	room, exists, err := s.getRoom(ctx, config.LocalRoomID(remote.Target.RoomID))
	if err != nil {
		return false, err
	}
	if !exists || room.Follow == nil || room.Follow.ID != config.ID || room.Follow.RemoteBaseURL != config.RemoteBaseURL {
		return false, fmt.Errorf("follow mirror room is missing or changed")
	}
	if networkID == "" || networkID == s.networkID {
		return false, fmt.Errorf("follow origin must have a non-local network identity")
	}
	if remote.ID == "" {
		return false, fmt.Errorf("origin returned an empty message id")
	}
	origin := remote.Origin
	if origin.NetworkID == "" {
		origin.NetworkID = networkID
	}
	if origin.MessageID == "" {
		origin.MessageID = remote.ID
	}
	origin.ReceivedVia = "follow:" + config.ID
	message := remote
	message.ID = followMessageID(networkID, remote.ID)
	message.NetworkID = s.networkID
	message.Origin = origin
	message.Target.RoomID = room.ID
	if remote.Target.Kind == protocol.TargetKindThread {
		message.Target.ThreadID = followMessageID(networkID, remote.Target.ThreadID)
		if remote.Target.ParentMessageID != "" {
			message.Target.ParentMessageID = followMessageID(networkID, remote.Target.ParentMessageID)
		}
	} else if remote.Target.Kind != protocol.TargetKindRoom {
		return false, fmt.Errorf("follow only imports room and thread messages")
	}
	// Claims from an anonymous origin cannot establish a local or third-party identity.
	claimedAuthor := remote.From
	message.Origin.ClaimedAuthor = &claimedAuthor
	message.From = remote.From
	message.From.NetworkID = networkID
	message.From.FQID = protocol.AgentFQID(networkID, remote.From.ID)
	message.From.CredentialBound = false
	event := protocol.Event{ID: eventIDForMessage(message.ID), Type: protocol.EventTypeMessageCreated, NetworkID: s.networkID, Message: &message, CreatedAt: message.CreatedAt}
	_, err = s.appendAndPublishMessage(ctx, protocol.SendMessageRequest{}, message, event)
	duplicate := errors.Is(err, store.ErrDuplicateMessage)
	if err != nil && !duplicate {
		return false, err
	}

	return !duplicate, nil
}

func followReadOnlyError(roomID string) error {
	return &Error{status: http.StatusForbidden, msg: "room " + roomID + " is a read-only follow mirror", cause: ErrWriteForbidden}
}
