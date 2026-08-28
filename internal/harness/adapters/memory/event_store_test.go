package memory

import (
	"context"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/application/eventstoretest"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

func TestListSessionHeadsFiltersBeforeLimitAndPaginatesDeterministically(t *testing.T) {
	t.Parallel()

	authority := application.WriterAuthority{RuntimeID: "runtime-list", FencingToken: 1}
	store := mustV2Store(t, authority)
	ids := testkit.NewSequenceIDs()
	baseTime := time.Date(2026, 8, 28, 3, 0, 0, 0, time.UTC)

	appendMemoryEvent(t, store, authority, ids, "session-idle", 0, baseTime.Add(time.Second), domain.SessionCreated{WorkspaceRoot: "/workspace"})
	appendMemoryEvent(t, store, authority, ids, "session-running", 0, baseTime.Add(2*time.Second), domain.SessionCreated{WorkspaceRoot: "/workspace"})
	appendMemoryEvent(t, store, authority, ids, "session-running", 1, baseTime.Add(3*time.Second), domain.TurnStarted{TurnID: "turn-1", Input: "hello"})
	appendMemoryEvent(t, store, authority, ids, "session-closed", 0, baseTime.Add(4*time.Second), domain.SessionCreated{WorkspaceRoot: "/workspace"})
	appendMemoryEvent(t, store, authority, ids, "session-closed", 1, baseTime.Add(5*time.Second), domain.SessionClosed{})
	appendMemoryEvent(t, store, authority, ids, "session-deleted", 0, baseTime.Add(6*time.Second), domain.SessionCreated{WorkspaceRoot: "/workspace"})
	appendMemoryEvent(t, store, authority, ids, "session-deleted", 1, baseTime.Add(7*time.Second), domain.SessionDeleted{})
	appendMemoryEvent(t, store, authority, ids, "session-foreign", 0, baseTime.Add(8*time.Second), domain.SessionCreated{WorkspaceRoot: "/foreign"})

	first, err := store.ListSessionHeads(context.Background(), application.ListSessionHeadsRequest{
		WorkspaceRoot: "/workspace", Limit: 2,
	})
	if err != nil {
		t.Fatalf("ListSessionHeads(first) error = %v", err)
	}
	wantFirst := []application.SessionHead{
		{SessionID: "session-closed", WorkspaceRoot: "/workspace", Status: application.SessionHeadStatusClosed, UpdatedAt: baseTime.Add(5 * time.Second)},
		{SessionID: "session-running", WorkspaceRoot: "/workspace", Status: application.SessionHeadStatusRunning, UpdatedAt: baseTime.Add(3 * time.Second)},
	}
	if !reflect.DeepEqual(first.Sessions, wantFirst) || first.NextCursor == "" {
		t.Fatalf("ListSessionHeads(first) = %#v, want sessions %#v and cursor", first, wantFirst)
	}

	second, err := store.ListSessionHeads(context.Background(), application.ListSessionHeadsRequest{
		WorkspaceRoot: "/workspace", Cursor: first.NextCursor, Limit: 2,
	})
	if err != nil {
		t.Fatalf("ListSessionHeads(second) error = %v", err)
	}
	wantSecond := []application.SessionHead{{
		SessionID: "session-idle", WorkspaceRoot: "/workspace", Status: application.SessionHeadStatusIdle, UpdatedAt: baseTime.Add(time.Second),
	}}
	if !reflect.DeepEqual(second.Sessions, wantSecond) || second.NextCursor != "" {
		t.Fatalf("ListSessionHeads(second) = %#v, want %#v without cursor", second, wantSecond)
	}

	for _, cursor := range []string{
		"%%%",
		strings.Repeat("a", 513),
		base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"p":1,"s":"session-idle","extra":true}`)),
	} {
		_, err := store.ListSessionHeads(context.Background(), application.ListSessionHeadsRequest{
			WorkspaceRoot: "/workspace", Cursor: cursor, Limit: 2,
		})
		if !application.IsStoreCode(err, application.StoreCodeInvalidRead) {
			t.Fatalf("ListSessionHeads(cursor %q) error = %v, want %q", cursor, err, application.StoreCodeInvalidRead)
		}
	}
}

func appendMemoryEvent(
	t *testing.T,
	store *EventStore,
	authority application.WriterAuthority,
	ids application.IDGenerator,
	sessionID domain.SessionID,
	version uint64,
	when time.Time,
	event domain.Event,
) {
	t.Helper()
	commandID, err := ids.NewCommandID()
	if err != nil {
		t.Fatal(err)
	}
	intent, err := application.BuildAppendIntent(
		testkit.FixedClock{Time: when},
		ids,
		authority,
		sessionID,
		version,
		commandID,
		nil,
		[]domain.UncommittedEvent{{Event: event}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(context.Background(), intent.Request); err != nil {
		t.Fatal(err)
	}
}

func TestEventStoreContract(t *testing.T) {
	eventstoretest.Run(t, func(t *testing.T) eventstoretest.Harness {
		store := mustV2Store(t, application.WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1})
		return eventstoretest.Harness{
			Store: store, RotateAuthority: store.SetAuthority,
			FailNext:       func(point eventstoretest.FaultPoint, err error) { store.FailNext(FaultPoint(point), err) },
			CorruptReceipt: store.CorruptReceipt,
			SetCommitHook: func(point eventstoretest.CommitHookPoint, hook func()) {
				store.SetCommitHook(CommitHookPoint(point), hook)
			},
		}
	})
}

func mustV2Store(t *testing.T, authority application.WriterAuthority) *EventStore {
	t.Helper()
	store, err := NewEventStore(authority)
	if err != nil {
		t.Fatalf("NewEventStore() error = %v", err)
	}
	return store
}
