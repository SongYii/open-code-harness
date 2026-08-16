package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/SongYii/open-code-harness/internal/harness/application"

	_ "modernc.org/sqlite" // pure-Go driver; CGO stays disabled
)

// Backup writes a consistent snapshot copy of the database to destination
// and verifies the copy against the live facts before reporting success.
// The destination must not already exist.
//
// Mechanism note: the pure-Go driver does not export the SQLite Online
// Backup API, so the copy is produced with VACUUM INTO, which SQLite
// documents as producing the same consistent snapshot. The evidence ledger
// records this deviation from the preferred API.
func (store *Store) Backup(ctx context.Context, destination string) error {
	if err := contextError(ctx); err != nil {
		return newStoreError(application.StoreCodeUnavailable, "", err)
	}
	if destination == "" || destination == store.config.Path {
		return fmt.Errorf("sqlite backup: destination must be a new path distinct from the live database")
	}

	live, err := store.collectInvariantCounts(ctx, store.db)
	if err != nil {
		return mapStorageError(err, "")
	}
	if _, err := store.db.ExecContext(ctx, "VACUUM INTO ?", destination); err != nil {
		return mapStorageError(err, "")
	}
	return verifyBackupCopy(ctx, destination, live)
}

// invariantCounts captures the facts a verified copy must reproduce.
type invariantCounts struct {
	events       int
	appends      int
	requests     int
	identities   int
	streams      int
	headPosition uint64
	maxCommit    uint64
}

func (store *Store) collectInvariantCounts(ctx context.Context, db *sql.DB) (invariantCounts, error) {
	var counts invariantCounts
	err := db.QueryRowContext(ctx,
		"SELECT (SELECT COUNT(*) FROM events), (SELECT COUNT(*) FROM event_appends), (SELECT COUNT(*) FROM command_requests), "+
			"(SELECT COUNT(*) FROM domain_identities), (SELECT COUNT(*) FROM event_streams), "+
			"(SELECT head_commit_position FROM store_metadata WHERE id = 1), "+
			"(SELECT COALESCE(MAX(commit_position), 0) FROM event_appends)").Scan(
		&counts.events, &counts.appends, &counts.requests, &counts.identities, &counts.streams,
		&counts.headPosition, &counts.maxCommit)
	return counts, err
}

// verifyBackupCopy opens the copy independently and checks schema version,
// metadata sanity, contiguity, and the counts against the live database.
func verifyBackupCopy(ctx context.Context, destination string, live invariantCounts) error {
	copyDB, err := sql.Open("sqlite", "file:"+destination)
	if err != nil {
		return fmt.Errorf("sqlite backup: open copy: %w", err)
	}
	defer copyDB.Close()

	var userVersion int
	if err := copyDB.QueryRowContext(ctx, "PRAGMA user_version").Scan(&userVersion); err != nil {
		return fmt.Errorf("sqlite backup: read copy schema version: %w", err)
	}
	if userVersion != latestMigrationVersion {
		return &CorruptError{Detail: fmt.Sprintf("backup copy reports schema version %d, want %d", userVersion, latestMigrationVersion)}
	}

	copied, err := (&Store{}).collectInvariantCounts(ctx, copyDB)
	if err != nil {
		return &CorruptError{Detail: "backup copy invariants unreadable: " + err.Error()}
	}
	if copied != live {
		return &CorruptError{Detail: fmt.Sprintf("backup copy counts %+v disagree with live database %+v", copied, live)}
	}
	if copied.appends > 0 {
		if uint64(copied.appends) != copied.maxCommit || copied.maxCommit != copied.headPosition {
			return &CorruptError{Detail: fmt.Sprintf("backup copy has %d appends, max position %d, head %d; contiguity broken", copied.appends, copied.maxCommit, copied.headPosition)}
		}
	}
	return nil
}
