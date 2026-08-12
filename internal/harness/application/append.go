package application

import (
	"context"
	"errors"
	"math"
	"reflect"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func (service *Service) appendAndApply(ctx context.Context, sessionID domain.SessionID, state domain.Session, decided []domain.UncommittedEvent, commandID domain.CommandID) (domain.Session, []domain.RecordedEvent, error) {
	if len(decided) == 0 || uint64(len(decided)) > math.MaxUint64-state.Version {
		return domain.Session{}, nil, applicationError(CategoryInternal, "store_contract_violation", false, nil)
	}
	expectedEvents := make([]domain.Event, len(decided))
	for index, uncommitted := range decided {
		cloned, err := domain.CloneEvent(uncommitted.Event)
		if err != nil {
			return domain.Session{}, nil, applicationError(CategoryInternal, "store_contract_violation", false, err)
		}
		expectedEvents[index] = cloned
	}
	requestEvents := make([]domain.Event, len(expectedEvents))
	for index, expected := range expectedEvents {
		cloned, err := domain.CloneEvent(expected)
		if err != nil {
			return domain.Session{}, nil, applicationError(CategoryInternal, "store_contract_violation", false, err)
		}
		requestEvents[index] = cloned
	}
	if err := contextError(ctx); err != nil {
		return domain.Session{}, nil, err
	}
	records, err := service.store.Append(ctx, AppendRequest{
		SessionID:       sessionID,
		ExpectedVersion: state.Version,
		CommandID:       commandID,
		Events:          requestEvents,
	})
	if !isNilValue(err) {
		return domain.Session{}, nil, mapAppendError(ctx, err)
	}
	if len(records) != len(expectedEvents) {
		return domain.Session{}, nil, applicationError(CategoryInternal, "store_contract_violation", false, nil)
	}

	next := state.Clone()
	expectedFinalVersion := state.Version + uint64(len(expectedEvents))
	seenEventIDs := make(map[domain.EventID]struct{}, len(records))
	var occurredAt time.Time
	for index, record := range records {
		expectedSequence := state.Version + uint64(index) + 1
		if record.Sequence != expectedSequence || record.SessionID != sessionID || record.CommandID != commandID ||
			record.SchemaVersion != 1 || record.OccurredAt.IsZero() || record.OccurredAt.Location() != time.UTC ||
			!reflect.DeepEqual(record.Event, expectedEvents[index]) {
			return domain.Session{}, nil, applicationError(CategoryInternal, "store_contract_violation", false, nil)
		}
		if _, err := domain.ParseEventID(string(record.ID)); err != nil {
			return domain.Session{}, nil, applicationError(CategoryInternal, "store_contract_violation", false, err)
		}
		if _, duplicate := seenEventIDs[record.ID]; duplicate {
			return domain.Session{}, nil, applicationError(CategoryInternal, "store_contract_violation", false, nil)
		}
		seenEventIDs[record.ID] = struct{}{}
		if index == 0 {
			occurredAt = record.OccurredAt
		} else if record.OccurredAt != occurredAt {
			return domain.Session{}, nil, applicationError(CategoryInternal, "store_contract_violation", false, nil)
		}
		applied, applyErr := domain.Apply(next, record)
		if applyErr != nil {
			return domain.Session{}, nil, applicationError(CategoryInternal, "store_contract_violation", false, applyErr)
		}
		next = applied
	}
	if next.Version != expectedFinalVersion {
		return domain.Session{}, nil, applicationError(CategoryInternal, "store_contract_violation", false, nil)
	}
	defensiveRecords, err := domain.CloneRecordedEvents(records)
	if err != nil {
		return domain.Session{}, nil, applicationError(CategoryInternal, "store_contract_violation", false, err)
	}
	return next.Clone(), defensiveRecords, nil
}

func mapAppendError(ctx context.Context, cause error) error {
	if IsVersionConflict(cause) {
		return applicationError(CategoryConflict, "version_conflict", false, cause)
	}
	if err := contextError(ctx); err != nil {
		return applicationError(CategoryCanceled, "canceled", false, errors.Join(ctx.Err(), cause))
	}
	return applicationError(CategoryPersistence, "append_failed", false, cause)
}

func mapLoadError(ctx context.Context, cause error) error {
	if err := contextError(ctx); err != nil {
		return applicationError(CategoryCanceled, "canceled", false, errors.Join(ctx.Err(), cause))
	}
	return applicationError(CategoryPersistence, "load_failed", false, cause)
}

func contextError(ctx context.Context) error {
	if isNilValue(ctx) {
		return applicationError(CategoryValidation, "invalid_context", false, nil)
	}
	if cause := ctx.Err(); cause != nil {
		return applicationError(CategoryCanceled, "canceled", false, cause)
	}
	return nil
}
