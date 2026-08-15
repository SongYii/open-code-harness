package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestResolveAppendIntentCommittedReceipt(t *testing.T) {
	intent := mustResolutionIntent(t)
	receipt := CommitReceipt{AppendID: intent.Request.AppendID, CommitPosition: 3, FirstSequence: 1, LastSequence: 1}
	store := &resolutionScript{
		resolves: []resolutionStep{{resolution: AppendResolution{Kind: AppendResolutionCommitted, Receipt: &receipt}}},
	}
	got, err := ResolveAppendIntent(context.Background(), store, intent, AppendResolutionConfig{MaxOperations: 4})
	if err != nil || got != receipt || store.resolveCalls != 1 || store.appendCalls != 0 {
		t.Fatalf("got=%#v err=%v resolves=%d appends=%d", got, err, store.resolveCalls, store.appendCalls)
	}
}

func TestResolveAppendIntentNotFoundExactAppend(t *testing.T) {
	intent := mustResolutionIntent(t)
	receipt := CommitReceipt{AppendID: intent.Request.AppendID, CommitPosition: 2, FirstSequence: 1, LastSequence: 1}
	store := &resolutionScript{
		resolves: []resolutionStep{{resolution: AppendResolution{Kind: AppendResolutionNotFound}}},
		appends:  []appendStep{{receipt: receipt}},
	}
	got, err := ResolveAppendIntent(context.Background(), store, intent, AppendResolutionConfig{MaxOperations: 4})
	if err != nil || got != receipt || store.resolveCalls != 1 || store.appendCalls != 1 {
		t.Fatalf("got=%#v err=%v resolves=%d appends=%d", got, err, store.resolveCalls, store.appendCalls)
	}
	if store.lastAppend.AppendID != intent.Request.AppendID || store.lastAppend.ExpectedVersion != intent.Request.ExpectedVersion {
		t.Fatalf("exact append reused identity: %#v", store.lastAppend)
	}
}

func TestResolveAppendIntentUnavailableBudgetReturnsUnknown(t *testing.T) {
	intent := mustResolutionIntent(t)
	unavailable, err := NewStoreError(StoreError{Code: StoreCodeUnavailable, Cause: errors.New("down")})
	if err != nil {
		t.Fatal(err)
	}
	store := &resolutionScript{
		resolves: []resolutionStep{
			{err: unavailable}, {err: unavailable}, {err: unavailable}, {err: unavailable},
		},
	}
	_, resolveErr := ResolveAppendIntent(context.Background(), store, intent, AppendResolutionConfig{MaxOperations: 4})
	if !isAppendOutcomeUnknown(resolveErr) || store.resolveCalls != 4 || store.appendCalls != 0 {
		t.Fatalf("err=%v resolves=%d appends=%d", resolveErr, store.resolveCalls, store.appendCalls)
	}
}

func TestResolveAppendIntentHonorsInjectedTimeout(t *testing.T) {
	intent := mustResolutionIntent(t)
	started := make(chan struct{}, 1)
	store := &resolutionScript{resolveFn: func(ctx context.Context) (AppendResolution, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return AppendResolution{}, ctx.Err()
	}}
	_, err := ResolveAppendIntent(context.Background(), store, intent, AppendResolutionConfig{Timeout: 20 * time.Millisecond, MaxOperations: 4})
	if !isAppendOutcomeUnknown(err) {
		t.Fatalf("err=%v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("resolver was not invoked")
	}
}

func TestResolveAppendIntentIdentityMismatchFailsClosed(t *testing.T) {
	intent := mustResolutionIntent(t)
	store := &resolutionScript{resolves: []resolutionStep{{resolution: AppendResolution{Kind: AppendResolutionIdentityMismatch}}}}
	_, err := ResolveAppendIntent(context.Background(), store, intent, AppendResolutionConfig{MaxOperations: 4})
	var appErr *Error
	if !errors.As(err, &appErr) || appErr.Code != string(StoreCodeAppendIdentityMismatch) || store.appendCalls != 0 {
		t.Fatalf("err=%v appends=%d", err, store.appendCalls)
	}
}

func mustResolutionIntent(t *testing.T) AppendIntent {
	t.Helper()
	intent, err := BuildAppendIntent(&countingClock{value: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)}, &intentIDs{}, WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1}, "session-1", 0, "command-1", nil, []domain.UncommittedEvent{{Event: domain.SessionCreated{WorkspaceRoot: "/workspace"}}})
	if err != nil {
		t.Fatal(err)
	}
	return intent
}

type resolutionStep struct {
	resolution AppendResolution
	err        error
}

type appendStep struct {
	receipt CommitReceipt
	err     error
}

type resolutionScript struct {
	resolves     []resolutionStep
	appends      []appendStep
	resolveFn    func(context.Context) (AppendResolution, error)
	resolveCalls int
	appendCalls  int
	lastAppend   AppendRequest
}

func (store *resolutionScript) ReadStream(context.Context, ReadStreamRequest) (StreamPage, error) {
	return StreamPage{}, errors.New("unexpected read")
}

func (store *resolutionScript) Append(_ context.Context, request AppendRequest) (CommitReceipt, error) {
	store.appendCalls++
	store.lastAppend = request
	if store.appendCalls-1 < len(store.appends) {
		step := store.appends[store.appendCalls-1]
		return step.receipt, step.err
	}
	return CommitReceipt{}, errors.New("unexpected append")
}

func (store *resolutionScript) ResolveAppend(ctx context.Context, _ ResolveAppendRequest) (AppendResolution, error) {
	store.resolveCalls++
	if store.resolveFn != nil {
		return store.resolveFn(ctx)
	}
	if store.resolveCalls-1 < len(store.resolves) {
		step := store.resolves[store.resolveCalls-1]
		return step.resolution, step.err
	}
	return AppendResolution{}, errors.New("unexpected resolve")
}

func (store *resolutionScript) FindCommandRequest(context.Context, FindCommandRequestRequest) (CommandRequestLookup, error) {
	return CommandRequestLookup{}, errors.New("unexpected find")
}
