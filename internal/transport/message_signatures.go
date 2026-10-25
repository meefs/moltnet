package transport

import (
	"context"
	"net/http"

	"github.com/noopolis/moltnet/internal/rooms"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// messageSignatureProvider is optional so test fakes of Service keep working.
type messageSignatureProvider interface {
	MessageSignatures(ctx context.Context, messages []protocol.Message) (map[string]rooms.MessageSignature, error)
}

// signedOriginView adds origin signature state to the HTTP message wire,
// additively: origin.signature is verified|introduced|unsigned and
// origin.fingerprint names the key that signed. The machine v1 projection
// never sees these fields.
type signedOriginView struct {
	protocol.MessageOrigin
	Signature   string `json:"signature,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

// signedMessageView shadows the embedded Message.Origin: encoding/json
// prefers the shallower "origin" field.
type signedMessageView struct {
	protocol.Message
	Origin signedOriginView `json:"origin,omitempty"`
}

type signedMessagePage struct {
	Messages []signedMessageView `json:"messages"`
	Page     protocol.PageInfo   `json:"page"`
}

func writeMessagePage(response http.ResponseWriter, ctx context.Context, service Service, page protocol.MessagePage) {
	provider, ok := service.(messageSignatureProvider)
	if !ok {
		writeJSON(response, http.StatusOK, page)
		return
	}
	signatures, err := provider.MessageSignatures(ctx, page.Messages)
	if err != nil {
		writeError(response, statusForError(err), err)
		return
	}
	view := signedMessagePage{Messages: make([]signedMessageView, 0, len(page.Messages)), Page: page.Page}
	for _, message := range page.Messages {
		signature := signatures[message.ID]
		view.Messages = append(view.Messages, signedMessageView{
			Message: message,
			Origin:  signedOriginView{MessageOrigin: message.Origin, Signature: signature.State, Fingerprint: signature.Fingerprint},
		})
	}
	writeJSON(response, http.StatusOK, view)
}
