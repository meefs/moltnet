package protocol

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func testGenesis(t *testing.T) (RoomControlEvent, func([]byte) []byte, []byte) {
	t.Helper()
	public, sign := testEnvelopeKey(t)
	fingerprint := NetworkKeyFingerprint(public)
	return RoomControlEvent{
		V: RoomControlVersionV1, GlobalRoomID: GlobalRoomID(fingerprint, "research"), Seq: 1, Epoch: 1,
		Kind: RoomControlGenesis, NetworkID: "home", Fingerprint: fingerprint, PublicKey: EncodeNetworkPublicKey(public),
		Role: RoomRoleOwner, History: RoomHistoryFromJoin, CreatedAt: "2026-10-25T10:00:00Z",
	}, sign, public
}

func TestRoomControlSignsAndVerifies(t *testing.T) {
	genesis, sign, public := testGenesis(t)
	signed, digest, err := SignRoomControl(genesis, sign)
	if err != nil {
		t.Fatal(err)
	}
	parsed, again, err := VerifyRoomControl(signed, public)
	if err != nil || again != digest || parsed != genesis {
		t.Fatalf("verify = %#v %q %v", parsed, again, err)
	}
	otherKey, _ := testEnvelopeKey(t)
	if _, _, err := VerifyRoomControl(signed, otherKey); !errors.Is(err, ErrRoomControlSignature) {
		t.Fatalf("wrong key: %v", err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(signed.Control)
	tampered := signed
	tampered.Control = base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(raw), "from_join", "full_history", 1)))
	if _, _, err := VerifyRoomControl(tampered, public); !errors.Is(err, ErrRoomControlSignature) {
		t.Fatalf("tampered control: %v", err)
	}
}

func TestRoomControlShapeRules(t *testing.T) {
	genesis, sign, _ := testGenesis(t)
	memberKey, _ := testEnvelopeKey(t)
	add := RoomControlEvent{
		V: RoomControlVersionV1, GlobalRoomID: genesis.GlobalRoomID, Seq: 2, Epoch: 2, PrevHash: strings.Repeat("a", 64),
		Kind: RoomControlAdd, NetworkID: "member", Fingerprint: NetworkKeyFingerprint(memberKey),
		PublicKey: EncodeNetworkPublicKey(memberKey), Role: RoomRoleWriter, CreatedAt: "2026-10-25T10:00:01Z",
	}
	if _, _, err := SignRoomControl(add, sign); err != nil {
		t.Fatalf("valid add: %v", err)
	}
	bad := map[string]func(*RoomControlEvent){
		"epoch != seq":        func(e *RoomControlEvent) { e.Epoch = 3 },
		"missing prev":        func(e *RoomControlEvent) { e.PrevHash = "" },
		"owner role":          func(e *RoomControlEvent) { e.Role = RoomRoleOwner },
		"key mismatch":        func(e *RoomControlEvent) { e.Fingerprint = genesis.Fingerprint },
		"history on add":      func(e *RoomControlEvent) { e.History = RoomHistoryFromJoin },
		"unknown kind":        func(e *RoomControlEvent) { e.Kind = "promote" },
		"bad global room id":  func(e *RoomControlEvent) { e.GlobalRoomID = "research" },
		"remove carries key":  func(e *RoomControlEvent) { e.Kind, e.Role = RoomControlRemove, "" },
		"set_policy w/o hist": func(e *RoomControlEvent) { e.Kind, e.Role, e.NetworkID, e.Fingerprint, e.PublicKey = RoomControlSetPolicy, "", "", "", "" },
	}
	for name, mutate := range bad {
		event := add
		mutate(&event)
		if _, _, err := SignRoomControl(event, sign); !errors.Is(err, ErrRoomControlMalformed) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRoomReceiptSignsVerifiesAndRejectsTampering(t *testing.T) {
	public, sign := testEnvelopeKey(t)
	receipt := RoomReceipt{
		V: RoomReceiptVersionV1, GlobalRoomID: GlobalRoomID(NetworkKeyFingerprint(public), "research"),
		HubEpoch: HubEpochV1, MembershipEpoch: 2, RoomSeq: 1, EventID: strings.Repeat("b", 64),
	}
	signed, digest, err := SignRoomReceipt(receipt, sign)
	if err != nil {
		t.Fatal(err)
	}
	parsed, again, err := VerifyRoomReceipt(signed, public)
	if err != nil || again != digest || parsed != receipt {
		t.Fatalf("verify = %#v %v", parsed, err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(signed.Receipt)
	tampered := signed
	tampered.Receipt = base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(raw), `"room_seq":1`, `"room_seq":2`, 1)))
	if _, _, err := VerifyRoomReceipt(tampered, public); err == nil {
		t.Fatal("tampered receipt verified")
	}
	second := receipt
	second.RoomSeq = 2
	if _, _, err := SignRoomReceipt(second, sign); !errors.Is(err, ErrRoomReceiptMalformed) {
		t.Fatalf("receipt after the first without prev hash: %v", err)
	}
}

func TestParseGlobalRoomID(t *testing.T) {
	public, _ := testEnvelopeKey(t)
	fingerprint := NetworkKeyFingerprint(public)
	if fp, room, err := ParseGlobalRoomID(GlobalRoomID(fingerprint, "research")); err != nil || fp != fingerprint || room != "research" {
		t.Fatalf("parse = %q %q %v", fp, room, err)
	}
	for _, bad := range []string{"research", fingerprint, strings.ToUpper(fingerprint) + "/research", fingerprint + "/", fingerprint + "/bad room"} {
		if _, _, err := ParseGlobalRoomID(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
