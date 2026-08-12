package engine

import (
	"context"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// RunRequest describes one bounded streamed model request.
type RunRequest struct {
	ModelRequest
	MaxAssistantBytes int
}

// RunResult is the exactly concatenated assistant output from a completed run.
type RunResult struct {
	Text string
}

// TurnRunner synchronously consumes one ModelStream at a time per Run call.
// It creates no goroutines or channels.
type TurnRunner struct {
	model Model
}

// NewTurnRunner constructs a runner around one provider-neutral model.
func NewTurnRunner(model Model) (*TurnRunner, error) {
	if isNil(model) {
		return nil, engineError(CodeInvalidRequest, nil)
	}
	return &TurnRunner{model: model}, nil
}

// Run acquires, consumes, and closes one bounded model stream synchronously.
func (runner *TurnRunner) Run(ctx context.Context, request RunRequest, emitter *Emitter) (RunResult, error) {
	if runner == nil || isNil(runner.model) || isNil(emitter) || !validRunRequest(request) || isNil(ctx) {
		return RunResult{}, engineError(CodeInvalidRequest, nil)
	}
	if cause := ctx.Err(); cause != nil {
		return RunResult{}, engineError(CodeCanceled, cause)
	}

	streamCtx, cancel := context.WithCancel(ctx)
	stream, streamErr := runner.model.Stream(streamCtx, request.ModelRequest)
	if isNil(stream) {
		if cause := ctx.Err(); cause != nil {
			cancel()
			return RunResult{}, engineError(CodeCanceled, cause)
		}
		cancel()
		if streamErr != nil {
			return RunResult{}, engineError(CodeModelStartup, errorCause(streamErr, CodeModelStartup))
		}
		return RunResult{}, engineError(CodeInvalidStream, nil)
	}

	if cause := ctx.Err(); cause != nil {
		return runner.fail(cancel, stream, engineError(CodeCanceled, cause))
	}
	if streamErr != nil {
		return runner.fail(cancel, stream, engineError(CodeModelStartup, errorCause(streamErr, CodeModelStartup)))
	}
	if err := emitter.Emit(streamCtx, RuntimePayload{Type: RuntimeModelStreamStarted}); err != nil {
		if cause := ctx.Err(); cause != nil {
			return runner.fail(cancel, stream, engineError(CodeCanceled, cause))
		}
		return runner.fail(cancel, stream, engineError(CodeDelivery, errorCause(err, CodeDelivery)))
	}

	var builder strings.Builder
	for {
		if cause := ctx.Err(); cause != nil {
			return runner.fail(cancel, stream, engineError(CodeCanceled, cause))
		}
		event, err := stream.Next(streamCtx)
		if err != nil {
			if cause := ctx.Err(); cause != nil {
				return runner.fail(cancel, stream, engineError(CodeCanceled, cause))
			}
			if errors.Is(err, io.EOF) {
				return runner.fail(cancel, stream, engineError(CodeInvalidStream, nil))
			}
			return runner.fail(cancel, stream, engineError(CodeModelStream, errorCause(err, CodeModelStream)))
		}
		if cause := ctx.Err(); cause != nil {
			return runner.fail(cancel, stream, engineError(CodeCanceled, cause))
		}

		switch event.Type {
		case StreamEventTextDelta:
			if event.Text == "" || !utf8.ValidString(event.Text) {
				return runner.fail(cancel, stream, engineError(CodeInvalidStream, nil))
			}
			if len(event.Text) > request.MaxAssistantBytes-builder.Len() {
				return runner.fail(cancel, stream, engineError(CodeOutputLimit, ErrAssistantOutputLimit))
			}
			if err := emitter.Emit(streamCtx, RuntimePayload{Type: RuntimeModelTextDelta, Text: event.Text}); err != nil {
				if cause := ctx.Err(); cause != nil {
					return runner.fail(cancel, stream, engineError(CodeCanceled, cause))
				}
				return runner.fail(cancel, stream, engineError(CodeDelivery, errorCause(err, CodeDelivery)))
			}
			builder.WriteString(event.Text)
		case StreamEventCompleted:
			if event.Text != "" {
				return runner.fail(cancel, stream, engineError(CodeInvalidStream, nil))
			}
			return runner.succeed(cancel, stream, RunResult{Text: builder.String()})
		default:
			return runner.fail(cancel, stream, engineError(CodeInvalidStream, nil))
		}
	}
}

func (runner *TurnRunner) fail(cancel context.CancelFunc, stream ModelStream, primary *Error) (RunResult, error) {
	cancel()
	if closeErr := stream.Close(); closeErr != nil {
		return RunResult{}, &Error{Code: primary.Code, Cause: errors.Join(primary.Cause, errorCause(closeErr, CodeModelStream))}
	}
	return RunResult{}, primary
}

func (runner *TurnRunner) succeed(cancel context.CancelFunc, stream ModelStream, result RunResult) (RunResult, error) {
	if closeErr := stream.Close(); closeErr != nil {
		cancel()
		return RunResult{}, engineError(CodeModelStream, errorCause(closeErr, CodeModelStream))
	}
	cancel()
	return result, nil
}

func validRunRequest(request RunRequest) bool {
	if request.MaxAssistantBytes <= 0 || !utf8.ValidString(request.Input) || strings.TrimSpace(request.Input) == "" {
		return false
	}
	_, sessionErr := domain.ParseSessionID(string(request.SessionID))
	_, turnErr := domain.ParseTurnID(string(request.TurnID))
	_, itemErr := domain.ParseItemID(string(request.ItemID))
	return sessionErr == nil && turnErr == nil && itemErr == nil
}

func engineError(code ErrorCode, cause error) *Error {
	if cause == nil {
		cause = sentinelForCode(code)
	}
	return &Error{Code: code, Cause: cause}
}

func errorCause(err error, code ErrorCode) error {
	if cause := nonEngineCause(err); cause != nil {
		return cause
	}
	return sentinelForCode(code)
}

func nonEngineCause(err error) error {
	if err == nil {
		return nil
	}
	if engineErr, ok := err.(*Error); ok {
		if engineErr == nil {
			return nil
		}
		return nonEngineCause(engineErr.Cause)
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		causes := make([]error, 0, len(joined.Unwrap()))
		for _, cause := range joined.Unwrap() {
			if cause := nonEngineCause(cause); cause != nil {
				causes = append(causes, cause)
			}
		}
		return errors.Join(causes...)
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return nonEngineCause(wrapped.Unwrap())
	}
	return err
}

func sentinelForCode(code ErrorCode) error {
	switch code {
	case CodeInvalidRequest:
		return errInvalidRunRequest
	case CodeModelStartup:
		return errModelStartup
	case CodeModelStream:
		return errModelStream
	case CodeCanceled:
		return errRunCanceled
	case CodeOutputLimit:
		return ErrAssistantOutputLimit
	case CodeDelivery:
		return errDelivery
	case CodeInvalidStream:
		return errInvalidStream
	default:
		return errEngineFailure
	}
}

var (
	errInvalidRunRequest = errors.New("invalid run request")
	errModelStartup      = errors.New("model startup failed")
	errModelStream       = errors.New("model stream failed")
	errRunCanceled       = errors.New("run canceled")
	errDelivery          = errors.New("runtime delivery failed")
	errInvalidStream     = errors.New("invalid model stream")
	errEngineFailure     = errors.New("engine failure")

	// ErrAssistantOutputLimit is the stable cause for an assistant byte-bound failure.
	ErrAssistantOutputLimit = errors.New("assistant output exceeds configured byte limit")
)
