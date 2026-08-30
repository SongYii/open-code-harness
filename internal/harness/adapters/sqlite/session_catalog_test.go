package sqlite

import (
	"context"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestListSessionHeadsFiltersDeletedBeforeLimitAndPaginates(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	mustAppend(t, store, appendRequest("append-list-idle", "session-list-idle", 0, "command-list-idle",
		domain.SessionCreated{WorkspaceRoot: "/workspace"}))
	mustAppend(t, store, appendRequest("append-list-running", "session-list-running", 0, "command-list-running",
		domain.SessionCreated{WorkspaceRoot: "/workspace"},
		domain.TurnStarted{TurnID: "turn-list", Input: "hi"}))
	mustAppend(t, store, appendRequest("append-list-closed", "session-list-closed", 0, "command-list-closed",
		domain.SessionCreated{WorkspaceRoot: "/workspace"}, domain.SessionClosed{}))
	mustAppend(t, store, appendRequest("append-list-deleted", "session-list-deleted", 0, "command-list-deleted",
		domain.SessionCreated{WorkspaceRoot: "/workspace"}, domain.SessionDeleted{}))
	mustAppend(t, store, appendRequest("append-list-foreign", "session-list-foreign", 0, "command-list-foreign",
		domain.SessionCreated{WorkspaceRoot: "/foreign"}))

	first, err := store.ListSessionHeads(context.Background(), application.ListSessionHeadsRequest{
		WorkspaceRoot: "/workspace", Limit: 2,
	})
	if err != nil {
		t.Fatalf("ListSessionHeads(first) error = %v", err)
	}
	if got, want := sessionHeadIDs(first.Sessions), []domain.SessionID{"session-list-closed", "session-list-running"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first IDs = %v, want %v", got, want)
	}
	if first.NextCursor == "" {
		t.Fatal("first NextCursor is empty")
	}
	for _, head := range first.Sessions {
		if head.WorkspaceRoot != "/workspace" || head.UpdatedAt.IsZero() || head.UpdatedAt.Location() != time.UTC {
			t.Fatalf("invalid listed head: %#v", head)
		}
	}
	if first.Sessions[0].Status != application.SessionHeadStatusClosed || first.Sessions[1].Status != application.SessionHeadStatusRunning {
		t.Fatalf("first statuses = %q/%q", first.Sessions[0].Status, first.Sessions[1].Status)
	}

	second, err := store.ListSessionHeads(context.Background(), application.ListSessionHeadsRequest{
		WorkspaceRoot: "/workspace", Cursor: first.NextCursor, Limit: 2,
	})
	if err != nil {
		t.Fatalf("ListSessionHeads(second) error = %v", err)
	}
	if got, want := sessionHeadIDs(second.Sessions), []domain.SessionID{"session-list-idle"}; !reflect.DeepEqual(got, want) || second.NextCursor != "" {
		t.Fatalf("second = %#v, want IDs %v without cursor", second, want)
	}

	for _, cursor := range []string{
		"%%%",
		strings.Repeat("a", 513),
		base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"p":1,"s":"session-list-idle","extra":true}`)),
	} {
		_, err := store.ListSessionHeads(context.Background(), application.ListSessionHeadsRequest{
			WorkspaceRoot: "/workspace", Cursor: cursor, Limit: 2,
		})
		if !application.IsStoreCode(err, application.StoreCodeInvalidRead) {
			t.Fatalf("cursor %q error = %v, want invalid_read", cursor, err)
		}
	}
}

func TestListSessionHeadsBreaksCommitPositionTiesBySessionID(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	mustAppend(t, store, appendRequest("append-tie-a", "session-tie-a", 0, "command-tie-a",
		domain.SessionCreated{WorkspaceRoot: "/tie"}))
	mustAppend(t, store, appendRequest("append-tie-b", "session-tie-b", 0, "command-tie-b",
		domain.SessionCreated{WorkspaceRoot: "/tie"}))
	if _, err := store.writer.ExecContext(context.Background(),
		"UPDATE session_heads SET updated_at_commit_position = 2 WHERE workspace_root = '/tie'"); err != nil {
		t.Fatal(err)
	}

	first, err := store.ListSessionHeads(context.Background(), application.ListSessionHeadsRequest{WorkspaceRoot: "/tie", Limit: 1})
	if err != nil || len(first.Sessions) != 1 || first.Sessions[0].SessionID != "session-tie-b" || first.NextCursor == "" {
		t.Fatalf("first tie page = %#v, %v", first, err)
	}
	second, err := store.ListSessionHeads(context.Background(), application.ListSessionHeadsRequest{WorkspaceRoot: "/tie", Cursor: first.NextCursor, Limit: 1})
	if err != nil || len(second.Sessions) != 1 || second.Sessions[0].SessionID != "session-tie-a" || second.NextCursor != "" {
		t.Fatalf("second tie page = %#v, %v", second, err)
	}
}

func TestListSessionHeadsStrictlyDecodesCursorsAndBindsCursorValues(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	for _, id := range []domain.SessionID{"session-bound-1", "session-bound-2", "session-bound-3"} {
		mustAppend(t, store, appendRequest(domain.AppendID("append-"+id), id, 0, domain.CommandID("command-"+id),
			domain.SessionCreated{WorkspaceRoot: "/bound"}))
	}

	for _, cursor := range []string{
		base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"p":1}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"s":"session-bound-1"}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"p":0,"s":"session-bound-1"}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"v":2,"p":1,"s":"session-bound-1"}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"p":1,"s":" session-bound-1"}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"p":1,"s":"session-bound-1"}`)) + "=",
	} {
		_, err := store.ListSessionHeads(context.Background(), application.ListSessionHeadsRequest{
			WorkspaceRoot: "/bound", Cursor: cursor, Limit: 2,
		})
		if !application.IsStoreCode(err, application.StoreCodeInvalidRead) {
			t.Fatalf("ListSessionHeads(cursor %q) error = %v, want invalid_read", cursor, err)
		}
	}

	// This is a valid identifier, but shaped to alter SQL if it were interpolated
	// rather than bound. A bound cursor excludes position 3 and returns only older heads.
	injection := base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"p":3,"s":"' OR 1=1 --"}`))
	page, err := store.ListSessionHeads(context.Background(), application.ListSessionHeadsRequest{
		WorkspaceRoot: "/bound", Cursor: injection, Limit: 3,
	})
	if err != nil {
		t.Fatalf("ListSessionHeads(injection-shaped cursor) error = %v", err)
	}
	if got, want := sessionHeadIDs(page.Sessions), []domain.SessionID{"session-bound-2", "session-bound-1"}; !reflect.DeepEqual(got, want) || page.NextCursor != "" {
		t.Fatalf("bound cursor page = %#v, want IDs %v without cursor", page, want)
	}
}

func TestListSessionHeadsConvertsUTCAndRejectsCorruptVisibleRows(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	mustAppend(t, store, appendRequest("append-catalog-time", "session-catalog-time", 0, "command-catalog-time",
		domain.SessionCreated{WorkspaceRoot: "/time"}))
	if _, err := store.writer.ExecContext(context.Background(),
		"UPDATE event_appends SET committed_at_unix = ? WHERE commit_position = 1", 1712345678.123); err != nil {
		t.Fatalf("set committed_at_unix: %v", err)
	}

	page, err := store.ListSessionHeads(context.Background(), application.ListSessionHeadsRequest{WorkspaceRoot: "/time", Limit: 1})
	if err != nil || len(page.Sessions) != 1 {
		t.Fatalf("ListSessionHeads() = %#v, %v", page, err)
	}
	if got, want := page.Sessions[0].UpdatedAt, time.Unix(1712345678, 123000000).UTC(); !got.Equal(want) || got.Location() != time.UTC {
		t.Fatalf("UpdatedAt = %s (%s), want %s (UTC)", got, got.Location(), want)
	}
	page.Sessions[0].WorkspaceRoot = "/mutated"
	again, err := store.ListSessionHeads(context.Background(), application.ListSessionHeadsRequest{WorkspaceRoot: "/time", Limit: 1})
	if err != nil || again.Sessions[0].WorkspaceRoot != "/time" {
		t.Fatalf("fresh catalog result = %#v, %v; want independent result", again, err)
	}

	if _, err := store.writer.ExecContext(context.Background(), "PRAGMA ignore_check_constraints = ON"); err != nil {
		t.Fatalf("allow corrupt session head status: %v", err)
	}
	if _, err := store.writer.ExecContext(context.Background(),
		"UPDATE session_heads SET status = 'unexpected' WHERE session_id = 'session-catalog-time'"); err != nil {
		t.Fatalf("corrupt session head status: %v", err)
	}
	if _, err := store.writer.ExecContext(context.Background(), "PRAGMA ignore_check_constraints = OFF"); err != nil {
		t.Fatalf("restore session head constraints: %v", err)
	}
	_, err = store.ListSessionHeads(context.Background(), application.ListSessionHeadsRequest{WorkspaceRoot: "/time", Limit: 1})
	if !application.IsStoreCode(err, application.StoreCodeCorrupt) {
		t.Fatalf("ListSessionHeads(corrupt status) error = %v, want corrupt", err)
	}
}

func TestListSessionHeadsUsesOneSnapshotDuringConcurrentAppend(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	mustAppend(t, store, appendRequest("append-catalog-before", "session-catalog-before", 0, "command-catalog-before",
		domain.SessionCreated{WorkspaceRoot: "/snapshot"}))

	type listResult struct {
		page application.SessionHeadPage
		err  error
	}
	listed := make(chan listResult, 1)
	store.SetCommitHook(commitHookBeforePublish, func() {
		page, err := store.ListSessionHeads(context.Background(), application.ListSessionHeadsRequest{WorkspaceRoot: "/snapshot", Limit: 2})
		listed <- listResult{page: page, err: err}
	})
	t.Cleanup(func() { store.SetCommitHook(commitHookBeforePublish, nil) })
	appended := make(chan error, 1)
	go func() {
		_, err := store.Append(context.Background(), appendRequest("append-catalog-concurrent", "session-catalog-concurrent", 0, "command-catalog-concurrent",
			domain.SessionCreated{WorkspaceRoot: "/snapshot"}))
		appended <- err
	}()

	var result listResult
	select {
	case result = <-listed:
	case <-time.After(5 * time.Second):
		t.Fatal("ListSessionHeads did not complete while append was awaiting publish")
	}
	if result.err != nil {
		t.Fatalf("ListSessionHeads during append error = %v", result.err)
	}
	if got, want := sessionHeadIDs(result.page.Sessions), []domain.SessionID{"session-catalog-before"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot page IDs = %v, want %v", got, want)
	}
	select {
	case err := <-appended:
		if err != nil {
			t.Fatalf("concurrent Append error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent append did not publish after ListSessionHeads completed")
	}

	after, err := store.ListSessionHeads(context.Background(), application.ListSessionHeadsRequest{WorkspaceRoot: "/snapshot", Limit: 2})
	if err != nil {
		t.Fatalf("ListSessionHeads after append error = %v", err)
	}
	if got, want := sessionHeadIDs(after.Sessions), []domain.SessionID{"session-catalog-concurrent", "session-catalog-before"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("post-append IDs = %v, want %v", got, want)
	}
}

func sessionHeadIDs(heads []application.SessionHead) []domain.SessionID {
	ids := make([]domain.SessionID, len(heads))
	for index := range heads {
		ids[index] = heads[index].SessionID
	}
	return ids
}
