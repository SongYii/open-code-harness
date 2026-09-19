package composition

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	corepolicy "github.com/SongYii/open-code-harness/internal/harness/policy"
	"github.com/SongYii/open-code-harness/sdk/toolpolicy"
)

type publicScriptedPolicy struct {
	decision toolpolicy.Decision
	err      error
	input    toolpolicy.Input
}

func (policy *publicScriptedPolicy) Decide(_ context.Context, input toolpolicy.Input) (toolpolicy.Decision, error) {
	policy.input = input
	return policy.decision, policy.err
}

func TestResolveToolPolicyFreezesIdentityAndConfig(t *testing.T) {
	var received []byte
	implementation := &publicScriptedPolicy{}
	registration := toolpolicy.Registration{
		ID: "deny_tools", Version: "1.0.0",
		Factory: func(raw json.RawMessage) (toolpolicy.Policy, error) {
			received = raw
			return implementation, nil
		},
	}
	config := Config{
		Policy:       corepolicy.ModeDefault,
		ToolPolicy:   ToolPolicy{ID: "deny_tools", Version: "1.0.0", Config: `{"b":2,"a":1}`},
		ToolPolicies: []toolpolicy.Registration{registration},
	}
	strategy, identity, err := resolveToolPolicy(config)
	if err != nil || strategy == nil || identity == nil {
		t.Fatalf("resolve = %#v %#v %v", strategy, identity, err)
	}
	if string(received) != `{"a":1,"b":2}` || identity.ID != "deny_tools" || identity.Version != "1.0.0" || len(identity.ConfigDigest) != 64 {
		t.Fatalf("config=%s identity=%#v", received, identity)
	}
	wantIdentity := *identity
	received[0] = '['
	config.ToolPolicy.Config = `{"mutated":true}`
	if *identity != wantIdentity {
		t.Fatalf("identity changed after factory/caller mutation: %#v", identity)
	}

	implementation.decision = toolpolicy.Decision{Effect: toolpolicy.EffectRequireApproval, RuleID: "deny_tools.exec", Reason: "configured"}
	decision, err := strategy.Decide(context.Background(), corepolicy.Input{Name: "exec", Risk: "exec", Mutates: true, WorkspaceIn: true, PathLiteral: "bin/tool"})
	if err != nil || decision != (corepolicy.Decision{Effect: corepolicy.EffectRequireApproval, RuleID: "deny_tools.exec", Reason: "configured"}) {
		t.Fatalf("adapter decision = %#v, %v", decision, err)
	}
	wantInput := toolpolicy.Input{Name: "exec", Risk: toolpolicy.RiskExec, Mutates: true, WorkspaceIn: true, PathLiteral: "bin/tool"}
	if !reflect.DeepEqual(implementation.input, wantInput) {
		t.Fatalf("adapter input = %#v, want %#v", implementation.input, wantInput)
	}
}

func TestResolveToolPolicyFailsClosed(t *testing.T) {
	valid := toolpolicy.Registration{ID: "deny_tools", Version: "1.0.0", Factory: func(json.RawMessage) (toolpolicy.Policy, error) { return &publicScriptedPolicy{}, nil }}
	var typedNil *publicScriptedPolicy
	tests := []struct {
		name   string
		config Config
	}{
		{name: "duplicate IDs", config: Config{ToolPolicies: []toolpolicy.Registration{valid, valid}}},
		{name: "reserved builtin registration", config: Config{ToolPolicies: []toolpolicy.Registration{{ID: toolpolicy.DefaultID, Version: "1", Factory: valid.Factory}}}},
		{name: "invalid registration ID", config: Config{ToolPolicies: []toolpolicy.Registration{{ID: "deny/tools", Version: "1", Factory: valid.Factory}}}},
		{name: "invalid registration version", config: Config{ToolPolicies: []toolpolicy.Registration{{ID: "deny_tools", Version: "bad version", Factory: valid.Factory}}}},
		{name: "nil factory", config: Config{ToolPolicies: []toolpolicy.Registration{{ID: "deny_tools", Version: "1"}}}},
		{name: "invalid unselected registration", config: Config{ToolPolicies: []toolpolicy.Registration{valid, {ID: "bad/id", Version: "1", Factory: valid.Factory}}}},
		{name: "unknown selection", config: Config{ToolPolicy: ToolPolicy{ID: "missing"}, ToolPolicies: []toolpolicy.Registration{valid}}},
		{name: "version mismatch", config: Config{ToolPolicy: ToolPolicy{ID: valid.ID, Version: "2"}, ToolPolicies: []toolpolicy.Registration{valid}}},
		{name: "nil policy", config: Config{ToolPolicy: ToolPolicy{ID: valid.ID}, ToolPolicies: []toolpolicy.Registration{{ID: valid.ID, Version: valid.Version, Factory: func(json.RawMessage) (toolpolicy.Policy, error) { return nil, nil }}}}},
		{name: "typed nil policy", config: Config{ToolPolicy: ToolPolicy{ID: valid.ID}, ToolPolicies: []toolpolicy.Registration{{ID: valid.ID, Version: valid.Version, Factory: func(json.RawMessage) (toolpolicy.Policy, error) { return typedNil, nil }}}}},
		{name: "factory error", config: Config{ToolPolicy: ToolPolicy{ID: valid.ID}, ToolPolicies: []toolpolicy.Registration{{ID: valid.ID, Version: valid.Version, Factory: func(json.RawMessage) (toolpolicy.Policy, error) { return nil, errors.New("factory failed") }}}}},
		{name: "builtin config", config: Config{ToolPolicy: ToolPolicy{Config: `{"deny":true}`}}},
		{name: "builtin version", config: Config{ToolPolicy: ToolPolicy{Version: "1"}}},
		{name: "custom read-only mode", config: Config{Policy: corepolicy.ModeReadOnly, ToolPolicy: ToolPolicy{ID: valid.ID}, ToolPolicies: []toolpolicy.Registration{valid}}},
		{name: "duplicate config key", config: Config{ToolPolicy: ToolPolicy{ID: valid.ID, Config: `{"a":1,"a":2}`}, ToolPolicies: []toolpolicy.Registration{valid}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if strategy, identity, err := resolveToolPolicy(test.config); err == nil || strategy != nil || identity != nil {
				t.Fatalf("resolve = %#v %#v %v", strategy, identity, err)
			}
		})
	}
}

func TestResolveToolPolicyBuiltinPreservesLegacySelection(t *testing.T) {
	for _, id := range []string{"", toolpolicy.DefaultID} {
		strategy, identity, err := resolveToolPolicy(Config{ToolPolicy: ToolPolicy{ID: id}})
		if err != nil || strategy != nil || identity != nil {
			t.Fatalf("resolve(%q) = %#v %#v %v", id, strategy, identity, err)
		}
	}
	if _, _, err := resolveToolPolicy(Config{ToolPolicy: ToolPolicy{ID: strings.Repeat("x", 129)}}); err == nil {
		t.Fatal("oversized unknown selection accepted")
	}
}
