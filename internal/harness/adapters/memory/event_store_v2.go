package memory

import (
	"context"
	"fmt"
	"sync"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// FaultPoint names deterministic test-only v2 storage failure boundaries.
type FaultPoint string

const (
	FaultBeforeCommit         FaultPoint = "before_commit"
	FaultAfterCommitBeforeAck FaultPoint = "after_commit_before_ack"
	FaultResolve              FaultPoint = "resolve"
)

// CommitHookPoint is a bounded conformance-only callback boundary.
type CommitHookPoint string

const (
	CommitHookBeforePublish CommitHookPoint = "before_publish"
	CommitHookAfterPublish  CommitHookPoint = "after_publish"
)

// EventStoreV2 is the deterministic, mutex-protected reference implementation
// of the v2 stream storage contract.
type EventStoreV2 struct {
	mu          sync.Mutex
	authority   application.WriterAuthority
	state       eventStoreV2State
	faults      map[FaultPoint][]error
	commitHooks map[CommitHookPoint]func()
}

type storedAppend struct {
	digest         application.Digest
	receipt        application.CommitReceipt
	sessionID      domain.SessionID
	commitPosition uint64
	firstSequence  uint64
	eventCount     uint64
}

// eventStoreV2State owns every fact changed by a successful append. Append
// constructs a complete copy and publishes it with one assignment.
type eventStoreV2State struct {
	commitPosition uint64
	streams        map[domain.SessionID][]domain.RecordedEvent
	appends        map[domain.AppendID]storedAppend
	requests       map[domain.RunTurnRequestID]application.CommandRequestRecord
	eventIDs       map[domain.EventID]struct{}
	turnIDs        map[domain.SessionID]map[domain.TurnID]struct{}
	itemIDs        map[domain.SessionID]map[domain.ItemID]struct{}
}

var _ application.EventStoreV2 = (*EventStoreV2)(nil)

// NewEventStoreV2 constructs an empty store owned by authority.
func NewEventStoreV2(authority application.WriterAuthority) (*EventStoreV2, error) {
	if err := authority.Validate(); err != nil {
		return nil, fmt.Errorf("invalid writer authority: %w", err)
	}
	return &EventStoreV2{
		authority: authority, state: newEventStoreV2State(), faults: make(map[FaultPoint][]error), commitHooks: make(map[CommitHookPoint]func()),
	}, nil
}

func newEventStoreV2State() eventStoreV2State {
	return eventStoreV2State{streams: make(map[domain.SessionID][]domain.RecordedEvent), appends: make(map[domain.AppendID]storedAppend), requests: make(map[domain.RunTurnRequestID]application.CommandRequestRecord), eventIDs: make(map[domain.EventID]struct{}), turnIDs: make(map[domain.SessionID]map[domain.TurnID]struct{}), itemIDs: make(map[domain.SessionID]map[domain.ItemID]struct{})}
}

// SetAuthority rotates the current deterministic owner. Invalid values are
// ignored so the last known valid owner remains authoritative.
func (store *EventStoreV2) SetAuthority(authority application.WriterAuthority) {
	if authority.Validate() != nil {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if authority.FencingToken > store.authority.FencingToken {
		store.authority = authority
	}
}

// FailNext enqueues one bounded deterministic failure at point.
func (store *EventStoreV2) FailNext(point FaultPoint, err error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err == nil {
		delete(store.faults, point)
		return
	}
	store.faults[point] = []error{err}
}

// CorruptReceipt is a conformance-only control that prevents a stored receipt
// from being emitted; no Store-port caller can access it.
func (store *EventStoreV2) CorruptReceipt(appendID domain.AppendID) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if value, ok := store.state.appends[appendID]; ok {
		stream := store.state.streams[value.sessionID]
		// Pick a real current stream range and global commit position so the
		// receipt remains structurally plausible while disagreeing with the
		// append's immutable publication metadata.
		value.receipt.CommitPosition = store.state.commitPosition
		value.receipt.FirstSequence = uint64(len(stream))
		value.receipt.LastSequence = uint64(len(stream))
		store.state.appends[appendID] = value
	}
}

// SetCommitHook installs one bounded conformance-only hook.
func (store *EventStoreV2) SetCommitHook(point CommitHookPoint, hook func()) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if hook == nil {
		delete(store.commitHooks, point)
		return
	}
	store.commitHooks[point] = hook
}

func (store *EventStoreV2) Append(ctx context.Context, request application.AppendRequestV2) (application.CommitReceipt, error) {
	if err := contextError(ctx); err != nil {
		return application.CommitReceipt{}, appendError(application.StoreCodeInvalidAppend, request.SessionID, err)
	}
	request, err := cloneAppendRequest(request)
	if err != nil {
		return application.CommitReceipt{}, appendError(application.StoreCodeInvalidAppend, request.SessionID, err)
	}
	digest, err := application.DigestAppendRequest(request)
	if err != nil {
		return application.CommitReceipt{}, appendError(application.StoreCodeInvalidAppend, request.SessionID, err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if old, exists := store.state.appends[request.AppendID]; exists {
		if old.digest == digest {
			if err := store.validateStoredReceipt(request.AppendID, old); err != nil {
				return application.CommitReceipt{}, err
			}
			return old.receipt, nil
		}
		return application.CommitReceipt{}, appendError(application.StoreCodeAppendIdentityMismatch, request.SessionID, nil)
	}
	if err := contextError(ctx); err != nil {
		return application.CommitReceipt{}, appendError(application.StoreCodeInvalidAppend, request.SessionID, err)
	}
	// Repeat digest construction while holding the critical section. This makes
	// the immutable identity used for receipt publication independent of caller
	// mutations that race with Append.
	digest, err = application.DigestAppendRequest(request)
	if err != nil {
		return application.CommitReceipt{}, appendError(application.StoreCodeInvalidAppend, request.SessionID, err)
	}
	if request.Authority != store.authority {
		return application.CommitReceipt{}, appendError(application.StoreCodeWriterFenced, request.SessionID, nil)
	}
	if request.Admission != nil {
		if existing, exists := store.state.requests[request.Admission.RunTurnRequestID]; exists {
			if existing.SessionID == request.SessionID && existing.RequestDigest != request.Admission.RequestDigest {
				return application.CommitReceipt{}, appendError(application.StoreCodeCommandIdentityMismatch, request.SessionID, nil)
			}
			return application.CommitReceipt{}, appendError(application.StoreCodeCommandRequestConflict, request.SessionID, nil)
		}
	}
	current := store.state.streams[request.SessionID]
	if actual := uint64(len(current)); actual != request.ExpectedVersion {
		return application.CommitReceipt{}, storeError(application.StoreCodeVersionConflict, request.SessionID, request.ExpectedVersion, actual, "", nil)
	}

	batch, turnIDs, itemIDs, err := store.buildBatch(request, current)
	if err != nil {
		return application.CommitReceipt{}, err
	}
	if request.Admission != nil && (!containsTurnID(turnIDs, request.Admission.TurnID) || !containsItemID(itemIDs, request.Admission.ItemID)) {
		return application.CommitReceipt{}, appendError(application.StoreCodeInvalidAppend, request.SessionID, fmt.Errorf("admission requires matching turn and item start events"))
	}
	candidate := make([]domain.RecordedEvent, 0, len(current)+len(batch))
	candidate = append(candidate, current...)
	candidate = append(candidate, batch...)
	if _, err := domain.ReplayCompact(candidate); err != nil {
		return application.CommitReceipt{}, appendError(application.StoreCodeInvalidAppend, request.SessionID, err)
	}
	if err := contextError(ctx); err != nil {
		return application.CommitReceipt{}, appendError(application.StoreCodeInvalidAppend, request.SessionID, err)
	}
	if cause := store.popFault(FaultBeforeCommit); cause != nil {
		return application.CommitReceipt{}, appendError(application.StoreCodeUnavailable, request.SessionID, cause)
	}

	// Build every independently owned candidate map before the single publish.
	candidateState, err := cloneV2State(store.state)
	if err != nil {
		return application.CommitReceipt{}, storeError(application.StoreCodeCorrupt, request.SessionID, 0, 0, "", err)
	}
	position := candidateState.commitPosition + 1
	receipt := application.CommitReceipt{AppendID: request.AppendID, CommitPosition: position, FirstSequence: batch[0].Sequence, LastSequence: batch[len(batch)-1].Sequence}
	candidateState.streams[request.SessionID] = candidate
	candidateState.commitPosition = position
	candidateState.appends[request.AppendID] = storedAppend{digest: digest, receipt: receipt, sessionID: request.SessionID, commitPosition: position, firstSequence: receipt.FirstSequence, eventCount: uint64(len(batch))}
	for _, record := range batch {
		candidateState.eventIDs[record.ID] = struct{}{}
	}
	reserveIDs(candidateState.turnIDs, request.SessionID, turnIDs)
	reserveIDsItems(candidateState.itemIDs, request.SessionID, itemIDs)
	if request.Admission != nil {
		candidateState.requests[request.Admission.RunTurnRequestID] = application.CommandRequestRecord{RunTurnRequestID: request.Admission.RunTurnRequestID, RequestDigest: request.Admission.RequestDigest, SessionID: request.SessionID, CommandID: request.CommandID, TurnID: request.Admission.TurnID, ItemID: request.Admission.ItemID, AdmissionAppendID: request.AppendID}
	}
	store.runCommitHook(CommitHookBeforePublish)
	if err := contextError(ctx); err != nil {
		return application.CommitReceipt{}, appendError(application.StoreCodeInvalidAppend, request.SessionID, err)
	}
	// This assignment is the sole publication point for append state.
	store.state = candidateState
	store.runCommitHook(CommitHookAfterPublish)
	if cause := store.popFault(FaultAfterCommitBeforeAck); cause != nil {
		return application.CommitReceipt{}, storeError(application.StoreCodeCommitOutcomeUnknown, request.SessionID, 0, 0, "", cause)
	}
	return receipt, nil
}

// cloneAppendRequest takes ownership of every event before authorization or
// storage state is observed. The canonical digest and candidate records then
// describe the same immutable append decision.
func cloneAppendRequest(request application.AppendRequestV2) (application.AppendRequestV2, error) {
	cloned := request
	if request.Admission != nil {
		admission := *request.Admission
		cloned.Admission = &admission
	}
	cloned.Events = make([]application.ProposedEvent, len(request.Events))
	for i, event := range request.Events {
		copy := event
		var err error
		copy.Event, err = domain.CloneEvent(event.Event)
		if err != nil {
			return application.AppendRequestV2{}, err
		}
		cloned.Events[i] = copy
	}
	return cloned, nil
}

func (store *EventStoreV2) buildBatch(request application.AppendRequestV2, current []domain.RecordedEvent) ([]domain.RecordedEvent, []domain.TurnID, []domain.ItemID, error) {
	seenEvents := make(map[domain.EventID]struct{}, len(request.Events))
	seenTurns := make(map[domain.TurnID]struct{})
	seenItems := make(map[domain.ItemID]struct{})
	batch := make([]domain.RecordedEvent, len(request.Events))
	for i, proposed := range request.Events {
		if _, exists := seenEvents[proposed.ID]; exists {
			return nil, nil, nil, appendError(application.StoreCodeInvalidAppend, request.SessionID, fmt.Errorf("duplicate event ID"))
		}
		if _, exists := store.state.eventIDs[proposed.ID]; exists {
			return nil, nil, nil, appendError(application.StoreCodeInvalidAppend, request.SessionID, fmt.Errorf("duplicate event ID"))
		}
		cloned, err := domain.CloneEvent(proposed.Event)
		if err != nil {
			return nil, nil, nil, appendError(application.StoreCodeInvalidAppend, request.SessionID, err)
		}
		seenEvents[proposed.ID] = struct{}{}
		batch[i] = domain.RecordedEvent{SchemaVersion: int(proposed.SchemaVersion), ID: proposed.ID, CommandID: request.CommandID, SessionID: request.SessionID, Sequence: uint64(len(current) + i + 1), OccurredAt: proposed.OccurredAt, Event: cloned}
		switch event := cloned.(type) {
		case domain.TurnStarted:
			if _, exists := store.state.turnIDs[request.SessionID][event.TurnID]; exists {
				return nil, nil, nil, identityError(request.SessionID, "turn")
			}
			if _, exists := seenTurns[event.TurnID]; exists {
				return nil, nil, nil, identityError(request.SessionID, "turn")
			}
			seenTurns[event.TurnID] = struct{}{}
		case domain.AssistantMessageStarted:
			if _, exists := store.state.itemIDs[request.SessionID][event.ItemID]; exists {
				return nil, nil, nil, identityError(request.SessionID, "item")
			}
			if _, exists := seenItems[event.ItemID]; exists {
				return nil, nil, nil, identityError(request.SessionID, "item")
			}
			seenItems[event.ItemID] = struct{}{}
		}
	}
	turns := make([]domain.TurnID, 0, len(seenTurns))
	for id := range seenTurns {
		turns = append(turns, id)
	}
	items := make([]domain.ItemID, 0, len(seenItems))
	for id := range seenItems {
		items = append(items, id)
	}
	return batch, turns, items, nil
}

func (store *EventStoreV2) ReadStream(ctx context.Context, request application.ReadStreamRequest) (application.StreamPage, error) {
	if err := contextError(ctx); err != nil {
		return application.StreamPage{}, readError(request.SessionID, err)
	}
	if _, err := domain.ParseSessionID(string(request.SessionID)); err != nil || request.Limit == 0 || request.Limit > 256 {
		return application.StreamPage{}, readError(request.SessionID, err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := contextError(ctx); err != nil {
		return application.StreamPage{}, readError(request.SessionID, err)
	}
	stream := store.state.streams[request.SessionID]
	currentHead := uint64(len(stream))
	head := currentHead
	if request.HeadVersion != nil {
		head = *request.HeadVersion
	}
	if request.AfterSequence > head || head > currentHead || (request.HeadVersion != nil && head < request.AfterSequence) {
		return application.StreamPage{}, readError(request.SessionID, fmt.Errorf("invalid pinned cursor"))
	}
	start := request.AfterSequence
	end := start + uint64(request.Limit)
	if end > head {
		end = head
	}
	var records []domain.RecordedEvent
	if start < end {
		var err error
		records, err = domain.CloneRecordedEvents(stream[start:end])
		if err != nil {
			return application.StreamPage{}, storeError(application.StoreCodeCorrupt, request.SessionID, 0, 0, "", err)
		}
	}
	next := start
	if len(records) > 0 {
		next = records[len(records)-1].Sequence
	}
	return application.StreamPage{Records: records, HeadVersion: head, NextAfterSequence: next, End: next == head}, nil
}

func (store *EventStoreV2) ResolveAppend(ctx context.Context, request application.ResolveAppendRequest) (application.AppendResolution, error) {
	if err := contextError(ctx); err != nil {
		return application.AppendResolution{}, appendError(application.StoreCodeInvalidAppend, "", err)
	}
	if _, err := domain.ParseAppendID(string(request.AppendID)); err != nil {
		return application.AppendResolution{}, appendError(application.StoreCodeInvalidAppend, "", err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := contextError(ctx); err != nil {
		return application.AppendResolution{}, appendError(application.StoreCodeInvalidAppend, "", err)
	}
	if cause := store.popFault(FaultResolve); cause != nil {
		return application.AppendResolution{}, appendError(application.StoreCodeUnavailable, "", cause)
	}
	if old, exists := store.state.appends[request.AppendID]; exists {
		if old.digest == request.RequestDigest {
			if err := store.validateStoredReceipt(request.AppendID, old); err != nil {
				return application.AppendResolution{}, err
			}
			receipt := old.receipt
			return application.AppendResolution{Kind: application.AppendResolutionCommitted, Receipt: &receipt}, nil
		}
		return application.AppendResolution{Kind: application.AppendResolutionIdentityMismatch}, nil
	}
	return application.AppendResolution{Kind: application.AppendResolutionNotFound}, nil
}

func (store *EventStoreV2) FindCommandRequest(ctx context.Context, request application.FindCommandRequestRequest) (application.CommandRequestLookup, error) {
	if err := contextError(ctx); err != nil {
		return application.CommandRequestLookup{}, appendError(application.StoreCodeInvalidAppend, request.SessionID, err)
	}
	if _, err := domain.ParseRunTurnRequestID(string(request.RunTurnRequestID)); err != nil {
		return application.CommandRequestLookup{}, appendError(application.StoreCodeInvalidAppend, request.SessionID, err)
	}
	if _, err := domain.ParseSessionID(string(request.SessionID)); err != nil {
		return application.CommandRequestLookup{}, appendError(application.StoreCodeInvalidAppend, request.SessionID, err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := contextError(ctx); err != nil {
		return application.CommandRequestLookup{}, appendError(application.StoreCodeInvalidAppend, request.SessionID, err)
	}
	if record, exists := store.state.requests[request.RunTurnRequestID]; exists {
		if record.SessionID == request.SessionID && record.RequestDigest == request.RequestDigest {
			copy := record
			return application.CommandRequestLookup{Kind: application.CommandRequestLookupFound, Record: &copy}, nil
		}
		return application.CommandRequestLookup{Kind: application.CommandRequestLookupIdentityMismatch}, nil
	}
	return application.CommandRequestLookup{Kind: application.CommandRequestLookupNotFound}, nil
}

func (store *EventStoreV2) popFault(point FaultPoint) error {
	queue := store.faults[point]
	if len(queue) == 0 {
		return nil
	}
	cause := queue[0]
	if len(queue) == 1 {
		delete(store.faults, point)
	} else {
		store.faults[point] = queue[1:]
	}
	return cause
}

func (store *EventStoreV2) runCommitHook(point CommitHookPoint) {
	hook := store.commitHooks[point]
	delete(store.commitHooks, point)
	if hook != nil {
		hook()
	}
}

func (store *EventStoreV2) validateStoredReceipt(appendID domain.AppendID, stored storedAppend) error {
	receipt := stored.receipt
	lastSequence := stored.firstSequence + stored.eventCount - 1
	if stored.commitPosition == 0 || stored.firstSequence == 0 || stored.eventCount == 0 || receipt.AppendID != appendID || receipt.CommitPosition != stored.commitPosition || receipt.FirstSequence != stored.firstSequence || receipt.LastSequence != lastSequence {
		return storeError(application.StoreCodeCorrupt, stored.sessionID, 0, 0, "", fmt.Errorf("invalid stored receipt"))
	}
	stream := store.state.streams[stored.sessionID]
	if receipt.LastSequence > uint64(len(stream)) || stream[receipt.FirstSequence-1].Sequence != receipt.FirstSequence || stream[receipt.LastSequence-1].Sequence != receipt.LastSequence {
		return storeError(application.StoreCodeCorrupt, stored.sessionID, 0, 0, "", fmt.Errorf("receipt sequence range is corrupt"))
	}
	return nil
}

func cloneV2State(source eventStoreV2State) (eventStoreV2State, error) {
	cloned := newEventStoreV2State()
	cloned.commitPosition = source.commitPosition
	for session, records := range source.streams {
		copied, err := domain.CloneRecordedEvents(records)
		if err != nil {
			return eventStoreV2State{}, err
		}
		cloned.streams[session] = copied
	}
	for id, value := range source.appends {
		cloned.appends[id] = value
	}
	for id, value := range source.requests {
		cloned.requests[id] = value
	}
	for id := range source.eventIDs {
		cloned.eventIDs[id] = struct{}{}
	}
	for session, ids := range source.turnIDs {
		copied := make(map[domain.TurnID]struct{}, len(ids))
		for id := range ids {
			copied[id] = struct{}{}
		}
		cloned.turnIDs[session] = copied
	}
	for session, ids := range source.itemIDs {
		copied := make(map[domain.ItemID]struct{}, len(ids))
		for id := range ids {
			copied[id] = struct{}{}
		}
		cloned.itemIDs[session] = copied
	}
	return cloned, nil
}

func containsTurnID(ids []domain.TurnID, want domain.TurnID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func containsItemID(ids []domain.ItemID, want domain.ItemID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
func reserveIDs(index map[domain.SessionID]map[domain.TurnID]struct{}, session domain.SessionID, ids []domain.TurnID) {
	if len(ids) == 0 {
		return
	}
	set := index[session]
	if set == nil {
		set = make(map[domain.TurnID]struct{})
		index[session] = set
	}
	for _, id := range ids {
		set[id] = struct{}{}
	}
}
func reserveIDsItems(index map[domain.SessionID]map[domain.ItemID]struct{}, session domain.SessionID, ids []domain.ItemID) {
	if len(ids) == 0 {
		return
	}
	set := index[session]
	if set == nil {
		set = make(map[domain.ItemID]struct{})
		index[session] = set
	}
	for _, id := range ids {
		set[id] = struct{}{}
	}
}
func contextError(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("context is required")
	}
	return ctx.Err()
}
func storeError(code application.StoreErrorCode, session domain.SessionID, expected, actual uint64, kind string, cause error) error {
	err, build := application.NewStoreError(application.StoreError{Code: code, SessionID: session, ExpectedVersion: expected, ActualVersion: actual, IdentityKind: kind, MayHaveCommitted: code == application.StoreCodeCommitOutcomeUnknown, Cause: cause})
	if build != nil {
		panic(build)
	}
	return err
}
func appendError(code application.StoreErrorCode, session domain.SessionID, cause error) error {
	return storeError(code, session, 0, 0, "", cause)
}
func readError(session domain.SessionID, cause error) error {
	return storeError(application.StoreCodeInvalidRead, session, 0, 0, "", cause)
}
func identityError(session domain.SessionID, kind string) error {
	return storeError(application.StoreCodeDomainIdentityConflict, session, 0, 0, kind, nil)
}
