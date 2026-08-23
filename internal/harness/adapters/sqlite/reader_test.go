package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func openReader(t *testing.T, config ReaderConfig) *Reader {
	t.Helper()
	reader, err := OpenReader(context.Background(), config)
	if err != nil {
		t.Fatalf("OpenReader() error = %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	return reader
}

func TestOpenReaderAppliesReadProfile(t *testing.T) {
	writer := openStore(t, tempStoreConfig(t))
	config := ReaderConfig{Path: writer.config.Path, BusyTimeout: 1500 * time.Millisecond}
	reader := openReader(t, config)

	ctx := context.Background()
	var journalMode string
	if err := reader.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}
	var foreignKeys int
	if err := reader.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}
	var busyTimeoutMS int
	if err := reader.db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeoutMS); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if busyTimeoutMS != 1500 {
		t.Fatalf("busy_timeout = %d ms, want 1500", busyTimeoutMS)
	}
	var queryOnly int
	if err := reader.db.QueryRowContext(ctx, "PRAGMA query_only").Scan(&queryOnly); err != nil {
		t.Fatalf("read query_only: %v", err)
	}
	if queryOnly != 1 {
		t.Fatalf("query_only = %d, want 1", queryOnly)
	}
}

func TestOpenReaderRejectsDeniedPathPrefix(t *testing.T) {
	dir := t.TempDir()
	config := ReaderConfig{
		Path:               filepath.Join(dir, "synced", "harness.db"),
		DeniedPathPrefixes: []string{filepath.Join(dir, "synced")},
	}
	_, err := OpenReader(context.Background(), config)
	if err == nil {
		t.Fatal("OpenReader() on denied prefix = nil, want error")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Fatalf("error = %v, want denied-path diagnosis", err)
	}
}

func TestOpenReaderRejectsInvalidBusyTimeout(t *testing.T) {
	config := ReaderConfig{Path: filepath.Join(t.TempDir(), "harness.db"), BusyTimeout: time.Millisecond}
	if _, err := OpenReader(context.Background(), config); err == nil {
		t.Fatal("OpenReader() with out-of-range busy timeout = nil, want error")
	}
}

func TestOpenReaderDoesNotConvertNonWALDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "delete-mode.db")
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := raw.Exec("CREATE TABLE t(x INTEGER)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	var before string
	if err := raw.QueryRow("PRAGMA journal_mode").Scan(&before); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if before == "wal" {
		t.Fatal("setup journal_mode is already wal")
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("raw close: %v", err)
	}

	_, err = OpenReader(context.Background(), ReaderConfig{Path: path})
	if err == nil {
		t.Fatal("OpenReader() on non-WAL database = nil, want error")
	}
	if !strings.Contains(err.Error(), "not wal") {
		t.Fatalf("error = %v, want journal-mode refusal", err)
	}

	check, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer check.Close()
	var after string
	if err := check.QueryRow("PRAGMA journal_mode").Scan(&after); err != nil {
		t.Fatalf("read journal_mode after OpenReader: %v", err)
	}
	if after != before {
		t.Fatalf("journal_mode changed from %q to %q", before, after)
	}
}

func TestOpenReaderDoesNotCreateMissingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	_, err := OpenReader(context.Background(), ReaderConfig{Path: path})
	if err == nil {
		t.Fatal("OpenReader() on missing path = nil, want error")
	}
	if strings.Contains(err.Error(), "writer must migrate first") {
		t.Fatalf("error = %v, want open failure not older-format diagnosis", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("OpenReader created %q: %v", path, statErr)
	}
}

func TestOpenReaderRefusesNewerFormat(t *testing.T) {
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

	_, err = OpenReader(context.Background(), ReaderConfig{Path: path})
	if err == nil {
		t.Fatal("OpenReader() on newer format = nil, want error")
	}
	var newer *FormatNewerError
	if !errors.As(err, &newer) {
		t.Fatalf("error = %v, want *FormatNewerError", err)
	}
	if newer.Have != 999 || newer.Supported != latestMigrationVersion {
		t.Fatalf("FormatNewerError have=%d supported=%d, want 999/%d", newer.Have, newer.Supported, latestMigrationVersion)
	}
}

func TestOpenReaderRefusesOlderFormatWithoutMigrating(t *testing.T) {
	path := filepath.Join(t.TempDir(), "older.db")
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
	if _, err := raw.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatalf("stamp older user_version: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("raw close: %v", err)
	}

	_, err = OpenReader(context.Background(), ReaderConfig{Path: path})
	if err == nil {
		t.Fatal("OpenReader() on older format = nil, want error")
	}
	if !strings.Contains(err.Error(), "writer must migrate first") {
		t.Fatalf("error = %v, want writer-must-migrate diagnosis", err)
	}

	check, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("reopen for version check: %v", err)
	}
	defer check.Close()
	var version int
	if err := check.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != 1 {
		t.Fatalf("user_version = %d after refused OpenReader, want 1 (no migrate)", version)
	}
}

func TestOpenReaderDoesNotAcquireLease(t *testing.T) {
	ctx := context.Background()
	config := tempStoreConfig(t)
	writer := openStore(t, config)
	var runtimeID string
	var token uint64
	if err := writer.db.QueryRowContext(ctx,
		"SELECT runtime_id, fencing_token FROM runtime_leases WHERE id = 1").Scan(&runtimeID, &token); err != nil {
		t.Fatalf("read lease: %v", err)
	}

	_ = openReader(t, ReaderConfig{Path: config.Path})

	var afterID string
	var afterToken uint64
	if err := writer.db.QueryRowContext(ctx,
		"SELECT runtime_id, fencing_token FROM runtime_leases WHERE id = 1").Scan(&afterID, &afterToken); err != nil {
		t.Fatalf("read lease after OpenReader: %v", err)
	}
	if afterID != runtimeID || afterToken != token {
		t.Fatalf("lease mutated by OpenReader: %s/%d -> %s/%d", runtimeID, token, afterID, afterToken)
	}
}

func TestOpenReaderReadsWhileLeasedWriterCommits(t *testing.T) {
	ctx := context.Background()
	config := tempStoreConfig(t)
	writer := openStore(t, config)
	mustAppend(t, writer, appendRequest("append-reader-1", "session-reader", 0, "command-reader-1",
		domain.SessionCreated{WorkspaceRoot: "/w"}))

	reader := openReader(t, ReaderConfig{Path: config.Path})
	page, err := reader.ReadStream(ctx, application.ReadStreamRequest{SessionID: "session-reader", Limit: 256})
	if err != nil {
		t.Fatalf("initial read: %v", err)
	}
	if len(page.Records) != 1 || page.HeadVersion != 1 {
		t.Fatalf("initial page = %d records at head %d, want 1 at 1", len(page.Records), page.HeadVersion)
	}

	mustAppend(t, writer, appendRequest("append-reader-2", "session-reader", 1, "command-reader-2",
		domain.TurnStarted{TurnID: "turn-reader", Input: "hi"}))

	page, err = reader.ReadStream(ctx, application.ReadStreamRequest{SessionID: "session-reader", Limit: 256})
	if err != nil {
		t.Fatalf("read after writer commit: %v", err)
	}
	if len(page.Records) != 2 || page.HeadVersion != 2 {
		t.Fatalf("page after commit = %d records at head %d, want 2 at 2", len(page.Records), page.HeadVersion)
	}
}

func TestOpenReaderWaitsOnWriterImmediateLock(t *testing.T) {
	ctx := context.Background()
	config := tempStoreConfig(t)
	config.BusyTimeout = time.Second
	writer := openStore(t, config)
	mustAppend(t, writer, appendRequest("append-busy-reader", "session-busy-reader", 0, "command-busy-reader",
		domain.SessionCreated{WorkspaceRoot: "/w"}))

	reader := openReader(t, ReaderConfig{Path: config.Path, BusyTimeout: time.Second})

	writer.writeMu.Lock()
	if _, err := writer.writer.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		writer.writeMu.Unlock()
		t.Fatalf("writer BEGIN IMMEDIATE: %v", err)
	}
	unlocked := false
	defer func() {
		if !unlocked {
			_, _ = writer.writer.ExecContext(context.Background(), "ROLLBACK")
			writer.writeMu.Unlock()
		}
	}()

	started := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := reader.ReadStream(ctx, application.ReadStreamRequest{SessionID: "session-busy-reader", Limit: 256})
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("reader failed immediately on writer BEGIN IMMEDIATE after %s: %v", time.Since(started), err)
		}
	case <-time.After(200 * time.Millisecond):
		if _, err := writer.writer.ExecContext(ctx, "COMMIT"); err != nil {
			t.Fatalf("writer commit: %v", err)
		}
		writer.writeMu.Unlock()
		unlocked = true
		if err := <-done; err != nil {
			t.Fatalf("reader after writer commit: %v", err)
		}
		if elapsed := time.Since(started); elapsed < 200*time.Millisecond {
			t.Fatalf("reader returned too quickly (%s) while BEGIN IMMEDIATE was held", elapsed)
		}
	}
}

func TestReaderTypeHasNoAppend(t *testing.T) {
	typ := reflect.TypeOf((*Reader)(nil))
	for _, name := range []string{"Append", "ResolveAppend", "FindCommandRequest", "AcquireLease"} {
		if _, ok := typ.MethodByName(name); ok {
			t.Errorf("Reader must not expose %s", name)
		}
	}
}
