package tools

import "errors"

type ErrorCode string

const (
	CodeInvalidSpec   ErrorCode = "invalid_spec"
	CodeInvalidArgs   ErrorCode = "invalid_args"
	CodeScopeDenied   ErrorCode = "scope_denied"
	CodeDuplicateName ErrorCode = "duplicate_name"
)

// Filesystem codes for guarded mutation.
//
// Each names a distinct thing a caller can act on, and the distinctions are
// the point. "You never read this file" and "you read it and it changed
// underneath you" call for different next steps -- read it, versus read it
// again and reconsider -- so they may not collapse into one "write refused".
const (
	// CodeFSNotObserved: a destructive change was attempted against a file
	// this session never read. Freshness is not authorization, but it is a
	// precondition: an agent may not overwrite what it has not looked at.
	CodeFSNotObserved ErrorCode = "fs_not_observed"

	// CodeFSNotFound: an edit targeted a file this session read and observed
	// absent. Reading again cannot reveal text to edit unless it first appears.
	CodeFSNotFound ErrorCode = "fs_not_found"

	// CodeFSStaleVersion: the target no longer carries the version the guard
	// named. Something else wrote in between, and the change is refused
	// rather than applied over the other writer.
	CodeFSStaleVersion ErrorCode = "fs_stale_version"

	// CodeFSEditNotFound: the literal an edit searched for is not present.
	CodeFSEditNotFound ErrorCode = "fs_edit_not_found"

	// CodeFSAmbiguousEdit: the literal occurs more than once and the caller
	// did not ask for every occurrence. Editing an arbitrary one of several
	// matches is a silent wrong answer, so it is refused.
	CodeFSAmbiguousEdit ErrorCode = "fs_ambiguous_edit"

	// CodeFSNotRegularFile: the target is a directory, device, socket, or
	// similar. These are refused before any mutation is staged.
	CodeFSNotRegularFile ErrorCode = "fs_not_regular_file"

	// CodeFSNotText: the content is not valid UTF-8. A literal edit over
	// arbitrary bytes would corrupt the file it claims to be editing.
	CodeFSNotText ErrorCode = "fs_not_text"

	// CodeFSTooLarge: the file exceeds MaxEditFileBytes.
	CodeFSTooLarge ErrorCode = "fs_too_large"
)

// ErrOutOfScope is returned by FileSystem.Resolve when the real path leaves
// the workspace. Error() is the stable code only.
var ErrOutOfScope = &Error{Code: CodeScopeDenied}

// Error carries a stable tools code. Error() never includes paths, args, or
// schema text.
type Error struct {
	Code ErrorCode
}

func (err *Error) Error() string {
	if err == nil {
		return "<nil>"
	}
	return "tools/" + string(err.Code)
}

func (err *Error) Is(target error) bool {
	if err == nil {
		return false
	}
	wanted, ok := target.(*Error)
	return ok && wanted != nil && validErrorCode(wanted.Code) && err.Code == wanted.Code
}

func IsCode(err error, wanted ErrorCode) bool {
	if err == nil || !validErrorCode(wanted) {
		return false
	}
	return errors.Is(err, &Error{Code: wanted})
}

func validErrorCode(code ErrorCode) bool {
	switch code {
	case CodeInvalidSpec, CodeInvalidArgs, CodeScopeDenied, CodeDuplicateName,
		CodeFSNotObserved, CodeFSNotFound, CodeFSStaleVersion, CodeFSEditNotFound, CodeFSAmbiguousEdit,
		CodeFSNotRegularFile, CodeFSNotText, CodeFSTooLarge:
		return true
	default:
		return false
	}
}

func specError() error { return &Error{Code: CodeInvalidSpec} }

func argsError() error { return &Error{Code: CodeInvalidArgs} }

func duplicateNameError() error { return &Error{Code: CodeDuplicateName} }
