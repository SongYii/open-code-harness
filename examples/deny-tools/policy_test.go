package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/sdk/toolpolicy"
)

func TestDenyToolsPolicy(t *testing.T) {
	policy, err := newPolicy(json.RawMessage(`{"names":["exec"]}`))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		input  toolpolicy.Input
		effect toolpolicy.Effect
		rule   string
		reason string
	}{
		{name: "configured", input: toolpolicy.Input{Name: "exec", Risk: toolpolicy.RiskExec}, effect: toolpolicy.EffectDeny, rule: "deny_tools.configured_name", reason: "configured_deny"},
		{name: "read default", input: toolpolicy.Input{Name: "read_file", Risk: toolpolicy.RiskRead}, effect: toolpolicy.EffectAllow, rule: "default.read_allow", reason: "in_workspace"},
		{name: "write default", input: toolpolicy.Input{Name: "write_file", Risk: toolpolicy.RiskWrite}, effect: toolpolicy.EffectRequireApproval, rule: "default.write_requires_approval", reason: "in_workspace"},
		{name: "exec default", input: toolpolicy.Input{Name: "other_exec", Risk: toolpolicy.RiskExec}, effect: toolpolicy.EffectRequireApproval, rule: "default.exec_requires_approval", reason: "in_workspace"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, err := policy.Decide(context.Background(), test.input)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Effect != test.effect || decision.RuleID != test.rule || decision.Reason != test.reason {
				t.Fatalf("decision = %#v", decision)
			}
		})
	}
	if _, err := policy.Decide(context.Background(), toolpolicy.Input{Name: "unknown", Risk: "network"}); err == nil {
		t.Fatal("unknown risk was accepted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := policy.Decide(canceled, toolpolicy.Input{Name: "read_file", Risk: toolpolicy.RiskRead}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled decision error = %v", err)
	}
}

func TestDenyToolsConfigValidation(t *testing.T) {
	tooMany := make([]string, 65)
	for index := range tooMany {
		tooMany[index] = "tool_" + string(rune('a'+index))
	}
	encodedTooMany, _ := json.Marshal(config{Names: tooMany})
	tests := []struct {
		name string
		raw  string
	}{
		{name: "unknown field", raw: `{"names":[],"extra":true}`},
		{name: "duplicate", raw: `{"names":["exec","exec"]}`},
		{name: "blank", raw: `{"names":[" "]}`},
		{name: "too long", raw: `{"names":["` + strings.Repeat("x", 129) + `"]}`},
		{name: "too many", raw: string(encodedTooMany)},
		{name: "trailing", raw: `{"names":[]} {}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := newPolicy(json.RawMessage(test.raw)); err == nil {
				t.Fatal("invalid config was accepted")
			}
		})
	}
}
