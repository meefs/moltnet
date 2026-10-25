package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/noopolis/moltnet/pkg/protocol"
)

type envelopeTestStore interface {
	pairingTestStore
	MessageEnvelopeStore
	AppendMessageWithLifecycleContext(context.Context, protocol.Message) (AppendLifecycle, error)
}

func receivedEnvelopeMessage(id string) protocol.Message {
	message := pairingTestMessage(id)
	message.NetworkID = "local"
	message.Origin = protocol.MessageOrigin{NetworkID: "remote", MessageID: "origin-1", ReceivedVia: "pair-a"}
	return message
}

func envelopeRecord(digest string) MessageEnvelopeRecord {
	return MessageEnvelopeRecord{
		Digest: digest, State: protocol.SignatureStateVerified, Fingerprint: "sha256:" + digest,
		Envelope: protocol.SignedEnvelope{Envelope: "e-" + digest, PublicKey: "k", Signature: "s-" + digest},
	}
}

func appendWithEnvelope(storage envelopeTestStore, message protocol.Message, digest string) error {
	ctx := context.Background()
	if digest != "" {
		ctx = WithMessageEnvelope(ctx, envelopeRecord(digest))
	}
	_, err := storage.AppendMessageWithLifecycleContext(ctx, message)
	return err
}

func TestEnvelopeConflictDetectionBackends(t *testing.T) {
	for name, factory := range pairingStoreFactories() {
		t.Run(name, func(t *testing.T) {
			storage := factory(t).(envelopeTestStore)
			preparePairingTestRoom(t, storage)
			assertEnvelopeConflictRules(t, storage)
		})
	}
}

func assertEnvelopeConflictRules(t *testing.T, storage envelopeTestStore) {
	t.Helper()
	if err := appendWithEnvelope(storage, receivedEnvelopeMessage("stored-1"), "aaaa"); err != nil {
		t.Fatal(err)
	}
	var duplicate *DuplicateMessageError
	if err := appendWithEnvelope(storage, receivedEnvelopeMessage("retry-1"), "aaaa"); !errors.As(err, &duplicate) || duplicate.MessageID != "stored-1" {
		t.Fatalf("identical envelope retry = %v, want duplicate of stored-1", err)
	}
	if err := appendWithEnvelope(storage, receivedEnvelopeMessage("fork-1"), "bbbb"); !errors.Is(err, ErrEnvelopeConflict) {
		t.Fatalf("different envelope for same origin tuple = %v, want conflict", err)
	}
	if err := appendWithEnvelope(storage, receivedEnvelopeMessage("stripped-1"), ""); !errors.Is(err, ErrEnvelopeConflict) {
		t.Fatalf("unsigned copy of a signed origin tuple = %v, want conflict", err)
	}
	records, err := storage.MessageEnvelopesContext(context.Background(), []string{"stored-1", "fork-1", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	record, ok := records["stored-1"]
	if len(records) != 1 || !ok || record.Digest != "aaaa" || record.OriginNetworkID != "remote" ||
		record.OriginMessageID != "origin-1" || record.Envelope.Signature != "s-aaaa" {
		t.Fatalf("stored envelopes = %#v", records)
	}
	page, err := storage.ListRoomMessages("delivery-room", "", 10)
	if err != nil || len(page.Messages) != 1 {
		t.Fatalf("conflicts changed history: %#v, %v", page, err)
	}
	// Unsigned receipts stay idempotent for legacy peers.
	legacy := receivedEnvelopeMessage("legacy-1")
	legacy.Origin.MessageID = "origin-legacy"
	if err := appendWithEnvelope(storage, legacy, ""); err != nil {
		t.Fatal(err)
	}
	legacy.ID = "legacy-retry"
	if err := appendWithEnvelope(storage, legacy, ""); !errors.As(err, &duplicate) {
		t.Fatalf("unsigned legacy retry = %v, want duplicate", err)
	}
}

func TestEnvelopeConflictSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	openers := map[string]func() envelopeTestStore{
		"json": func() envelopeTestStore {
			value, err := NewFileStore(filepath.Join(dir, "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			return value
		},
		"sqlite": func() envelopeTestStore {
			value, err := NewSQLiteStore(filepath.Join(dir, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = value.Close() })
			return value
		},
	}
	for name, open := range openers {
		t.Run(name, func(t *testing.T) {
			first := open()
			preparePairingTestRoom(t, first)
			if err := appendWithEnvelope(first, receivedEnvelopeMessage("stored-1"), "aaaa"); err != nil {
				t.Fatal(err)
			}
			if closer, ok := first.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
			reopened := open()
			if err := appendWithEnvelope(reopened, receivedEnvelopeMessage("fork-1"), "bbbb"); !errors.Is(err, ErrEnvelopeConflict) {
				t.Fatalf("conflict after reopen = %v", err)
			}
			records, err := reopened.MessageEnvelopesContext(context.Background(), []string{"stored-1"})
			if err != nil || records["stored-1"].Digest != "aaaa" {
				t.Fatalf("envelope after reopen = %#v, %v", records, err)
			}
		})
	}
}
