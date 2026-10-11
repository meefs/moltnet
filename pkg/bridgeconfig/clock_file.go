package bridgeconfig

import (
	"encoding/json"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ValidateClockField distinguishes an omitted clock from an explicit null at
// the enclosing file boundary, where pointer decoding otherwise loses presence.
// The caller still decodes and validates the complete configuration normally.
func ValidateClockField(data []byte, yamlFormat bool) error {
	var fields map[string]any
	var err error
	if yamlFormat {
		err = yaml.Unmarshal(data, &fields)
	} else {
		err = json.Unmarshal(data, &fields)
	}
	if err != nil {
		return err
	}
	for key, value := range fields {
		if (key == "clock" || !yamlFormat && strings.EqualFold(key, "clock")) && value == nil {
			return fmt.Errorf("clock must be an object; omit it to use real time")
		}
	}
	return nil
}
