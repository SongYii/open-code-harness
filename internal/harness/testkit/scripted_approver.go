package testkit

import (
	"context"
	"errors"
	"sync"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

type ScriptedApproval struct {
	Answer        tools.ApprovalAnswer
	Err           error
	WaitForCancel bool
	Entered       chan<- struct{}
	Release       <-chan struct{}
}

type ScriptedApprover struct {
	mu    sync.Mutex
	steps []ScriptedApproval
	index int
	calls []tools.ApprovalRequest
}

func NewScriptedApprover(steps ...ScriptedApproval) *ScriptedApprover {
	return &ScriptedApprover{steps: append([]ScriptedApproval(nil), steps...)}
}

func (approver *ScriptedApprover) Decide(ctx context.Context, req tools.ApprovalRequest) (tools.ApprovalAnswer, error) {
	approver.mu.Lock()
	approver.calls = append(approver.calls, req)
	if approver.index >= len(approver.steps) {
		approver.mu.Unlock()
		return tools.ApprovalAnswer{}, errors.New("script exhausted")
	}
	step := approver.steps[approver.index]
	approver.index++
	approver.mu.Unlock()

	if step.Entered != nil {
		step.Entered <- struct{}{}
	}
	if step.Release != nil {
		select {
		case <-step.Release:
		case <-ctx.Done():
			return tools.ApprovalAnswer{}, ctx.Err()
		}
	}
	if step.WaitForCancel {
		<-ctx.Done()
		return tools.ApprovalAnswer{}, ctx.Err()
	}
	return step.Answer, step.Err
}

func (approver *ScriptedApprover) Calls() []tools.ApprovalRequest {
	approver.mu.Lock()
	defer approver.mu.Unlock()
	return append([]tools.ApprovalRequest(nil), approver.calls...)
}

var _ tools.Approver = (*ScriptedApprover)(nil)
