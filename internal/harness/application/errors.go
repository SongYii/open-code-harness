package application

import (
	"fmt"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
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
	// CategoryPolicy is composition/config only. Tool denials are not RunTurn errors.
	CategoryPolicy ErrorCategory = "policy"
)

const (
	CodeCommandIdentityMismatch = "command_identity_mismatch"
	CodeReconciliationRequired  = "reconciliation_required"
	CodeStepLimit               = "step_limit"
	CodeEnvelopeLimit           = "envelope_limit"

	CodePolicyDenied    = "policy_denied"
	CodeApprovalDenied  = "approval_denied"
	CodeApprovalTimeout = "approval_timeout"
	CodeScopeDenied     = "scope_denied"
	CodeUnknownTool     = "unknown_tool"
	CodeInvalidArgs     = "invalid_args"
	CodeToolOutputLimit = "output_limit"
	CodeExecTimeout     = "exec_timeout"
	CodeResourceLimit   = "resource_limit"
	// CodeExternalToolFailed marks a tool that ran and reported its own
	// failure, as distinct from a call that could not reach it. The first is
	// an ordinary event inside a Turn that the model can read and react to;
	// the second ends the Turn.
	CodeExternalToolFailed = "external_tool_failed"
)

// Filesystem guard codes. Each mirrors a tools.ErrorCode one-for-one, and
// each carries a different instruction, which is the reason they are not one
// code.
//
// A model reads these and decides what to do next. "You never read this file"
// and "the file changed since you read it" are both refusals of the same
// write, but the first is answered by reading and the second by reading
// again and reconsidering whether the change still makes sense. Collapsing
// them into a generic failure leaves the model guessing, and a guessing model
// retries.
const (
	CodeFSNotObserved    = "fs_not_observed"
	CodeFSNotFound       = "fs_not_found"
	CodeFSStaleVersion   = "fs_stale_version"
	CodeFSEditNotFound   = "fs_edit_not_found"
	CodeFSAmbiguousEdit  = "fs_ambiguous_edit"
	CodeFSNotRegularFile = "fs_not_regular_file"
	CodeFSNotText        = "fs_not_text"
	CodeFSTooLarge       = "fs_too_large"
)

// Context Engine failure codes (design §16). The pre-existing
// "context_overflow" string (request_result.go, turn.go, openaicompat's
// classify.go) keeps its own meaning and is not redefined here; these are
// new.
const (
	CodeContextBudgetInvalid     = "context_budget_invalid"
	CodeContextProjectionInvalid = "context_projection_invalid"
	CodeContextUnitTooLarge      = "context_unit_too_large"
	CodeContextCompactionBusy    = "context_compaction_busy"
	CodeContextNothingToCompact  = "context_nothing_to_compact"
	CodeContextSummaryFailed     = "context_summary_failed"
	CodeContextSummaryInvalid    = "context_summary_invalid"
	CodeContextCheckpointInvalid = "context_checkpoint_invalid"
	CodeContextCompactionLimit   = "context_compaction_limit"
)

const (
	ToolTextPolicyDenied    = "policy denied this tool"
	ToolTextApprovalDenied  = "approval denied this tool"
	ToolTextApprovalTimeout = "approval timed out"
	ToolTextScopeDenied     = "path is outside the workspace"
	ToolTextUnknownTool     = "unknown tool"
	ToolTextInvalidArgs     = "invalid tool arguments"
	ToolTextOutputLimit     = "tool output exceeded the size limit"
	ToolTextExecTimeout     = "command timed out"
	ToolTextResourceLimit   = "command exceeded a resource limit"
	ToolTextExternalFailed  = "external tool reported a failure"
	TruncationMarker        = "\n[truncated]"
)

// Filesystem guard messages.
//
// Each says what to do next rather than only what went wrong, because the
// reader is a model choosing its next tool call. None of them renders a path,
// a version, or any file content: these strings reach the model verbatim, and
// the workspace layout is not theirs to learn from a failure message.
const (
	ToolTextFSNotObserved    = "read the file before changing it"
	ToolTextFSNotFound       = "file does not exist; create it or re-read after it appears"
	ToolTextFSStaleVersion   = "file changed since it was read; re-read it and retry"
	ToolTextFSEditNotFound   = "literal was not found"
	ToolTextFSAmbiguousEdit  = "literal appears more than once; include more context or use replace_all"
	ToolTextFSNotRegularFile = "target is not a regular file"
	ToolTextFSNotText        = "file is not valid UTF-8 text"
	ToolTextFSTooLarge       = "file exceeds the edit size limit"

	// A successful edit acknowledges itself in a sentence and does not copy
	// the resulting file back. Returning the file would spend exactly the
	// context budget an edit tool exists to save, and the model already knows
	// what it asked for.
	ToolTextEdited      = "edited file"
	ToolTextReplacedAll = "replaced all occurrences"
)

// classifyFilesystemError maps an adapter refusal to the Tool Result a model
// sees, or reports that this is not a filesystem guard failure at all.
//
// Anything unmapped keeps falling through to the caller's existing handling,
// so a genuine I/O error is never dressed up as a guard refusal.
// A raw fs.ErrExist is deliberately absent from this table. The adapter
// reports a create conflict as fs.ErrExist and cannot say more, because it
// does not know what the session looked at: "you never read this" and "you
// read this, found nothing, and something appeared" reach it identically.
// Only the observation table can tell them apart, so the write path resolves
// fs.ErrExist into one of the two codes before an error gets here. Classifying
// it blindly would tell a session that did read to read again.
func classifyFilesystemError(err error) (code string, text string, ok bool) {
	switch {
	case tools.IsCode(err, tools.CodeFSNotObserved):
		return CodeFSNotObserved, ToolTextFSNotObserved, true
	case tools.IsCode(err, tools.CodeFSNotFound):
		return CodeFSNotFound, ToolTextFSNotFound, true
	case tools.IsCode(err, tools.CodeFSStaleVersion):
		return CodeFSStaleVersion, ToolTextFSStaleVersion, true
	case tools.IsCode(err, tools.CodeFSEditNotFound):
		return CodeFSEditNotFound, ToolTextFSEditNotFound, true
	case tools.IsCode(err, tools.CodeFSAmbiguousEdit):
		return CodeFSAmbiguousEdit, ToolTextFSAmbiguousEdit, true
	case tools.IsCode(err, tools.CodeFSNotRegularFile):
		return CodeFSNotRegularFile, ToolTextFSNotRegularFile, true
	case tools.IsCode(err, tools.CodeFSNotText):
		return CodeFSNotText, ToolTextFSNotText, true
	case tools.IsCode(err, tools.CodeFSTooLarge):
		return CodeFSTooLarge, ToolTextFSTooLarge, true
	}
	return "", "", false
}

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
