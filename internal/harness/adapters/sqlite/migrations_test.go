package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
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

func TestMigration4ReplaysPopulatedV3Head(t *testing.T) {
	config := populatedV3Store(t)
	store, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("Open(v3) error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	var root, status, turn string
	var position uint64
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT workspace_root, status, active_turn_id, updated_at_commit_position FROM session_heads WHERE session_id = 'session-v3'").Scan(
		&root, &status, &turn, &position); err != nil {
		t.Fatalf("read migrated head: %v", err)
	}
	if root != "/repo" || status != "running" || turn != "turn-v3" || position != 1 {
		t.Fatalf("migrated head = %q/%q/%q/%d, want /repo/running/turn-v3/1", root, status, turn, position)
	}
}

// TestMigration4AcceptsLegacyPreDeletionStatusForDeletedSession proves that a
// v3 database containing a session that was already domain-deleted before
// migration 4 existed can still migrate. domain.SessionDeleted predates this
// slice's SQLite catalog work (Tasks 1-2 landed before Task 4), and the
// pre-migration-4 per-append projection had no case for it, so it left the
// legacy session_heads row at whatever status preceded the deletion (idle or
// closed — deletion requires an idle session). Migration 4's canonical
// replay correctly derives "deleted" for that same session; the legacy
// compatibility check must accept idle/closed as legacy evidence of a
// session that predates deletion tracking, not report it as corrupt.
func TestMigration4AcceptsLegacyPreDeletionStatusForDeletedSession(t *testing.T) {
	config := deletedV3Store(t)
	store, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("Open(v3 with pre-tracked deletion) error = %v, want a successful migration", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	var root, status string
	var turn, item sql.NullString
	var position uint64
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT workspace_root, status, active_turn_id, active_item_id, updated_at_commit_position FROM session_heads WHERE session_id = 'session-v3-deleted'").Scan(
		&root, &status, &turn, &item, &position); err != nil {
		t.Fatalf("read migrated head: %v", err)
	}
	if root != "/repo" || status != "deleted" || turn.Valid || item.Valid || position != 1 {
		t.Fatalf("migrated head = %q/%q/turn=%v/item=%v/%d, want /repo/deleted/NULL/NULL/1", root, status, turn, item, position)
	}
}

func TestMigration4MigratesEmptyV3Database(t *testing.T) {
	config := emptyV3Store(t)
	store, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("Open(empty v3) error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	var version int
	if err := store.db.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 4 {
		t.Fatalf("migrated user_version = %d, want 4", version)
	}
	var heads int
	if err := store.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM session_heads").Scan(&heads); err != nil {
		t.Fatal(err)
	}
	if heads != 0 {
		t.Fatalf("migrated empty v3 heads = %d, want 0", heads)
	}
}

func TestMigration4MismatchRollsBackToV3(t *testing.T) {
	config := populatedV3Store(t)
	raw, err := sql.Open("sqlite", dataSourceName(config.Path, config.WithDefaults()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("UPDATE session_heads SET status = 'idle'"); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	assertMigration4RollsBack(t, config)
}

func TestMigration4RejectsNonLegacyRunningStatus(t *testing.T) {
	config := populatedV3Store(t)
	raw, err := sql.Open("sqlite", dataSourceName(config.Path, config.WithDefaults()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("UPDATE session_heads SET status = 'running'"); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	assertMigration4RollsBack(t, config)
}

func TestMigration4RebuildsMissingLegacyHead(t *testing.T) {
	config := populatedV3Store(t)
	raw, err := sql.Open("sqlite", dataSourceName(config.Path, config.WithDefaults()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("DELETE FROM session_heads WHERE session_id = 'session-v3'"); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("Open(v3 missing derived head) error = %v", err)
	}
	defer store.Close()
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM session_heads WHERE session_id = 'session-v3'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("rebuilt head count/error = %d/%v, want 1/nil", count, err)
	}
}

func TestMigration4MalformedEventRollsBackToV3(t *testing.T) {
	config := populatedV3Store(t)
	raw, err := sql.Open("sqlite", dataSourceName(config.Path, config.WithDefaults()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("UPDATE events SET payload = X'7B' WHERE session_id = 'session-v3' AND sequence = 1"); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	assertMigration4RollsBack(t, config)
}

func TestMigration4OrphanLegacyHeadRollsBackToV3(t *testing.T) {
	config := populatedV3Store(t)
	raw, err := sql.Open("sqlite", "file:"+config.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("INSERT INTO session_heads (session_id, status, updated_at_commit_position) VALUES ('session-orphan', 'idle', 1)"); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	assertMigration4RollsBack(t, config)
}

func assertMigration4RollsBack(t *testing.T, config Config) {
	t.Helper()
	if store, err := Open(context.Background(), config); err == nil {
		_ = store.Close()
		t.Fatal("Open(corrupt v3) = nil, want migration refusal")
	} else {
		var corrupt *CorruptError
		if !errors.As(err, &corrupt) {
			t.Fatalf("Open(corrupt v3) error = %v, want sqlite database corrupt", err)
		}
	}
	raw, err := sql.Open("sqlite", dataSourceName(config.Path, config.WithDefaults()))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var version int
	if err := raw.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	var shadowTables int
	if err := raw.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'session_heads_v4'").Scan(&shadowTables); err != nil {
		t.Fatal(err)
	}
	if version != 3 || shadowTables != 0 {
		t.Fatalf("failed migration left version/shadow = %d/%d, want 3/0", version, shadowTables)
	}
}

func populatedV3Store(t *testing.T) Config {
	t.Helper()
	config := tempStoreConfig(t).WithDefaults()
	seedV3Store(t, config, true)
	return config
}

func emptyV3Store(t *testing.T) Config {
	t.Helper()
	config := tempStoreConfig(t).WithDefaults()
	seedV3Store(t, config, false)
	return config
}

// deletedV3Store seeds a v3-shaped database containing only a session whose
// canonical stream is session.created then session.deleted, with its legacy
// session_heads row left at "idle" — the status the pre-migration-4
// per-append projection would have produced, since it had no case for
// session.deleted and therefore never updated the row past whatever it held
// beforehand.
func deletedV3Store(t *testing.T) Config {
	t.Helper()
	config := tempStoreConfig(t).WithDefaults()
	raw, err := sql.Open("sqlite", dataSourceName(config.Path, config))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	conn, err := raw.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	for _, statement := range []string{migration1DDL, migration2DDL, migration3DDL, "CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at_unix REAL NOT NULL)"} {
		if _, err := conn.ExecContext(context.Background(), statement); err != nil {
			t.Fatalf("create v3 fixture schema: %v", err)
		}
	}
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO store_metadata (id, storage_format_version, head_commit_position, head_audit_digest, created_at_unix, last_migration_at_unix) VALUES (1, 3, 1, NULL, 0, 0)"); err != nil {
		t.Fatal(err)
	}

	records := []domain.RecordedEvent{
		{SchemaVersion: 1, ID: "event-v3-deleted-0", CommandID: "command-v3-deleted", SessionID: "session-v3-deleted", Sequence: 1, OccurredAt: testTime, Event: domain.SessionCreated{WorkspaceRoot: "/repo/."}},
		{SchemaVersion: 1, ID: "event-v3-deleted-1", CommandID: "command-v3-deleted", SessionID: "session-v3-deleted", Sequence: 2, OccurredAt: testTime, Event: domain.SessionDeleted{}},
	}
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO event_streams (session_id, version, created_at_commit_position, last_append_commit_position) VALUES ('session-v3-deleted', 2, 1, 1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO event_appends (append_id, commit_position, session_id, expected_version, first_sequence, last_sequence, event_count, command_id, request_digest, writer_runtime_id, writer_fencing_token, committed_at_unix) VALUES ('append-v3-deleted', 1, 'session-v3-deleted', 0, 1, 2, 2, 'command-v3-deleted', ?, 'runtime-v3', 1, 0)",
		make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	for index, record := range records {
		payload, err := domain.MarshalRecordedEvent(record)
		if err != nil {
			t.Fatal(err)
		}
		digest := auditEventPayloadDigest(payload)
		if _, err := conn.ExecContext(context.Background(),
			"INSERT INTO events (session_id, sequence, event_id, append_id, order_in_append, command_id, event_type, schema_version, occurred_at, payload, payload_digest) VALUES (?, ?, ?, 'append-v3-deleted', ?, 'command-v3-deleted', ?, 1, ?, ?, ?)",
			"session-v3-deleted", record.Sequence, record.ID, index+1, record.Event.EventType(), record.OccurredAt.Format("2006-01-02T15:04:05Z07:00"), payload, digest[:]); err != nil {
			t.Fatal(err)
		}
	}
	// The legacy per-append projection had no case for session.deleted, so it
	// left the row at "idle" — the status session.created produced and
	// nothing after it ever changed.
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO session_heads (session_id, status, active_turn_id, active_item_id, updated_at_commit_position) VALUES ('session-v3-deleted', 'idle', NULL, NULL, 1)"); err != nil {
		t.Fatal(err)
	}
	if err := backfillAuditChain(context.Background(), conn); err != nil {
		t.Fatalf("backfill v3 fixture audit chain: %v", err)
	}
	for version, name := range map[int]string{1: "full target shape", 2: "append receipt verification index", 3: "audit chain backfill"} {
		if _, err := conn.ExecContext(context.Background(), "INSERT INTO schema_migrations (version, name, applied_at_unix) VALUES (?, ?, 0)", version, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.ExecContext(context.Background(), "PRAGMA user_version = 3"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), "COMMIT"); err != nil {
		t.Fatal(err)
	}
	committed = true
	return config
}

// seedV3Store creates the historical version-3 shape directly. It never
// opens the database through the current migrator, so migration tests cannot
// accidentally depend on version-4 setup or downgrade behavior.
func seedV3Store(t *testing.T, config Config, populated bool) {
	t.Helper()
	raw, err := sql.Open("sqlite", dataSourceName(config.Path, config.WithDefaults()))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	conn, err := raw.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	for _, statement := range []string{migration1DDL, migration2DDL, migration3DDL, "CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at_unix REAL NOT NULL)"} {
		if _, err := conn.ExecContext(context.Background(), statement); err != nil {
			t.Fatalf("create v3 fixture schema: %v", err)
		}
	}
	headPosition := 0
	if populated {
		headPosition = 1
	}
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO store_metadata (id, storage_format_version, head_commit_position, head_audit_digest, created_at_unix, last_migration_at_unix) VALUES (1, 3, ?, NULL, 0, 0)", headPosition); err != nil {
		t.Fatal(err)
	}
	if populated {
		seedPopulatedV3Session(t, conn)
	}
	if err := backfillAuditChain(context.Background(), conn); err != nil {
		t.Fatalf("backfill v3 fixture audit chain: %v", err)
	}
	for version, name := range map[int]string{1: "full target shape", 2: "append receipt verification index", 3: "audit chain backfill"} {
		if _, err := conn.ExecContext(context.Background(), "INSERT INTO schema_migrations (version, name, applied_at_unix) VALUES (?, ?, 0)", version, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.ExecContext(context.Background(), "PRAGMA user_version = 3"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), "COMMIT"); err != nil {
		t.Fatal(err)
	}
	committed = true
}

func seedPopulatedV3Session(t *testing.T, conn *sql.Conn) {
	t.Helper()
	records := []domain.RecordedEvent{
		{SchemaVersion: 1, ID: "event-v3-0", CommandID: "command-v3", SessionID: "session-v3", Sequence: 1, OccurredAt: testTime, Event: domain.SessionCreated{WorkspaceRoot: "/repo/."}},
		{SchemaVersion: 1, ID: "event-v3-1", CommandID: "command-v3", SessionID: "session-v3", Sequence: 2, OccurredAt: testTime, Event: domain.TurnStarted{TurnID: "turn-v3", Input: "hello"}},
	}
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO event_streams (session_id, version, created_at_commit_position, last_append_commit_position) VALUES ('session-v3', 2, 1, 1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO event_appends (append_id, commit_position, session_id, expected_version, first_sequence, last_sequence, event_count, command_id, request_digest, writer_runtime_id, writer_fencing_token, committed_at_unix) VALUES ('append-v3', 1, 'session-v3', 0, 1, 2, 2, 'command-v3', ?, 'runtime-v3', 1, 0)",
		make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	for index, record := range records {
		payload, err := domain.MarshalRecordedEvent(record)
		if err != nil {
			t.Fatal(err)
		}
		digest := auditEventPayloadDigest(payload)
		if _, err := conn.ExecContext(context.Background(),
			"INSERT INTO events (session_id, sequence, event_id, append_id, order_in_append, command_id, event_type, schema_version, occurred_at, payload, payload_digest) VALUES (?, ?, ?, 'append-v3', ?, 'command-v3', ?, 1, ?, ?, ?)",
			"session-v3", record.Sequence, record.ID, index+1, record.Event.EventType(), record.OccurredAt.Format("2006-01-02T15:04:05Z07:00"), payload, digest[:]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO domain_identities (session_id, identity_kind, identity_id, introducing_event_id) VALUES ('session-v3', 'turn', 'turn-v3', 'event-v3-1')"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(),
		"INSERT INTO session_heads (session_id, status, active_turn_id, active_item_id, updated_at_commit_position) VALUES ('session-v3', 'active', 'turn-v3', NULL, 1)"); err != nil {
		t.Fatal(err)
	}
}
