package tools

import "errors"

type ErrorCode string

const (
	CodeInvalidSpec   ErrorCode = "invalid_spec"
	CodeInvalidArgs   ErrorCode = "invalid_args"
	CodeScopeDenied   ErrorCode = "scope_denied"
	CodeDuplicateName ErrorCode = "duplicate_name"

	CodeFilesystemNotObserved    ErrorCode = "fs_not_observed"
	CodeFilesystemNotFound       ErrorCode = "fs_not_found"
	CodeFilesystemStaleVersion   ErrorCode = "fs_stale_version"
	CodeFilesystemEditNotFound   ErrorCode = "fs_edit_not_found"
	CodeFilesystemAmbiguousEdit  ErrorCode = "fs_ambiguous_edit"
	CodeFilesystemNotRegularFile ErrorCode = "fs_not_regular_file"
	CodeFilesystemNotText        ErrorCode = "fs_not_text"
	CodeFilesystemTooLarge       ErrorCode = "fs_too_large"
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
		CodeFilesystemNotObserved, CodeFilesystemNotFound, CodeFilesystemStaleVersion,
		CodeFilesystemEditNotFound, CodeFilesystemAmbiguousEdit,
		CodeFilesystemNotRegularFile, CodeFilesystemNotText, CodeFilesystemTooLarge:
		return true
	default:
		return false
	}
}

func specError() error { return &Error{Code: CodeInvalidSpec} }

func argsError() error { return &Error{Code: CodeInvalidArgs} }

func duplicateNameError() error { return &Error{Code: CodeDuplicateName} }
