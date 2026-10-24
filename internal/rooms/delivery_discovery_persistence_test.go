package rooms

import (
	"context"
	"errors"
	"testing"

	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

type failedDiscoveryBindingStore struct{ *store.MemoryStore }

func (s *failedDiscoveryBindingStore) BindPairingDeliveryDestinationContext(context.Context, string, string) (int, error) {
	return 0, errors.New("binding persistence unavailable")
}

func TestDiscoveryBindingPersistenceFailureMarksPairingErrored(t *testing.T) {
	storage := &failedDiscoveryBindingStore{store.NewMemoryStore()}
	client := &recordingPairingClient{network: compatibleRemoteNetwork("remote")}
	service := NewService(ServiceConfig{
		NetworkID: "local", Store: storage, Messages: storage, Broker: events.NewBroker(), PairingClient: client,
		Pairings: []protocol.Pairing{{ID: "peer", RemoteNetworkID: "remote", RemoteBaseURL: "http://peer.example", Status: protocol.PairingStatusConnected}},
	})
	defer service.Close()
	if _, err := service.PairingNetwork(context.Background(), "peer"); err == nil {
		t.Fatal("binding failure was hidden")
	}
	pairs, err := service.ListPairings()
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].Status != protocol.PairingStatusError || pairs[0].Diagnostics == nil || pairs[0].Diagnostics.Reason != pairingDiagnosticRemoteRequestFailure {
		t.Fatalf("binding failure left stale pairing status: %#v", pairs)
	}
}
