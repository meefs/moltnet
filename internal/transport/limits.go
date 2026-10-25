package transport

import "github.com/noopolis/moltnet/pkg/protocol"

const (
	maxJSONBodyBytes        = protocol.MaxPairingPayloadBytes
	maxAttachmentFrameBytes = 1 << 20
)
