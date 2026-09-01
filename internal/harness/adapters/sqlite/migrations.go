package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

const latestMigrationVersion = 5

type migration struct {
	version    int
	name       string
	statements string
	// apply is an optional code-driven step executed after statements inside
	// the same write transaction.
	apply func(ctx context.Context, conn *sql.Conn) error
}

var migrations = []migration{
	{version: 1, name: "full target shape", statements: migration1DDL},
	{version: 2, name: "append receipt verification index", statements: migration2DDL},
	{version: 3, name: "audit chain backfill", statements: migration3DDL, apply: backfillAuditChain},
	{version: 4, name: "session head catalog", statements: migration4DDL, apply: migrateSessionHeadsV4},
	{version: 5, name: "context checkpoint heads", statements: migration5DDL},
}

func readUserVersion(ctx context.Context, conn *sql.Conn) (int, error) {
	var version int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("sqlite migrate: read user_version: %w", err)
	}
	return version, nil
}

// migrate applies pending ordered migrations inside one BEGIN IMMEDIATE
// transaction on the writer connection. A database stamped by a newer format
// is refused before any write; a mismatch between the user_version stamp and
// the migration history is corruption.
func (store *Store) migrate(ctx context.Context) error {
	conn := store.writer
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("sqlite migrate: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	if _, err := conn.ExecContext(ctx,
		"CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at_unix REAL NOT NULL)"); err != nil {
		return fmt.Errorf("sqlite migrate: bootstrap history: %w", err)
	}

	userVersion, err := readUserVersion(ctx, conn)
	if err != nil {
		return err
	}
	if userVersion > latestMigrationVersion {
		return &FormatNewerError{Have: userVersion, Supported: latestMigrationVersion}
	}

	var historyMax sql.NullInt64
	var historyCount int
	if err := conn.QueryRowContext(ctx,
		"SELECT MAX(version), COUNT(*) FROM schema_migrations").Scan(&historyMax, &historyCount); err != nil {
		return fmt.Errorf("sqlite migrate: read history: %w", err)
	}
	if historyCount > 0 && historyMax.Valid && int(historyMax.Int64) != userVersion {
		return &CorruptError{Detail: fmt.Sprintf("user_version %d disagrees with migration history head %d", userVersion, historyMax.Int64)}
	}

	applied := false
	for _, step := range migrations {
		if step.version <= userVersion {
			continue
		}
		if _, err := conn.ExecContext(ctx, step.statements); err != nil {
			return fmt.Errorf("sqlite migrate: apply %d %q: %w", step.version, step.name, err)
		}
		if step.apply != nil {
			if err := step.apply(ctx, conn); err != nil {
				return fmt.Errorf("sqlite migrate: code step %d %q: %w", step.version, step.name, err)
			}
		}
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO schema_migrations (version, name, applied_at_unix) VALUES (?, ?, unixepoch('subsec'))",
			step.version, step.name); err != nil {
			return fmt.Errorf("sqlite migrate: record %d: %w", step.version, err)
		}
		applied = true
	}

	if applied {
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", latestMigrationVersion)); err != nil {
			return fmt.Errorf("sqlite migrate: stamp user_version: %w", err)
		}
		if err := store.upsertMetadataAfterMigration(ctx, conn); err != nil {
			return err
		}
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("sqlite migrate: commit: %w", err)
	}
	committed = true
	return nil
}

func (store *Store) upsertMetadataAfterMigration(ctx context.Context, conn *sql.Conn) error {
	result, err := conn.ExecContext(ctx,
		"UPDATE store_metadata SET storage_format_version = ?, last_migration_at_unix = unixepoch('subsec') WHERE id = 1",
		latestMigrationVersion)
	if err != nil {
		return fmt.Errorf("sqlite migrate: update metadata: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite migrate: metadata rows affected: %w", err)
	}
	if updated == 1 {
		return nil
	}
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO store_metadata (id, storage_format_version, head_commit_position, head_audit_digest, created_at_unix, last_migration_at_unix) VALUES (1, ?, 0, NULL, unixepoch('subsec'), unixepoch('subsec'))",
		latestMigrationVersion); err != nil {
		return fmt.Errorf("sqlite migrate: insert metadata: %w", err)
	}
	return nil
}

// verifyMetadata enforces the singleton invariant after migration: exactly
// one row at id 1 carrying the supported format version and a non-negative
// head position. The audit digest stays NULL until Slice 3 activates the
// chain.
func (store *Store) verifyMetadata(ctx context.Context) error {
	return verifyMetadata(ctx, store.db)
}

func verifyMetadata(ctx context.Context, db *sql.DB) error {
	var rows int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM store_metadata").Scan(&rows); err != nil {
		return fmt.Errorf("sqlite open: verify metadata count: %w", err)
	}
	if rows != 1 {
		return &CorruptError{Detail: fmt.Sprintf("store_metadata has %d rows, want exactly 1", rows)}
	}
	var formatVersion int
	var headPosition int
	var headAuditDigest []byte
	err := db.QueryRowContext(ctx,
		"SELECT storage_format_version, head_commit_position, head_audit_digest FROM store_metadata WHERE id = 1").Scan(
		&formatVersion, &headPosition, &headAuditDigest)
	if err != nil {
		return fmt.Errorf("sqlite open: read metadata: %w", err)
	}
	if formatVersion != latestMigrationVersion {
		return &CorruptError{Detail: fmt.Sprintf("storage format version %d does not match supported %d", formatVersion, latestMigrationVersion)}
	}
	if headPosition < 0 {
		return &CorruptError{Detail: fmt.Sprintf("head commit position %d is negative", headPosition)}
	}
	return nil
}
