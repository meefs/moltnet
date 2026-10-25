package protocol

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// EnvelopeVersionV1 versions the signed message envelope. The envelope is
// encoded as noopolis.canonical-json.v1, so two implementations that agree
// on the fields agree on the bytes, the digest and the signature.
const EnvelopeVersionV1 = "moltnet.envelope.v1"

// Domain separation: neither the digest nor the signature input can be
// confused with any other SHA-256 or Ed25519 use of the same network key.
const (
	envelopeDigestDomain    = "moltnet.envelope.v1/digest\x00"
	envelopeSignatureDomain = "moltnet.envelope.v1/signature\x00"
)

var (
	ErrEnvelopeMalformed = errors.New("malformed message envelope")
	ErrEnvelopeSignature = errors.New("message envelope signature is invalid")
	ErrEnvelopeMismatch  = errors.New("message envelope does not match the message")
	// ErrEnvelopeUnrepresentable means the message holds a value that
	// canonical-json.v1 cannot encode (for example an unsafe integer in part
	// data). Such a message is sent unsigned; it is never altered to fit.
	ErrEnvelopeUnrepresentable = errors.New("message cannot be canonically encoded for signing")
)

// EnvelopeActor is the signed author: the origin network attests that its
// local actor of this type, id and name wrote the message.
type EnvelopeActor struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// EnvelopeTarget binds the message to its conversation, including thread
// parentage and DM participants.
type EnvelopeTarget struct {
	Kind            string   `json:"kind"`
	RoomID          string   `json:"room_id,omitempty"`
	ThreadID        string   `json:"thread_id,omitempty"`
	ParentMessageID string   `json:"parent_message_id,omitempty"`
	DMID            string   `json:"dm_id,omitempty"`
	ParticipantIDs  []string `json:"participant_ids,omitempty"`
}

// MessageEnvelope is the immutable, origin-signed statement of a message.
//
// GlobalRoomID, MembershipEpoch and OriginSeq are reserved for hub rooms
// (chunk B). They are optional members of the same v1 encoding, so filling
// them in later does not change the signature format. OriginSeq, when used,
// is per room: a per-network counter would leak cross-room volume.
type MessageEnvelope struct {
	V                 string         `json:"v"`
	GlobalRoomID      string         `json:"global_room_id,omitempty"`
	MembershipEpoch   *int64         `json:"membership_epoch,omitempty"`
	Target            EnvelopeTarget `json:"target"`
	OriginNetworkID   string         `json:"origin_network_id"`
	OriginFingerprint string         `json:"origin_fingerprint"`
	OriginMessageID   string         `json:"origin_message_id"`
	OriginSeq         *int64         `json:"origin_seq,omitempty"`
	Author            EnvelopeActor  `json:"author"`
	CreatedAt         string         `json:"created_at"`
	Parts             []Part         `json:"parts"`
	Mentions          []string       `json:"mentions,omitempty"`
}

// SignedEnvelope travels beside a relayed message. Envelope is the
// base64url of the exact canonical bytes that were digested and signed, so
// no JSON re-encoding on the way (HTML escaping, key order) can change them.
type SignedEnvelope struct {
	Envelope  string `json:"envelope"`
	PublicKey string `json:"public_key"`
	Signature string `json:"signature"`
}

// EnvelopeFromRequest projects the signed fields of a relay request. Both
// the origin (when signing) and the receiver (when matching) use it, so the
// signed statement and the delivered message cannot drift apart.
func EnvelopeFromRequest(request SendMessageRequest, originFingerprint, createdAt string) MessageEnvelope {
	return MessageEnvelope{
		V: EnvelopeVersionV1,
		Target: EnvelopeTarget{
			Kind: request.Target.Kind, RoomID: request.Target.RoomID, ThreadID: request.Target.ThreadID,
			ParentMessageID: request.Target.ParentMessageID, DMID: request.Target.DMID,
			ParticipantIDs: append([]string(nil), request.Target.ParticipantIDs...),
		},
		OriginNetworkID:   request.Origin.NetworkID,
		OriginFingerprint: originFingerprint,
		OriginMessageID:   request.Origin.MessageID,
		Author:            EnvelopeActor{Type: request.From.Type, ID: request.From.ID, Name: request.From.Name},
		CreatedAt:         createdAt,
		Parts:             append([]Part(nil), request.Parts...),
		Mentions:          append([]string(nil), request.Mentions...),
	}
}

// EnvelopeCanonicalBytes encodes an envelope as canonical-json.v1.
func EnvelopeCanonicalBytes(envelope MessageEnvelope) ([]byte, error) {
	value, err := envelopeCanonicalValue(envelope)
	if err != nil {
		return nil, err
	}
	encoded, err := CanonicalJSONBytes(value)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEnvelopeUnrepresentable, err)
	}
	return encoded, nil
}

// EnvelopeDigest is the envelope's event id: lowercase hex SHA-256 over the
// domain-separated canonical bytes.
func EnvelopeDigest(canonical []byte) string {
	hash := sha256.New()
	hash.Write([]byte(envelopeDigestDomain))
	hash.Write(canonical)
	return hex.EncodeToString(hash.Sum(nil))
}

func envelopeSignatureInput(digest string) []byte {
	return append([]byte(envelopeSignatureDomain), digest...)
}

// SignEnvelope encodes, digests and signs an envelope. sign is the origin
// network's Ed25519 signer, so private keys never cross this package.
func SignEnvelope(envelope MessageEnvelope, publicKey ed25519.PublicKey, sign func([]byte) []byte) (SignedEnvelope, string, error) {
	if envelope.OriginFingerprint != NetworkKeyFingerprint(publicKey) {
		return SignedEnvelope{}, "", fmt.Errorf("%w: origin fingerprint does not match the signing key", ErrEnvelopeMalformed)
	}
	if err := validateEnvelopeFields(envelope); err != nil {
		return SignedEnvelope{}, "", err
	}
	canonical, err := EnvelopeCanonicalBytes(envelope)
	if err != nil {
		return SignedEnvelope{}, "", err
	}
	digest := EnvelopeDigest(canonical)
	return SignedEnvelope{
		Envelope:  base64.RawURLEncoding.EncodeToString(canonical),
		PublicKey: EncodeNetworkPublicKey(publicKey),
		Signature: base64.RawURLEncoding.EncodeToString(sign(envelopeSignatureInput(digest))),
	}, digest, nil
}

// ParseSignedEnvelope strictly decodes the envelope bytes without checking
// the signature: the bytes must already be canonical, carry no unknown or
// duplicate fields, and re-encode to themselves.
func ParseSignedEnvelope(signed SignedEnvelope) (MessageEnvelope, string, error) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(signed.Envelope)
	if err != nil {
		return MessageEnvelope{}, "", fmt.Errorf("%w: envelope is not unpadded base64url", ErrEnvelopeMalformed)
	}
	value, err := ParseCanonicalJSONBytes(raw)
	if err != nil {
		return MessageEnvelope{}, "", fmt.Errorf("%w: %v", ErrEnvelopeMalformed, err)
	}
	if canonical, err := CanonicalJSONBytes(value); err != nil || !bytes.Equal(canonical, raw) {
		return MessageEnvelope{}, "", fmt.Errorf("%w: envelope bytes are not canonical", ErrEnvelopeMalformed)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var envelope MessageEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return MessageEnvelope{}, "", fmt.Errorf("%w: %v", ErrEnvelopeMalformed, err)
	}
	if err := validateEnvelopeFields(envelope); err != nil {
		return MessageEnvelope{}, "", err
	}
	// Re-encoding rules out shapes the struct cannot round-trip, such as an
	// explicit empty optional field, so one meaning has exactly one encoding.
	if reencoded, err := EnvelopeCanonicalBytes(envelope); err != nil || !bytes.Equal(reencoded, raw) {
		return MessageEnvelope{}, "", fmt.Errorf("%w: envelope has non-canonical field shapes", ErrEnvelopeMalformed)
	}
	return envelope, EnvelopeDigest(raw), nil
}

// VerifySignedEnvelope parses the envelope and checks that the attached key
// matches the signed origin fingerprint and produced the signature. Whether
// that key belongs to the origin network is the caller's pin decision.
func VerifySignedEnvelope(signed SignedEnvelope) (MessageEnvelope, string, error) {
	envelope, digest, err := ParseSignedEnvelope(signed)
	if err != nil {
		return MessageEnvelope{}, "", err
	}
	publicKey, err := ParseNetworkPublicKey(signed.PublicKey)
	if err != nil {
		return MessageEnvelope{}, "", fmt.Errorf("%w: %v", ErrEnvelopeMalformed, err)
	}
	if NetworkKeyFingerprint(publicKey) != envelope.OriginFingerprint {
		return MessageEnvelope{}, "", fmt.Errorf("%w: public key does not match origin fingerprint", ErrEnvelopeSignature)
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(signed.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return MessageEnvelope{}, "", fmt.Errorf("%w: signature encoding", ErrEnvelopeSignature)
	}
	if !ed25519.Verify(publicKey, envelopeSignatureInput(digest), signature) {
		return MessageEnvelope{}, "", ErrEnvelopeSignature
	}
	return envelope, digest, nil
}

// EnvelopeMatchesRequest reports whether a verified envelope states exactly
// the message the request delivers. Fields only the envelope carries
// (creation time, fingerprint, hub-room fields) are taken from it.
func EnvelopeMatchesRequest(envelope MessageEnvelope, request SendMessageRequest) error {
	expected := EnvelopeFromRequest(request, envelope.OriginFingerprint, envelope.CreatedAt)
	expected.GlobalRoomID, expected.MembershipEpoch, expected.OriginSeq = envelope.GlobalRoomID, envelope.MembershipEpoch, envelope.OriginSeq
	want, err := EnvelopeCanonicalBytes(expected)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrEnvelopeMismatch, err)
	}
	got, err := EnvelopeCanonicalBytes(envelope)
	if err != nil || !bytes.Equal(want, got) {
		return ErrEnvelopeMismatch
	}
	return nil
}

func validateEnvelopeFields(envelope MessageEnvelope) error {
	missing := func(field string) error {
		return fmt.Errorf("%w: %s is required", ErrEnvelopeMalformed, field)
	}
	switch {
	case envelope.V != EnvelopeVersionV1:
		return fmt.Errorf("%w: unsupported version %q", ErrEnvelopeMalformed, envelope.V)
	case envelope.Target.Kind == "":
		return missing("target.kind")
	case envelope.OriginNetworkID == "":
		return missing("origin_network_id")
	case envelope.OriginMessageID == "":
		return missing("origin_message_id")
	case envelope.Author.Type == "" || envelope.Author.ID == "":
		return missing("author")
	case envelope.CreatedAt == "":
		return missing("created_at")
	}
	if envelope.OriginNetworkID != strings.TrimSpace(envelope.OriginNetworkID) ||
		envelope.OriginMessageID != strings.TrimSpace(envelope.OriginMessageID) {
		return fmt.Errorf("%w: origin ids must not have surrounding whitespace", ErrEnvelopeMalformed)
	}
	if normalized, err := NormalizeFingerprint(envelope.OriginFingerprint); err != nil || normalized != envelope.OriginFingerprint {
		return fmt.Errorf("%w: origin_fingerprint", ErrEnvelopeMalformed)
	}
	return nil
}
