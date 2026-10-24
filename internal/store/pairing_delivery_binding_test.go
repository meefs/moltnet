package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPairingDeliveryDiscoveryBindsThenPurges(t *testing.T) {
	for name, factory := range pairingStoreFactories() {
		t.Run(name, func(t *testing.T) {
			storage := factory(t)
			preparePairingTestRoom(t, storage)
			ctx := context.Background()
			appendUnbound := func(message string) {
				t.Helper()
				if _, err := storage.AppendMessageWithPairingDeliveriesContext(ctx, pairingTestMessage(message), nil, []string{"peer"}); err != nil {
					t.Fatal(err)
				}
			}
			appendUnbound("first")
			for _, destinations := range []map[string]string{{"peer": ""}, {"peer": "X"}} {
				dropped, err := storage.ReconcilePairingDeliveriesContext(ctx, destinations)
				if err != nil || len(dropped) != 0 || pairingHeads(t, storage)["peer"].MessageID != "first" {
					t.Fatalf("unbound row purged: %v %v", dropped, err)
				}
			}
			if dropped, err := storage.BindPairingDeliveryDestinationContext(ctx, "peer", "X"); err != nil || dropped != 0 {
				t.Fatalf("bind: %d %v", dropped, err)
			}
			if head := pairingHeads(t, storage)["peer"]; head.RemoteNetworkID != "X" {
				t.Fatalf("binding not persisted: %#v", head)
			}
			appendUnbound("second")
			if dropped, err := storage.BindPairingDeliveryDestinationContext(ctx, "peer", "Y"); err != nil || dropped != 2 {
				t.Fatalf("changed discovery: %d %v", dropped, err)
			}
			appendUnbound("third")
			if head := pairingHeads(t, storage)["peer"]; head.RemoteNetworkID != "Y" {
				t.Fatalf("enqueue lost learned default: %#v", head)
			}
			if dropped, err := storage.ReconcilePairingDeliveriesContext(ctx, nil); err != nil || dropped["peer"] != 1 {
				t.Fatalf("removal: %v %v", dropped, err)
			}
			state, err := storage.PairingDeliveryStateContext(ctx, "peer")
			if err != nil || state.Pending != 0 || state.Dropped != 3 || state.RemoteNetworkID != "" {
				t.Fatalf("removal state: %#v %v", state, err)
			}
			appendUnbound("replacement")
			if head := pairingHeads(t, storage)["peer"]; head.RemoteNetworkID != "" {
				t.Fatalf("removed binding reused: %#v", head)
			}
		})
	}
}

func TestPairingDeliveryLearnedBindingReopensAfterDrain(t *testing.T) {
	for _, backend := range []string{"json", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state")
			open := func() pairingTestStore {
				t.Helper()
				if backend == "json" {
					s, err := NewFileStore(path)
					if err != nil {
						t.Fatal(err)
					}
					return s
				}
				s, err := NewSQLiteStore(path)
				if err != nil {
					t.Fatal(err)
				}
				return s
			}
			ctx := context.Background()
			storage := open()
			preparePairingTestRoom(t, storage)
			if _, err := storage.BindPairingDeliveryDestinationContext(ctx, "peer", "X"); err != nil {
				t.Fatal(err)
			}
			if s, ok := storage.(*SQLStore); ok {
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			}
			storage = open()
			if s, ok := storage.(*SQLStore); ok {
				defer s.Close()
			}
			if _, err := storage.ReconcilePairingDeliveriesContext(ctx, map[string]string{"peer": ""}); err != nil {
				t.Fatal(err)
			}
			if _, err := storage.AppendMessageWithPairingDeliveriesContext(ctx, pairingTestMessage("after-reopen"), nil, []string{"peer"}); err != nil {
				t.Fatal(err)
			}
			if head := pairingHeads(t, storage)["peer"]; head.RemoteNetworkID != "X" {
				t.Fatalf("reopen lost binding: %#v", head)
			}
		})
	}
}
