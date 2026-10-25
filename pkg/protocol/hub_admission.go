package protocol

import (
	"encoding/base64"
	"errors"
	"fmt"
)

const (
	roomAdmissionDigestDomain    = "moltnet.room-admission.v1/digest\x00"
	roomAdmissionSignatureDomain = "moltnet.room-admission.v1/signature\x00"
)

// ErrRoomAdmission covers malformed or badly signed admission requests.
var ErrRoomAdmission = errors.New("invalid room admission request")

// RoomAdmission is what a joining member signs when it redeems a room
// invite: "network NetworkID, holding this key, asks to join this room".
// The home pins the key over the invite's pairing and adds the network to
// the control log with the role the invite granted.
type RoomAdmission struct {
	V            string `json:"v"`
	GlobalRoomID string `json:"global_room_id"`
	NetworkID    string `json:"network_id"`
	Fingerprint  string `json:"fingerprint"`
	PublicKey    string `json:"public_key"`
	CreatedAt    string `json:"created_at"`
}

// SignedRoomAdmission is POST /v1/hub/admit's body.
type SignedRoomAdmission struct {
	Admission string `json:"admission"`
	Signature string `json:"signature"`
}

func roomAdmissionValue(admission RoomAdmission) map[string]any {
	return map[string]any{
		"v": admission.V, "global_room_id": admission.GlobalRoomID, "network_id": admission.NetworkID,
		"fingerprint": admission.Fingerprint, "public_key": admission.PublicKey, "created_at": admission.CreatedAt,
	}
}

func validateRoomAdmission(admission RoomAdmission) error {
	switch {
	case admission.V != HubAdmissionVersionV1:
		return fmt.Errorf("%w: unsupported version", ErrRoomAdmission)
	case ValidateNetworkNamespace(admission.NetworkID) != nil:
		return fmt.Errorf("%w: network_id is invalid", ErrRoomAdmission)
	case admission.CreatedAt == "":
		return fmt.Errorf("%w: created_at is required", ErrRoomAdmission)
	}
	if _, _, err := ParseGlobalRoomID(admission.GlobalRoomID); err != nil {
		return fmt.Errorf("%w: %v", ErrRoomAdmission, err)
	}
	key, err := ParseNetworkPublicKey(admission.PublicKey)
	if err != nil || NetworkKeyFingerprint(key) != admission.Fingerprint {
		return fmt.Errorf("%w: key does not match its fingerprint", ErrRoomAdmission)
	}
	return nil
}

// SignRoomAdmission encodes and signs an admission with the joiner's key.
func SignRoomAdmission(admission RoomAdmission, sign func([]byte) []byte) (SignedRoomAdmission, error) {
	if err := validateRoomAdmission(admission); err != nil {
		return SignedRoomAdmission{}, err
	}
	canonical, err := CanonicalJSONBytes(roomAdmissionValue(admission))
	if err != nil {
		return SignedRoomAdmission{}, fmt.Errorf("%w: %v", ErrRoomAdmission, err)
	}
	digest := domainDigest(roomAdmissionDigestDomain, canonical)
	return SignedRoomAdmission{
		Admission: base64.RawURLEncoding.EncodeToString(canonical),
		Signature: base64.RawURLEncoding.EncodeToString(sign(append([]byte(roomAdmissionSignatureDomain), digest...))),
	}, nil
}

// VerifyRoomAdmission strictly parses an admission and checks it is signed
// by the key it names (proof of possession; who holds it is the home's
// call, by the invite's pairing).
func VerifyRoomAdmission(signed SignedRoomAdmission) (RoomAdmission, error) {
	var admission RoomAdmission
	raw, err := parseStrictCanonical(signed.Admission, &admission)
	if err != nil {
		return RoomAdmission{}, fmt.Errorf("%w: %v", ErrRoomAdmission, err)
	}
	if err := validateRoomAdmission(admission); err != nil {
		return RoomAdmission{}, err
	}
	key, _ := ParseNetworkPublicKey(admission.PublicKey)
	if !verifyDomainSignature(key, roomAdmissionSignatureDomain, domainDigest(roomAdmissionDigestDomain, raw), signed.Signature) {
		return RoomAdmission{}, fmt.Errorf("%w: signature does not verify", ErrRoomAdmission)
	}
	return admission, nil
}
