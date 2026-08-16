package testkit

import (
	"context"
	"errors"
	"sync"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

type ScriptedRun struct {
	Result        tools.CommandResult
	Err           error
	WaitForCancel bool
	Entered       chan<- struct{}
	Release       <-chan struct{}
}

type ScriptedRunner struct {
	mu    sync.Mutex
	steps []ScriptedRun
	index int
	calls []tools.CommandSpec
}

func NewScriptedRunner(steps ...ScriptedRun) *ScriptedRunner {
	return &ScriptedRunner{steps: append([]ScriptedRun(nil), steps...)}
}

func (runner *ScriptedRunner) Run(ctx context.Context, spec tools.CommandSpec) (tools.CommandResult, error) {
	recorded := cloneCommandSpec(spec)
	runner.mu.Lock()
	if runner.index >= len(runner.steps) {
		runner.calls = append(runner.calls, recorded)
		runner.mu.Unlock()
		return tools.CommandResult{}, errors.New("script exhausted")
	}
	step := runner.steps[runner.index]
	runner.index++
	runner.calls = append(runner.calls, recorded)
	runner.mu.Unlock()

	if step.Entered != nil {
		step.Entered <- struct{}{}
	}
	if step.Release != nil {
		select {
		case <-step.Release:
		case <-ctx.Done():
			return tools.CommandResult{}, ctx.Err()
		}
	}
	if step.WaitForCancel {
		<-ctx.Done()
		return tools.CommandResult{}, ctx.Err()
	}
	return cloneCommandResult(step.Result), step.Err
}

func (runner *ScriptedRunner) Calls() []tools.CommandSpec {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	out := make([]tools.CommandSpec, len(runner.calls))
	for i, call := range runner.calls {
		out[i] = cloneCommandSpec(call)
	}
	return out
}

func cloneCommandSpec(spec tools.CommandSpec) tools.CommandSpec {
	spec.Argv = append([]string(nil), spec.Argv...)
	return spec
}

func cloneCommandResult(result tools.CommandResult) tools.CommandResult {
	return result
}

var _ tools.CommandRunner = (*ScriptedRunner)(nil)
