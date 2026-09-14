package composition

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SongYii/open-code-harness/sdk/contextpolicy"
)

type noopContextPolicy struct{}

func (*noopContextPolicy) Plan(context.Context, contextpolicy.Input) (contextpolicy.Decision, error) {
	return contextpolicy.Decision{}, nil
}

func TestContextPolicyRegistryFailsClosed(t *testing.T) {
	registration := contextpolicy.Registration{ID: "test", Version: "1", Factory: func(json.RawMessage) (contextpolicy.Policy, error) { return &noopContextPolicy{}, nil }}
	for _, config := range []Config{
		{Context: Context{PolicyID: "unknown"}},
		{ContextPolicies: []contextpolicy.Registration{registration, registration}},
		{Context: Context{PolicyConfig: `{"unexpected":true}`}},
		{Context: Context{PolicyID: "test", PolicyVersion: "2"}, ContextPolicies: []contextpolicy.Registration{registration}},
		{Context: Context{PolicyID: "test"}, ContextPolicies: []contextpolicy.Registration{{ID: "test", Version: "1", Factory: func(json.RawMessage) (contextpolicy.Policy, error) { var p *noopContextPolicy; return p, nil }}}},
	} {
		if _, _, err := resolveContextPolicy(config); err == nil {
			t.Fatalf("accepted invalid registry/config: %+v", config.Context)
		}
	}
	config := Config{Context: Context{PolicyID: "test", PolicyConfig: `{"b":2,"a":1}`}, ContextPolicies: []contextpolicy.Registration{registration}}
	_, first, err := resolveContextPolicy(config)
	if err != nil {
		t.Fatal(err)
	}
	config.Context.PolicyConfig = `{ "a":1, "b":2 }`
	_, second, err := resolveContextPolicy(config)
	if err != nil || *first != *second {
		t.Fatalf("unstable identity: %v", err)
	}
	if _, _, err := resolveContextPolicy(Config{Context: Context{PolicyID: "test"}}); err == nil {
		t.Fatal("registration leaked across launchers")
	}
	_, identity, err := resolveContextPolicy(Config{})
	if err != nil || identity != nil {
		t.Fatal("default must omit identity")
	}
}
