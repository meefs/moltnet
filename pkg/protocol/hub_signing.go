package protocol

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const (
	roomControlDigestDomain    = "moltnet.room-control.v1/digest\x00"
	roomControlSignatureDomain = "moltnet.room-control.v1/signature\x00"
	roomReceiptDigestDomain    = "moltnet.room-receipt.v1/digest\x00"
	roomReceiptSignatureDomain = "moltnet.room-receipt.v1/signature\x00"
)

func domainDigest(domain string, canonical []byte) string {
	hash := sha256.New()
	hash.Write([]byte(domain))
	hash.Write(canonical)
	return hex.EncodeToString(hash.Sum(nil))
}

func roomControlValue(event RoomControlEvent) map[string]any {
	value := map[string]any{
		"v": event.V, "global_room_id": event.GlobalRoomID, "seq": event.Seq,
		"epoch": event.Epoch, "kind": event.Kind, "created_at": event.CreatedAt,
	}
	putString(value, "prev_hash", event.PrevHash)
	putString(value, "network_id", event.NetworkID)
	putString(value, "fingerprint", event.Fingerprint)
	putString(value, "public_key", event.PublicKey)
	putString(value, "role", event.Role)
	putString(value, "history", event.History)
	return value
}

func roomReceiptValue(receipt RoomReceipt) map[string]any {
	value := map[string]any{
		"v": receipt.V, "global_room_id": receipt.GlobalRoomID, "hub_epoch": receipt.HubEpoch,
		"membership_epoch": receipt.MembershipEpoch, "room_seq": receipt.RoomSeq, "event_id": receipt.EventID,
	}
	putString(value, "prev_receipt_hash", receipt.PrevReceiptHash)
	return value
}

// SignRoomControl encodes, digests and signs a control event with the home key.
func SignRoomControl(event RoomControlEvent, sign func([]byte) []byte) (SignedRoomControl, string, error) {
	if err := validateRoomControlShape(event); err != nil {
		return SignedRoomControl{}, "", err
	}
	canonical, err := CanonicalJSONBytes(roomControlValue(event))
	if err != nil {
		return SignedRoomControl{}, "", fmt.Errorf("%w: %v", ErrRoomControlMalformed, err)
	}
	digest := domainDigest(roomControlDigestDomain, canonical)
	return SignedRoomControl{
		Control:   base64.RawURLEncoding.EncodeToString(canonical),
		Signature: base64.RawURLEncoding.EncodeToString(sign(append([]byte(roomControlSignatureDomain), digest...))),
	}, digest, nil
}

// VerifyRoomControl strictly parses a control event and checks the home's
// signature. Chain, epoch and membership rules are the caller's.
func VerifyRoomControl(signed SignedRoomControl, homeKey ed25519.PublicKey) (RoomControlEvent, string, error) {
	var event RoomControlEvent
	raw, err := parseStrictCanonical(signed.Control, &event)
	if err != nil {
		return RoomControlEvent{}, "", fmt.Errorf("%w: %v", ErrRoomControlMalformed, err)
	}
	if err := validateRoomControlShape(event); err != nil {
		return RoomControlEvent{}, "", err
	}
	if reencoded, err := CanonicalJSONBytes(roomControlValue(event)); err != nil || !bytes.Equal(reencoded, raw) {
		return RoomControlEvent{}, "", fmt.Errorf("%w: non-canonical field shapes", ErrRoomControlMalformed)
	}
	digest := domainDigest(roomControlDigestDomain, raw)
	if !verifyDomainSignature(homeKey, roomControlSignatureDomain, digest, signed.Signature) {
		return RoomControlEvent{}, "", ErrRoomControlSignature
	}
	return event, digest, nil
}

// SignRoomReceipt encodes, digests and signs a receipt with the home key.
func SignRoomReceipt(receipt RoomReceipt, sign func([]byte) []byte) (SignedRoomReceipt, string, error) {
	if err := validateRoomReceiptShape(receipt); err != nil {
		return SignedRoomReceipt{}, "", err
	}
	canonical, err := CanonicalJSONBytes(roomReceiptValue(receipt))
	if err != nil {
		return SignedRoomReceipt{}, "", fmt.Errorf("%w: %v", ErrRoomReceiptMalformed, err)
	}
	digest := domainDigest(roomReceiptDigestDomain, canonical)
	return SignedRoomReceipt{
		Receipt:   base64.RawURLEncoding.EncodeToString(canonical),
		Signature: base64.RawURLEncoding.EncodeToString(sign(append([]byte(roomReceiptSignatureDomain), digest...))),
	}, digest, nil
}

// VerifyRoomReceipt strictly parses a receipt and checks the home signature.
func VerifyRoomReceipt(signed SignedRoomReceipt, homeKey ed25519.PublicKey) (RoomReceipt, string, error) {
	var receipt RoomReceipt
	raw, err := parseStrictCanonical(signed.Receipt, &receipt)
	if err != nil {
		return RoomReceipt{}, "", fmt.Errorf("%w: %v", ErrRoomReceiptMalformed, err)
	}
	if err := validateRoomReceiptShape(receipt); err != nil {
		return RoomReceipt{}, "", err
	}
	if reencoded, err := CanonicalJSONBytes(roomReceiptValue(receipt)); err != nil || !bytes.Equal(reencoded, raw) {
		return RoomReceipt{}, "", fmt.Errorf("%w: non-canonical field shapes", ErrRoomReceiptMalformed)
	}
	digest := domainDigest(roomReceiptDigestDomain, raw)
	if !verifyDomainSignature(homeKey, roomReceiptSignatureDomain, digest, signed.Signature) {
		return RoomReceipt{}, "", ErrRoomReceiptSignature
	}
	return receipt, digest, nil
}

// parseStrictCanonical decodes unpadded base64url canonical-json.v1 bytes
// into target, refusing unknown or duplicate fields and non-canonical bytes.
func parseStrictCanonical(encoded string, target any) ([]byte, error) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("not unpadded base64url")
	}
	value, err := ParseCanonicalJSONBytes(raw)
	if err != nil {
		return nil, err
	}
	if canonical, err := CanonicalJSONBytes(value); err != nil || !bytes.Equal(canonical, raw) {
		return nil, fmt.Errorf("bytes are not canonical")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return nil, err
	}
	return raw, nil
}

func verifyDomainSignature(key ed25519.PublicKey, domain, digest, encoded string) bool {
	signature, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(signature) != ed25519.SignatureSize || len(key) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(key, append([]byte(domain), digest...), signature)
}

func validateRoomControlShape(event RoomControlEvent) error {
	malformed := func(reason string) error { return fmt.Errorf("%w: %s", ErrRoomControlMalformed, reason) }
	if event.V != RoomControlVersionV1 {
		return malformed("unsupported version")
	}
	if _, _, err := ParseGlobalRoomID(event.GlobalRoomID); err != nil {
		return malformed(err.Error())
	}
	if event.Seq < 1 || event.Epoch != event.Seq {
		return malformed("seq must be positive and equal to epoch")
	}
	if (event.Seq == 1) != (event.PrevHash == "") {
		return malformed("only genesis has no prev_hash")
	}
	if event.CreatedAt == "" {
		return malformed("created_at is required")
	}
	if event.Kind == RoomControlSetPolicy || event.Kind == RoomControlGenesis {
		if !ValidRoomHistory(event.History) {
			return malformed("history policy is required")
		}
	} else if event.History != "" {
		return malformed("history only belongs to genesis and set_policy")
	}
	needsKey := event.Kind == RoomControlGenesis || event.Kind == RoomControlAdd
	switch event.Kind {
	case RoomControlGenesis, RoomControlAdd, RoomControlRemove, RoomControlLeave, RoomControlSetRole:
		if ValidateNetworkNamespace(event.NetworkID) != nil {
			return malformed("network_id is required")
		}
	case RoomControlSetPolicy:
		if event.NetworkID != "" {
			return malformed("set_policy names no network")
		}
	default:
		return malformed(fmt.Sprintf("unknown kind %q", event.Kind))
	}
	if needsKey {
		key, err := ParseNetworkPublicKey(event.PublicKey)
		if err != nil || NetworkKeyFingerprint(key) != event.Fingerprint {
			return malformed("subject key does not match its fingerprint")
		}
	} else if event.PublicKey != "" || event.Fingerprint != "" {
		return malformed("only genesis and add carry a key")
	}
	switch event.Kind {
	case RoomControlGenesis:
		if event.Role != RoomRoleOwner {
			return malformed("genesis role must be owner")
		}
	case RoomControlAdd, RoomControlSetRole:
		if !ValidMemberRole(event.Role) {
			return malformed("member role must be admin, writer or reader")
		}
	default:
		if event.Role != "" {
			return malformed("role does not belong to this kind")
		}
	}
	return nil
}

func validateRoomReceiptShape(receipt RoomReceipt) error {
	malformed := func(reason string) error { return fmt.Errorf("%w: %s", ErrRoomReceiptMalformed, reason) }
	switch {
	case receipt.V != RoomReceiptVersionV1:
		return malformed("unsupported version")
	case receipt.HubEpoch < 1 || receipt.MembershipEpoch < 1 || receipt.RoomSeq < 1:
		return malformed("epochs and room_seq must be positive")
	case (receipt.RoomSeq == 1) != (receipt.PrevReceiptHash == ""):
		return malformed("only the first receipt has no prev_receipt_hash")
	case len(receipt.EventID) != sha256.Size*2:
		return malformed("event_id must be a hex digest")
	}
	if _, err := hex.DecodeString(receipt.EventID); err != nil {
		return malformed("event_id must be a hex digest")
	}
	if _, _, err := ParseGlobalRoomID(receipt.GlobalRoomID); err != nil {
		return malformed(err.Error())
	}
	return nil
}
