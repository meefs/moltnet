package bridgeconfig

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

func TestClockRuntimeBounds(t *testing.T) {
	for _, test := range []struct {
		origin string
		step   time.Duration
	}{
		{"9999-12-31T23:59:59.999Z", time.Millisecond},
		{"0001-01-01T00:00:00.001Z", -time.Millisecond},
	} {
		t.Run(test.origin, func(t *testing.T) {
			real := time.UnixMilli(0)
			config := ClockConfig{Version: TaskClockVersion, Origin: test.origin, AnchorEpochMS: 0}
			now, err := config.Now(func() time.Time { return real })
			if err != nil {
				t.Fatal(err)
			}
			if _, err := now(); err != nil {
				t.Fatalf("boundary itself must be valid: %v", err)
			}
			real = real.Add(test.step)
			value, err := now()
			if err == nil || !strings.Contains(err.Error(), "clock result is outside") || !value.IsZero() {
				t.Fatalf("exhausted clock emitted %v, %v", value, err)
			}
		})
	}
}

func TestClockLowerBoundaryParsing(t *testing.T) {
	for _, test := range []struct {
		name, origin string
		anchor       int64
		valid        bool
	}{
		{"first millisecond", "0001-01-01T00:00:00.001Z", -62135596799999, true},
		{"zero origin", "0001-01-01T00:00:00Z", 0, false},
		{"sub-millisecond origin", "0001-01-01T00:00:00.000999999Z", 0, false},
		{"zero origin with offset", "0001-01-01T01:00:00+01:00", 0, false},
		{"zero anchor", "2025-10-01T08:00:00Z", -62135596800000, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := ClockConfig{Version: TaskClockVersion, Origin: test.origin, AnchorEpochMS: test.anchor}
			for _, format := range []struct {
				name      string
				marshal   func(any) ([]byte, error)
				unmarshal func([]byte, any) error
			}{
				{"JSON", json.Marshal, json.Unmarshal},
				{"YAML", yaml.Marshal, yaml.Unmarshal},
			} {
				t.Run(format.name, func(t *testing.T) {
					data, err := format.marshal(config)
					if err != nil {
						t.Fatal(err)
					}
					var parsed ClockConfig
					err = format.unmarshal(data, &parsed)
					if (err == nil) != test.valid {
						t.Fatalf("valid=%t, parse error: %v", test.valid, err)
					}
				})
			}
		})
	}
}

func TestClockInverseLowerBoundary(t *testing.T) {
	first := time.Date(1, 1, 1, 0, 0, 0, int(time.Millisecond), time.UTC)
	config := ClockConfig{Version: TaskClockVersion, Origin: "1970-01-01T00:00:00.001Z"}
	got, err := config.RealTime(first.Add(time.Millisecond))
	if err != nil || !got.Equal(first) {
		t.Fatalf("first inverse instant: %v, %v", got, err)
	}
	if _, err := config.RealTime(first); !errors.Is(err, ErrClockRange) {
		t.Fatalf("inverse produced zero time: %v", err)
	}
	// Even a shift into the supported range must reject a zero-time input.
	config.Origin = "1969-12-31T23:59:59.999Z"
	if _, err := config.RealTime(time.Time{}); !errors.Is(err, ErrClockRange) {
		t.Fatalf("inverse accepted zero time: %v", err)
	}
}

func TestClockRejectsZeroInput(t *testing.T) {
	real := time.UnixMilli(0)
	config := ClockConfig{Version: TaskClockVersion, Origin: "1970-01-01T00:00:00.001Z"}
	now, err := config.Now(func() time.Time { return real })
	if err != nil {
		t.Fatal(err)
	}
	real = time.Time{}
	if _, err := now(); !errors.Is(err, ErrClockRange) {
		t.Fatalf("clock accepted zero input: %v", err)
	}
}
