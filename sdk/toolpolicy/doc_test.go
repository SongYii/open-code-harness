package toolpolicy_test

import (
	"context"
	"encoding/json"

	"github.com/SongYii/open-code-harness/sdk/toolpolicy"
)

type staticPolicy struct{}

func (staticPolicy) Decide(context.Context, toolpolicy.Input) (toolpolicy.Decision, error) {
	return toolpolicy.Decision{Effect: toolpolicy.EffectDeny, RuleID: "example.deny", Reason: "example"}, nil
}

var _ toolpolicy.Policy = staticPolicy{}

var _ = toolpolicy.Registration{
	ID:      "example",
	Version: "1.0.0",
	Factory: func(json.RawMessage) (toolpolicy.Policy, error) { return staticPolicy{}, nil },
}

var _ = toolpolicy.Input{
	Name:        "write_file",
	Risk:        toolpolicy.RiskWrite,
	Mutates:     true,
	WorkspaceIn: true,
	PathLiteral: "out.txt",
}
