// A separately compiled launcher: deliberately imports no internal packages.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/sdk/och"
	"github.com/SongYii/open-code-harness/sdk/toolpolicy"
)

const (
	policyID      = "deny_tools"
	policyVersion = "1.0.0"
	maxNames      = 64
	maxNameBytes  = 128
)

type config struct {
	Names []string `json:"names"`
}

type denyTools struct {
	names map[string]struct{}
}

func newPolicy(raw json.RawMessage) (toolpolicy.Policy, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var parsed config
	if err := decoder.Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode deny_tools config: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	if len(parsed.Names) > maxNames {
		return nil, fmt.Errorf("deny_tools config has more than %d names", maxNames)
	}
	names := make(map[string]struct{}, len(parsed.Names))
	for _, name := range parsed.Names {
		if strings.TrimSpace(name) == "" || !utf8.ValidString(name) || len(name) > maxNameBytes {
			return nil, fmt.Errorf("deny_tools config contains an invalid name")
		}
		if _, exists := names[name]; exists {
			return nil, fmt.Errorf("deny_tools config contains duplicate name %q", name)
		}
		names[name] = struct{}{}
	}
	return denyTools{names: names}, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("deny_tools config contains a trailing JSON value")
		}
		return fmt.Errorf("decode deny_tools config: %w", err)
	}
	return nil
}

func (policy denyTools) Decide(ctx context.Context, input toolpolicy.Input) (toolpolicy.Decision, error) {
	if err := ctx.Err(); err != nil {
		return toolpolicy.Decision{}, err
	}
	if _, denied := policy.names[input.Name]; denied {
		return toolpolicy.Decision{Effect: toolpolicy.EffectDeny, RuleID: "deny_tools.configured_name", Reason: "configured_deny"}, nil
	}
	switch input.Risk {
	case toolpolicy.RiskRead:
		return toolpolicy.Decision{Effect: toolpolicy.EffectAllow, RuleID: "default.read_allow", Reason: "in_workspace"}, nil
	case toolpolicy.RiskWrite:
		return toolpolicy.Decision{Effect: toolpolicy.EffectRequireApproval, RuleID: "default.write_requires_approval", Reason: "in_workspace"}, nil
	case toolpolicy.RiskExec:
		return toolpolicy.Decision{Effect: toolpolicy.EffectRequireApproval, RuleID: "default.exec_requires_approval", Reason: "in_workspace"}, nil
	default:
		return toolpolicy.Decision{}, fmt.Errorf("deny_tools received unknown risk %q", input.Risk)
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := och.Run(ctx, os.Args[1:], och.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}, och.Extensions{
		ToolPolicies: []toolpolicy.Registration{{ID: policyID, Version: policyVersion, Factory: newPolicy}},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
