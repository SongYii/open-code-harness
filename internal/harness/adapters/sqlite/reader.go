package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// Reader serves pinned ReadStream against a live WAL database without taking
// the runtime lease. The type has no Append.
type Reader struct {
	db     *sql.DB
	config ReaderConfig
}

// OpenReader opens Path for pinned ReadStream only. It does not acquire
// runtime_leases, does not run migrations, and does not expose Append.
func OpenReader(ctx context.Context, config ReaderConfig) (*Reader, error) {
	config = config.WithDefaults()
	if err := config.validate(); err != nil {
		return nil, err
	}
	if err := rejectDeniedPath(config.Path, config.DeniedPathPrefixes); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dataSourceNamePragmas(config.Path, config.BusyTimeout, config.WALAutoCheckpoint, true))
	if err != nil {
		return nil, fmt.Errorf("sqlite reader: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	reader := &Reader{db: db, config: config}
	if err := verifyOperatingProfile(ctx, db, config.BusyTimeout, true); err != nil {
		_ = reader.Close()
		return nil, err
	}
	if err := reader.verifyFormat(ctx); err != nil {
		_ = reader.Close()
		return nil, err
	}
	if err := verifyMetadata(ctx, db); err != nil {
		_ = reader.Close()
		return nil, err
	}
	return reader, nil
}

// Close releases the reader connection.
func (reader *Reader) Close() error {
	if reader == nil || reader.db == nil {
		return nil
	}
	err := reader.db.Close()
	reader.db = nil
	return err
}

func (reader *Reader) verifyFormat(ctx context.Context) error {
	return verifyExactFormatVersion(ctx, reader.db)
}

// verifyExactFormatVersion is shared by every cold, non-migrating open path
// (Reader and the evaluation.go cold evidence path): the database must
// already be at exactly latestMigrationVersion. A newer format is a refused
// upgrade direction (FormatNewerError); an older one means the writer must
// migrate first -- neither path migrates on the caller's behalf.
func verifyExactFormatVersion(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("sqlite: read user_version: %w", err)
	}
	if version > latestMigrationVersion {
		return &FormatNewerError{Have: version, Supported: latestMigrationVersion}
	}
	if version < latestMigrationVersion {
		return fmt.Errorf("sqlite database format %d is older than supported %d; writer must migrate first", version, latestMigrationVersion)
	}
	return nil
}
