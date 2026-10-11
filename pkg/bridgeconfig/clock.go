package bridgeconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"

	"go.yaml.in/yaml/v3"
)

const TaskClockVersion = "noopolis.task-clock.v1"

const supportedClockRange = "0001-01-01T00:00:00.001Z..9999-12-31T23:59:59.999Z"

// ErrClockRange is safe to expose to callers; an exhausted clock cannot stamp
// new domain data. Existing recorded timestamps remain readable.
var ErrClockRange = errors.New("clock result is outside supported range " + supportedClockRange)

// ClockConfig is the explicit cross-process task clock. JSON uses the ecosystem
// contract; YAML uses the config file's snake_case convention. Omission is real time.
type ClockConfig struct {
	Version       string `json:"version" yaml:"version"`
	Origin        string `json:"origin" yaml:"origin"`
	AnchorEpochMS int64  `json:"anchorEpochMs" yaml:"anchor_epoch_ms"`
}

// Exclude Go's zero-time sentinel: causal records and wakes require a timestamp.
// Millisecond arithmetic avoids time.Duration's 292-year limit.
const minClockMS int64 = -62135596799999
const maxClockMS int64 = 253402300799999

var clockOriginPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]([01]\d|2[0-3]):[0-5]\d)$`)

func (c ClockConfig) offsetMS() (int64, error) {
	if c.Version != TaskClockVersion {
		return 0, fmt.Errorf("clock.version must be %q", TaskClockVersion)
	}
	origin, err := time.Parse(time.RFC3339Nano, c.Origin)
	if err != nil || !clockOriginPattern.MatchString(c.Origin) {
		return 0, fmt.Errorf("clock.origin must be RFC3339 with Z or an explicit offset")
	}
	if origin.UnixMilli() < minClockMS || origin.UnixMilli() > maxClockMS ||
		c.AnchorEpochMS < minClockMS || c.AnchorEpochMS > maxClockMS {
		return 0, fmt.Errorf("clock origin and anchor must be within %s", supportedClockRange)
	}
	return origin.UnixMilli() - c.AnchorEpochMS, nil
}

func (c ClockConfig) Validate() error {
	_, err := c.offsetMS()
	return err
}

// Now injects the real clock only at this boundary. Like Date.parse/Date.now,
// task timestamps have millisecond precision. A nil config preserves time.Now.
// Each read checks the supported range; callers must stop the operation on error.
func (c *ClockConfig) Now(realNow func() time.Time) (func() (time.Time, error), error) {
	if realNow == nil {
		realNow = time.Now
	}
	if c == nil {
		return func() (time.Time, error) { return realNow(), nil }, nil
	}
	offset, err := c.offsetMS()
	if err != nil {
		return nil, err
	}
	if _, err := shiftedClockTime(realNow(), offset); err != nil {
		return nil, err
	}
	return func() (time.Time, error) { return shiftedClockTime(realNow(), offset) }, nil
}

// RealTime reverses the shift for deliverers whose receiver applies task time.
func (c *ClockConfig) RealTime(taskTime time.Time) (time.Time, error) {
	if c == nil {
		return taskTime, nil
	}
	offset, err := c.offsetMS()
	if err != nil {
		return time.Time{}, err
	}
	return shiftedClockTime(taskTime, -offset)
}

func shiftedClockTime(value time.Time, offset int64) (time.Time, error) {
	value = value.UTC()
	if value.Year() < 1 || value.Year() > 9999 || value.UnixMilli() < minClockMS {
		return time.Time{}, fmt.Errorf("%w: input is outside the supported range", ErrClockRange)
	}
	ms := value.UnixMilli() + offset
	if ms < minClockMS || ms > maxClockMS {
		return time.Time{}, ErrClockRange
	}
	return time.UnixMilli(ms).UTC(), nil
}

func (c *ClockConfig) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return fmt.Errorf("clock must be an object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := token.(string)
		if !ok || (name != "version" && name != "origin" && name != "anchorEpochMs") {
			return fmt.Errorf("unknown clock field %q", token)
		}
		if _, exists := fields[name]; exists {
			return fmt.Errorf("duplicate clock field %q", name)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return err
		}
		if bytes.Equal(raw, []byte("null")) {
			return fmt.Errorf("clock.%s must not be null", name)
		}
		fields[name] = raw
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("clock has trailing data")
	}
	if len(fields) != 3 {
		return fmt.Errorf("clock requires version, origin and anchorEpochMs")
	}
	type plain ClockConfig
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*c = ClockConfig(value)
	return c.Validate()
}

func (c *ClockConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode || len(node.Content) != 6 {
		return fmt.Errorf("clock requires version, origin and anchor_epoch_ms")
	}
	fields := make(map[string]any)
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i].Value, node.Content[i+1]
		if key == "anchor_epoch_ms" {
			key = "anchorEpochMs"
			if value.Tag != "!!int" {
				return fmt.Errorf("clock.anchor_epoch_ms must be an integer")
			}
			var anchor int64
			if err := value.Decode(&anchor); err != nil {
				return err
			}
			fields[key] = anchor
			continue
		}
		if key != "version" && key != "origin" {
			return fmt.Errorf("unknown clock field %q", key)
		}
		if value.Kind != yaml.ScalarNode || (value.Tag != "!!str" && value.Tag != "!!timestamp") {
			return fmt.Errorf("clock.%s must be a string", key)
		}
		fields[key] = value.Value
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return c.UnmarshalJSON(data)
}
