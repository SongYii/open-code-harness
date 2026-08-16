package sqlite

import (
	"context"
	"strings"
	"testing"
)

var fullShapeTables = []string{
	"command_requests",
	"domain_identities",
	"event_appends",
	"event_streams",
	"events",
	"export_checkpoints",
	"export_outbox",
	"runtime_leases",
	"schema_migrations",
	"session_heads",
	"snapshots",
	"store_metadata",
	"transcript_entries",
}

func TestMigrationCreatesFullShape(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	rows, err := store.db.QueryContext(context.Background(),
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer rows.Close()
	var present []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		present = append(present, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}
	joined := strings.Join(present, ",")
	for _, want := range fullShapeTables {
		if !strings.Contains(joined+",", want+",") {
			t.Fatalf("table %q missing; sqlite_master has %v", want, present)
		}
	}
}

func TestStoreMetadataIsSingleton(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	ctx := context.Background()
	var formatVersion int
	var headPosition int
	var headAuditDigest []byte
	err := store.db.QueryRowContext(ctx,
		"SELECT storage_format_version, head_commit_position, head_audit_digest FROM store_metadata WHERE id = 1").Scan(
		&formatVersion, &headPosition, &headAuditDigest)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if formatVersion != latestMigrationVersion {
		t.Fatalf("storage_format_version = %d, want %d", formatVersion, latestMigrationVersion)
	}
	if headPosition != 0 {
		t.Fatalf("fresh head_commit_position = %d, want 0", headPosition)
	}
	// Since Slice 3 the chain is active from migration: a fresh database
	// sits at the genesis seed with no batches.
	if headAuditDigest == nil {
		t.Fatal("fresh head_audit_digest = NULL, want genesis seed")
	}
	if string(headAuditDigest) != string(auditGenesisDigest[:]) {
		t.Fatalf("fresh head_audit_digest = %x, want genesis %x", headAuditDigest, auditGenesisDigest[:])
	}

	if _, err := store.db.ExecContext(ctx,
		"INSERT INTO store_metadata (id, storage_format_version, head_commit_position, head_audit_digest, created_at_unix, last_migration_at_unix) VALUES (2, 1, 0, NULL, 0, 0)"); err == nil {
		t.Fatal("second metadata row inserted; singleton constraint missing")
	}
}

func TestUserVersionTracksLatestMigration(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	var userVersion int
	if err := store.db.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&userVersion); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if userVersion != latestMigrationVersion {
		t.Fatalf("user_version = %d, want %d", userVersion, latestMigrationVersion)
	}
}

func TestReopenRunsNoMigration(t *testing.T) {
	config := tempStoreConfig(t)
	store := openStore(t, config)
	var applied int
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM schema_migrations").Scan(&applied); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if applied != latestMigrationVersion {
		t.Fatalf("migration rows = %d, want %d", applied, latestMigrationVersion)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened := openStore(t, config)
	if err := reopened.db.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM schema_migrations").Scan(&applied); err != nil {
		t.Fatalf("count migrations after reopen: %v", err)
	}
	if applied != latestMigrationVersion {
		t.Fatalf("migration rows after reopen = %d, want unchanged %d", applied, latestMigrationVersion)
	}
}
