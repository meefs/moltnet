package clisession

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/bridgeconfig"
	"github.com/noopolis/moltnet/pkg/protocol"
)

type zeroBackoff struct{}

func (zeroBackoff) Delay(int) time.Duration { return 0 }

// scriptedNotifier hands each Notify call to the test over a channel and
// returns whatever the test answers, so the test controls exactly when a
// nudge is "in flight".
type scriptedNotifier struct {
	calls chan notifyCall
}

type notifyCall struct {
	text   string
	result chan error
}

func (n scriptedNotifier) Name() string { return "fake-attached" }

func (n scriptedNotifier) Notify(ctx context.Context, _ bridgeconfig.Config, text string) error {
	call := notifyCall{text: text, result: make(chan error, 1)}
	select {
	case n.calls <- call:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-call.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func attachedTestConfig() bridgeconfig.Config {
	return bridgeconfig.Config{
		Agent:   bridgeconfig.AgentConfig{ID: "claude_bot"},
		Moltnet: bridgeconfig.MoltnetConfig{BaseURL: "http://moltnet", NetworkID: "local"},
		Runtime: bridgeconfig.RuntimeConfig{
			Kind:    bridgeconfig.RuntimeClaudeCode,
			Mode:    bridgeconfig.RuntimeModeAttached,
			Session: "lmc-work",
		},
		Rooms: []bridgeconfig.RoomBinding{{ID: "research", Wake: bridgeconfig.WakeAll}},
	}
}

func roomEvent(id string, from string) protocol.Event {
	return protocol.Event{
		Type: protocol.EventTypeMessageCreated,
		Message: &protocol.Message{
			ID:        id,
			NetworkID: "local",
			Target:    protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "research"},
			From:      protocol.Actor{Type: "agent", ID: from},
			Parts:     []protocol.Part{{Kind: "text", Text: "secret body " + id}},
		},
	}
}

func nextCall(t *testing.T, calls <-chan notifyCall) notifyCall {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for a nudge")
		return notifyCall{}
	}
}

func runAttached(t *testing.T, streamer eventStreamer, notifier Notifier) (context.CancelFunc, *sync.WaitGroup) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = RunAttached(ctx, attachedTestConfig(), notifier, streamer, zeroBackoff{})
	}()
	return cancel, &wg
}

func TestAttachedRunnerCoalescesIntoFailedPendingNudge(t *testing.T) {
	t.Parallel()

	notifier := scriptedNotifier{calls: make(chan notifyCall)}
	var first notifyCall
	released := make(chan struct{})
	streamer := pacedStreamer{
		events: []protocol.Event{roomEvent("m1", "alice"), roomEvent("m2", "bob"), roomEvent("m3", "alice")},
		// Hold the first nudge in flight while the later messages arrive.
		afterFirst: func() { first = nextCall(t, notifier.calls) },
		afterEvents: func() {
			first.result <- errors.New("session unreachable")
			close(released)
		},
	}
	cancel, wg := runAttached(t, streamer, notifier)
	defer func() { cancel(); wg.Wait() }()

	<-released
	retry := nextCall(t, notifier.calls)
	retry.result <- nil

	if want := "[moltnet] 1 new message in #research from alice (latest m1). Read: moltnet read --network local --member claude_bot --since-last room:research"; first.text != want {
		t.Fatalf("first nudge = %q, want %q", first.text, want)
	}
	if want := "[moltnet] 3 new messages in #research from alice, bob (latest m3). Read: moltnet read --network local --member claude_bot --since-last room:research"; retry.text != want {
		t.Fatalf("retried nudge = %q, want %q", retry.text, want)
	}
	select {
	case extra := <-notifier.calls:
		t.Fatalf("unexpected extra nudge %q", extra.text)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAttachedRunnerSendsOneFollowUpForMessagesDuringDelivery(t *testing.T) {
	t.Parallel()

	notifier := scriptedNotifier{calls: make(chan notifyCall)}
	var first notifyCall
	released := make(chan struct{})
	streamer := pacedStreamer{
		events:      []protocol.Event{roomEvent("m1", "alice"), roomEvent("m2", "bob"), roomEvent("m3", "carol")},
		afterFirst:  func() { first = nextCall(t, notifier.calls) },
		afterEvents: func() { first.result <- nil; close(released) },
	}
	cancel, wg := runAttached(t, streamer, notifier)
	defer func() { cancel(); wg.Wait() }()

	<-released
	follow := nextCall(t, notifier.calls)
	follow.result <- nil
	if want := "[moltnet] 2 new messages in #research from bob, carol (latest m3). Read: moltnet read --network local --member claude_bot --since-last room:research"; follow.text != want {
		t.Fatalf("follow-up nudge = %q, want %q", follow.text, want)
	}
}

func TestAttachedRunnerSkipsBootstrapAndUnmatchedMessages(t *testing.T) {
	t.Parallel()

	notifier := scriptedNotifier{calls: make(chan notifyCall)}
	own := roomEvent("m0", "claude_bot")
	other := roomEvent("m1", "alice")
	other.Message.Target.RoomID = "elsewhere"
	streamer := pacedStreamer{events: []protocol.Event{own, other}}
	cancel, wg := runAttached(t, streamer, notifier)
	defer func() { cancel(); wg.Wait() }()

	select {
	case call := <-notifier.calls:
		t.Fatalf("unexpected nudge %q", call.text)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestNudgeText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target protocol.Target
		from   []string
		id     string
		want   string
	}{
		{
			name:   "room",
			target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "research"},
			from:   []string{"codex", "claude-code", "codex"},
			want:   "[moltnet] 3 new messages in #research from codex, claude-code. Read: moltnet read --network net --member me --since-last room:research",
		},
		{
			name:   "dm",
			target: protocol.Target{Kind: protocol.TargetKindDM, DMID: "dm_42"},
			from:   []string{"alice"},
			id:     "msg_9",
			want:   "[moltnet] 1 new message in dm:dm_42 from alice (latest msg_9). Read: moltnet read --network net --member me --since-last dm:dm_42",
		},
		{
			name:   "thread",
			target: protocol.Target{Kind: protocol.TargetKindThread, RoomID: "research", ThreadID: "th_7"},
			from:   []string{"bob", "carol"},
			want:   "[moltnet] 2 new messages in thread:th_7 from bob, carol. Read: moltnet read --network net --member me --since-last thread:th_7",
		},
		{
			name:   "many senders",
			target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "r"},
			from:   []string{"a", "b", "c", "d", "e", "f", "g"},
			want:   "[moltnet] 7 new messages in #r from a, b, c, d, e and 2 more. Read: moltnet read --network net --member me --since-last room:r",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var nudge Nudge
			for _, from := range test.from {
				nudge.Add(&protocol.Message{ID: test.id, Target: test.target, From: protocol.Actor{ID: from}, Parts: []protocol.Part{{Kind: "text", Text: "body"}}})
			}
			if got := nudge.Text("net", "me"); got != test.want {
				t.Fatalf("Text() = %q, want %q", got, test.want)
			}
		})
	}
}
