package application

import (
	"fmt"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

type StoreErrorCode string

const (
	StoreCodeInvalidRead             StoreErrorCode = "invalid_read"
	StoreCodeInvalidAppend           StoreErrorCode = "invalid_append"
	StoreCodeVersionConflict         StoreErrorCode = "version_conflict"
	StoreCodeAppendIdentityMismatch  StoreErrorCode = "append_identity_mismatch"
	StoreCodeCommandRequestConflict  StoreErrorCode = "command_request_conflict"
	StoreCodeCommandIdentityMismatch StoreErrorCode = "command_identity_mismatch"
	StoreCodeDomainIdentityConflict  StoreErrorCode = "domain_identity_conflict"
	StoreCodeWriterFenced            StoreErrorCode = "writer_fenced"
	StoreCodeUnavailable             StoreErrorCode = "store_unavailable"
	StoreCodeCommitOutcomeUnknown    StoreErrorCode = "commit_outcome_unknown"
	StoreCodeCorrupt                 StoreErrorCode = "store_corrupt"
)

// StoreError contains stable store failure metadata. Cause is retained for
// errors.Is/errors.As but deliberately omitted from Error text.
type StoreError struct {
	Code             StoreErrorCode
	SessionID        domain.SessionID
	ExpectedVersion  uint64
	ActualVersion    uint64
	IdentityKind     string
	MayHaveCommitted bool
	Cause            error
}

func NewStoreError(err StoreError) (*StoreError, error) {
	if err.MayHaveCommitted && err.Code != StoreCodeCommitOutcomeUnknown {
		return nil, fmt.Errorf("store code %q cannot have an unknown commit outcome", err.Code)
	}
	return &err, nil
}

func (err *StoreError) Error() string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("store/%s (session=%s expected=%d actual=%d identity_kind=%s may_have_committed=%t)",
		err.Code, err.SessionID, err.ExpectedVersion, err.ActualVersion, err.IdentityKind, err.MayHaveCommitted)
}

func (err *StoreError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

// IsStoreCode reports whether the error tree contains the requested stable
// Store error code.
func IsStoreCode(err error, code StoreErrorCode) bool {
	if isNilValue(err) {
		return false
	}
	if storeErr, ok := err.(*StoreError); ok && storeErr != nil && storeErr.Code == code {
		return true
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		for _, child := range wrapped.Unwrap() {
			if IsStoreCode(child, code) {
				return true
			}
		}
	case interface{ Unwrap() error }:
		return IsStoreCode(wrapped.Unwrap(), code)
	}
	return false
}
