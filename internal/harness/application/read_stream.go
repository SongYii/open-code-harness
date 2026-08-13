package application

import (
	"context"
	"fmt"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// ReadWholeStreamPinned reads a complete immutable view of a stream using the
// EV2-08 pinned-head protocol. Returned records are detached from the Store.
func ReadWholeStreamPinned(ctx context.Context, store EventStoreV2, sessionID domain.SessionID, limit uint32) ([]domain.RecordedEvent, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if isNilValue(store) || limit == 0 || limit > 256 {
		return nil, applicationError(CategoryValidation, "invalid_request", false, nil)
	}
	if _, err := domain.ParseSessionID(string(sessionID)); err != nil {
		return nil, applicationError(CategoryValidation, "invalid_request", false, err)
	}

	var all []domain.RecordedEvent
	var cursor uint64
	var head *uint64
	for {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		request := ReadStreamRequest{SessionID: sessionID, AfterSequence: cursor, Limit: limit, HeadVersion: head}
		page, err := store.ReadStream(ctx, request)
		if !isNilValue(err) {
			return nil, mapV2StoreError(ctx, err, "read")
		}
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		if head == nil {
			value := page.HeadVersion
			head = &value
		} else if page.HeadVersion != *head {
			return nil, storeContractViolation(fmt.Errorf("read head changed from %d to %d", *head, page.HeadVersion))
		}
		if page.HeadVersion < cursor || uint32(len(page.Records)) > limit {
			return nil, storeContractViolation(fmt.Errorf("invalid pinned page bounds"))
		}
		next := cursor
		for index, record := range page.Records {
			expected := cursor + uint64(index) + 1
			if record.SessionID != sessionID || record.Sequence != expected || record.Sequence > page.HeadVersion {
				return nil, storeContractViolation(fmt.Errorf("invalid record at index %d", index))
			}
			cloned, cloneErr := domain.CloneRecordedEvents([]domain.RecordedEvent{record})
			if cloneErr != nil {
				return nil, storeContractViolation(cloneErr)
			}
			all = append(all, cloned[0])
			next = record.Sequence
		}
		if page.NextAfterSequence != next || page.End != (next == page.HeadVersion) {
			return nil, storeContractViolation(fmt.Errorf("invalid pinned page cursor or end"))
		}
		if page.End {
			return all, nil
		}
		if next == cursor {
			return nil, storeContractViolation(fmt.Errorf("non-terminal page made no progress"))
		}
		cursor = next
	}
}

// loadCompactSessionPinned replays each page as it arrives, retaining only the
// bounded compact aggregate rather than a second copy of the whole stream.
func loadCompactSessionPinned(ctx context.Context, store EventStoreV2, sessionID domain.SessionID) (domain.CompactSession, error) {
	if err := contextError(ctx); err != nil {
		return domain.CompactSession{}, err
	}
	if isNilValue(store) {
		return domain.CompactSession{}, applicationError(CategoryValidation, "invalid_request", false, nil)
	}
	if _, err := domain.ParseSessionID(string(sessionID)); err != nil {
		return domain.CompactSession{}, applicationError(CategoryValidation, "invalid_request", false, err)
	}
	var state domain.CompactSession
	var cursor uint64
	var head *uint64
	for {
		if err := contextError(ctx); err != nil {
			return domain.CompactSession{}, err
		}
		page, err := store.ReadStream(ctx, ReadStreamRequest{SessionID: sessionID, AfterSequence: cursor, Limit: 256, HeadVersion: head})
		if !isNilValue(err) {
			return domain.CompactSession{}, mapV2StoreError(ctx, err, "read")
		}
		if head == nil {
			value := page.HeadVersion
			head = &value
		} else if page.HeadVersion != *head {
			return domain.CompactSession{}, storeContractViolation(fmt.Errorf("read head changed"))
		}
		if page.HeadVersion < cursor || len(page.Records) > 256 {
			return domain.CompactSession{}, storeContractViolation(fmt.Errorf("invalid pinned page bounds"))
		}
		next := cursor
		for index, record := range page.Records {
			if record.SessionID != sessionID || record.Sequence != cursor+uint64(index)+1 || record.Sequence > page.HeadVersion {
				return domain.CompactSession{}, storeContractViolation(fmt.Errorf("invalid record"))
			}
			applied, applyErr := domain.ApplyCompact(state, record)
			if applyErr != nil {
				return domain.CompactSession{}, storeContractViolation(applyErr)
			}
			state = applied
			next = record.Sequence
		}
		if page.NextAfterSequence != next || page.End != (next == page.HeadVersion) {
			return domain.CompactSession{}, storeContractViolation(fmt.Errorf("invalid pinned page cursor or end"))
		}
		if page.End {
			break
		}
		if next == cursor {
			return domain.CompactSession{}, storeContractViolation(fmt.Errorf("non-terminal page made no progress"))
		}
		cursor = next
	}
	if !state.Exists() {
		return domain.CompactSession{}, applicationError(CategoryValidation, "session_not_found", false, nil)
	}
	if state.ID != sessionID {
		return domain.CompactSession{}, storeContractViolation(fmt.Errorf("replayed session mismatch"))
	}
	return state.Clone(), nil
}

func storeContractViolation(cause error) error {
	return applicationError(CategoryInternal, "store_contract_violation", false, cause)
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

func mapV2StoreError(ctx context.Context, cause error, operation string) error {
	if err := contextError(ctx); err != nil {
		return applicationError(CategoryCanceled, "canceled", false, fmt.Errorf("%w: %w", err, cause))
	}
	if IsStoreCode(cause, StoreCodeVersionConflict) || IsVersionConflict(cause) {
		return applicationError(CategoryConflict, "version_conflict", false, cause)
	}
	if IsStoreCode(cause, StoreCodeAppendIdentityMismatch) || IsStoreCode(cause, StoreCodeCommandIdentityMismatch) || IsStoreCode(cause, StoreCodeCommandRequestConflict) || IsStoreCode(cause, StoreCodeDomainIdentityConflict) {
		return applicationError(CategoryConflict, string(storeCode(cause)), false, cause)
	}
	if IsStoreCode(cause, StoreCodeCommitOutcomeUnknown) {
		return applicationError(CategoryPersistence, "append_outcome_unknown", false, cause)
	}
	if IsStoreCode(cause, StoreCodeCorrupt) {
		return storeContractViolation(cause)
	}
	if operation == "read" {
		return applicationError(CategoryPersistence, "load_failed", false, cause)
	}
	return applicationError(CategoryPersistence, "append_failed", false, cause)
}

func storeCode(cause error) StoreErrorCode {
	for _, code := range []StoreErrorCode{StoreCodeAppendIdentityMismatch, StoreCodeCommandIdentityMismatch, StoreCodeCommandRequestConflict, StoreCodeDomainIdentityConflict} {
		if IsStoreCode(cause, code) {
			return code
		}
	}
	return ""
}
