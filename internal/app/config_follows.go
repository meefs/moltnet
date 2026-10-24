package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func validateConfigFollows(follows []protocol.Follow, rooms []RoomConfig) error {
	ids := make([]string, 0, len(rooms))
	for _, room := range rooms {
		ids = append(ids, room.ID)
	}
	return protocol.ValidateFollows(follows, ids)
}

func mergeEnvFollows(config *Config) error {
	if err := protocol.ValidateNetworkNamespace(config.NetworkID); err != nil {
		return err
	}
	value, ok := envValue("MOLTNET_FOLLOWS_JSON")
	if !ok {
		value, ok = envValue("MOLTNET_FOLLOWS")
	}
	if ok {
		decoder := json.NewDecoder(bytes.NewBufferString(value))
		decoder.DisallowUnknownFields()
		var follows []protocol.Follow
		if err := decoder.Decode(&follows); err != nil {
			return fmt.Errorf("MOLTNET_FOLLOWS_JSON must contain valid follows JSON: %w", err)
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return fmt.Errorf("MOLTNET_FOLLOWS_JSON must contain exactly one JSON list")
		}
		config.Follows = follows
	}
	if value, ok := envValue("MOLTNET_BASE_URL"); ok {
		config.PublicBaseURL = value
	}
	if config.PublicBaseURL != "" {
		if err := protocol.ValidateFollowBaseURL(config.PublicBaseURL); err != nil {
			return fmt.Errorf("server.base_url: %w", err)
		}
	}
	return validateConfigFollows(config.Follows, config.Rooms)
}

func followApplyList(follows []protocol.Follow) *[]protocol.Follow {
	list := append([]protocol.Follow{}, follows...)
	return &list
}

func normalizedPublicBaseURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}
