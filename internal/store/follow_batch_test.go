package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/noopolis/moltnet/internal/follow"
)

type batchCheckpointStore interface {
	GetFollowCheckpoint(context.Context, string) (follow.Checkpoint, error)
	SetFollowCheckpoint(context.Context, string, follow.Checkpoint) error
	SetFollowCheckpoints(context.Context, map[string]follow.Checkpoint, []string) error
}

func TestFollowCheckpointBatchReplacesLiteralPrefixesAndPersists(t *testing.T) {
	for _, kind := range []string{"memory", "json", "sqlite"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "checkpoints")
			open := func() batchCheckpointStore {
				t.Helper()
				switch kind {
				case "memory":
					return NewMemoryStore()
				case "json":
					s, err := NewFileStore(path)
					if err != nil {
						t.Fatal(err)
					}
					return s
				default:
					s, err := NewSQLiteStore(path)
					if err != nil {
						t.Fatal(err)
					}
					return s
				}
			}
			storage := open()
			initial := map[string]follow.Checkpoint{}
			for _, key := range []string{"room_%/old", "roomAB/keep", "é/old", "e/keep", "other"} {
				initial[key] = follow.Checkpoint{Initialized: true, Cursor: key}
			}
			if err := storage.SetFollowCheckpoints(t.Context(), initial, nil); err != nil {
				t.Fatal(err)
			}
			replacement := follow.Checkpoint{Initialized: true, Cursor: "page-end", Count: 200}
			if err := storage.SetFollowCheckpoints(t.Context(), map[string]follow.Checkpoint{"room_%/checkpoint": replacement}, []string{"room_%/", "é/"}); err != nil {
				t.Fatal(err)
			}
			if kind != "memory" {
				if closer, ok := storage.(interface{ Close() error }); ok {
					if err := closer.Close(); err != nil {
						t.Fatal(err)
					}
				}
				storage = open()
				if closer, ok := storage.(interface{ Close() error }); ok {
					t.Cleanup(func() { closer.Close() })
				}
			}
			for _, key := range []string{"room_%/old", "é/old"} {
				if got, err := storage.GetFollowCheckpoint(t.Context(), key); err != nil || got.Initialized {
					t.Fatalf("deleted %q: %+v %v", key, got, err)
				}
			}
			for _, key := range []string{"roomAB/keep", "e/keep", "other"} {
				if got, err := storage.GetFollowCheckpoint(t.Context(), key); err != nil || got.Cursor != key {
					t.Fatalf("untouched %q: %+v %v", key, got, err)
				}
			}
			if got, err := storage.GetFollowCheckpoint(t.Context(), "room_%/checkpoint"); err != nil || got != replacement {
				t.Fatalf("replacement: %+v %v", got, err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := storage.SetFollowCheckpoints(ctx, map[string]follow.Checkpoint{"bad": replacement}, []string{""}); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled batch: %v", err)
			}
			if got, err := storage.GetFollowCheckpoint(t.Context(), "other"); err != nil || got.Cursor != "other" {
				t.Fatalf("canceled batch deleted state: %+v %v", got, err)
			}
			if got, err := storage.GetFollowCheckpoint(t.Context(), "bad"); err != nil || got.Initialized {
				t.Fatalf("canceled batch inserted state: %+v %v", got, err)
			}
		})
	}
}

func TestFollowCheckpointBatchFileFailureDoesNotMutateMemory(t *testing.T) {
	storage, err := NewFileStore(filepath.Join(t.TempDir(), "checkpoints"))
	if err != nil {
		t.Fatal(err)
	}
	original := follow.Checkpoint{Initialized: true, Cursor: "original"}
	if err := storage.SetFollowCheckpoint(t.Context(), "keep", original); err != nil {
		t.Fatal(err)
	}
	storage.path = t.TempDir() // Replacing a directory with the snapshot must fail.
	if err := storage.SetFollowCheckpoints(t.Context(), map[string]follow.Checkpoint{"new": original}, []string{"keep"}); err == nil {
		t.Fatal("snapshot failure was not returned")
	}
	if got, err := storage.GetFollowCheckpoint(t.Context(), "keep"); err != nil || got != original {
		t.Fatalf("failed snapshot deleted state: %+v %v", got, err)
	}
	if got, err := storage.GetFollowCheckpoint(t.Context(), "new"); err != nil || got.Initialized {
		t.Fatalf("failed snapshot inserted state: %+v %v", got, err)
	}
}

func TestFollowCheckpointBatchSQLFailureRollsBackDeletion(t *testing.T) {
	storage, err := NewSQLiteStore(filepath.Join(t.TempDir(), "checkpoints.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	original := follow.Checkpoint{Initialized: true, Cursor: "original"}
	if err := storage.SetFollowCheckpoint(t.Context(), "keep", original); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.db.Exec(`CREATE TRIGGER reject_bad_checkpoint BEFORE INSERT ON follow_checkpoints WHEN NEW.key = 'bad' BEGIN SELECT RAISE(ABORT, 'injected checkpoint failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := storage.SetFollowCheckpoints(t.Context(), map[string]follow.Checkpoint{"bad": original}, []string{"keep"}); err == nil {
		t.Fatal("SQL failure was not returned")
	}
	if got, err := storage.GetFollowCheckpoint(t.Context(), "keep"); err != nil || got != original {
		t.Fatalf("transaction deleted state: %+v %v", got, err)
	}
	if got, err := storage.GetFollowCheckpoint(t.Context(), "bad"); err != nil || got.Initialized {
		t.Fatalf("transaction inserted state: %+v %v", got, err)
	}
}
