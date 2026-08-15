package application

import (
	"fmt"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// ErrorCategory is a stable class of application-facing failure.
type ErrorCategory string

const (
	CategoryValidation  ErrorCategory = "validation"
	CategoryConflict    ErrorCategory = "conflict"
	CategoryModel       ErrorCategory = "model"
	CategoryCanceled    ErrorCategory = "canceled"
	CategoryOutputLimit ErrorCategory = "output_limit"
	CategoryDelivery    ErrorCategory = "delivery"
	CategoryPersistence ErrorCategory = "persistence"
	CategoryInternal    ErrorCategory = "internal"
)

const (
	CodeCommandIdentityMismatch = "command_identity_mismatch"
	CodeReconciliationRequired  = "reconciliation_required"
	CodeStepLimit               = "step_limit"
	CodeEnvelopeLimit           = "envelope_limit"
)

// Error is a stable application-facing failure. Cause remains available for
// deliberate programmatic inspection but is never rendered by Error.
type Error struct {
	Category          ErrorCategory
	Code              string
	TerminalCommitted bool
	Cause             error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s/%s (terminal_committed=%t)", e.Category, e.Code, e.TerminalCommitted)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// IsCategory reports whether the error tree contains an application Error
// with the requested stable category.
func IsCategory(err error, category ErrorCategory) bool {
	if isNilValue(err) {
		return false
	}
	if applicationError, ok := err.(*Error); ok {
		if applicationError.Category == category {
			return true
		}
	}

	switch err := err.(type) {
	case interface{ Unwrap() []error }:
		if isNilValue(err) {
			return false
		}
		for _, child := range err.Unwrap() {
			if IsCategory(child, category) {
				return true
			}
		}
	case interface{ Unwrap() error }:
		if isNilValue(err) {
			return false
		}
		return IsCategory(err.Unwrap(), category)
	}
	return false
}

// VersionConflictError reports an exact per-Session stream-version mismatch.
type VersionConflictError struct {
	SessionID       domain.SessionID
	ExpectedVersion uint64
	ActualVersion   uint64
}

func (e *VersionConflictError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf(
		"version conflict for session %s: expected %d, actual %d",
		e.SessionID,
		e.ExpectedVersion,
		e.ActualVersion,
	)
}

// IsVersionConflict reports whether the error chain contains a typed version
// conflict.
func IsVersionConflict(err error) bool {
	if isNilValue(err) {
		return false
	}
	if conflict, ok := err.(*VersionConflictError); ok {
		return conflict != nil
	}
	switch err := err.(type) {
	case interface{ Unwrap() []error }:
		if isNilValue(err) {
			return false
		}
		for _, child := range err.Unwrap() {
			if IsVersionConflict(child) {
				return true
			}
		}
	case interface{ Unwrap() error }:
		if isNilValue(err) {
			return false
		}
		return IsVersionConflict(err.Unwrap())
	}
	return false
}

func applicationError(category ErrorCategory, code string, terminalCommitted bool, cause error) *Error {
	return &Error{Category: category, Code: code, TerminalCommitted: terminalCommitted, Cause: cause}
}
