package daimon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/nodeconfig"
)

func TestClockNodeFileDaimonWake(t *testing.T) {
	const input = `version: moltnet.node.v1
clock:
  version: noopolis.task-clock.v1
  origin: 2025-10-01T08:00:00.125Z
  anchor_epoch_ms: 1791619200000
moltnet:
  base_url: http://localhost:8787
  network_id: local
attachments:
  - agent: {id: first}
    runtime: {kind: daimon, control_url: "http://control.invalid", token_env: DAIMON_TOKEN, receipt_store_path: /tmp/clock-first.json}
  - agent: {id: second}
    runtime: {kind: daimon, control_url: "http://control.invalid", token_env: DAIMON_TOKEN, receipt_store_path: /tmp/clock-second.json}
`
	path := filepath.Join(t.TempDir(), "MoltnetNode")
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	node, err := nodeconfig.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	configs := node.BridgeConfigs()
	if len(configs) != 2 {
		t.Fatalf("attachments: %d", len(configs))
	}
	for _, config := range configs {
		delivery := daimonDelivery()
		delivery.OccurredAt = time.Date(2025, 10, 1, 8, 11, 12, 470000000, time.UTC)
		request, err := NewCodec("").EncodeRequest(context.Background(), config, delivery)
		if err != nil {
			t.Fatal(err)
		}
		var wake wakeRequest
		err = json.NewDecoder(request.Body).Decode(&wake)
		request.Body.Close()
		if err != nil || wake.Event.OccurredAt != "2026-10-10T08:11:12.345Z" {
			t.Fatalf("attachment %s wake: %s, %v", config.Agent.ID, wake.Event.OccurredAt, err)
		}
	}
}
