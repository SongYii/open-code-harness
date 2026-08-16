package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"

	_ "modernc.org/sqlite"
)

func TestOpenAcquiresLeaseWithTokenOne(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	authority := store.Authority()
	if authority.RuntimeID != application.RuntimeID("runtime-1") || authority.FencingToken != 1 {
		t.Fatalf("authority = %+v, want runtime-1 token 1", authority)
	}
	var runtimeID string
	var token uint64
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT runtime_id, fencing_token FROM runtime_leases WHERE id = 1").Scan(&runtimeID, &token); err != nil {
		t.Fatalf("read lease: %v", err)
	}
	if runtimeID != "runtime-1" || token != 1 {
		t.Fatalf("lease row = %s/%d", runtimeID, token)
	}
}

func TestReopenSameRuntimeKeepsToken(t *testing.T) {
	config := tempStoreConfig(t)
	store := openStore(t, config)
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	reopened := openStore(t, config)
	if authority := reopened.Authority(); authority.FencingToken != 1 {
		t.Fatalf("reopened token = %d, want 1", authority.FencingToken)
	}
}

func TestOpenRefusesLiveForeignLease(t *testing.T) {
	config := tempStoreConfig(t)
	store := openStore(t, config)
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	raw, err := sql.Open("sqlite", "file:"+config.Path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer raw.Close()
	if _, err := raw.Exec(
		"UPDATE runtime_leases SET runtime_id = 'runtime-holder', lease_expires_at_unix = unixepoch('subsec') + 3600 WHERE id = 1"); err != nil {
		t.Fatalf("seed holder: %v", err)
	}

	foreign := config
	foreign.RuntimeID = "runtime-challenger"
	if _, err := Open(context.Background(), foreign); err == nil {
		t.Fatal("Open() against live foreign lease = nil, want refusal")
	}
}

func TestExpiredLeaseIsRetakenWithNextToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "expired.db")
	config := Config{Path: path, RuntimeID: "runtime-first", LeaseDuration: time.Second}
	store, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	time.Sleep(1100 * time.Millisecond)

	successorConfig := Config{Path: path, RuntimeID: "runtime-second", LeaseDuration: 30 * time.Second}
	successor, err := Open(context.Background(), successorConfig)
	if err != nil {
		t.Fatalf("successor open: %v", err)
	}
	defer successor.Close()
	if authority := successor.Authority(); authority.FencingToken != 2 {
		t.Fatalf("successor token = %d, want 2", authority.FencingToken)
	}

	// The expired holder's authority is fenced.
	stale := appendRequest("append-stale", "session-stale", 0, "command-stale", domain.SessionCreated{WorkspaceRoot: "/w"})
	stale.Authority = application.WriterAuthority{RuntimeID: "runtime-first", FencingToken: 1}
	if _, err := successor.Append(context.Background(), stale); err == nil {
		t.Fatal("expired holder appended; want writer fenced")
	} else {
		requireStoreCode(t, err, application.StoreCodeWriterFenced)
	}

	current := appendRequest("append-ok", "session-ok", 0, "command-ok", domain.SessionCreated{WorkspaceRoot: "/w"})
	current.Authority = successor.Authority()
	mustAppend(t, successor, current)
}

func TestExpiredLeaseRefusesAppendUntilReacquired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shortlease.db")
	config := Config{Path: path, RuntimeID: "runtime-1", LeaseDuration: time.Second}
	store, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	time.Sleep(1100 * time.Millisecond)
	request := appendRequest("append-late", "session-late", 0, "command-late", domain.SessionCreated{WorkspaceRoot: "/w"})
	request.Authority = store.Authority()
	if _, err := store.Append(context.Background(), request); err == nil {
		t.Fatal("expired lease accepted append; want writer fenced")
	} else {
		requireStoreCode(t, err, application.StoreCodeWriterFenced)
	}

	if err := store.RenewLease(context.Background()); err == nil {
		t.Fatal("renewal of expired lease = nil; want fenced")
	} else {
		requireStoreCode(t, err, application.StoreCodeWriterFenced)
	}

	if _, err := store.AcquireLease(context.Background()); err != nil {
		t.Fatalf("reacquire: %v", err)
	}
	if authority := store.Authority(); authority.FencingToken != 2 {
		t.Fatalf("reacquired token = %d, want 2", authority.FencingToken)
	}
	request.Authority = store.Authority()
	mustAppend(t, store, request)
}

func TestRenewLeaseExtendsExpiry(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	if err := store.RenewLease(context.Background()); err != nil {
		t.Fatalf("renew: %v", err)
	}
	var remaining float64
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT lease_expires_at_unix - unixepoch('subsec') FROM runtime_leases WHERE id = 1").Scan(&remaining); err != nil {
		t.Fatalf("read remaining: %v", err)
	}
	if remaining <= 0 || remaining > 31 {
		t.Fatalf("remaining lease = %f seconds, want within (0, 31]", remaining)
	}
}

func TestRotateAuthorityFencesOldWriter(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	old := store.Authority()
	if err := store.rotateAuthorityForTesting(application.WriterAuthority{RuntimeID: "runtime-2", FencingToken: old.FencingToken + 1}); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	request := appendRequest("append-rotated", "session-rot", 0, "command-rot", domain.SessionCreated{WorkspaceRoot: "/w"})
	request.Authority = old
	if _, err := store.Append(context.Background(), request); err == nil {
		t.Fatal("old writer accepted; want fenced")
	} else {
		requireStoreCode(t, err, application.StoreCodeWriterFenced)
	}

	request.Authority = store.Authority()
	mustAppend(t, store, request)
}
