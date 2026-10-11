package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/bridgeconfig"
)

const appClockJSON = `{"version":"noopolis.task-clock.v1","origin":"2025-10-01T12:00:00Z","anchorEpochMs":1790000000000}`

func TestClockConfigLoadAndEnv(t *testing.T) {
	for _, format := range []string{"yaml", "json"} {
		path := filepath.Join(t.TempDir(), "moltnet."+format)
		data := "version: moltnet.v1\nclock:\n  version: noopolis.task-clock.v1\n  origin: 2025-10-01T12:00:00Z\n  anchor_epoch_ms: 1790000000000\nstorage:\n  kind: memory\n"
		if format == "json" {
			data = `{"version":"moltnet.v1","storage":{"kind":"memory"},"clock":` + appClockJSON + `}`
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		config, err := LoadFile(path, "test")
		if err != nil || config.Clock == nil || config.Clock.AnchorEpochMS != 1790000000000 {
			t.Fatalf("load %s: %#v %v", format, config.Clock, err)
		}
		t.Setenv("MOLTNET_CLOCK", strings.Replace(appClockJSON, "2025-10-01", "2025-09-01", 1))
		config, err = LoadConfigForPath(path, "test")
		if err != nil || config.Clock == nil || config.Clock.Origin != "2025-09-01T12:00:00Z" {
			t.Fatalf("override: %#v %v", config.Clock, err)
		}
	}
}

func TestClockConfigRefusals(t *testing.T) {
	for name, input := range map[string]string{
		"unknown":    strings.Replace(appClockJSON, `"origin":`, `"extra":1,"origin":`, 1),
		"version":    strings.Replace(appClockJSON, "v1", "v2", 1),
		"offsetless": strings.Replace(appClockJSON, "00Z", "00", 1),
		"fraction":   strings.Replace(appClockJSON, "1790000000000", "1.5", 1),
		"range":      strings.Replace(appClockJSON, "1790000000000", "9223372036854775807", 1),
		"missing":    `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("MOLTNET_CLOCK", input)
			if _, err := ConfigFromEnv("test"); err == nil {
				t.Fatal("accepted invalid env")
			}
			for _, format := range []string{"json", "yaml"} {
				path := filepath.Join(t.TempDir(), "moltnet."+format)
				body := `{"clock":` + input + `}`
				if format == "yaml" {
					body = "clock: " + strings.ReplaceAll(input, "anchorEpochMs", "anchor_epoch_ms")
				}
				if err := os.WriteFile(path, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := LoadFile(path, "test"); err == nil {
					t.Fatalf("accepted invalid %s", format)
				}
			}
		})
	}
	for _, input := range []string{"", "null", appClockJSON + "{}"} {
		t.Setenv("MOLTNET_CLOCK", input)
		if _, err := ConfigFromEnv("test"); err == nil {
			t.Fatalf("accepted env %q", input)
		}
	}
	config := Config{Clock: &bridgeconfig.ClockConfig{Version: "wrong"}}
	if _, err := New(config); err == nil {
		t.Fatal("startup accepted invalid direct config")
	}
}

func TestClockConfigDefault(t *testing.T) {
	config := defaultConfig("test")
	if config.Clock != nil {
		t.Fatal("clock enabled by default")
	}
	var clock bridgeconfig.ClockConfig
	if err := json.Unmarshal([]byte(appClockJSON), &clock); err != nil {
		t.Fatal(err)
	}
	now, err := clock.Now(func() time.Time { return time.UnixMilli(clock.AnchorEpochMS + 1500) })
	if err != nil {
		t.Fatal(err)
	}
	value, err := now()
	if err != nil || value.Format(time.RFC3339Nano) != "2025-10-01T12:00:01.5Z" {
		t.Fatalf("clock formula: %v", err)
	}
}
