package daimon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/bridgeconfig"
)

func TestClockDaimonWakeAndDigest(t *testing.T) {
	config := daimonConfig("http://control.invalid")
	anchor := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	config.Clock = &bridgeconfig.ClockConfig{Version: bridgeconfig.TaskClockVersion, Origin: "2025-10-01T10:00:00.125+02:00", AnchorEpochMS: anchor.UnixMilli()}
	delivery := daimonDelivery()
	delivery.EventID = "moltnet:special"
	delivery.Message = "<&>\u2028\u000b"
	delivery.OccurredAt = time.Date(2025, 10, 1, 8, 11, 12, 470999999, time.UTC)
	request, err := NewCodec("").EncodeRequest(context.Background(), config, delivery)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Body.Close()
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	var wake wakeRequest
	if err := json.Unmarshal(body, &wake); err != nil {
		t.Fatal(err)
	}
	if wake.Event.OccurredAt != "2026-10-10T08:11:12.345Z" {
		t.Fatalf("double-shift risk: %s", wake.Event.OccurredAt)
	}
	// Independently generated with Node crypto SHA256(JSON.stringify(request)).
	// The text exercises JavaScript's HTML, U+2028, and control escaping rules.
	const want = "ba16d9804e69a8f6bafa7caf05b0460ec3c59495463f97e9a08804a1e0d62c74"
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != want {
		t.Fatalf("body differs from Daimon JSON.stringify: %s", body)
	}
	digest, err := wakeDigest(config, delivery)
	if err != nil || digest != want {
		t.Fatalf("digest %q, %v", digest, err)
	}
	acceptance := acceptanceBody(t, config, delivery)
	result, err := NewCodec("").DecodeResponse(config, delivery, daimonResponse(http.StatusAccepted, acceptance))
	if err != nil || result.Acceptance == nil || result.Acceptance.RequestDigest != want {
		t.Fatalf("receipt: %#v, %v", result, err)
	}
	config.Clock = nil
	request, err = NewCodec("").EncodeRequest(context.Background(), config, delivery)
	if err != nil {
		t.Fatal(err)
	}
	defer request.Body.Close()
	body, err = io.ReadAll(request.Body)
	if err != nil || !strings.Contains(string(body), `"occurred_at":"2025-10-01T08:11:12.470Z"`) {
		t.Fatalf("unclocked changed: %s %v", body, err)
	}
	config.Clock = &bridgeconfig.ClockConfig{Version: "wrong"}
	if _, err := NewCodec("").EncodeRequest(context.Background(), config, delivery); err == nil {
		t.Fatal("invalid clock encoded")
	}
}
