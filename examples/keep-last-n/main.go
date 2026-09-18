// A separately compiled launcher: deliberately imports no internal packages.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/SongYii/open-code-harness/sdk/contextpolicy"
	"github.com/SongYii/open-code-harness/sdk/och"
	"github.com/SongYii/open-code-harness/sdk/toolpolicy"
)

type keepLast struct{ turns uint64 }

type denyExec struct{}

func (denyExec) Decide(_ context.Context, input toolpolicy.Input) (toolpolicy.Decision, error) {
	if input.Risk == toolpolicy.RiskExec {
		return toolpolicy.Decision{Effect: toolpolicy.EffectDeny, RuleID: "deny_exec.risk", Reason: "exec_disabled"}, nil
	}
	return toolpolicy.Decision{Effect: toolpolicy.EffectAllow, RuleID: "deny_exec.allow", Reason: "allowed"}, nil
}

func (policy keepLast) Plan(ctx context.Context, input contextpolicy.Input) (contextpolicy.Decision, error) {
	if err := ctx.Err(); err != nil {
		return contextpolicy.Decision{}, err
	}
	var chosen uint64
	for _, candidate := range input.Candidates {
		if candidate.RetainedTurns >= policy.turns {
			chosen = candidate.ID
		}
	}
	// The user retention preference may yield to forced recovery; the core's
	// protected floor NEVER does. Choose the least destructive safe cut.
	if chosen == 0 && input.Force && len(input.Candidates) > 0 {
		chosen = input.Candidates[0].ID
	}
	return contextpolicy.Decision{Compact: chosen != 0, CandidateID: chosen}, nil
}

func newPolicy(raw json.RawMessage) (contextpolicy.Policy, error) {
	var config struct {
		Turns uint64 `json:"turns"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, err
	}
	if config.Turns < 1 || config.Turns > 10000 {
		return nil, fmt.Errorf("turns must be 1..10000")
	}
	return keepLast{turns: config.Turns}, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := och.Run(ctx, os.Args[1:], och.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}, och.Extensions{
		ContextPolicies: []contextpolicy.Registration{{ID: "keep_last_n_turns", Version: "1.0.0", Factory: newPolicy}},
		ToolPolicies: []toolpolicy.Registration{{ID: "deny_exec", Version: "1.0.0", Factory: func(json.RawMessage) (toolpolicy.Policy, error) {
			return denyExec{}, nil
		}}},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
