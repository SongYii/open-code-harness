package application

import (
	"errors"
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

// Error is a stable application-facing failure. Cause remains available for
// deliberate programmatic inspection but is never rendered by Error.
type Error struct {
	Category          ErrorCategory
	Code              string
	TerminalCommitted bool
	Cause             error
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s/%s (terminal_committed=%t)", e.Category, e.Code, e.TerminalCommitted)
}

func (e *Error) Unwrap() error { return e.Cause }

// IsCategory reports whether the error tree contains an application Error
// with the requested stable category.
func IsCategory(err error, category ErrorCategory) bool {
	if err == nil {
		return false
	}
	if applicationError, ok := err.(*Error); ok && applicationError != nil && applicationError.Category == category {
		return true
	}

	switch err := err.(type) {
	case interface{ Unwrap() error }:
		return IsCategory(err.Unwrap(), category)
	case interface{ Unwrap() []error }:
		for _, child := range err.Unwrap() {
			if IsCategory(child, category) {
				return true
			}
		}
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
	var conflict *VersionConflictError
	return errors.As(err, &conflict)
}
