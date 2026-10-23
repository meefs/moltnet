package protocol

import (
	"fmt"
	"net/url"
	"strings"
)

// ValidateFollowBaseURL accepts a credential-free HTTP(S) service base URL.
func ValidateFollowBaseURL(value string) error {
	u, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("invalid base URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Opaque != "" {
		return fmt.Errorf("base URL must have an http/https scheme and host")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return fmt.Errorf("base URL must not contain credentials, query, or fragment")
	}
	return nil
}

// ValidateFollows also reserves wildcard mirror namespaces at config load,
// before the remote room inventory is known.
func ValidateFollows(follows []Follow, localRoomIDs []string) error {
	ids := map[string]bool{}
	locals := map[string]bool{}
	for _, id := range localRoomIDs {
		locals[id] = true
	}
	for i, f := range follows {
		if err := ValidateRoomID(f.ID); err != nil {
			return fmt.Errorf("follows[%d].id: %w", i, err)
		}
		if ids[f.ID] {
			return fmt.Errorf("duplicate follow id %q", f.ID)
		}
		ids[f.ID] = true
		if err := ValidateFollowBaseURL(f.RemoteBaseURL); err != nil {
			return fmt.Errorf("follows[%d].remote_base_url: %w", i, err)
		}
		if f.RemoteNetworkID != "" && strings.TrimSpace(f.RemoteNetworkID) != f.RemoteNetworkID {
			return fmt.Errorf("follows[%d].remote_network_id must be trimmed", i)
		}
		if err := ValidateRoomVisibility(f.Visibility); err != nil {
			return fmt.Errorf("follows[%d].visibility: %w", i, err)
		}
		if f.BackfillLimit() < 0 || f.BackfillLimit() > 100000 {
			return fmt.Errorf("follows[%d].backfill must be between 0 and 100000", i)
		}
		d := f.WithDefaults()
		if d.PollIntervalSeconds < 1 || d.PollIntervalSeconds > 86400 || d.ReconnectMinSeconds < 1 || d.ReconnectMaxSeconds < d.ReconnectMinSeconds || d.ReconnectMaxSeconds > 3600 {
			return fmt.Errorf("follows[%d] has invalid poll/reconnect intervals", i)
		}
		remoteIDs := map[string]bool{}
		for _, remote := range f.Rooms {
			if err := ValidateRoomID(remote); err != nil {
				return fmt.Errorf("follows[%d].rooms: %w", i, err)
			}
			if remoteIDs[remote] {
				return fmt.Errorf("follows[%d] duplicate room %q", i, remote)
			}
			remoteIDs[remote] = true
		}
		for remote := range f.RoomMapping {
			if err := ValidateRoomID(remote); err != nil {
				return fmt.Errorf("follows[%d].room_mapping: %w", i, err)
			}
			if len(f.Rooms) > 0 && !remoteIDs[remote] {
				return fmt.Errorf("follows[%d] mapping for unselected room %q", i, remote)
			}
			remoteIDs[remote] = true
		}
		for remote := range remoteIDs {
			local := f.LocalRoomID(remote)
			if err := ValidateRoomID(local); err != nil {
				return fmt.Errorf("follows[%d] mirror room: %w", i, err)
			}
			if locals[local] {
				return fmt.Errorf("follow mirror room %q collides with another room", local)
			}
			locals[local] = true
		}
	}
	// Wildcard origins may add rooms later. Their entire default prefix is
	// reserved; mappings from other follows cannot occupy it either.
	for i, f := range follows {
		if len(f.Rooms) > 0 {
			continue
		}
		prefix := f.ID + "."
		for _, local := range localRoomIDs {
			if strings.HasPrefix(local, prefix) {
				return fmt.Errorf("follow %q mirror namespace collides with local room %q", f.ID, local)
			}
		}
		for j, other := range follows {
			if i == j {
				continue
			}
			if len(other.Rooms) == 0 && (strings.HasPrefix(other.ID+".", prefix) || strings.HasPrefix(prefix, other.ID+".")) {
				return fmt.Errorf("follow %q mirror namespace collides with follow %q", f.ID, other.ID)
			}
			for _, room := range other.Rooms {
				if strings.HasPrefix(other.LocalRoomID(room), prefix) {
					return fmt.Errorf("follow %q mirror namespace collides with follow %q", f.ID, other.ID)
				}
			}
			for _, local := range other.RoomMapping {
				if strings.HasPrefix(local, prefix) {
					return fmt.Errorf("follow %q mirror namespace collides with follow %q", f.ID, other.ID)
				}
			}
		}
	}
	return nil
}
