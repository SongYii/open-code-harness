package engine

import (
	"errors"
	"fmt"
)

type ErrorCode string

const (
	CodeInvalidRequest ErrorCode = "invalid_request"
	CodeModelStartup   ErrorCode = "model_startup"
	CodeModelStream    ErrorCode = "model_stream"
	CodeCanceled       ErrorCode = "canceled"
	CodeOutputLimit    ErrorCode = "output_limit"
	CodeDelivery       ErrorCode = "delivery"
	CodeInvalidStream  ErrorCode = "invalid_stream"
)

// Error carries a stable Engine code while retaining Cause for explicit
// programmatic unwrapping. Its methods are safe on a typed-nil receiver.
type Error struct {
	Code  ErrorCode
	Cause error
}

func (err *Error) Error() string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("engine/%s", err.Code)
}

func (err *Error) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

func (err *Error) Is(target error) bool {
	if err == nil {
		return false
	}
	wanted, ok := target.(*Error)
	return ok && wanted != nil && validErrorCode(wanted.Code) && err.Code == wanted.Code
}

// IsCode traverses complete wrapped and joined trees without matching typed-nil
// *Error values.
func IsCode(err error, wanted ErrorCode) bool {
	if err == nil || !validErrorCode(wanted) {
		return false
	}
	return errors.Is(err, &Error{Code: wanted})
}

func validErrorCode(code ErrorCode) bool {
	switch code {
	case CodeInvalidRequest, CodeModelStartup, CodeModelStream, CodeCanceled, CodeOutputLimit, CodeDelivery, CodeInvalidStream:
		return true
	default:
		return false
	}
}
