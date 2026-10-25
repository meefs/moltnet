package protocol

import (
	"encoding/json"
	"fmt"
)

// envelopeCanonicalValue maps an envelope onto canonical-json.v1 values.
// Optional fields follow their JSON tags: empty means absent, never "".
func envelopeCanonicalValue(envelope MessageEnvelope) (map[string]any, error) {
	value := map[string]any{
		"v":                  envelope.V,
		"target":             envelopeTargetValue(envelope.Target),
		"origin_network_id":  envelope.OriginNetworkID,
		"origin_fingerprint": envelope.OriginFingerprint,
		"origin_message_id":  envelope.OriginMessageID,
		"author":             envelopeActorValue(envelope.Author),
		"created_at":         envelope.CreatedAt,
	}
	putString(value, "global_room_id", envelope.GlobalRoomID)
	if envelope.MembershipEpoch != nil {
		value["membership_epoch"] = *envelope.MembershipEpoch
	}
	if envelope.OriginSeq != nil {
		value["origin_seq"] = *envelope.OriginSeq
	}
	parts := make([]any, 0, len(envelope.Parts))
	for index, part := range envelope.Parts {
		encoded, err := envelopePartValue(part)
		if err != nil {
			return nil, fmt.Errorf("%w: parts[%d]: %v", ErrEnvelopeUnrepresentable, index, err)
		}
		parts = append(parts, encoded)
	}
	value["parts"] = parts
	if len(envelope.Mentions) > 0 {
		value["mentions"] = stringValues(envelope.Mentions)
	}
	return value, nil
}

func envelopeTargetValue(target EnvelopeTarget) map[string]any {
	value := map[string]any{"kind": target.Kind}
	putString(value, "room_id", target.RoomID)
	putString(value, "thread_id", target.ThreadID)
	putString(value, "parent_message_id", target.ParentMessageID)
	putString(value, "dm_id", target.DMID)
	if len(target.ParticipantIDs) > 0 {
		value["participant_ids"] = stringValues(target.ParticipantIDs)
	}
	return value
}

func envelopeActorValue(actor EnvelopeActor) map[string]any {
	value := map[string]any{"type": actor.Type, "id": actor.ID}
	putString(value, "name", actor.Name)
	return value
}

func envelopePartValue(part Part) (map[string]any, error) {
	value := map[string]any{"kind": part.Kind}
	putString(value, "text", part.Text)
	putString(value, "media_type", part.MediaType)
	putString(value, "url", part.URL)
	putString(value, "filename", part.Filename)
	if len(part.Data) > 0 {
		// A JSON round trip reduces arbitrary decoded values (json.Number,
		// typed slices) to the plain JSON model the receiver also decodes.
		// Values canonical JSON refuses fail here; nothing is rounded to fit.
		raw, err := json.Marshal(part.Data)
		if err != nil {
			return nil, err
		}
		var data any
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, err
		}
		normalized, err := normalizeCanonicalValue(data)
		if err != nil {
			return nil, err
		}
		value["data"] = normalized
	}
	return value, nil
}

func putString(value map[string]any, key, field string) {
	if field != "" {
		value[key] = field
	}
}

func stringValues(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}
