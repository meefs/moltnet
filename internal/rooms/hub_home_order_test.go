package rooms_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// gatedOriginSeqStore blocks the first origin-seq reservation until
// released: the point where a home-originated post has (or, fixed, has
// not yet) read the roster outside the placement lock.
type gatedOriginSeqStore struct {
	*store.MemoryStore
	once    sync.Once
	reached chan struct{}
	release chan struct{}
}

func (s *gatedOriginSeqStore) NextHubOriginSeqContext(ctx context.Context, globalRoomID string) (int64, error) {
	s.once.Do(func() {
		close(s.reached)
		<-s.release
	})
	return s.MemoryStore.NextHubOriginSeqContext(ctx, globalRoomID)
}

func doneWithin(done <-chan struct{}, wait time.Duration) bool {
	select {
	case <-done:
		return true
	case <-time.After(wait):
		return false
	}
}

// A home-originated post reads the roster, signs and places under one
// placement lock, so a control change can never slip between its signing
// epoch and its receipt: no post fails on an epoch rollback, and none is
// placed under an epoch older than one already placed.
func TestHubHomePostPlacesUnderTheEpochItSignedAgainstConcurrentControlChanges(t *testing.T) {
	gated := &gatedOriginSeqStore{MemoryStore: store.NewMemoryStore(), reached: make(chan struct{}), release: make(chan struct{})}
	opened := 0
	h := newHubTopology(t, func(*testing.T) backendStore {
		opened++
		if opened == 1 {
			return gated // the home
		}
		return store.NewMemoryStore()
	})
	h.add(t, h.alpha)
	h.join(t, h.alpha)
	errs := make(chan error, 2)
	go func() { errs <- postErr(h.home, "first, signed at epoch 2") }()
	<-gated.reached
	added := make(chan struct{})
	go func() {
		defer close(added)
		h.add(t, h.beta)
	}()
	doneWithin(added, 200*time.Millisecond)
	second := make(chan struct{})
	go func() {
		defer close(second)
		errs <- postErr(h.home, "second")
	}()
	doneWithin(second, 200*time.Millisecond)
	close(gated.release)
	<-added
	<-second
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("home post refused: %v", err)
		}
	}
	h.home.waitTexts(t, "first, signed at epoch 2", "second")
}

func postErr(n *hubNet, text string) error {
	_, err := n.service.SendMessage(protocol.SendMessageRequest{
		Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: localRoom(n)},
		From:   protocol.Actor{Type: "agent", ID: "lead"}, Parts: []protocol.Part{{Kind: "text", Text: text}},
	})
	return err
}
