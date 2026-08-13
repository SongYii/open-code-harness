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

// EventStoreV2 is the deterministic, mutex-protected reference implementation
// of the v2 stream storage contract.
type EventStoreV2 struct {
	mu             sync.Mutex
	authority      application.WriterAuthority
	commitPosition uint64
	streams        map[domain.SessionID][]domain.RecordedEvent
	appends        map[domain.AppendID]storedAppend
	requests       map[domain.RunTurnRequestID]application.CommandRequestRecord
	eventIDs       map[domain.EventID]struct{}
	turnIDs        map[domain.SessionID]map[domain.TurnID]struct{}
	itemIDs        map[domain.SessionID]map[domain.ItemID]struct{}
	faults         map[FaultPoint][]error
}

type storedAppend struct {
	digest  application.Digest
	receipt application.CommitReceipt
}

var _ application.EventStoreV2 = (*EventStoreV2)(nil)

// NewEventStoreV2 constructs an empty store owned by authority.
func NewEventStoreV2(authority application.WriterAuthority) (*EventStoreV2, error) {
	if err := authority.Validate(); err != nil {
		return nil, fmt.Errorf("invalid writer authority: %w", err)
	}
	return &EventStoreV2{
		authority: authority, streams: make(map[domain.SessionID][]domain.RecordedEvent),
		appends: make(map[domain.AppendID]storedAppend), requests: make(map[domain.RunTurnRequestID]application.CommandRequestRecord),
		eventIDs: make(map[domain.EventID]struct{}), turnIDs: make(map[domain.SessionID]map[domain.TurnID]struct{}),
		itemIDs: make(map[domain.SessionID]map[domain.ItemID]struct{}), faults: make(map[FaultPoint][]error),
	}, nil
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
	store.faults[point] = append(store.faults[point], err)
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
	if old, exists := store.appends[request.AppendID]; exists {
		if old.digest == digest {
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
		if existing, exists := store.requests[request.Admission.RunTurnRequestID]; exists {
			if existing.SessionID == request.SessionID && existing.RequestDigest != request.Admission.RequestDigest {
				return application.CommitReceipt{}, appendError(application.StoreCodeCommandIdentityMismatch, request.SessionID, nil)
			}
			return application.CommitReceipt{}, appendError(application.StoreCodeCommandRequestConflict, request.SessionID, nil)
		}
	}
	current := store.streams[request.SessionID]
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

	// Publish a complete candidate only after every validation succeeds.
	position := store.commitPosition + 1
	receipt := application.CommitReceipt{AppendID: request.AppendID, CommitPosition: position, FirstSequence: batch[0].Sequence, LastSequence: batch[len(batch)-1].Sequence}
	store.streams[request.SessionID] = candidate
	store.commitPosition = position
	store.appends[request.AppendID] = storedAppend{digest: digest, receipt: receipt}
	for _, record := range batch {
		store.eventIDs[record.ID] = struct{}{}
	}
	store.reserveIDs(store.turnIDs, request.SessionID, turnIDs)
	store.reserveIDsItems(store.itemIDs, request.SessionID, itemIDs)
	if request.Admission != nil {
		store.requests[request.Admission.RunTurnRequestID] = application.CommandRequestRecord{RunTurnRequestID: request.Admission.RunTurnRequestID, RequestDigest: request.Admission.RequestDigest, SessionID: request.SessionID, CommandID: request.CommandID, TurnID: request.Admission.TurnID, ItemID: request.Admission.ItemID, AdmissionAppendID: request.AppendID}
	}
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
		if _, exists := store.eventIDs[proposed.ID]; exists {
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
			if _, exists := store.turnIDs[request.SessionID][event.TurnID]; exists {
				return nil, nil, nil, identityError(request.SessionID, "turn")
			}
			if _, exists := seenTurns[event.TurnID]; exists {
				return nil, nil, nil, identityError(request.SessionID, "turn")
			}
			seenTurns[event.TurnID] = struct{}{}
		case domain.AssistantMessageStarted:
			if _, exists := store.itemIDs[request.SessionID][event.ItemID]; exists {
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
	stream := store.streams[request.SessionID]
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
	if cause := store.popFault(FaultResolve); cause != nil {
		return application.AppendResolution{}, appendError(application.StoreCodeUnavailable, "", cause)
	}
	if old, exists := store.appends[request.AppendID]; exists {
		if old.digest == request.RequestDigest {
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
	if record, exists := store.requests[request.RunTurnRequestID]; exists {
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
func (store *EventStoreV2) reserveIDs(index map[domain.SessionID]map[domain.TurnID]struct{}, session domain.SessionID, ids []domain.TurnID) {
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
func (store *EventStoreV2) reserveIDsItems(index map[domain.SessionID]map[domain.ItemID]struct{}, session domain.SessionID, ids []domain.ItemID) {
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
