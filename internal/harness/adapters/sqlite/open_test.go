package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openStore(t *testing.T, config Config) *Store {
	t.Helper()
	store, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// testLeaseDuration is the lease every store built by tempStoreConfig holds.
//
// It is long on purpose, and the reason is not "tests are slow". Nothing in
// this package runs a Runtime Host, and the Runtime Host is the only thing
// that ever calls RenewLease -- so a store opened here holds a lease that is
// never renewed and simply expires. Under the production default of 30
// seconds, any test whose wall-clock from Open exceeds that window starts
// failing its next Append as writer_fenced, which is the store behaving
// exactly as designed and the test measuring something it never meant to.
//
// That is not hypothetical: TestConformance/limits_copies_cancellation_and_corruption
// builds several 16 MiB requests and took 32.81s under a full parallel -race
// run, and failed. Setting this to a second reproduces it on demand.
//
// An hour is chosen rather than a value tuned to today's slowest test, so a
// later test that grows past some threshold does not quietly reintroduce the
// same failure. Expiry itself is not left untested: lease_test.go opens its
// own stores with a one-second lease precisely to watch them expire.
const testLeaseDuration = time.Hour

func tempStoreConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		Path:          filepath.Join(t.TempDir(), "harness.db"),
		RuntimeID:     "runtime-1",
		LeaseDuration: testLeaseDuration,
	}
}

// TestTheSharedHarnessNeverRunsOnTheProductionLeaseClock is a guard rather
// than a behaviour test.
//
// The failure it prevents is silent and looks like flakiness: a store with no
// heartbeat, a lease that expires on a wall clock the test does not control,
// and a fenced Append reported as whatever assertion happened to come next.
// Reverting tempStoreConfig to the default would restore that, so the rule is
// a test instead of a comment.
func TestTheSharedHarnessNeverRunsOnTheProductionLeaseClock(t *testing.T) {
	config := tempStoreConfig(t)
	if config.LeaseDuration == 0 {
		t.Fatal("the shared harness took the production default lease; nothing here renews it")
	}
	if config.LeaseDuration <= defaultLeaseDuration {
		t.Fatalf("harness lease = %v, want well above the %v production default: no test in this package runs a Runtime Host, so the lease is never renewed",
			config.LeaseDuration, defaultLeaseDuration)
	}
	if config.LeaseDuration > maxLeaseDuration {
		t.Fatalf("harness lease = %v exceeds the adapter's own maximum %v", config.LeaseDuration, maxLeaseDuration)
	}
}

func TestOpenAppliesAndReportsOperatingProfile(t *testing.T) {
	config := tempStoreConfig(t)
	config.BusyTimeout = 1500 * time.Millisecond
	store := openStore(t, config)

	ctx := context.Background()
	var journalMode string
	if err := store.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}
	var synchronous int
	if err := store.db.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatalf("read synchronous: %v", err)
	}
	if synchronous != 2 {
		t.Fatalf("synchronous = %d, want 2 (FULL)", synchronous)
	}
	var foreignKeys int
	if err := store.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}
	var busyTimeoutMS int
	if err := store.db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeoutMS); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if busyTimeoutMS != 1500 {
		t.Fatalf("busy_timeout = %d ms, want 1500", busyTimeoutMS)
	}
}

func TestJournalModeVerificationAcceptsOnlyWAL(t *testing.T) {
	for _, mode := range []string{"delete", "truncate", "persist", "memory", "off", "wal2"} {
		if err := verifyJournalMode(mode); err == nil {
			t.Fatalf("verifyJournalMode(%q) = nil, want error", mode)
		}
	}
	if err := verifyJournalMode("wal"); err != nil {
		t.Fatalf("verifyJournalMode(wal) = %v, want nil", err)
	}
}

func TestOpenVerifiesJournalModeOfOpenDatabase(t *testing.T) {
	var mode string
	store := openStore(t, tempStoreConfig(t))
	if err := store.writer.QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("read journal_mode from writer: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("writer journal_mode = %q, want wal", mode)
	}
}

func TestOpenRejectsDeniedPathPrefix(t *testing.T) {
	dir := t.TempDir()
	config := Config{
		Path:               filepath.Join(dir, "synced", "harness.db"),
		RuntimeID:          "runtime-1",
		DeniedPathPrefixes: []string{filepath.Join(dir, "synced")},
	}
	_, err := Open(context.Background(), config)
	if err == nil {
		t.Fatal("Open() on denied prefix = nil, want error")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Fatalf("error = %v, want denied-path diagnosis", err)
	}
}

func TestOpenRejectsInvalidConfig(t *testing.T) {
	config := tempStoreConfig(t)
	config.BusyTimeout = time.Millisecond
	if _, err := Open(context.Background(), config); err == nil {
		t.Fatal("Open() with out-of-range busy timeout = nil, want error")
	}
}

func TestSQLiteVersionSupportsSubsecondUnixEpoch(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	ctx := context.Background()
	var version string
	if err := store.db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&version); err != nil {
		t.Fatalf("read sqlite_version: %v", err)
	}
	var major, minor, patch int
	if _, err := fmt.Sscanf(version, "%d.%d.%d", &major, &minor, &patch); err != nil {
		t.Fatalf("parse sqlite version %q: %v", version, err)
	}
	if major < 3 || (major == 3 && minor < 42) {
		t.Fatalf("sqlite version %q predates unixepoch subsec support", version)
	}
	var stamp float64
	if err := store.db.QueryRowContext(ctx, "SELECT unixepoch('subsec')").Scan(&stamp); err != nil {
		t.Fatalf("unixepoch('subsec') failed: %v", err)
	}
	if stamp <= 0 {
		t.Fatalf("unixepoch('subsec') = %v, want positive", stamp)
	}
}

func TestOpenRefusesNewerFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	store, err := Open(context.Background(), Config{Path: path, RuntimeID: "runtime-1"})
	if err != nil {
		t.Fatalf("initial Open: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := raw.Exec("PRAGMA user_version = 999"); err != nil {
		t.Fatalf("stamp newer user_version: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("raw close: %v", err)
	}

	_, err = Open(context.Background(), Config{Path: path, RuntimeID: "runtime-1"})
	if err == nil {
		t.Fatal("Open() on newer format = nil, want error")
	}
	var newer *FormatNewerError
	if !errors.As(err, &newer) {
		t.Fatalf("error = %v, want *FormatNewerError", err)
	}
	if newer.Have != 999 || newer.Supported != latestMigrationVersion {
		t.Fatalf("FormatNewerError have=%d supported=%d, want 999/%d", newer.Have, newer.Supported, latestMigrationVersion)
	}
	if !strings.Contains(err.Error(), "newer") || !strings.Contains(err.Error(), "upgrade") {
		t.Fatalf("error = %v, want upgrade-direction message", err)
	}
}

func TestOpenDetectsTamperedMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tampered.db")
	store, err := Open(context.Background(), Config{Path: path, RuntimeID: "runtime-1"})
	if err != nil {
		t.Fatalf("initial Open: %v", err)
	}
	if _, err := store.writer.ExecContext(context.Background(),
		"UPDATE store_metadata SET storage_format_version = ?", latestMigrationVersion+6); err != nil {
		t.Fatalf("tamper metadata: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	_, err = Open(context.Background(), Config{Path: path, RuntimeID: "runtime-1"})
	if err == nil {
		t.Fatal("Open() on tampered metadata = nil, want error")
	}
	var corrupt *CorruptError
	if !errors.As(err, &corrupt) {
		t.Fatalf("error = %v, want *CorruptError", err)
	}
}

func TestOpenDetectsMissingMetadataRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	store, err := Open(context.Background(), Config{Path: path, RuntimeID: "runtime-1"})
	if err != nil {
		t.Fatalf("initial Open: %v", err)
	}
	if _, err := store.writer.ExecContext(context.Background(), "DELETE FROM store_metadata"); err != nil {
		t.Fatalf("delete metadata: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	_, err = Open(context.Background(), Config{Path: path, RuntimeID: "runtime-1"})
	if err == nil {
		t.Fatal("Open() on missing metadata = nil, want error")
	}
	var corrupt *CorruptError
	if !errors.As(err, &corrupt) {
		t.Fatalf("error = %v, want *CorruptError", err)
	}
}
