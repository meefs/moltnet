package transport

import (
	"context"
	"net/http"
	"time"

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
// prefers the shallower "origin" field. For verified and introduced
// messages the signed facts (author, parts, mentions, created_at) come from
// the stored envelope; received_at and local_mentions keep the receiver's
// own projection beside them, so nothing locally rewritten shows as signed.
type signedMessageView struct {
	protocol.Message
	Origin        signedOriginView `json:"origin,omitempty"`
	ReceivedAt    *time.Time       `json:"received_at,omitempty"`
	LocalMentions []string         `json:"local_mentions,omitempty"`
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
		view.Messages = append(view.Messages, signedView(message, signatures[message.ID]))
	}
	writeJSON(response, http.StatusOK, view)
}

func signedView(message protocol.Message, signature rooms.MessageSignature) signedMessageView {
	view := signedMessageView{
		Origin: signedOriginView{MessageOrigin: message.Origin, Signature: signature.State, Fingerprint: signature.Fingerprint},
	}
	if envelope := signature.Signed; envelope != nil {
		receivedAt, localMentions := message.CreatedAt, message.Mentions
		if createdAt, err := time.Parse(time.RFC3339Nano, envelope.CreatedAt); err == nil {
			message.CreatedAt = createdAt
		}
		message.From.Type, message.From.ID, message.From.Name = envelope.Author.Type, envelope.Author.ID, envelope.Author.Name
		message.From.NetworkID = envelope.OriginNetworkID
		message.From.FQID = protocol.AgentFQID(envelope.OriginNetworkID, envelope.Author.ID)
		message.Parts = append([]protocol.Part(nil), envelope.Parts...)
		message.Mentions = append([]string(nil), envelope.Mentions...)
		view.ReceivedAt, view.LocalMentions = &receivedAt, localMentions
	}
	view.Message = message
	return view
}
