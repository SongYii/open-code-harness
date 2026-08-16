package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestBackupProducesVerifiedConsistentCopy(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	mustAppend(t, store, appendRequest("append-backup", "session-backup", 0, "command-backup",
		domain.SessionCreated{WorkspaceRoot: "/w"},
		domain.TurnStarted{TurnID: "turn-backup", Input: "x"},
		domain.TurnCompleted{TurnID: "turn-backup"}))

	destination := filepath.Join(t.TempDir(), "backup-copy.db")
	if err := store.Backup(context.Background(), destination); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	// The copy opens independently and serves the same canonical stream. The
	// lease row copied from the live database belongs to runtime-1, so the
	// same runtime reopens it (same-runtime reopen renews the lease).
	copyStore, err := Open(context.Background(), Config{Path: destination, RuntimeID: "runtime-1"})
	if err != nil {
		t.Fatalf("open copy: %v", err)
	}
	defer copyStore.Close()
	page, err := copyStore.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: "session-backup", Limit: 256})
	if err != nil {
		t.Fatalf("read copy: %v", err)
	}
	if len(page.Records) != 3 || page.HeadVersion != 3 {
		t.Fatalf("copy page = %d records at head %d, want 3 at 3", len(page.Records), page.HeadVersion)
	}

	// Backing up onto an existing destination fails; the live database and
	// the first copy are unchanged.
	if err := store.Backup(context.Background(), destination); err == nil {
		t.Fatal("Backup onto existing destination = nil, want error")
	}
	if got := tableCount(t, store, "events"); got != 3 {
		t.Fatalf("live events after failed backup = %d, want 3", got)
	}
}

func TestBackupRejectsInvalidDestination(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	if err := store.Backup(context.Background(), store.config.Path); err == nil {
		t.Fatal("Backup onto live path = nil, want error")
	}
	if err := store.Backup(context.Background(), ""); err == nil {
		t.Fatal("Backup to empty path = nil, want error")
	}
}
