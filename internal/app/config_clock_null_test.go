package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClockFileNull(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		for _, value := range []string{"null", "~", ""} {
			if format == "json" && value != "null" {
				continue
			}
			data := "clock: " + value + "\n"
			if format == "json" {
				data = `{"clock":null}`
			}
			path := filepath.Join(t.TempDir(), "moltnet."+format)
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadFile(path, "test"); err == nil || !strings.Contains(err.Error(), "clock") {
				t.Fatalf("%s %q: %v", format, value, err)
			}
		}
	}
}
