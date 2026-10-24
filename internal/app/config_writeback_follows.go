package app

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/noopolis/moltnet/pkg/protocol"
)

// WriteFollow appends a subscription without round-tripping existing secrets.
func WriteFollow(path string, follow protocol.Follow) error {
	return editFollows(path, func(list []map[string]any) ([]map[string]any, error) {
		for _, entry := range list {
			if stringField(entry, "id") == follow.ID {
				return nil, fmt.Errorf("follow %q already exists", follow.ID)
			}
		}
		encoded, err := json.Marshal(follow)
		if err != nil {
			return nil, err
		}
		var entry map[string]any
		if err := json.Unmarshal(encoded, &entry); err != nil {
			return nil, err
		}
		return append(list, entry), nil
	})
}

// RemoveFollow removes only the subscription. Durable mirror history remains.
func RemoveFollow(path, id string) error {
	return editFollows(path, func(list []map[string]any) ([]map[string]any, error) {
		for i, entry := range list {
			if stringField(entry, "id") == id {
				return append(list[:i], list[i+1:]...), nil
			}
		}
		return nil, fmt.Errorf("follow %q not found", id)
	})
}

func editFollows(path string, edit func([]map[string]any) ([]map[string]any, error)) error {
	if err := rejectSymlinkedConfigPath(path); err != nil {
		return err
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	format := configFormat(path)
	doc, err := decodeWritebackDocument(format, contents)
	if err != nil {
		return err
	}
	list, err := edit(asMapSlice(doc["follows"]))
	if err != nil {
		return err
	}
	doc["follows"] = toAnySlice(list)
	out, err := encodeWritebackDocument(format, doc)
	if err != nil {
		return err
	}
	// Validate before replacing the original; CLI additionally reloads with
	// environment overrides and rolls back through its shared writeback helper.
	var raw rawConfigFile
	if err := decodeConfigBytes(path, out, &raw); err != nil {
		return err
	}
	if err := validateConfigFile(raw); err != nil {
		return err
	}
	return atomicWriteConfigFile(path, out)
}
