package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

type testEventStore struct{}

func (testEventStore) Load(context.Context, domain.SessionID) ([]domain.RecordedEvent, error) {
	return nil, nil
}

func (testEventStore) Append(context.Context, AppendRequest) ([]domain.RecordedEvent, error) {
	return nil, nil
}

type testClock struct{}

func (testClock) Now() time.Time { return time.Time{} }

type testIDs struct{}

func (testIDs) NewSessionID() (domain.SessionID, error) { return "", nil }
func (testIDs) NewTurnID() (domain.TurnID, error)       { return "", nil }
func (testIDs) NewItemID() (domain.ItemID, error)       { return "", nil }
func (testIDs) NewCommandID() (domain.CommandID, error) { return "", nil }
func (testIDs) NewEventID() (domain.EventID, error)     { return "", nil }

func TestApplicationPortsHaveConsumerOwnedSignatures(t *testing.T) {
	var _ EventStore = testEventStore{}
	var _ Clock = testClock{}
	var _ IDGenerator = testIDs{}

	request := AppendRequest{
		SessionID:       "session-1",
		ExpectedVersion: 7,
		CommandID:       "command-1",
		Events:          []domain.Event{domain.SessionCreated{}},
	}
	if request.SessionID != "session-1" || request.ExpectedVersion != 7 || request.CommandID != "command-1" || len(request.Events) != 1 {
		t.Fatalf("append request lost typed fields: %#v", request)
	}
}

func TestAppendContractDoesNotRewriteCommittedSuccessAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &committedThenCanceledStore{cancel: cancel}
	records, err := store.Append(ctx, AppendRequest{
		SessionID: "session-1", CommandID: "command-1",
		Events: []domain.Event{domain.SessionCreated{WorkspaceRoot: "/workspace"}},
	})
	if err != nil || len(records) != 1 || records[0].Sequence != 1 {
		t.Fatalf("Append() = (%#v, %v), want committed success", records, err)
	}
	if ctx.Err() != context.Canceled || !store.committed {
		t.Fatalf("post-commit state = canceled %v committed %t", ctx.Err(), store.committed)
	}
}

type committedThenCanceledStore struct {
	cancel    context.CancelFunc
	committed bool
}

func (*committedThenCanceledStore) Load(context.Context, domain.SessionID) ([]domain.RecordedEvent, error) {
	return nil, nil
}

func (store *committedThenCanceledStore) Append(_ context.Context, request AppendRequest) ([]domain.RecordedEvent, error) {
	store.committed = true
	record := domain.RecordedEvent{
		SchemaVersion: 1, ID: "event-1", CommandID: request.CommandID, SessionID: request.SessionID,
		Sequence: 1, OccurredAt: time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC), Event: request.Events[0],
	}
	store.cancel()
	return []domain.RecordedEvent{record}, nil
}

func TestApplicationErrorHasStableTextAndPreservesCause(t *testing.T) {
	cause := errors.New("provider secret: request body")
	err := &Error{
		Category:          CategoryModel,
		Code:              "model_stream_failed",
		TerminalCommitted: true,
		Cause:             cause,
	}

	if got, want := err.Error(), "model/model_stream_failed (terminal_committed=true)"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	if strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("Error() leaked cause text: %q", err.Error())
	}
	if !errors.Is(err, cause) {
		t.Fatal("errors.Is did not preserve the wrapped cause")
	}

	wrapped := fmt.Errorf("request failed: %w", err)
	if !errors.Is(wrapped, err) {
		t.Fatal("errors.Is did not preserve the application error in its chain")
	}
	var got *Error
	if !errors.As(wrapped, &got) || got != err {
		t.Fatalf("errors.As = %#v, want original application error", got)
	}
	if !IsCategory(wrapped, CategoryModel) {
		t.Fatal("IsCategory did not inspect the error chain")
	}
	if IsCategory(wrapped, CategoryPersistence) || IsCategory(nil, CategoryModel) {
		t.Fatal("IsCategory reported a category that is not present")
	}
}

func TestIsCategoryFindsEveryNestedApplicationError(t *testing.T) {
	inner := &Error{
		Category: CategoryModel,
		Code:     "model_stream_failed",
		Cause:    errors.New("provider detail"),
	}
	outer := &Error{
		Category: CategoryPersistence,
		Code:     "append_failed",
		Cause:    fmt.Errorf("persist model failure: %w", inner),
	}

	if !IsCategory(outer, CategoryPersistence) {
		t.Fatal("IsCategory did not find the outer application error")
	}
	if !IsCategory(outer, CategoryModel) {
		t.Fatal("IsCategory did not find the nested application error")
	}
	if IsCategory(outer, CategoryConflict) {
		t.Fatal("IsCategory reported an absent nested category")
	}
}

func TestIsCategoryTraversesEveryErrorsJoinBranch(t *testing.T) {
	validation := &Error{Category: CategoryValidation, Code: "invalid_request"}
	model := &Error{Category: CategoryModel, Code: "model_stream_failed"}
	joined := errors.Join(
		fmt.Errorf("validate: %w", validation),
		fmt.Errorf("execute: %w", model),
	)
	outer := &Error{Category: CategoryInternal, Code: "operation_failed", Cause: joined}

	for _, category := range []ErrorCategory{CategoryInternal, CategoryValidation, CategoryModel} {
		if !IsCategory(outer, category) {
			t.Errorf("IsCategory did not find %q in the joined error tree", category)
		}
	}
	if IsCategory(outer, CategoryDelivery) {
		t.Fatal("IsCategory reported a category absent from the joined error tree")
	}
}

func TestIsCategoryRejectsTypedNilApplicationError(t *testing.T) {
	var typedNil *Error
	if IsCategory(typedNil, CategoryInternal) {
		t.Fatal("IsCategory accepted a typed nil application error")
	}
}

func TestIsCategoryTypedNilJoinBranchDoesNotHideValidSibling(t *testing.T) {
	var typedNil *Error
	joined := errors.Join(
		typedNil,
		&Error{Category: CategoryModel, Code: "model_stream_failed"},
	)

	if !IsCategory(joined, CategoryModel) {
		t.Fatal("typed nil branch hid a valid sibling category")
	}
	if IsCategory(joined, CategoryPersistence) {
		t.Fatal("IsCategory reported a category absent from the joined tree")
	}
}

func TestErrorCategoriesAreStable(t *testing.T) {
	tests := map[ErrorCategory]string{
		CategoryValidation:  "validation",
		CategoryConflict:    "conflict",
		CategoryModel:       "model",
		CategoryCanceled:    "canceled",
		CategoryOutputLimit: "output_limit",
		CategoryDelivery:    "delivery",
		CategoryPersistence: "persistence",
		CategoryInternal:    "internal",
	}

	if len(tests) != 8 {
		t.Fatalf("category count = %d, want 8", len(tests))
	}
	for category, want := range tests {
		if got := string(category); got != want {
			t.Errorf("category = %q, want %q", got, want)
		}
	}
}

func TestVersionConflictIsTypedAndStable(t *testing.T) {
	conflict := &VersionConflictError{
		SessionID:       "session-9",
		ExpectedVersion: 3,
		ActualVersion:   5,
	}
	wrapped := fmt.Errorf("append failed: %w", conflict)

	if got, want := conflict.Error(), "version conflict for session session-9: expected 3, actual 5"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	if !IsVersionConflict(wrapped) {
		t.Fatal("IsVersionConflict did not inspect the error chain")
	}
	if !errors.Is(wrapped, conflict) {
		t.Fatal("errors.Is did not preserve the version conflict in its chain")
	}
	if IsVersionConflict(errors.New("version conflict")) || IsVersionConflict(nil) {
		t.Fatal("IsVersionConflict accepted an untyped error")
	}

	var got *VersionConflictError
	if !errors.As(wrapped, &got) || got != conflict {
		t.Fatalf("errors.As = %#v, want original conflict", got)
	}
	if got.SessionID != "session-9" || got.ExpectedVersion != 3 || got.ActualVersion != 5 {
		t.Fatalf("conflict fields changed: %#v", got)
	}
}
