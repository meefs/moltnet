package protocol

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"
)

func testAdmissionKey(t *testing.T) (ed25519.PrivateKey, string, string) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return private, EncodeNetworkPublicKey(public), NetworkKeyFingerprint(public)
}

func TestRoomAdmissionSignsAndVerifiesStrictly(t *testing.T) {
	private, public, fingerprint := testAdmissionKey(t)
	sign := func(message []byte) []byte { return ed25519.Sign(private, message) }
	admission := RoomAdmission{V: HubAdmissionVersionV1, GlobalRoomID: GlobalRoomID(fingerprint, "research"), NetworkID: "alpha",
		Fingerprint: fingerprint, PublicKey: public, CreatedAt: "2026-10-25T10:00:00Z"}
	signed, err := SignRoomAdmission(admission, sign)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := VerifyRoomAdmission(signed); err != nil || got != admission {
		t.Fatalf("verify = %#v, %v", got, err)
	}
	otherPrivate, _, _ := testAdmissionKey(t)
	forged := signed
	forged.Signature = admissionSignatureBy(t, admission, otherPrivate)
	if _, err := VerifyRoomAdmission(forged); !errors.Is(err, ErrRoomAdmission) {
		t.Fatalf("admission signed by another key: %v", err)
	}
	_, otherPublic, _ := testAdmissionKey(t)
	mismatched := admission
	mismatched.PublicKey = otherPublic
	if _, err := SignRoomAdmission(mismatched, sign); !errors.Is(err, ErrRoomAdmission) {
		t.Fatalf("key not matching its fingerprint: %v", err)
	}
}

func admissionSignatureBy(t *testing.T, admission RoomAdmission, private ed25519.PrivateKey) string {
	t.Helper()
	signed, err := SignRoomAdmission(admission, func(message []byte) []byte { return ed25519.Sign(private, message) })
	if err != nil {
		t.Fatal(err)
	}
	return signed.Signature
}

func TestRoomInviteMustBeHomedByTheInvitersKey(t *testing.T) {
	_, public, fingerprint := testAdmissionKey(t)
	_, _, other := testAdmissionKey(t)
	invite := Invite{V: InviteVersion, RelayURL: "wss://relay.example", Room: "r", RelayToken: "t", PairingToken: "p", PairingID: "room-1",
		NetworkID: "home", Exp: time.Now().Add(time.Hour), NetworkPublicKey: public, NetworkFingerprint: fingerprint,
		HubRoom: &InviteHubRoom{GlobalRoomID: GlobalRoomID(fingerprint, "research"), Role: RoomRoleWriter}}
	if err := invite.Validate(); err != nil {
		t.Fatal(err)
	}
	foreign := invite
	foreign.HubRoom = &InviteHubRoom{GlobalRoomID: GlobalRoomID(other, "research"), Role: RoomRoleWriter}
	if err := foreign.Validate(); err == nil || !strings.Contains(err.Error(), "homed") {
		t.Fatalf("room invite for another home's room: %v", err)
	}
	owner := invite
	owner.HubRoom = &InviteHubRoom{GlobalRoomID: invite.HubRoom.GlobalRoomID, Role: RoomRoleOwner}
	if err := owner.Validate(); err == nil {
		t.Fatal("room invite granting owner accepted")
	}
}
