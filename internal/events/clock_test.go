package events

import (
	"errors"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/bridgeconfig"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestClockExhaustionRejectsReplayGap(t *testing.T) {
	real := time.UnixMilli(0)
	clock := bridgeconfig.ClockConfig{Version: bridgeconfig.TaskClockVersion, Origin: "9999-12-31T23:59:59.999Z"}
	now, err := clock.Now(func() time.Time { return real })
	if err != nil {
		t.Fatal(err)
	}
	broker := NewBroker()
	broker.SetNow(now)
	at, err := now()
	if err != nil {
		t.Fatal(err)
	}
	broker.Publish(protocol.Event{ID: "retained", CreatedAt: at})
	real = real.Add(time.Millisecond)
	stream, err := broker.SubscribeFromContext(t.Context(), "missing")
	if !errors.Is(err, bridgeconfig.ErrClockRange) || stream != nil || len(broker.subscribers) != 0 {
		t.Fatalf("failed replay created a subscription: %v, %v", stream, err)
	}
}
