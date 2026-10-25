package netkeys

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestPairingBindingSurvivesOperatorReSourcingThePin(t *testing.T) {
	for name, open := range map[string]func(t *testing.T) *PinStore{
		"file":   func(t *testing.T) *PinStore { return OpenPinStore(t.TempDir()) },
		"memory": func(*testing.T) *PinStore { return NewMemoryPinStore() },
	} {
		t.Run(name, func(t *testing.T) {
			store := open(t)
			pin, _ := testPin(t, "alpha")
			pin.Trust, pin.Source, pin.SourceID = protocol.KeyTrustUnconfirmed, PinSourcePairing, "friend-1"
			if _, err := store.PinKey(pin); err != nil {
				t.Fatal(err)
			}
			operator := pin
			operator.Trust, operator.Source, operator.SourceID = protocol.KeyTrustVerified, PinSourceOperator, ""
			if _, err := store.PinKey(operator); err != nil {
				t.Fatal(err)
			}
			if networks, err := store.PairingNetworks("friend-1"); err != nil || !slices.Equal(networks, []string{"alpha"}) {
				t.Fatalf("binding after keys trust = %q, %v", networks, err)
			}
			if err := store.ForgetPairing("friend-1"); err != nil {
				t.Fatal(err)
			}
			if networks, _ := store.PairingNetworks("friend-1"); len(networks) != 0 {
				t.Fatalf("binding after ForgetPairing = %q", networks)
			}
		})
	}
}

func TestDamagedPairingBindingsFailClosed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, bindingsFileName), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPinStore(dir).PairingNetworks("friend-1"); err == nil {
		t.Fatal("damaged bindings read as empty")
	}
}
