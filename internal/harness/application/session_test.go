package application_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/memory"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

func TestCanonicalWorkspaceRootRequiresAbsoluteLexicallyCleanPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "clean", input: "/workspace", want: "/workspace"},
		{name: "dot segment", input: "/workspace/./nested/..", want: "/workspace"},
		{name: "root", input: "/", want: "/"},
		{name: "blank", input: ""},
		{name: "relative", input: "workspace"},
		{name: "padded", input: "/workspace "},
		{name: "invalid UTF-8", input: string([]byte{'/', 0xff})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := application.CanonicalWorkspaceRoot(test.input)
			if test.want == "" {
				if err == nil || got != "" {
					t.Fatalf("CanonicalWorkspaceRoot(%q) = (%q, %v), want rejection", test.input, got, err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("CanonicalWorkspaceRoot(%q) = (%q, %v), want (%q, nil)", test.input, got, err, test.want)
			}
		})
	}
}

func TestCreateSessionPersistsCanonicalWorkspaceRoot(t *testing.T) {
	t.Parallel()

	service, store := newSessionServiceForTest(t, testkit.NewSequenceIDs())
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace/."})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	loaded, err := service.LoadSession(context.Background(), created.SessionID)
	if err != nil {
		t.Fatalf("LoadSession() error = %v", err)
	}
	if loaded.WorkspaceRoot != "/workspace" {
		t.Fatalf("LoadSession().WorkspaceRoot = %q, want /workspace", loaded.WorkspaceRoot)
	}
	records, err := application.ReadWholeStreamPinned(context.Background(), store, created.SessionID, 256)
	if err != nil {
		t.Fatalf("ReadWholeStreamPinned() error = %v", err)
	}
	event, ok := records[0].Event.(domain.SessionCreated)
	if !ok || event.WorkspaceRoot != "/workspace" {
		t.Fatalf("session.created = %#v, want canonical root", records[0].Event)
	}
}

func TestCreateLoadCloseSession(t *testing.T) {
	service, store := newSessionServiceForTest(t, testkit.NewSequenceIDs())
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	loaded, err := service.LoadSession(context.Background(), created.SessionID)
	if err != nil {
		t.Fatalf("LoadSession() error = %v", err)
	}
	if loaded.Status != domain.SessionStatusActive || loaded.Version != 1 {
		t.Fatalf("loaded state = %#v", loaded)
	}
	closed, err := service.CloseSession(context.Background(), application.CloseSessionRequest{SessionID: created.SessionID})
	if err != nil {
		t.Fatalf("CloseSession() error = %v", err)
	}
	if closed.Session.Status != domain.SessionStatusClosed || closed.Session.Version != 2 {
		t.Fatalf("closed state = %#v", closed.Session)
	}
	records, err := application.ReadWholeStreamPinned(context.Background(), store, created.SessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sessionEventTypes(records), []string{"session.created", "session.closed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	created.Records[0].Event = domain.SessionCreated{WorkspaceRoot: "/mutated"}
	closed.Records[0].Event = domain.SessionCreated{WorkspaceRoot: "/mutated"}
	if loaded.ActiveTurn != nil {
		t.Fatal("new session unexpectedly has an active turn")
	}
	fresh, err := service.LoadSession(context.Background(), created.SessionID)
	if err != nil || fresh.WorkspaceRoot != "/workspace" || fresh.ActiveTurn != nil {
		t.Fatalf("fresh LoadSession() = (%#v, %v), want defensive state", fresh, err)
	}
}

func TestCreateSessionRejectsDuplicateAsConflict(t *testing.T) {
	service, _ := newSessionServiceForTest(t, &sessionIDs{sessionID: "session-duplicate", commandID: "command-duplicate"})
	if _, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/first"}); err != nil {
		t.Fatal(err)
	}
	_, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/second"})
	assertApplicationError(t, err, application.CategoryConflict, "version_conflict")
}

func TestLoadSessionMapsMissingCorruptAndStoreFailure(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		service, _ := newSessionServiceForTest(t, testkit.NewSequenceIDs())
		_, err := service.LoadSession(context.Background(), "session-missing")
		assertApplicationError(t, err, application.CategoryValidation, "session_not_found")
	})

	t.Run("deleted", func(t *testing.T) {
		service, store := newSessionServiceForTest(t, testkit.NewSequenceIDs())
		seedV2Event(t, store, "session-deleted", 0, "command-create", domain.SessionCreated{WorkspaceRoot: "/workspace"})
		seedV2Event(t, store, "session-deleted", 1, "command-delete", domain.SessionDeleted{})

		_, err := service.LoadSession(context.Background(), "session-deleted")
		assertApplicationError(t, err, application.CategoryValidation, "session_not_found")

		records, readErr := application.ReadWholeStreamPinned(context.Background(), store, "session-deleted", 256)
		if readErr != nil || len(records) != 2 || records[1].Event.EventType() != domain.EventSessionDeleted {
			t.Fatalf("authoritative stream after deletion = (%#v, %v), want deletion evidence", records, readErr)
		}
	})

	t.Run("corrupt replay", func(t *testing.T) {
		store := &sessionStore{loadRecords: []domain.RecordedEvent{{
			SchemaVersion: 1, ID: "event-corrupt", CommandID: "command-corrupt", SessionID: "session-corrupt",
			Sequence: 2, OccurredAt: validSessionTime(), Event: domain.SessionCreated{WorkspaceRoot: "/workspace"},
		}}}
		service := newSessionServiceWithStore(t, store, testkit.NewSequenceIDs())
		_, err := service.LoadSession(context.Background(), "session-corrupt")
		assertApplicationError(t, err, application.CategoryInternal, "store_contract_violation")
	})

	t.Run("load failure", func(t *testing.T) {
		cause := errors.New("load unavailable")
		store := &sessionStore{loadErr: fmt.Errorf("adapter: %w", cause)}
		service := newSessionServiceWithStore(t, store, testkit.NewSequenceIDs())
		_, err := service.LoadSession(context.Background(), "session-load")
		assertApplicationError(t, err, application.CategoryPersistence, "load_failed")
		if !errors.Is(err, cause) {
			t.Fatalf("LoadSession() error = %v, want source cause", err)
		}
	})

	t.Run("dependency canceled is still persistence", func(t *testing.T) {
		store := &sessionStore{loadErr: fmt.Errorf("adapter: %w", context.Canceled)}
		service := newSessionServiceWithStore(t, store, testkit.NewSequenceIDs())
		_, err := service.LoadSession(context.Background(), "session-load")
		assertApplicationError(t, err, application.CategoryPersistence, "load_failed")
	})

	t.Run("caller canceled at load boundary", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		store := &sessionStore{
			onLoad: cancel,
			loadRecords: []domain.RecordedEvent{{
				SchemaVersion: 1, ID: "event-load-canceled", CommandID: "command-load-canceled", SessionID: "session-load-canceled",
				Sequence: 1, OccurredAt: validSessionTime(), Event: domain.SessionCreated{WorkspaceRoot: "/workspace"},
			}},
		}
		service := newSessionServiceWithStore(t, store, testkit.NewSequenceIDs())
		_, err := service.LoadSession(ctx, "session-load-canceled")
		assertApplicationError(t, err, application.CategoryCanceled, "canceled")
	})

	t.Run("caller canceled after successful page", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		store := &sessionStore{
			onLoad:           cancel,
			ignoreContextErr: true,
			loadRecords:      []domain.RecordedEvent{{SchemaVersion: 1, ID: "event-load-canceled-page", CommandID: "command-load-canceled-page", SessionID: "session-load-canceled-page", Sequence: 1, OccurredAt: validSessionTime(), Event: domain.SessionCreated{WorkspaceRoot: "/workspace"}}},
		}
		service := newSessionServiceWithStore(t, store, testkit.NewSequenceIDs())
		_, err := service.LoadSession(ctx, "session-load-canceled-page")
		assertApplicationError(t, err, application.CategoryCanceled, "canceled")
	})
}

func TestDeleteSessionUsesCanonicalAppendAndNonEnumeratingBoundary(t *testing.T) {
	t.Run("active idle resolves unknown outcome and becomes hidden", func(t *testing.T) {
		service, store := newSessionServiceForTest(t, testkit.NewSequenceIDs())
		seedV2Event(t, store, "session-delete", 0, "command-create", domain.SessionCreated{WorkspaceRoot: "/workspace"})
		store.FailNext(memory.FaultAfterCommitBeforeAck, errors.New("ack lost"))

		err := service.DeleteSession(context.Background(), application.DeleteSessionRequest{
			SessionID: "session-delete", WorkspaceRoot: "/workspace/.",
		})
		if err != nil {
			t.Fatalf("DeleteSession() error = %v", err)
		}
		records, readErr := application.ReadWholeStreamPinned(context.Background(), store, "session-delete", 256)
		if readErr != nil || len(records) != 2 || records[1].Event.EventType() != domain.EventSessionDeleted {
			t.Fatalf("stream after DeleteSession() = (%#v, %v)", records, readErr)
		}
		_, loadErr := service.LoadSession(context.Background(), "session-delete")
		assertApplicationError(t, loadErr, application.CategoryValidation, "session_not_found")

		err = service.DeleteSession(context.Background(), application.DeleteSessionRequest{
			SessionID: "session-delete", WorkspaceRoot: "/workspace",
		})
		assertApplicationError(t, err, application.CategoryValidation, "session_not_found")
		records, readErr = application.ReadWholeStreamPinned(context.Background(), store, "session-delete", 256)
		if readErr != nil || len(records) != 2 {
			t.Fatalf("second DeleteSession() mutated stream = (%#v, %v)", records, readErr)
		}
	})

	t.Run("closed idle deletes", func(t *testing.T) {
		service, store := newSessionServiceForTest(t, testkit.NewSequenceIDs())
		seedV2Event(t, store, "session-closed-delete", 0, "command-create", domain.SessionCreated{WorkspaceRoot: "/workspace"})
		seedV2Event(t, store, "session-closed-delete", 1, "command-close", domain.SessionClosed{})
		if err := service.DeleteSession(context.Background(), application.DeleteSessionRequest{
			SessionID: "session-closed-delete", WorkspaceRoot: "/workspace",
		}); err != nil {
			t.Fatalf("DeleteSession(closed) error = %v", err)
		}
		records, err := application.ReadWholeStreamPinned(context.Background(), store, "session-closed-delete", 256)
		if err != nil || len(records) != 3 || records[2].Event.EventType() != domain.EventSessionDeleted {
			t.Fatalf("closed delete stream = (%#v, %v)", records, err)
		}
	})

	t.Run("absent foreign and deleted have the same public result", func(t *testing.T) {
		service, store := newSessionServiceForTest(t, testkit.NewSequenceIDs())
		seedV2Event(t, store, "session-foreign", 0, "command-create", domain.SessionCreated{WorkspaceRoot: "/workspace"})

		for _, request := range []application.DeleteSessionRequest{
			{SessionID: "session-missing", WorkspaceRoot: "/workspace"},
			{SessionID: "session-foreign", WorkspaceRoot: "/foreign"},
		} {
			err := service.DeleteSession(context.Background(), request)
			assertApplicationError(t, err, application.CategoryValidation, "session_not_found")
		}
		records, err := application.ReadWholeStreamPinned(context.Background(), store, "session-foreign", 256)
		if err != nil || len(records) != 1 {
			t.Fatalf("foreign delete mutated stream = (%#v, %v)", records, err)
		}
	})

	t.Run("running is rejected without append", func(t *testing.T) {
		service, store := newSessionServiceForTest(t, testkit.NewSequenceIDs())
		seedV2Event(t, store, "session-running-delete", 0, "command-create", domain.SessionCreated{WorkspaceRoot: "/workspace"})
		seedV2Event(t, store, "session-running-delete", 1, "command-turn", domain.TurnStarted{TurnID: "turn-1", Input: "hello"})

		err := service.DeleteSession(context.Background(), application.DeleteSessionRequest{
			SessionID: "session-running-delete", WorkspaceRoot: "/workspace",
		})
		assertApplicationError(t, err, application.CategoryValidation, "domain_rejected")
		records, readErr := application.ReadWholeStreamPinned(context.Background(), store, "session-running-delete", 256)
		if readErr != nil || len(records) != 2 {
			t.Fatalf("running delete mutated stream = (%#v, %v)", records, readErr)
		}
	})
}

func TestResumeSessionRequiresSameWorkspaceAndActiveIdleState(t *testing.T) {
	t.Parallel()

	service, store := newSessionServiceForTest(t, testkit.NewSequenceIDs())
	seedV2Event(t, store, "session-idle", 0, "command-idle-create", domain.SessionCreated{WorkspaceRoot: "/workspace"})
	seedV2Event(t, store, "session-closed", 0, "command-closed-create", domain.SessionCreated{WorkspaceRoot: "/workspace"})
	seedV2Event(t, store, "session-closed", 1, "command-close", domain.SessionClosed{})
	seedV2Event(t, store, "session-running", 0, "command-running-create", domain.SessionCreated{WorkspaceRoot: "/workspace"})
	seedV2Event(t, store, "session-running", 1, "command-turn", domain.TurnStarted{TurnID: "turn-1", Input: "hello"})
	seedV2Event(t, store, "session-deleted", 0, "command-deleted-create", domain.SessionCreated{WorkspaceRoot: "/workspace"})
	seedV2Event(t, store, "session-deleted", 1, "command-delete", domain.SessionDeleted{})

	resumed, err := service.ResumeSession(context.Background(), application.ResumeSessionRequest{
		SessionID: "session-idle", WorkspaceRoot: "/workspace/.",
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	if resumed.ID != "session-idle" || resumed.Status != domain.SessionStatusActive || resumed.ActiveTurn != nil || resumed.WorkspaceRoot != "/workspace" {
		t.Fatalf("ResumeSession() = %#v", resumed)
	}
	records, err := application.ReadWholeStreamPinned(context.Background(), store, "session-idle", 256)
	if err != nil || len(records) != 1 {
		t.Fatalf("ResumeSession() mutated stream = (%#v, %v)", records, err)
	}

	tests := []struct {
		name      string
		sessionID domain.SessionID
		workspace string
		code      string
	}{
		{name: "missing", sessionID: "session-missing", workspace: "/workspace", code: "session_not_found"},
		{name: "foreign", sessionID: "session-idle", workspace: "/foreign", code: "session_not_found"},
		{name: "closed", sessionID: "session-closed", workspace: "/workspace", code: "domain_rejected"},
		{name: "running", sessionID: "session-running", workspace: "/workspace", code: "domain_rejected"},
		{name: "deleted", sessionID: "session-deleted", workspace: "/workspace", code: "session_not_found"},
		{name: "relative workspace", sessionID: "session-idle", workspace: "workspace", code: "invalid_request"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.ResumeSession(context.Background(), application.ResumeSessionRequest{
				SessionID: test.sessionID, WorkspaceRoot: test.workspace,
			})
			assertApplicationError(t, err, application.CategoryValidation, test.code)
		})
	}
}

func TestCloseSessionRejectsMissingAndRunningSession(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		service, _ := newSessionServiceForTest(t, testkit.NewSequenceIDs())
		_, err := service.CloseSession(context.Background(), application.CloseSessionRequest{SessionID: "session-missing"})
		assertApplicationError(t, err, application.CategoryValidation, "session_not_found")
	})

	t.Run("running turn", func(t *testing.T) {
		ids := &sessionIDs{sessionID: "session-running", commandID: "command-running-create"}
		service, store := newSessionServiceForTest(t, ids)
		created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
		if err != nil {
			t.Fatal(err)
		}
		seedV2Event(t, store, created.SessionID, 1, "command-running", domain.TurnStarted{TurnID: "turn-running", Input: "hello"})
		_, err = service.CloseSession(context.Background(), application.CloseSessionRequest{SessionID: created.SessionID})
		assertApplicationError(t, err, application.CategoryValidation, "domain_rejected")
		if ids.calls != 4 {
			t.Fatalf("ID calls = %d, want CreateSession identity plus append/event IDs", ids.calls)
		}
	})
}

func TestSessionUseCasesMapAppendFailuresAndCancellation(t *testing.T) {
	t.Run("append failure", func(t *testing.T) {
		cause := errors.New("append unavailable")
		store := &sessionStore{appendErr: fmt.Errorf("adapter: %w", cause)}
		service := newSessionServiceWithStore(t, store, testkit.NewSequenceIDs())
		_, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
		assertApplicationError(t, err, application.CategoryPersistence, "append_failed")
		if !errors.Is(err, cause) {
			t.Fatalf("CreateSession() error = %v, want source cause", err)
		}
	})

	t.Run("caller canceled before append", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		ids := &sessionIDs{sessionID: "session-canceled", commandID: "command-canceled", onCommand: cancel}
		store := &sessionStore{}
		service := newSessionServiceWithStore(t, store, ids)
		_, err := service.CreateSession(ctx, application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
		assertApplicationError(t, err, application.CategoryCanceled, "canceled")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("CreateSession() error = %v, want caller cancellation cause", err)
		}
		if store.appendCalls != 0 {
			t.Fatalf("Append() calls = %d, want 0", store.appendCalls)
		}
	})

	t.Run("dependency canceled is still persistence", func(t *testing.T) {
		store := &sessionStore{appendErr: fmt.Errorf("adapter: %w", context.Canceled)}
		service := newSessionServiceWithStore(t, store, testkit.NewSequenceIDs())
		_, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
		assertApplicationError(t, err, application.CategoryPersistence, "append_failed")
	})

	t.Run("caller cancellation wins ID source race and preserves both causes", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		sourceCause := errors.New("command ID source failed")
		ids := &sessionIDs{sessionID: "session-id-race", commandErr: sourceCause, onCommand: cancel}
		service := newSessionServiceWithStore(t, &sessionStore{}, ids)
		_, err := service.CreateSession(ctx, application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
		assertApplicationError(t, err, application.CategoryCanceled, "canceled")
		if !errors.Is(err, context.Canceled) || !errors.Is(err, sourceCause) {
			t.Fatalf("CreateSession() error = %v, want caller and source causes", err)
		}
	})
}

func TestCreateSessionMapsEveryGeneratedIDFailure(t *testing.T) {
	sourceCause := errors.New("ID source unavailable")
	tests := []struct {
		name string
		ids  *sessionIDs
		code string
	}{
		{name: "session source", ids: &sessionIDs{sessionErr: sourceCause}, code: "id_generation_failed"},
		{name: "command source", ids: &sessionIDs{sessionID: "session-1", commandErr: sourceCause}, code: "id_generation_failed"},
		{name: "invalid session", ids: &sessionIDs{sessionID: " session-1", commandID: "command-1"}, code: "id_generator_contract_violation"},
		{name: "invalid command", ids: &sessionIDs{sessionID: "session-1", commandID: " command-1"}, code: "id_generator_contract_violation"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &sessionStore{}
			service := newSessionServiceWithStore(t, store, test.ids)
			_, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
			assertApplicationError(t, err, application.CategoryInternal, test.code)
			if test.code == "id_generation_failed" && !errors.Is(err, sourceCause) {
				t.Fatalf("CreateSession() error = %v, want source cause", err)
			}
			if store.appendCalls != 0 {
				t.Fatalf("Append() calls = %d, want 0", store.appendCalls)
			}
		})
	}
}

func TestCloseSessionMapsEveryGeneratedCommandIDFailure(t *testing.T) {
	tests := []struct {
		name     string
		ids      *sessionIDs
		category application.ErrorCategory
		code     string
	}{
		{name: "source", ids: &sessionIDs{commandErr: errors.New("command ID unavailable")}, category: application.CategoryInternal, code: "id_generation_failed"},
		{name: "invalid", ids: &sessionIDs{commandID: " command-1"}, category: application.CategoryInternal, code: "id_generator_contract_violation"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, err := memory.NewEventStore(v2Authority)
			if err != nil {
				t.Fatal(err)
			}
			seedV2Event(t, store, "session-close-id", 0, "command-seed", domain.SessionCreated{WorkspaceRoot: "/workspace"})
			service := newSessionServiceWithStore(t, store, test.ids)
			_, err = service.CloseSession(context.Background(), application.CloseSessionRequest{SessionID: "session-close-id"})
			assertApplicationError(t, err, test.category, test.code)
			records, loadErr := application.ReadWholeStreamPinned(context.Background(), store, "session-close-id", 256)
			if loadErr != nil || len(records) != 1 {
				t.Fatalf("stream after rejected close = (%#v, %v), want unchanged version 1", records, loadErr)
			}
		})
	}
}

func TestSessionUseCasesValidateInputsBeforePorts(t *testing.T) {
	store := &sessionStore{}
	ids := &sessionIDs{}
	service := newSessionServiceWithStore(t, store, ids)

	if _, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "  "}); err == nil {
		t.Fatal("CreateSession(blank) error = nil")
	}
	if _, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: string([]byte{0xff})}); err == nil {
		t.Fatal("CreateSession(invalid UTF-8) error = nil")
	}
	if _, err := service.LoadSession(context.Background(), " session-1"); err == nil {
		t.Fatal("LoadSession(invalid ID) error = nil")
	}
	if _, err := service.CloseSession(context.Background(), application.CloseSessionRequest{SessionID: " session-1"}); err == nil {
		t.Fatal("CloseSession(invalid ID) error = nil")
	}
	if store.loadCalls != 0 || store.appendCalls != 0 || ids.calls != 0 {
		t.Fatalf("port calls = load %d append %d IDs %d, want zero", store.loadCalls, store.appendCalls, ids.calls)
	}
}

func TestCreateSessionRejectsMalformedAppendReturn(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*application.CommitReceipt)
	}{
		{name: "wrong append ID", mutate: func(receipt *application.CommitReceipt) { receipt.AppendID = "append-other" }},
		{name: "zero commit position", mutate: func(receipt *application.CommitReceipt) { receipt.CommitPosition = 0 }},
		{name: "wrong first sequence", mutate: func(receipt *application.CommitReceipt) { receipt.FirstSequence = 2 }},
		{name: "wrong last sequence", mutate: func(receipt *application.CommitReceipt) { receipt.LastSequence = 2 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := application.CommitReceipt{AppendID: "append-1", CommitPosition: 1, FirstSequence: 1, LastSequence: 1}
			test.mutate(&receipt)
			store := &sessionStore{appendReceipt: &receipt}
			service := newSessionServiceWithStore(t, store, &sessionIDs{sessionID: "session-1", commandID: "command-1"})
			_, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
			assertApplicationError(t, err, application.CategoryInternal, "store_contract_violation")
		})
	}

}

func TestNewServiceValidatesDependenciesAndConfiguration(t *testing.T) {
	validStore := &sessionStore{}
	validIDs := testkit.NewSequenceIDs()
	validRunner := sessionRunnerForTest(t)
	var typedNilStore *sessionStore
	var typedNilIDs *sessionIDs
	var typedNilRunner *engine.TurnRunner
	var typedNilAuthority *liveAuthoritySource

	tests := []struct {
		name      string
		store     application.EventStore
		ids       application.IDGenerator
		runner    *engine.TurnRunner
		authority application.AuthoritySource
		config    application.Config
	}{
		{name: "nil store", ids: validIDs, runner: validRunner, authority: v2Authority, config: application.DefaultConfig()},
		{name: "typed nil store", store: typedNilStore, ids: validIDs, runner: validRunner, authority: v2Authority, config: application.DefaultConfig()},
		{name: "nil IDs", store: validStore, runner: validRunner, authority: v2Authority, config: application.DefaultConfig()},
		{name: "typed nil IDs", store: validStore, ids: typedNilIDs, runner: validRunner, authority: v2Authority, config: application.DefaultConfig()},
		{name: "nil runner", store: validStore, ids: validIDs, authority: v2Authority, config: application.DefaultConfig()},
		{name: "typed nil runner", store: validStore, ids: validIDs, runner: typedNilRunner, authority: v2Authority, config: application.DefaultConfig()},
		{name: "nil authority", store: validStore, ids: validIDs, runner: validRunner, config: application.DefaultConfig()},
		{name: "typed nil authority", store: validStore, ids: validIDs, runner: validRunner, authority: typedNilAuthority, config: application.DefaultConfig()},
		{name: "output limit", store: validStore, ids: validIDs, runner: validRunner, authority: v2Authority, config: application.Config{TerminalCommitTimeout: time.Second}},
		{name: "terminal timeout", store: validStore, ids: validIDs, runner: validRunner, authority: v2Authority, config: application.Config{MaxAssistantBytes: 1}},
		{name: "invalid request identity", store: validStore, ids: validIDs, runner: validRunner, authority: v2Authority, config: func() application.Config {
			config := application.DefaultConfig()
			identity := validTurnRequestIdentity()
			identity.AdapterFamily = "OpenAI"
			config.RequestIdentity = &identity
			return config
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, err := application.NewService(test.store, test.ids, testkit.FixedClock{Time: validSessionTime()}, test.runner, test.authority, test.config)
			if service != nil {
				t.Fatalf("NewService() service = %#v, want nil", service)
			}
			assertApplicationError(t, err, application.CategoryValidation, "invalid_configuration")
		})
	}

	service, err := application.NewService(validStore, validIDs, testkit.FixedClock{Time: validSessionTime()}, validRunner, v2Authority, application.DefaultConfig())
	if err != nil || service == nil {
		t.Fatalf("NewService(valid) = (%#v, %v)", service, err)
	}
	identity := validTurnRequestIdentity()
	withIdentity := application.DefaultConfig()
	withIdentity.RequestIdentity = &identity
	service, err = application.NewService(validStore, validIDs, testkit.FixedClock{Time: validSessionTime()}, validRunner, v2Authority, withIdentity)
	if err != nil || service == nil {
		t.Fatalf("NewService(valid identity) = (%#v, %v)", service, err)
	}
}

func validTurnRequestIdentity() engine.RequestIdentity {
	return engine.RequestIdentity{
		AdapterFamily: "openai_compat",
		ModelID:       "test-model",
		EndpointID:    "api.example.com",
		Profile: engine.CapabilityProfile{
			NativeTools:      engine.CapabilityUnsupported,
			Images:           engine.CapabilityUnsupported,
			StructuredOutput: engine.CapabilityUnsupported,
			ReasoningFields:  engine.CapabilityUnsupported,
			PromptCache:      engine.CapabilityUnsupported,
		},
		IncludeUsage: true,
	}
}

func newSessionServiceForTest(t *testing.T, ids application.IDGenerator) (*application.Service, *memory.EventStore) {
	t.Helper()
	store, err := memory.NewEventStore(v2Authority)
	if err != nil {
		t.Fatal(err)
	}
	return newSessionServiceWithStore(t, store, ids), store
}

func newSessionServiceWithStore(t *testing.T, store application.EventStore, ids application.IDGenerator) *application.Service {
	t.Helper()
	service, err := application.NewService(store, ids, testkit.FixedClock{Time: validSessionTime()}, sessionRunnerForTest(t), v2Authority, application.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// TestServicePicksUpRotatedFencingToken is the P0-3 regression: NewService
// used to snapshot WriterAuthority, so an expired-takeover token rotation
// fenced every subsequent append. Passing the store as AuthoritySource
// makes the next CreateSession observe the live token.
func TestServicePicksUpRotatedFencingToken(t *testing.T) {
	store, err := memory.NewEventStore(v2Authority)
	if err != nil {
		t.Fatal(err)
	}
	service, err := application.NewService(store, testkit.NewSequenceIDs(), testkit.FixedClock{Time: validSessionTime()}, sessionRunnerForTest(t), store, application.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"}); err != nil {
		t.Fatalf("CreateSession before rotation: %v", err)
	}

	rotated := application.WriterAuthority{RuntimeID: v2Authority.RuntimeID, FencingToken: v2Authority.FencingToken + 1}
	store.SetAuthority(rotated)
	if _, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace-after"}); err != nil {
		t.Fatalf("CreateSession after fencing-token rotation: %v", err)
	}
}

// TestServiceSnapshotAuthorityIsFencedAfterRotation records the failure
// mode P0-3 fixed: a static WriterAuthority captured at construction does
// not see a later token rotation.
func TestServiceSnapshotAuthorityIsFencedAfterRotation(t *testing.T) {
	store, err := memory.NewEventStore(v2Authority)
	if err != nil {
		t.Fatal(err)
	}
	service, err := application.NewService(store, testkit.NewSequenceIDs(), testkit.FixedClock{Time: validSessionTime()}, sessionRunnerForTest(t), v2Authority, application.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"}); err != nil {
		t.Fatalf("CreateSession before rotation: %v", err)
	}

	store.SetAuthority(application.WriterAuthority{RuntimeID: v2Authority.RuntimeID, FencingToken: v2Authority.FencingToken + 1})
	_, err = service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace-after"})
	if err == nil {
		t.Fatal("CreateSession with a captured authority snapshot succeeded after rotation")
	}
}

type liveAuthoritySource struct{}

func (*liveAuthoritySource) CurrentAuthority() application.WriterAuthority { return v2Authority }

func sessionRunnerForTest(t *testing.T) *engine.TurnRunner {
	t.Helper()
	model, err := testkit.NewScriptedModel(engine.ModelRequest{}, testkit.ScriptedModelConfig{})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := engine.NewTurnRunner(model)
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func assertApplicationError(t *testing.T, err error, category application.ErrorCategory, code string) {
	t.Helper()
	var appErr *application.Error
	if !errors.As(err, &appErr) || appErr == nil {
		t.Fatalf("error = %v, want *application.Error", err)
	}
	if appErr.Category != category || appErr.Code != code || appErr.TerminalCommitted {
		t.Fatalf("application error = %#v, want category %q code %q terminal false", appErr, category, code)
	}
}

func sessionEventTypes(records []domain.RecordedEvent) []string {
	types := make([]string, len(records))
	for index, record := range records {
		types[index] = record.Event.EventType()
	}
	return types
}

func validSessionTime() time.Time {
	return time.Date(2026, 8, 12, 3, 4, 5, 6, time.UTC)
}

var v2Authority = application.WriterAuthority{RuntimeID: "runtime-test", FencingToken: 1}

func seedV2Event(t *testing.T, store application.EventStore, sessionID domain.SessionID, version uint64, commandID domain.CommandID, event domain.Event) {
	t.Helper()
	ids := testkit.NewSequenceIDs()
	for offset := uint64(0); offset <= version; offset++ {
		_, _ = ids.NewAppendID()
		_, _ = ids.NewEventID()
	}
	intent, err := application.BuildAppendIntent(testkit.FixedClock{Time: validSessionTime()}, ids, v2Authority, sessionID, version, commandID, nil, []domain.UncommittedEvent{{Event: event}})
	if err != nil {
		t.Fatal(err)
	}
	intent.Request.AppendID = domain.AppendID(fmt.Sprintf("seed-append-%s-%d", sessionID, version))
	intent.Request.Events[0].ID = domain.EventID(fmt.Sprintf("seed-event-%s-%d", sessionID, version))
	if _, err := store.Append(context.Background(), intent.Request); err != nil {
		t.Fatal(err)
	}
}

type sessionStore struct {
	loadRecords      []domain.RecordedEvent
	loadErr          error
	onLoad           func()
	ignoreContextErr bool
	appendReceipt    *application.CommitReceipt
	appendErr        error
	loadCalls        int
	appendCalls      int
}

func (store *sessionStore) ReadStream(ctx context.Context, request application.ReadStreamRequest) (application.StreamPage, error) {
	store.loadCalls++
	if store.onLoad != nil {
		store.onLoad()
	}
	if err := ctx.Err(); err != nil && !store.ignoreContextErr {
		return application.StreamPage{}, err
	}
	records, _ := domain.CloneRecordedEvents(store.loadRecords)
	if store.loadErr != nil {
		return application.StreamPage{}, store.loadErr
	}
	if request.AfterSequence > uint64(len(records)) {
		return application.StreamPage{}, errors.New("invalid cursor")
	}
	head := uint64(len(records))
	if request.HeadVersion != nil {
		head = *request.HeadVersion
	}
	if head > uint64(len(records)) {
		return application.StreamPage{}, errors.New("invalid head")
	}
	start := request.AfterSequence
	end := start + uint64(request.Limit)
	if end > head {
		end = head
	}
	page := application.StreamPage{HeadVersion: head, NextAfterSequence: start, End: start == head}
	if start < end {
		page.Records = records[start:end]
		page.NextAfterSequence = end
		page.End = end == head
	}
	return page, nil
}

func (store *sessionStore) Append(_ context.Context, request application.AppendRequest) (application.CommitReceipt, error) {
	store.appendCalls++
	if store.appendErr != nil {
		return application.CommitReceipt{}, store.appendErr
	}
	if store.appendReceipt != nil {
		return *store.appendReceipt, nil
	}
	return application.CommitReceipt{AppendID: request.AppendID, CommitPosition: 1, FirstSequence: request.ExpectedVersion + 1, LastSequence: request.ExpectedVersion + uint64(len(request.Events))}, nil
}

func (*sessionStore) ResolveAppend(context.Context, application.ResolveAppendRequest) (application.AppendResolution, error) {
	return application.AppendResolution{Kind: application.AppendResolutionNotFound}, nil
}
func (*sessionStore) FindCommandRequest(context.Context, application.FindCommandRequestRequest) (application.CommandRequestLookup, error) {
	return application.CommandRequestLookup{Kind: application.CommandRequestLookupNotFound}, nil
}

type sessionIDs struct {
	sessionID  domain.SessionID
	sessionErr error
	commandID  domain.CommandID
	commandErr error
	onCommand  func()
	calls      int
	appends    uint64
	events     uint64
}

func (ids *sessionIDs) NewSessionID() (domain.SessionID, error) {
	ids.calls++
	return ids.sessionID, ids.sessionErr
}
func (ids *sessionIDs) NewTurnID() (domain.TurnID, error) { ids.calls++; return "turn-1", nil }
func (ids *sessionIDs) NewItemID() (domain.ItemID, error) { ids.calls++; return "item-1", nil }
func (ids *sessionIDs) NewCommandID() (domain.CommandID, error) {
	ids.calls++
	if ids.onCommand != nil {
		ids.onCommand()
	}
	return ids.commandID, ids.commandErr
}
func (ids *sessionIDs) NewAppendID() (domain.AppendID, error) {
	ids.calls++
	ids.appends++
	return domain.AppendID(fmt.Sprintf("append-%d", ids.appends)), nil
}
func (ids *sessionIDs) NewEventID() (domain.EventID, error) {
	ids.calls++
	ids.events++
	return domain.EventID(fmt.Sprintf("event-%d", ids.events)), nil
}
func (ids *sessionIDs) NewApprovalID() (domain.ApprovalID, error) {
	ids.calls++
	return "approval-1", nil
}
