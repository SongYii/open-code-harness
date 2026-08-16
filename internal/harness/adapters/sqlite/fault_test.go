package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"

	_ "modernc.org/sqlite"
)

func TestFaultBeforeCommitLeavesNothingAndIsOneShot(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	ctx := context.Background()
	request := appendRequest("append-fault-pre", "session-fault-pre", 0, "command-fault-pre",
		domain.SessionCreated{WorkspaceRoot: "/w"})

	store.FailNext(faultBeforeCommit, errors.New("inject before commit"))
	_, err := store.Append(ctx, request)
	requireStoreCode(t, err, application.StoreCodeUnavailable)
	if got := tableCount(t, store, "events"); got != 0 {
		t.Fatalf("events rows = %d after before-commit fault, want 0", got)
	}
	if got := tableCount(t, store, "event_appends"); got != 0 {
		t.Fatalf("event_appends rows = %d, want 0", got)
	}

	mustAppend(t, store, request)
}

func TestFaultAfterCommitReportsUnknownAndResolves(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	ctx := context.Background()
	request := appendRequest("append-fault-post", "session-fault-post", 0, "command-fault-post",
		domain.SessionCreated{WorkspaceRoot: "/w"})

	store.FailNext(faultAfterCommitBeforeAck, errors.New("inject after commit"))
	receipt, err := store.Append(ctx, request)
	if err == nil {
		t.Fatalf("after-commit fault returned receipt %+v, want unknown outcome", receipt)
	}
	requireStoreCode(t, err, application.StoreCodeCommitOutcomeUnknown)

	// The batch is durable even though the acknowledgement was lost.
	if got := tableCount(t, store, "events"); got != 1 {
		t.Fatalf("events rows = %d after after-commit fault, want 1", got)
	}

	digest, err := application.DigestAppendRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := store.ResolveAppend(ctx, application.ResolveAppendRequest{AppendID: request.AppendID, RequestDigest: digest})
	if err != nil || resolved.Kind != application.AppendResolutionCommitted || resolved.Receipt == nil {
		t.Fatalf("ResolveAppend = (%#v, %v), want committed", resolved, err)
	}
	retried, err := store.Append(ctx, request)
	if err != nil || retried != *resolved.Receipt {
		t.Fatalf("exact retry after unknown = (%+v, %v), want %+v", retried, err, resolved.Receipt)
	}

	// The fault is one-shot: a fresh append succeeds.
	fresh := appendRequest("append-fault-fresh", "session-fault-fresh", 0, "command-fault-fresh",
		domain.SessionCreated{WorkspaceRoot: "/w"})
	mustAppend(t, store, fresh)
}

func TestFaultResolveIsUnavailableAndOneShot(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	ctx := context.Background()
	request := appendRequest("append-fault-res", "session-fault-res", 0, "command-fault-res",
		domain.SessionCreated{WorkspaceRoot: "/w"})
	mustAppend(t, store, request)
	digest, err := application.DigestAppendRequest(request)
	if err != nil {
		t.Fatal(err)
	}

	store.FailNext(faultResolve, errors.New("inject resolve"))
	if _, err := store.ResolveAppend(ctx, application.ResolveAppendRequest{AppendID: request.AppendID, RequestDigest: digest}); err == nil {
		t.Fatal("resolve fault = nil, want unavailable")
	} else {
		requireStoreCode(t, err, application.StoreCodeUnavailable)
	}
	resolved, err := store.ResolveAppend(ctx, application.ResolveAppendRequest{AppendID: request.AppendID, RequestDigest: digest})
	if err != nil || resolved.Kind != application.AppendResolutionCommitted {
		t.Fatalf("resolve after fault = (%#v, %v)", resolved, err)
	}
}

func TestCorruptReceiptDetectedOnRetryAndResolve(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	ctx := context.Background()
	request := appendRequest("append-corrupt", "session-corrupt", 0, "command-corrupt",
		domain.SessionCreated{WorkspaceRoot: "/w"})
	mustAppend(t, store, request)
	digest, err := application.DigestAppendRequest(request)
	if err != nil {
		t.Fatal(err)
	}

	store.CorruptReceiptForTesting(request.AppendID)
	if _, err := store.Append(ctx, request); err == nil {
		t.Fatal("corrupt receipt retry = nil, want corrupt")
	} else {
		requireStoreCode(t, err, application.StoreCodeCorrupt)
	}
	if _, err := store.ResolveAppend(ctx, application.ResolveAppendRequest{AppendID: request.AppendID, RequestDigest: digest}); err == nil {
		t.Fatal("corrupt receipt resolve = nil, want corrupt")
	} else {
		requireStoreCode(t, err, application.StoreCodeCorrupt)
	}
}

func TestBusyContentionIsBoundedUnavailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy.db")
	config := Config{Path: path, RuntimeID: "runtime-1", BusyTimeout: 200 * time.Millisecond}
	store, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	blocker, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("blocker open: %v", err)
	}
	defer blocker.Close()
	if _, err := blocker.Exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("blocker begin: %v", err)
	}
	if _, err := blocker.Exec("UPDATE store_metadata SET last_migration_at_unix = last_migration_at_unix WHERE id = 1"); err != nil {
		t.Fatalf("blocker write: %v", err)
	}

	request := appendRequest("append-busy", "session-busy", 0, "command-busy", domain.SessionCreated{WorkspaceRoot: "/w"})
	start := time.Now()
	_, err = store.Append(context.Background(), request)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("contended append = nil, want bounded unavailable")
	}
	requireStoreCode(t, err, application.StoreCodeUnavailable)
	if elapsed > 5*time.Second {
		t.Fatalf("busy wait = %s, want bounded near the configured timeout", elapsed)
	}
}

func TestReopenAfterTerminationKeepsConsistentState(t *testing.T) {
	config := tempStoreConfig(t)
	first := openStore(t, config)
	mustAppend(t, first, appendRequest("append-reopen", "session-reopen", 0, "command-reopen",
		domain.SessionCreated{WorkspaceRoot: "/w"},
		domain.TurnStarted{TurnID: "turn-reopen", Input: "x"},
		domain.TurnCompleted{TurnID: "turn-reopen"}))
	if err := first.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}

	second := openStore(t, config)
	defer second.Close()
	page, err := second.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: "session-reopen", Limit: 256})
	if err != nil {
		t.Fatalf("reopen read: %v", err)
	}
	if len(page.Records) != 3 || page.HeadVersion != 3 {
		t.Fatalf("reopen page = %d records at head %d, want 3 at 3", len(page.Records), page.HeadVersion)
	}
	var head uint64
	if err := second.db.QueryRowContext(context.Background(),
		"SELECT head_commit_position FROM store_metadata WHERE id = 1").Scan(&head); err != nil {
		t.Fatalf("reopen head read: %v", err)
	}
	if head != 1 {
		t.Fatalf("reopen head_commit_position = %d, want 1", head)
	}
}
