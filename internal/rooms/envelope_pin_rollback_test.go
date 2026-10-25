package rooms_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/netkeys"
	"github.com/noopolis/moltnet/internal/pairings"
	"github.com/noopolis/moltnet/internal/rooms"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/internal/transport"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// failingFirstAppendStore holds the append of origin message "doomed"
// until released, then fails it as a storage error would.
type failingFirstAppendStore struct {
	*store.MemoryStore
	reached chan struct{}
	release chan struct{}
}

func (s *failingFirstAppendStore) AppendMessageWithLifecycleContext(ctx context.Context, message protocol.Message) (store.AppendLifecycle, error) {
	if message.Origin.MessageID == "doomed" {
		close(s.reached)
		<-s.release
		return store.AppendLifecycle{}, errors.New("disk full")
	}
	return s.MemoryStore.AppendMessageWithLifecycleContext(ctx, message)
}

func TestAFailedFirstContactNeverRemovesAPinAnAcceptedMessageUsed(t *testing.T) {
	pins := netkeys.NewMemoryPinStore()
	storage := &failingFirstAppendStore{MemoryStore: store.NewMemoryStore(), reached: make(chan struct{}), release: make(chan struct{})}
	service := rooms.NewService(rooms.ServiceConfig{
		NetworkID: "peer", Store: storage, Messages: storage, Broker: events.NewBroker(),
		Pairings:      []protocol.Pairing{{ID: "origin", RemoteBaseURL: "http://127.0.0.1:1"}},
		PairingClient: pairings.NewClient(), KeyPins: pins,
	})
	t.Cleanup(func() { _ = service.Close() })
	policy, err := authn.NewPolicy(authn.Config{Mode: authn.ModeBearer, Tokens: []authn.TokenConfig{{
		ID: "origin", Value: "pair-secret", Scopes: []authn.Scope{authn.ScopePair}}}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(transport.NewHTTPHandler(service, policy))
	t.Cleanup(server.Close)
	createDeliveryRoomWithMembers(t, service, "origin:writer")
	key := newTestIdentity(t)

	doomed := make(chan int, 1)
	go func() {
		status, _ := deliveryPost(t, server.URL, "pair-secret", signedRequest(t, key, "doomed", "first contact, then a storage failure"))
		doomed <- status
	}()
	select { // the first contact pinned the key and is appending
	case <-storage.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("the first contact never reached its append")
	}
	accepted := make(chan int, 1)
	go func() {
		status, _ := deliveryPost(t, server.URL, "pair-secret", signedRequest(t, key, "fine", "uses that pin"))
		accepted <- status
	}()
	time.Sleep(200 * time.Millisecond) // the second message verified against the pin
	close(storage.release)
	if status := <-doomed; status == http.StatusAccepted {
		t.Fatalf("the failed append was acknowledged")
	}
	if status := <-accepted; status != http.StatusAccepted {
		t.Fatalf("second message: %d", status)
	}
	if _, ok, err := pins.Lookup("origin"); err != nil || !ok {
		t.Fatalf("the pin an accepted message was verified against is gone: %v, %v", ok, err)
	}
}
