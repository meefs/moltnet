package protocol

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func testEnvelopeKey(t *testing.T) (ed25519.PublicKey, func([]byte) []byte) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return public, func(message []byte) []byte { return ed25519.Sign(private, message) }
}

func testEnvelopeRequest() SendMessageRequest {
	return SendMessageRequest{
		ID:     "msg-1",
		Origin: MessageOrigin{NetworkID: "origin", MessageID: "msg-1"},
		Target: Target{Kind: TargetKindRoom, RoomID: "research"},
		From:   Actor{Type: "agent", ID: "writer", Name: "Writer <w&r>", NetworkID: "origin"},
		Parts: []Part{{Kind: "text", Text: "hello <world> & friends"},
			{Kind: "data", Data: map[string]any{"n": float64(3), "nested": []any{"a", true, nil}}}},
		Mentions: []string{"reader"},
	}
}

func signTestEnvelope(t *testing.T, request SendMessageRequest) (SignedEnvelope, string, ed25519.PublicKey) {
	t.Helper()
	public, sign := testEnvelopeKey(t)
	envelope := EnvelopeFromRequest(request, NetworkKeyFingerprint(public), "2026-10-25T10:00:00.123456789Z")
	signed, digest, err := SignEnvelope(envelope, public, sign)
	if err != nil {
		t.Fatal(err)
	}
	return signed, digest, public
}

func TestEnvelopeSignVerifyRoundTrip(t *testing.T) {
	request := testEnvelopeRequest()
	signed, digest, _ := signTestEnvelope(t, request)
	envelope, verifiedDigest, err := VerifySignedEnvelope(signed)
	if err != nil {
		t.Fatal(err)
	}
	if verifiedDigest != digest || len(digest) != 64 {
		t.Fatalf("digest = %q, want %q", verifiedDigest, digest)
	}
	if err := EnvelopeMatchesRequest(envelope, request); err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(signed.Envelope)
	if !strings.Contains(string(raw), "<world> & friends") {
		t.Fatalf("canonical bytes must not HTML-escape: %s", raw)
	}
}

func TestEnvelopeTamperedBodyFailsSignature(t *testing.T) {
	signed, _, _ := signTestEnvelope(t, testEnvelopeRequest())
	raw, _ := base64.RawURLEncoding.DecodeString(signed.Envelope)
	tampered := strings.Replace(string(raw), "hello", "HELLO", 1)
	signed.Envelope = base64.RawURLEncoding.EncodeToString([]byte(tampered))
	if _, _, err := VerifySignedEnvelope(signed); !errors.Is(err, ErrEnvelopeSignature) {
		t.Fatalf("tampered envelope error = %v, want signature failure", err)
	}
}

func TestEnvelopeWrongKeyFails(t *testing.T) {
	signed, _, _ := signTestEnvelope(t, testEnvelopeRequest())
	other, _ := testEnvelopeKey(t)
	signed.PublicKey = EncodeNetworkPublicKey(other)
	if _, _, err := VerifySignedEnvelope(signed); !errors.Is(err, ErrEnvelopeSignature) {
		t.Fatalf("wrong key error = %v", err)
	}
}

func TestEnvelopeRequestMismatchDetected(t *testing.T) {
	request := testEnvelopeRequest()
	signed, _, _ := signTestEnvelope(t, request)
	envelope, _, err := VerifySignedEnvelope(signed)
	if err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*SendMessageRequest){
		"text":      func(r *SendMessageRequest) { r.Parts[0].Text = "changed" },
		"author":    func(r *SendMessageRequest) { r.From.ID = "someone-else" },
		"name":      func(r *SendMessageRequest) { r.From.Name = "Admin" },
		"room":      func(r *SendMessageRequest) { r.Target.RoomID = "other" },
		"mentions":  func(r *SendMessageRequest) { r.Mentions = nil },
		"origin_id": func(r *SendMessageRequest) { r.Origin.MessageID = "msg-2" },
	}
	for name, mutate := range mutations {
		changed := testEnvelopeRequest()
		mutate(&changed)
		if err := EnvelopeMatchesRequest(envelope, changed); !errors.Is(err, ErrEnvelopeMismatch) {
			t.Fatalf("%s mutation not detected: %v", name, err)
		}
	}
}

func TestEnvelopeStrictParsing(t *testing.T) {
	signed, _, public := signTestEnvelope(t, testEnvelopeRequest())
	raw, _ := base64.RawURLEncoding.DecodeString(signed.Envelope)
	cases := map[string]string{
		"duplicate key": strings.Replace(string(raw), `{"author":`, `{"v":"x","author":`, 1),
		"whitespace":    strings.Replace(string(raw), `,"created_at"`, `, "created_at"`, 1),
		"unknown field": strings.Replace(string(raw), `"v":"moltnet.envelope.v1"`, `"v":"moltnet.envelope.v1","zz":1`, 1),
		"empty name":    strings.Replace(string(raw), `"author":{"id":"writer","name":"Writer <w&r>"`, `"author":{"id":"writer","name":""`, 1),
		"old version":   strings.Replace(string(raw), EnvelopeVersionV1, "moltnet.envelope.v0", 1),
	}
	for name, body := range cases {
		if body == string(raw) {
			t.Fatalf("%s: mutation did not apply to %s", name, raw)
		}
		candidate := signed
		candidate.Envelope = base64.RawURLEncoding.EncodeToString([]byte(body))
		if _, _, err := ParseSignedEnvelope(candidate); !errors.Is(err, ErrEnvelopeMalformed) {
			t.Fatalf("%s accepted: %v", name, err)
		}
	}
	_ = public
}

func TestEnvelopeUnsafeIntegerIsUnrepresentable(t *testing.T) {
	request := testEnvelopeRequest()
	request.Parts = []Part{{Kind: "data", Data: map[string]any{"big": int64(1) << 60}}}
	public, sign := testEnvelopeKey(t)
	envelope := EnvelopeFromRequest(request, NetworkKeyFingerprint(public), "2026-10-25T10:00:00Z")
	if _, _, err := SignEnvelope(envelope, public, sign); !errors.Is(err, ErrEnvelopeUnrepresentable) {
		t.Fatalf("unsafe integer error = %v", err)
	}
}

func TestEnvelopeReservedHubFieldsAreSigned(t *testing.T) {
	request := testEnvelopeRequest()
	public, sign := testEnvelopeKey(t)
	envelope := EnvelopeFromRequest(request, NetworkKeyFingerprint(public), "2026-10-25T10:00:00Z")
	epoch := int64(4)
	envelope.GlobalRoomID, envelope.MembershipEpoch = NetworkKeyFingerprint(public)+"/research", &epoch
	signed, _, err := SignEnvelope(envelope, public, sign)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _, err := VerifySignedEnvelope(signed)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.GlobalRoomID != envelope.GlobalRoomID || parsed.MembershipEpoch == nil || *parsed.MembershipEpoch != 4 {
		t.Fatalf("hub fields lost: %#v", parsed)
	}
}

func TestFingerprintFormats(t *testing.T) {
	public, _ := testEnvelopeKey(t)
	fingerprint := NetworkKeyFingerprint(public)
	if !strings.HasPrefix(fingerprint, FingerprintPrefix) || len(fingerprint) != len(FingerprintPrefix)+64 {
		t.Fatalf("fingerprint = %q", fingerprint)
	}
	for _, input := range []string{fingerprint, strings.ToUpper(fingerprint[7:]), DisplayFingerprint(fingerprint)} {
		normalized, err := NormalizeFingerprint(input)
		if err != nil || normalized != fingerprint {
			t.Fatalf("NormalizeFingerprint(%q) = %q, %v", input, normalized, err)
		}
	}
	if _, err := NormalizeFingerprint("sha256:abcd"); err == nil {
		t.Fatal("short fingerprint accepted")
	}
	identity := NetworkIdentity{Algorithm: NetworkKeyAlgorithmEd25519, PublicKey: EncodeNetworkPublicKey(public), Fingerprint: fingerprint}
	if err := ValidateNetworkIdentity(identity); err != nil {
		t.Fatal(err)
	}
	identity.Fingerprint = NetworkKeyFingerprint(make([]byte, 32))
	if err := ValidateNetworkIdentity(identity); err == nil {
		t.Fatal("mismatched identity accepted")
	}
}

func TestInviteNetworkKeyValidation(t *testing.T) {
	public, _ := testEnvelopeKey(t)
	invite := validInvite()
	invite.NetworkPublicKey, invite.NetworkFingerprint = EncodeNetworkPublicKey(public), NetworkKeyFingerprint(public)
	code, err := EncodeInvite(invite)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeInvite(code)
	if err != nil || decoded.NetworkFingerprint != invite.NetworkFingerprint {
		t.Fatalf("decoded = %#v, %v", decoded, err)
	}
	invite.NetworkFingerprint = NetworkKeyFingerprint(make([]byte, 32))
	if err := invite.Validate(); err == nil {
		t.Fatal("invite with mismatched fingerprint accepted")
	}
	invite.NetworkFingerprint = ""
	if err := invite.Validate(); err == nil {
		t.Fatal("invite with key but no fingerprint accepted")
	}
}
