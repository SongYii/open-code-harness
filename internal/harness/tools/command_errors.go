package tools

import "errors"

// These port errors distinguish an uncertain invocation from invalid model
// arguments. Callers must end the Turn instead of asking the model to retry.
var (
	ErrCommandOutputIncomplete = errors.New("tools: command output was not fully captured")
	ErrCommandWaitUnproven     = errors.New("tools: command wait outcome could not be proven")
	ErrCommandTeardownUnproven = errors.New("localexec: process teardown could not be proven")
)
