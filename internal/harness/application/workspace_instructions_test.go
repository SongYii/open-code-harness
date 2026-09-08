package application

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/agentinstructions"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func TestWorkspaceInstructionsReconcileUsesVersionAsHintAndDigestAsIdentity(t *testing.T) {
	mem := newVersionedInstructionFS()
	mem.AddFile("AGENTS.md", []byte("root\n"))
	files := &instructionReadSpy{FileSystem: mem}
	store, state := newInstructionStore(t)
	service := newInstructionServiceForTest(store, files)

	next, err := service.reconcileWorkspaceInstructions(context.Background(), state, nil)
	if err != nil {
		t.Fatalf("initial reconcile error = %v", err)
	}
	if next.Version != 2 || len(store.snapshot()) != 2 {
		t.Fatalf("initial state version = %d, records = %d", next.Version, len(store.snapshot()))
	}
	assertReadLimits(t, files.takeLimits(), []int{0, agentinstructions.MaxSourceBytes})
	initial := store.snapshot()[1].Event.(domain.WorkspaceInstructionsRecorded)
	if len(initial.Changes) != 1 || initial.Changes[0].Action != domain.InstructionActionSet || initial.Changes[0].Content != "root\n" {
		t.Fatalf("initial event = %#v", initial)
	}

	same, err := service.reconcileWorkspaceInstructions(context.Background(), next, nil)
	if err != nil {
		t.Fatalf("same-version reconcile error = %v", err)
	}
	if same.Version != 2 || len(store.snapshot()) != 2 {
		t.Fatalf("same-version reconcile appended: version=%d records=%d", same.Version, len(store.snapshot()))
	}
	assertReadLimits(t, files.takeLimits(), []int{0})

	mem.AddFile("AGENTS.md", []byte("root\n"))
	sameDigest, err := service.reconcileWorkspaceInstructions(context.Background(), same, nil)
	if err != nil {
		t.Fatalf("same-digest reconcile error = %v", err)
	}
	if sameDigest.Version != 2 || len(store.snapshot()) != 2 {
		t.Fatalf("same digest appended: version=%d records=%d", sameDigest.Version, len(store.snapshot()))
	}
	assertReadLimits(t, files.takeLimits(), []int{0, agentinstructions.MaxSourceBytes})

	mem.AddFile("AGENTS.md", []byte("changed\n"))
	changed, err := service.reconcileWorkspaceInstructions(context.Background(), sameDigest, nil)
	if err != nil {
		t.Fatalf("changed reconcile error = %v", err)
	}
	if changed.Version != 3 || len(store.snapshot()) != 3 {
		t.Fatalf("changed reconcile = version %d records %d", changed.Version, len(store.snapshot()))
	}
	assertReadLimits(t, files.takeLimits(), []int{0, agentinstructions.MaxSourceBytes})
	replacement := store.snapshot()[2].Event.(domain.WorkspaceInstructionsRecorded)
	if len(replacement.Changes) != 1 || replacement.Changes[0].Action != domain.InstructionActionReplace || replacement.Changes[0].Content != "changed\n" {
		t.Fatalf("replacement event = %#v", replacement)
	}
}

func TestWorkspaceInstructionsReconcileRecordsConfirmedRootAbsenceOnce(t *testing.T) {
	mem := newVersionedInstructionFS()
	files := &instructionReadSpy{FileSystem: mem}
	store, state := newInstructionStore(t)
	service := newInstructionServiceForTest(store, files)

	next, err := service.reconcileWorkspaceInstructions(context.Background(), state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.Version != 2 || len(store.snapshot()) != 2 {
		t.Fatalf("absence was not durable: version=%d records=%d", next.Version, len(store.snapshot()))
	}
	event := store.snapshot()[1].Event.(domain.WorkspaceInstructionsRecorded)
	if len(event.Discovered) != 1 || event.Discovered[0].Path != "AGENTS.md" || len(event.Changes) != 0 {
		t.Fatalf("absence event = %#v", event)
	}

	again, err := service.reconcileWorkspaceInstructions(context.Background(), next, nil)
	if err != nil || again.Version != 2 || len(store.snapshot()) != 2 {
		t.Fatalf("repeated absence = (%#v, %v), records=%d", again, err, len(store.snapshot()))
	}
	assertReadLimits(t, files.takeLimits(), []int{0, 0})
}

func TestWorkspaceInstructionsReconcileRetainsStateAndDeduplicatesFailureEpisode(t *testing.T) {
	mem := newVersionedInstructionFS()
	mem.AddFile("AGENTS.md", []byte("root\n"))
	files := &instructionReadSpy{FileSystem: mem}
	store, state := newInstructionStore(t)
	service := newInstructionServiceForTest(store, files)
	state, err := service.reconcileWorkspaceInstructions(context.Background(), state, nil)
	if err != nil {
		t.Fatal(err)
	}
	files.takeLimits()

	files.setFailure(fs.ErrPermission)
	failed, err := service.reconcileWorkspaceInstructions(context.Background(), state, nil)
	if err != nil {
		t.Fatalf("transient failure should be recorded, not returned: %v", err)
	}
	if failed.Version != 3 || len(store.snapshot()) != 3 {
		t.Fatalf("first failure = version %d records %d", failed.Version, len(store.snapshot()))
	}
	diagnostic := store.snapshot()[2].Event.(domain.WorkspaceInstructionsRecorded)
	if len(diagnostic.Diagnostics) != 1 || len(diagnostic.Changes) != 0 || diagnostic.EffectiveSetDigest != store.snapshot()[1].Event.(domain.WorkspaceInstructionsRecorded).EffectiveSetDigest {
		t.Fatalf("diagnostic did not retain effective state: %#v", diagnostic)
	}

	repeated, err := service.reconcileWorkspaceInstructions(context.Background(), failed, nil)
	if err != nil || repeated.Version != 3 || len(store.snapshot()) != 3 {
		t.Fatalf("repeated failure appended: state=%#v err=%v records=%d", repeated, err, len(store.snapshot()))
	}

	files.setFailure(nil)
	healthy, err := service.reconcileWorkspaceInstructions(context.Background(), repeated, nil)
	if err != nil || healthy.Version != 3 {
		t.Fatalf("healthy rearm = (%#v, %v)", healthy, err)
	}
	files.setFailure(fs.ErrPermission)
	rearmed, err := service.reconcileWorkspaceInstructions(context.Background(), healthy, nil)
	if err != nil || rearmed.Version != 4 || len(store.snapshot()) != 4 {
		t.Fatalf("rearmed failure = (%#v, %v), records=%d", rearmed, err, len(store.snapshot()))
	}
}

type instructionReadSpy struct {
	tools.FileSystem
	mu      sync.Mutex
	limits  []int
	failure error
}

func (spy *instructionReadSpy) Read(ctx context.Context, abs string, limit int) (tools.FileRead, error) {
	spy.mu.Lock()
	spy.limits = append(spy.limits, limit)
	failure := spy.failure
	spy.mu.Unlock()
	if failure != nil {
		return tools.FileRead{}, failure
	}
	return spy.FileSystem.Read(ctx, abs, limit)
}

func (spy *instructionReadSpy) takeLimits() []int {
	spy.mu.Lock()
	defer spy.mu.Unlock()
	limits := append([]int(nil), spy.limits...)
	spy.limits = nil
	return limits
}

func (spy *instructionReadSpy) setFailure(err error) {
	spy.mu.Lock()
	defer spy.mu.Unlock()
	spy.failure = err
}

func assertReadLimits(t *testing.T, got, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("read limits = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("read limits = %#v, want %#v", got, want)
		}
	}
}

type instructionStore struct {
	mu      sync.Mutex
	records []domain.RecordedEvent
}

func newInstructionStore(t *testing.T) (*instructionStore, domain.Session) {
	t.Helper()
	created := domain.RecordedEvent{
		SchemaVersion: 1,
		ID:            "event-created",
		CommandID:     "command-created",
		SessionID:     "session-1",
		Sequence:      1,
		OccurredAt:    time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC),
		Event:         domain.SessionCreated{WorkspaceRoot: "/workspace"},
	}
	state, err := domain.Apply(domain.Session{}, created)
	if err != nil {
		t.Fatal(err)
	}
	return &instructionStore{records: []domain.RecordedEvent{created}}, state
}

func (store *instructionStore) ReadStream(_ context.Context, request ReadStreamRequest) (StreamPage, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	head := uint64(len(store.records))
	if request.HeadVersion != nil && *request.HeadVersion != head {
		return StreamPage{}, &StoreError{Code: StoreCodeVersionConflict}
	}
	start := int(request.AfterSequence)
	if start > len(store.records) {
		return StreamPage{}, &StoreError{Code: StoreCodeInvalidRead}
	}
	records := append([]domain.RecordedEvent(nil), store.records[start:]...)
	return StreamPage{Records: records, HeadVersion: head, NextAfterSequence: head, End: true}, nil
}

func (*instructionStore) ListSessionHeads(context.Context, ListSessionHeadsRequest) (SessionHeadPage, error) {
	return SessionHeadPage{}, nil
}

func (store *instructionStore) Append(_ context.Context, request AppendRequest) (CommitReceipt, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if request.ExpectedVersion != uint64(len(store.records)) {
		return CommitReceipt{}, &StoreError{Code: StoreCodeVersionConflict}
	}
	first := request.ExpectedVersion + 1
	for index, proposed := range request.Events {
		store.records = append(store.records, domain.RecordedEvent{
			SchemaVersion: int(proposed.SchemaVersion),
			ID:            proposed.ID,
			CommandID:     request.CommandID,
			SessionID:     request.SessionID,
			Sequence:      first + uint64(index),
			OccurredAt:    proposed.OccurredAt,
			Event:         proposed.Event,
		})
	}
	last := uint64(len(store.records))
	return CommitReceipt{AppendID: request.AppendID, CommitPosition: last, FirstSequence: first, LastSequence: last}, nil
}

func (*instructionStore) ResolveAppend(context.Context, ResolveAppendRequest) (AppendResolution, error) {
	return AppendResolution{Kind: AppendResolutionNotFound}, nil
}

func (*instructionStore) FindCommandRequest(context.Context, FindCommandRequestRequest) (CommandRequestLookup, error) {
	return CommandRequestLookup{Kind: CommandRequestLookupNotFound}, nil
}

func (store *instructionStore) snapshot() []domain.RecordedEvent {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]domain.RecordedEvent(nil), store.records...)
}

type instructionAuthority struct{}

func (instructionAuthority) CurrentAuthority() WriterAuthority {
	return WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1}
}

func newInstructionServiceForTest(store EventStore, files tools.FileSystem) *Service {
	config := DefaultConfig()
	return &Service{
		store:        store,
		ids:          &instructionIDs{},
		clock:        instructionClock{},
		authority:    instructionAuthority{},
		config:       config,
		files:        files,
		instructions: newWorkspaceInstructionRegistry(),
	}
}

type versionedInstructionFS struct {
	mu      sync.Mutex
	exists  bool
	data    []byte
	version uint64
}

func newVersionedInstructionFS() *versionedInstructionFS { return &versionedInstructionFS{} }

func (files *versionedInstructionFS) AddFile(rel string, data []byte) {
	if rel != "AGENTS.md" {
		panic("unexpected test path: " + rel)
	}
	files.mu.Lock()
	defer files.mu.Unlock()
	files.exists = true
	files.data = append([]byte(nil), data...)
	files.version++
}

func (*versionedInstructionFS) Resolve(_ context.Context, workspace, requested string) (string, error) {
	if workspace != "/workspace" || requested != "AGENTS.md" {
		return "", fs.ErrInvalid
	}
	return path.Join(workspace, requested), nil
}

func (files *versionedInstructionFS) Read(_ context.Context, abs string, limit int) (tools.FileRead, error) {
	files.mu.Lock()
	defer files.mu.Unlock()
	if abs != "/workspace/AGENTS.md" || limit < 0 {
		return tools.FileRead{}, fs.ErrInvalid
	}
	if !files.exists {
		return tools.FileRead{}, fs.ErrNotExist
	}
	data := append([]byte(nil), files.data...)
	truncated := false
	if limit == 0 {
		truncated = len(data) > 0
		data = nil
	} else if len(data) > limit {
		data = data[:limit]
		truncated = true
	}
	return tools.FileRead{Data: data, Truncated: truncated, Version: tools.FileVersion(fmt.Sprintf("v%d", files.version))}, nil
}

func (*versionedInstructionFS) Write(context.Context, string, []byte, tools.MutationGuard) (tools.MutationResult, error) {
	return tools.MutationResult{}, fs.ErrInvalid
}

func (*versionedInstructionFS) Edit(context.Context, string, []byte, []byte, bool, tools.MutationGuard) (tools.MutationResult, error) {
	return tools.MutationResult{}, fs.ErrInvalid
}

func (*versionedInstructionFS) List(context.Context, string, int, int) ([]string, bool, error) {
	return nil, false, fs.ErrInvalid
}

type instructionClock struct{}

func (instructionClock) Now() time.Time {
	return time.Date(2026, 9, 8, 1, 0, 0, 0, time.UTC)
}

type instructionIDs struct {
	mu       sync.Mutex
	commands uint64
	appends  uint64
	events   uint64
}

func (*instructionIDs) NewSessionID() (domain.SessionID, error)   { return "session-unused", nil }
func (*instructionIDs) NewTurnID() (domain.TurnID, error)         { return "turn-unused", nil }
func (*instructionIDs) NewItemID() (domain.ItemID, error)         { return "item-unused", nil }
func (*instructionIDs) NewApprovalID() (domain.ApprovalID, error) { return "approval-unused", nil }
func (*instructionIDs) NewContextCompactionID() (domain.ContextCompactionID, error) {
	return "ctxcompaction-unused", nil
}
func (*instructionIDs) NewContextDecisionID() (domain.ContextDecisionID, error) {
	return "ctxdecision-unused", nil
}
func (ids *instructionIDs) NewCommandID() (domain.CommandID, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.commands++
	return domain.CommandID(fmt.Sprintf("command-instruction-%d", ids.commands)), nil
}
func (ids *instructionIDs) NewAppendID() (domain.AppendID, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.appends++
	return domain.AppendID(fmt.Sprintf("append-instruction-%d", ids.appends)), nil
}
func (ids *instructionIDs) NewEventID() (domain.EventID, error) {
	ids.mu.Lock()
	defer ids.mu.Unlock()
	ids.events++
	return domain.EventID(fmt.Sprintf("event-instruction-%d", ids.events)), nil
}
