package application

import (
	"context"
	"fmt"
	"math"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// AppendIntent is the immutable application-owned proposal and its canonical
// identity. A caller may safely retain it for exact retry/resolution later.
type AppendIntent struct {
	Request AppendRequest
	Digest  Digest
}

func BuildAppendIntent(clock Clock, ids IDGenerator, authority WriterAuthority, sessionID domain.SessionID, version uint64, commandID domain.CommandID, admission *CommandAdmission, events []domain.UncommittedEvent) (AppendIntent, error) {
	if isNilValue(clock) || isNilValue(ids) || authority.Validate() != nil || len(events) == 0 || len(events) > maxAppendEvents || uint64(len(events)) > math.MaxUint64-version {
		return AppendIntent{}, applicationError(CategoryValidation, "invalid_request", false, nil)
	}
	if _, err := domain.ParseSessionID(string(sessionID)); err != nil {
		return AppendIntent{}, applicationError(CategoryValidation, "invalid_request", false, err)
	}
	if _, err := domain.ParseCommandID(string(commandID)); err != nil {
		return AppendIntent{}, applicationError(CategoryValidation, "invalid_request", false, err)
	}
	var copiedAdmission *CommandAdmission
	if admission != nil {
		clone := *admission
		copiedAdmission = &clone
	}
	appendID, err := ids.NewAppendID()
	if err != nil {
		return AppendIntent{}, applicationError(CategoryInternal, "id_generation_failed", false, err)
	}
	if _, err := domain.ParseAppendID(string(appendID)); err != nil {
		return AppendIntent{}, applicationError(CategoryInternal, "id_generator_contract_violation", false, err)
	}
	occurredAt := clock.Now().UTC()
	if occurredAt.IsZero() {
		return AppendIntent{}, applicationError(CategoryInternal, "clock_contract_violation", false, nil)
	}
	proposed := make([]ProposedEvent, len(events))
	for index, uncommitted := range events {
		id, idErr := ids.NewEventID()
		if idErr != nil {
			return AppendIntent{}, applicationError(CategoryInternal, "id_generation_failed", false, idErr)
		}
		if _, idErr = domain.ParseEventID(string(id)); idErr != nil {
			return AppendIntent{}, applicationError(CategoryInternal, "id_generator_contract_violation", false, idErr)
		}
		event, cloneErr := domain.CloneEvent(uncommitted.Event)
		if cloneErr != nil {
			return AppendIntent{}, applicationError(CategoryInternal, "store_contract_violation", false, cloneErr)
		}
		proposed[index] = ProposedEvent{ID: id, SchemaVersion: 1, OccurredAt: occurredAt, Event: event}
	}
	request := AppendRequest{AppendID: appendID, SessionID: sessionID, ExpectedVersion: version, CommandID: commandID, Authority: authority, Admission: copiedAdmission, Events: proposed}
	digest, err := DigestAppendRequest(request)
	if err != nil {
		return AppendIntent{}, applicationError(CategoryInternal, "append_intent_invalid", false, err)
	}
	return cloneAppendIntent(AppendIntent{Request: request, Digest: digest})
}

func CommitAppendIntent(ctx context.Context, store EventStore, state domain.Session, intent AppendIntent) (domain.Session, []domain.RecordedEvent, error) {
	if err := contextError(ctx); err != nil {
		return domain.Session{}, nil, err
	}
	if isNilValue(store) {
		return domain.Session{}, nil, applicationError(CategoryValidation, "invalid_request", false, nil)
	}
	owned, err := cloneAppendIntent(intent)
	if err != nil {
		return domain.Session{}, nil, storeContractViolation(err)
	}
	digest, err := DigestAppendRequest(owned.Request)
	if err != nil || digest != owned.Digest {
		if err != nil {
			return domain.Session{}, nil, storeContractViolation(fmt.Errorf("intent digest mismatch: %w", err))
		}
		return domain.Session{}, nil, storeContractViolation(fmt.Errorf("intent digest mismatch"))
	}
	requestForStore, err := cloneAppendIntent(owned)
	if err != nil {
		return domain.Session{}, nil, storeContractViolation(err)
	}
	receipt, err := store.Append(ctx, requestForStore.Request)
	if !isNilValue(err) {
		return domain.Session{}, nil, mapV2StoreError(ctx, err, "append")
	}
	return ApplyCommittedIntent(state, owned, receipt)
}

func validateCommitReceipt(intent AppendIntent, receipt CommitReceipt) error {
	if receipt.AppendID != intent.Request.AppendID || receipt.CommitPosition == 0 || receipt.FirstSequence != intent.Request.ExpectedVersion+1 || receipt.LastSequence != intent.Request.ExpectedVersion+uint64(len(intent.Request.Events)) {
		return fmt.Errorf("invalid append receipt")
	}
	return nil
}

func ApplyCommittedIntent(state domain.Session, intent AppendIntent, receipt CommitReceipt) (domain.Session, []domain.RecordedEvent, error) {
	owned, err := cloneAppendIntent(intent)
	if err != nil {
		return domain.Session{}, nil, storeContractViolation(err)
	}
	if err := validateCommitReceipt(owned, receipt); err != nil {
		return domain.Session{}, nil, storeContractViolation(err)
	}
	records := make([]domain.RecordedEvent, len(owned.Request.Events))
	next := state.Clone()
	for index, proposed := range owned.Request.Events {
		event, cloneErr := domain.CloneEvent(proposed.Event)
		if cloneErr != nil {
			return domain.Session{}, nil, storeContractViolation(cloneErr)
		}
		record := domain.RecordedEvent{SchemaVersion: int(proposed.SchemaVersion), ID: proposed.ID, CommandID: owned.Request.CommandID, SessionID: owned.Request.SessionID, Sequence: receipt.FirstSequence + uint64(index), OccurredAt: proposed.OccurredAt, Event: event}
		applied, applyErr := domain.Apply(next, record)
		if applyErr != nil {
			return domain.Session{}, nil, storeContractViolation(applyErr)
		}
		next, records[index] = applied, record
	}
	defensive, err := domain.CloneRecordedEvents(records)
	if err != nil {
		return domain.Session{}, nil, storeContractViolation(err)
	}
	return next.Clone(), defensive, nil
}

func cloneAppendIntent(intent AppendIntent) (AppendIntent, error) {
	cloned := intent
	if intent.Request.Admission != nil {
		admission := *intent.Request.Admission
		cloned.Request.Admission = &admission
	}
	cloned.Request.Events = make([]ProposedEvent, len(intent.Request.Events))
	for index, proposed := range intent.Request.Events {
		event, err := domain.CloneEvent(proposed.Event)
		if err != nil {
			return AppendIntent{}, err
		}
		cloned.Request.Events[index] = proposed
		cloned.Request.Events[index].Event = event
	}
	return cloned, nil
}

func appendCompact(ctx context.Context, service *Service, sessionID domain.SessionID, state domain.Session, events []domain.UncommittedEvent, commandID domain.CommandID, admission *CommandAdmission) (domain.Session, []domain.RecordedEvent, error) {
	intent, err := BuildAppendIntent(service.clock, service.ids, service.authority, sessionID, state.Version, commandID, admission, events)
	if err != nil {
		return domain.Session{}, nil, err
	}
	return CommitAppendIntent(ctx, service.store, state, intent)
}
