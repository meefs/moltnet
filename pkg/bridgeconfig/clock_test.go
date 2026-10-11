package bridgeconfig

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

const validClockJSON = `{"version":"noopolis.task-clock.v1","origin":"2025-10-01T12:00:00.123+02:00","anchorEpochMs":1790000000000}`

func TestClockStrictJSON(t *testing.T) {
	tests := map[string]string{
		"unknown":        strings.Replace(validClockJSON, `"origin":`, `"other":0,"origin":`, 1),
		"version":        strings.Replace(validClockJSON, "v1", "v2", 1),
		"offsetless":     strings.Replace(validClockJSON, "+02:00", "", 1),
		"offset hour":    strings.Replace(validClockJSON, "+02:00", "+24:00", 1),
		"offset minute":  strings.Replace(validClockJSON, "+02:00", "+02:60", 1),
		"invalid date":   strings.Replace(validClockJSON, "10-01", "02-30", 1),
		"noninteger":     strings.Replace(validClockJSON, "1790000000000", "1.5", 1),
		"float integer":  strings.Replace(validClockJSON, "1790000000000", "1.0", 1),
		"exponent":       strings.Replace(validClockJSON, "1790000000000", "1e3", 1),
		"string anchor":  strings.Replace(validClockJSON, "1790000000000", `"1790000000000"`, 1),
		"overflow":       strings.Replace(validClockJSON, "1790000000000", "9223372036854775808", 1),
		"anchor high":    strings.Replace(validClockJSON, "1790000000000", "253402300800000", 1),
		"anchor low":     strings.Replace(validClockJSON, "1790000000000", "-62135596800001", 1),
		"origin low":     strings.Replace(validClockJSON, "2025-10-01", "0000-10-01", 1),
		"origin high":    strings.Replace(validClockJSON, "2025-10-01", "10000-10-01", 1),
		"null anchor":    strings.Replace(validClockJSON, "1790000000000", "null", 1),
		"missing anchor": strings.Replace(validClockJSON, `,"anchorEpochMs":1790000000000`, "", 1),
		"duplicate":      strings.Replace(validClockJSON, `"anchorEpochMs":`, `"anchorEpochMs":0,"anchorEpochMs":`, 1),
		"trailing":       validClockJSON + `{}`,
		"null":           "null",
		"array":          "[]",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			var config ClockConfig
			if err := json.Unmarshal([]byte(input), &config); err == nil {
				t.Fatalf("accepted %s", input)
			}
		})
	}
	var config ClockConfig
	if err := json.Unmarshal([]byte(validClockJSON), &config); err != nil {
		t.Fatal(err)
	}
}

func TestClockStrictYAML(t *testing.T) {
	good := "version: noopolis.task-clock.v1\norigin: 2025-10-01T12:00:00.123+02:00\nanchor_epoch_ms: 1790000000000\n"
	for _, input := range []string{
		strings.Replace(good, "v1", "v2", 1),
		strings.Replace(good, "+02:00", "", 1),
		strings.Replace(good, "1790000000000", "1.5", 1),
		strings.Replace(good, "1790000000000", "1.0", 1),
		strings.Replace(good, "1790000000000", `"1"`, 1),
		strings.Replace(good, "1790000000000", "253402300800000", 1),
		strings.Replace(good, "1790000000000", "9223372036854775808", 1),
		strings.Replace(good, "1790000000000", "null", 1),
		good + "unknown: true\n",
		strings.Replace(good, "anchor_epoch_ms", "origin", 1),
		strings.Replace(good, "anchor_epoch_ms: 1790000000000\n", "", 1),
	} {
		var config ClockConfig
		if err := yaml.Unmarshal([]byte(input), &config); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	var config ClockConfig
	if err := yaml.Unmarshal([]byte(good), &config); err != nil {
		t.Fatal(err)
	}
}

func TestClockFormulaAndInverse(t *testing.T) {
	var config ClockConfig
	if err := json.Unmarshal([]byte(validClockJSON), &config); err != nil {
		t.Fatal(err)
	}
	real := time.UnixMilli(config.AnchorEpochMS).Add(2345*time.Millisecond + 678*time.Microsecond)
	now, err := config.Now(func() time.Time { return real })
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2025, 10, 1, 10, 0, 2, 468000000, time.UTC)
	if !now().Equal(want) {
		t.Fatalf("got %s, want %s", now(), want)
	}
	restored, err := config.RealTime(want)
	if err != nil || !restored.Equal(real.Truncate(time.Millisecond)) {
		t.Fatalf("inverse: %s, %v", restored, err)
	}
	real = real.Add(time.Second)
	if !now().Equal(want.Add(time.Second)) {
		t.Fatal("clock did not advance with elapsed real time")
	}
	// Date.parse truncates sub-ms origin precision; do not round.
	config.Origin = "2025-10-01T10:00:00.123999Z"
	now, err = config.Now(func() time.Time { return time.UnixMilli(config.AnchorEpochMS) })
	if err != nil || now().Nanosecond() != 123000000 {
		t.Fatalf("origin precision: %v", err)
	}
	// A multi-century offset must not saturate a time.Duration.
	config.Origin = "1500-01-01T00:00:00Z"
	now, err = config.Now(func() time.Time { return time.UnixMilli(config.AnchorEpochMS) })
	if err != nil || now().Year() != 1500 {
		t.Fatalf("wide offset: %v", err)
	}
}

func TestClockDefaultAndResultBounds(t *testing.T) {
	real := time.Now()
	var absent *ClockConfig
	now, err := absent.Now(func() time.Time { return real })
	if err != nil || now() != real {
		t.Fatal("absent clock changed real time or precision")
	}
	now, err = absent.Now(nil)
	if err != nil || now().Before(real) || now().After(time.Now()) {
		t.Fatal("default is not time.Now")
	}
	config := ClockConfig{Version: TaskClockVersion, Origin: "9999-12-31T23:59:59.999Z", AnchorEpochMS: 0}
	if _, err := config.Now(func() time.Time { return time.UnixMilli(1) }); err == nil {
		t.Fatal("accepted unrepresentable task result")
	}
	config.Origin = "0001-01-01T00:00:00Z"
	if _, err := config.RealTime(time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("accepted unrepresentable inverse")
	}
}
