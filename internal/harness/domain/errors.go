package domain

import "errors"

type ErrorCode string

const (
	CodeInvalidID            ErrorCode = "invalid_id"
	CodeInvalidCommand       ErrorCode = "invalid_command"
	CodeInvalidEvent         ErrorCode = "invalid_event"
	CodeSessionAlreadyExists ErrorCode = "session_already_exists"
	CodeSessionNotFound      ErrorCode = "session_not_found"
	CodeSessionClosed        ErrorCode = "session_closed"
	CodeTurnAlreadyRunning   ErrorCode = "turn_already_running"
	CodeTurnNotRunning       ErrorCode = "turn_not_running"
	CodeTurnMismatch         ErrorCode = "turn_mismatch"
	CodeTurnAlreadyExists    ErrorCode = "turn_already_exists"
	CodeSequenceMismatch     ErrorCode = "sequence_mismatch"
)

type DomainError struct {
	Code    ErrorCode
	Message string
}

func (e *DomainError) Error() string { return string(e.Code) + ": " + e.Message }

func IsCode(err error, code ErrorCode) bool {
	var target *DomainError
	return errors.As(err, &target) && target.Code == code
}
