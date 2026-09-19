package och

import (
	"encoding/json"
	"testing"

	"github.com/SongYii/open-code-harness/sdk/contextpolicy"
	"github.com/SongYii/open-code-harness/sdk/toolpolicy"
)

func TestExtensionsAreCopiedAtRunBoundary(t *testing.T) {
	contextFactory := func(json.RawMessage) (contextpolicy.Policy, error) { return nil, nil }
	toolFactory := func(json.RawMessage) (toolpolicy.Policy, error) { return nil, nil }
	original := Extensions{
		ContextPolicies: []contextpolicy.Registration{{ID: "context_policy", Version: "1", Factory: contextFactory}},
		ToolPolicies:    []toolpolicy.Registration{{ID: "tool_policy", Version: "1", Factory: toolFactory}},
	}
	copied := cloneExtensions(original)
	original.ContextPolicies[0] = contextpolicy.Registration{ID: "mutated"}
	original.ToolPolicies[0] = toolpolicy.Registration{ID: "mutated"}
	original.ContextPolicies = append(original.ContextPolicies, contextpolicy.Registration{ID: "extra"})
	original.ToolPolicies = append(original.ToolPolicies, toolpolicy.Registration{ID: "extra"})
	if len(copied.ContextPolicies) != 1 || copied.ContextPolicies[0].ID != "context_policy" || copied.ContextPolicies[0].Factory == nil {
		t.Fatalf("context registrations were not copied: %#v", copied.ContextPolicies)
	}
	if len(copied.ToolPolicies) != 1 || copied.ToolPolicies[0].ID != "tool_policy" || copied.ToolPolicies[0].Factory == nil {
		t.Fatalf("tool registrations were not copied: %#v", copied.ToolPolicies)
	}
}
