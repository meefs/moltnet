package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

type hubTestBackend interface {
	HubStore
	PairingDeliveryStore
	RoomStore
	MessageStore
}

func hubBackends(t *testing.T) map[string]func(t *testing.T) hubTestBackend {
	t.Helper()
	backends := map[string]func(t *testing.T) hubTestBackend{
		"memory": func(*testing.T) hubTestBackend { return NewMemoryStore() },
		"file": func(t *testing.T) hubTestBackend {
			file, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			return file
		},
		"sqlite": func(t *testing.T) hubTestBackend {
			sqlite, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sqlite.Close() })
			return sqlite
		},
	}
	if dsn := os.Getenv("MOLTNET_TEST_POSTGRES_DSN"); dsn != "" {
		backends["postgres"] = func(t *testing.T) hubTestBackend {
			postgres, err := NewPostgresStore(dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = postgres.Close() })
			for _, table := range []string{"hub_rooms", "hub_control", "hub_receipts", "hub_origin_seqs", "hub_sync_cursors", "message_envelopes", "pairing_receive_receipts", "pairing_deliveries", "messages", "rooms"} {
				if _, err := postgres.db.Exec("DELETE FROM " + table); err != nil {
					t.Fatal(err)
				}
			}
			return postgres
		}
	}
	return backends
}

const hubTestGlobal = "sha256:" + "11111111111111111111111111111111" + "11111111111111111111111111111111" + "/research"

func hubControl(seq int64, digest string) HubControlRecord {
	return HubControlRecord{GlobalRoomID: hubTestGlobal, Seq: seq, Digest: digest, Control: protocol.SignedRoomControl{Control: "c" + digest, Signature: "s"}}
}

func hubReceipt(seq int64, event, digest, prev string) HubReceiptRecord {
	return HubReceiptRecord{GlobalRoomID: hubTestGlobal, HubEpoch: 1, RoomSeq: seq, EventID: event, Digest: digest, PrevDigest: prev,
		Receipt: protocol.SignedRoomReceipt{Receipt: "r" + digest, Signature: "s"}}
}

func hubMessage(id, origin string) protocol.Message {
	return protocol.Message{ID: id, NetworkID: "member", Origin: protocol.MessageOrigin{NetworkID: origin, MessageID: id + "-o", ReceivedVia: "home-link"},
		Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "local-research"}, From: protocol.Actor{Type: "agent", ID: "a", NetworkID: origin},
		Parts: []protocol.Part{{Kind: "text", Text: id}}, CreatedAt: time.Now().UTC()}
}

func appendWithReceipt(storage hubTestBackend, message protocol.Message, receipt HubReceiptRecord) error {
	ctx := WithHubReceipt(WithMessageEnvelope(context.Background(), MessageEnvelopeRecord{Digest: "env-" + message.ID, State: protocol.SignatureStateVerified}), receipt)
	_, err := storage.AppendMessageWithPairingDeliveriesContext(ctx, message, nil, nil)
	return err
}

func TestHubStoreAllBackends(t *testing.T) {
	for name, open := range hubBackends(t) {
		t.Run(name, func(t *testing.T) {
			storage := open(t)
			ctx := context.Background()
			if err := storage.CreateRoom(protocol.Room{ID: "local-research", NetworkID: "member", Name: "r"}); err != nil {
				t.Fatal(err)
			}
			room := HubRoom{GlobalRoomID: hubTestGlobal, LocalRoomID: "local-research", HomeNetworkID: "home", HomePairingID: "home-link", CreatedAt: time.Now().UTC()}
			if err := storage.CreateHubRoomContext(ctx, room, hubControl(1, "g")); err != nil {
				t.Fatal(err)
			}
			if err := storage.CreateHubRoomContext(ctx, room, hubControl(1, "g")); !errors.Is(err, ErrHubRoomExists) {
				t.Fatalf("duplicate hub room: %v", err)
			}

			// Control log: contiguous, idempotent, conflict on rewrite.
			if err := storage.AppendHubControlContext(ctx, []HubControlRecord{hubControl(1, "g"), hubControl(2, "a")}); err != nil {
				t.Fatal(err)
			}
			if err := storage.AppendHubControlContext(ctx, []HubControlRecord{hubControl(2, "rewritten")}); !errors.Is(err, ErrHubControlConflict) {
				t.Fatalf("rewritten control event: %v", err)
			}
			if err := storage.AppendHubControlContext(ctx, []HubControlRecord{hubControl(4, "gap")}); !errors.Is(err, ErrHubControlConflict) {
				t.Fatalf("control gap: %v", err)
			}
			if log, err := storage.HubControlLogContext(ctx, hubTestGlobal, 1); err != nil || len(log) != 1 || log[0].Digest != "a" || log[0].Control.Control != "ca" {
				t.Fatalf("control log = %#v, %v", log, err)
			}

			// Receipts commit with the message and guard every conflict.
			if err := appendWithReceipt(storage, hubMessage("m1", "alpha"), hubReceipt(1, "e1", "d1", "")); err != nil {
				t.Fatal(err)
			}
			if err := appendWithReceipt(storage, hubMessage("m1", "alpha"), hubReceipt(1, "e1", "d1", "")); !errors.Is(err, ErrDuplicateMessage) {
				t.Fatalf("identical redelivery should be a duplicate: %v", err)
			}
			conflicts := map[string]struct {
				message protocol.Message
				receipt HubReceiptRecord
			}{
				"same seq, other receipt":  {hubMessage("m2", "beta"), hubReceipt(1, "e2", "other", "")},
				"same event, other seq":    {hubMessage("m1", "alpha"), hubReceipt(2, "e1", "moved", "d1")},
				"broken chain":             {hubMessage("m3", "beta"), hubReceipt(2, "e3", "d3", "not-d1")},
				"same event as duplicate?": {hubMessage("m1", "alpha"), hubReceipt(1, "e1", "d1-forged", "")},
			}
			for label, conflict := range conflicts {
				if err := appendWithReceipt(storage, conflict.message, conflict.receipt); !errors.Is(err, ErrHubReceiptConflict) {
					t.Fatalf("%s: %v", label, err)
				}
			}
			if err := appendWithReceipt(storage, hubMessage("m3", "beta"), hubReceipt(2, "e3", "d3", "d1")); err != nil {
				t.Fatal(err)
			}
			head, ok, err := storage.HubReceiptHeadContext(ctx, hubTestGlobal, 1)
			if err != nil || !ok || head.RoomSeq != 2 || head.MessageID != "m3" || head.Receipt.Receipt != "rd3" {
				t.Fatalf("head = %#v, %v, %v", head, ok, err)
			}
			if found, ok, _ := storage.HubReceiptForEventContext(ctx, hubTestGlobal, "e1"); !ok || found.MessageID != "m1" {
				t.Fatalf("receipt for event = %#v", found)
			}
			if byMessage, _ := storage.HubReceiptsForMessagesContext(ctx, []string{"m1", "m3", "nope"}); len(byMessage) != 2 {
				t.Fatalf("receipts by message = %#v", byMessage)
			}
			page, _ := storage.ListRoomMessages("local-research", "", 10)
			if len(page.Messages) != 2 {
				t.Fatalf("a rejected receipt must not store its message: %#v", page.Messages)
			}

			first, _ := storage.NextHubOriginSeqContext(ctx, hubTestGlobal)
			second, _ := storage.NextHubOriginSeqContext(ctx, hubTestGlobal)
			if first != 1 || second != 2 {
				t.Fatalf("origin seqs = %d, %d", first, second)
			}
			if rooms, err := storage.HubRoomsContext(ctx); err != nil || len(rooms) != 1 || rooms[0].HomePairingID != "home-link" || rooms[0].IsHome() {
				t.Fatalf("hub rooms = %#v, %v", rooms, err)
			}
		})
	}
}

func TestHubStateSurvivesFileStoreReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	storage, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := storage.CreateRoom(protocol.Room{ID: "local-research", NetworkID: "member", Name: "r"}); err != nil {
		t.Fatal(err)
	}
	if err := storage.CreateHubRoomContext(ctx, HubRoom{GlobalRoomID: hubTestGlobal, LocalRoomID: "local-research", HomeNetworkID: "home"}, hubControl(1, "g")); err != nil {
		t.Fatal(err)
	}
	if err := appendWithReceipt(storage, hubMessage("m1", "alpha"), hubReceipt(1, "e1", "d1", "")); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.NextHubOriginSeqContext(ctx, hubTestGlobal); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if rooms, _ := reloaded.HubRoomsContext(ctx); len(rooms) != 1 || !rooms[0].IsHome() {
		t.Fatalf("reloaded rooms = %#v", rooms)
	}
	if err := appendWithReceipt(reloaded, hubMessage("m2", "beta"), hubReceipt(1, "e2", "other", "")); !errors.Is(err, ErrHubReceiptConflict) {
		t.Fatalf("receipt conflict lost across reload: %v", err)
	}
	if next, _ := reloaded.NextHubOriginSeqContext(ctx, hubTestGlobal); next != 2 {
		t.Fatalf("origin seq reused after reload: %d", next)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"hub"`) {
		t.Fatal("hub state not persisted")
	}
}
