package app

import (
	"testing"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestPairingDeliveryLimitsValidation(t *testing.T) {
	for _, test := range []struct {
		name   string
		limits *protocol.PairingDeliveryLimits
		valid  bool
	}{
		{"defaults", nil, true},
		{"explicit defaults", &protocol.PairingDeliveryLimits{}, true},
		{"bounded", &protocol.PairingDeliveryLimits{MaxAgeSeconds: 3600, MaxPending: 50}, true},
		{"negative size", &protocol.PairingDeliveryLimits{MaxPending: -1}, false},
		{"negative age", &protocol.PairingDeliveryLimits{MaxAgeSeconds: -1}, false},
		{"duration overflow", &protocol.PairingDeliveryLimits{MaxAgeSeconds: 1<<63 - 1}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validatePairings([]protocol.Pairing{{ID: "peer", RemoteNetworkID: "peer", RemoteBaseURL: "https://peer.example", DeliveryLimits: test.limits}})
			if (err == nil) != test.valid {
				t.Fatalf("validatePairings() = %v, valid = %v", err, test.valid)
			}
		})
	}
}
