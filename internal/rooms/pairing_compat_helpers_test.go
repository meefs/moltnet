package rooms

import (
	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
	"testing"
	"time"
)

func compatibleRemoteNetwork(networkID string) protocol.Network {
	return protocol.Network{
		ID:      networkID,
		Name:    networkID,
		Version: "test",
		Protocols: protocol.NetworkProtocols{
			HTTP: []string{protocol.HTTPProtocolV1},
			Pair: []string{protocol.PairProtocolV1},
		},
		Capabilities: protocol.NetworkCapabilities{
			DirectMessages:    true,
			MessagePagination: "cursor",
		},
	}
}

func newPairingCompatibilityService(t *testing.T, client *compatibilityPairingClient) *Service {
	memory := store.NewMemoryStore()
	service := NewService(ServiceConfig{
		NetworkID: "local",
		Pairings: []protocol.Pairing{{
			ID:              "pair_1",
			RemoteNetworkID: "remote",
			RemoteBaseURL:   "http://remote.example",
			Token:           "pair-secret",
		}},
		Store:                memory,
		Messages:             memory,
		Broker:               events.NewBroker(),
		PairingClient:        client,
		DeliveryRetryMin:     60 * time.Millisecond,
		DeliveryRetryMax:     60 * time.Millisecond,
		DeliveryPollInterval: time.Millisecond,
	})
	t.Cleanup(func() { _ = service.Close() })
	return service
}

func newRelayCompatibilityService(t *testing.T, client *recordingPairingClient) *Service {
	memory := store.NewMemoryStore()
	service := NewService(ServiceConfig{
		NetworkID: "net_a",
		Pairings: []protocol.Pairing{{
			ID:              "pair_b",
			RemoteNetworkID: "net_b",
			RemoteBaseURL:   "http://remote-b",
		}},
		Store:                memory,
		Messages:             memory,
		Broker:               events.NewBroker(),
		PairingClient:        client,
		DeliveryRetryMin:     60 * time.Millisecond,
		DeliveryRetryMax:     60 * time.Millisecond,
		DeliveryPollInterval: time.Millisecond,
	})
	t.Cleanup(func() { _ = service.Close() })
	return service
}

func waitForPairingStatus(t *testing.T, service *Service, pairingID string, want string) []protocol.Pairing {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		pairings, err := service.ListPairings()
		if err != nil {
			t.Fatalf("ListPairings() error = %v", err)
		}
		for _, pairing := range pairings {
			if pairing.ID == pairingID && pairing.Status == want {
				return pairings
			}
		}
		time.Sleep(5 * time.Millisecond)
	}

	pairings, err := service.ListPairings()
	if err != nil {
		t.Fatalf("ListPairings() error = %v", err)
	}
	t.Fatalf("timed out waiting for pairing %q status %q, got %#v", pairingID, want, pairings)
	return nil
}

func roomRelayMessage() protocol.Message {
	return protocol.Message{
		ID:        "msg_net_a_room",
		NetworkID: "net_a",
		Origin:    protocol.MessageOrigin{NetworkID: "net_a", MessageID: "msg_net_a_room"},
		Target:    protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "research"},
		From:      protocol.Actor{ID: "alpha", NetworkID: "net_a"},
	}
}

func dmRelayMessage() protocol.Message {
	return protocol.Message{
		ID:        "msg_net_a_dm",
		NetworkID: "net_a",
		Origin:    protocol.MessageOrigin{NetworkID: "net_a", MessageID: "msg_net_a_dm"},
		Target: protocol.Target{
			Kind:           protocol.TargetKindDM,
			DMID:           "dm_alpha_gamma",
			ParticipantIDs: []string{"net_a:alpha", "net_b:gamma"},
		},
		From: protocol.Actor{ID: "alpha", NetworkID: "net_a"},
	}
}

func sameStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (c *compatibilityPairingClient) snapshotNetworkCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.networkCalls
}

func waitForNetworkCalls(t *testing.T, client *recordingPairingClient, want int) {
	t.Helper()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(snapshotRecordingNetworkCalls(client)) >= want {
			return
		}
		select {
		case <-client.notify:
		case <-time.After(10 * time.Millisecond):
		}
	}

	if len(snapshotRecordingNetworkCalls(client)) >= want {
		return
	}
	t.Fatalf("timed out waiting for %d network calls", want)
}

func snapshotRecordingNetworkCalls(client *recordingPairingClient) []protocol.Pairing {
	client.mu.Lock()
	defer client.mu.Unlock()

	return append([]protocol.Pairing(nil), client.networkCalls...)
}
