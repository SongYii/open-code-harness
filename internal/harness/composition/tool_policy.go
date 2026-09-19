package composition

import (
	"context"
	"fmt"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	corepolicy "github.com/SongYii/open-code-harness/internal/harness/policy"
	"github.com/SongYii/open-code-harness/sdk/toolpolicy"
)

func resolveToolPolicy(config Config) (corepolicy.Engine, *domain.ToolPolicyIdentity, error) {
	registry := make(map[string]toolpolicy.Registration, len(config.ToolPolicies))
	for _, registration := range config.ToolPolicies {
		if !toolpolicy.ValidLabel(registration.ID) || !toolpolicy.ValidLabel(registration.Version) || registration.ID == toolpolicy.DefaultID || registration.Factory == nil {
			return nil, nil, fmt.Errorf("composition: invalid tool policy registration %q", registration.ID)
		}
		if _, exists := registry[registration.ID]; exists {
			return nil, nil, fmt.Errorf("composition: duplicate tool policy %q", registration.ID)
		}
		registry[registration.ID] = registration
	}

	canonical, digest, err := toolpolicy.CanonicalConfig([]byte(config.ToolPolicy.Config))
	if err != nil {
		return nil, nil, fmt.Errorf("composition: tool policy config: %w", err)
	}
	id := config.ToolPolicy.ID
	if id == "" || id == toolpolicy.DefaultID {
		if string(canonical) != "{}" || config.ToolPolicy.Version != "" {
			return nil, nil, fmt.Errorf("composition: builtin tool policy accepts no custom config/version")
		}
		return nil, nil, nil // Omit attribution to preserve legacy bytes.
	}
	mode := config.Policy
	if mode == "" {
		mode = corepolicy.ModeDefault
	}
	if mode != corepolicy.ModeDefault {
		return nil, nil, fmt.Errorf("composition: custom tool policy requires default builtin mode")
	}
	registration, ok := registry[id]
	if !ok {
		return nil, nil, fmt.Errorf("composition: unknown tool policy %q", id)
	}
	if config.ToolPolicy.Version != "" && config.ToolPolicy.Version != registration.Version {
		return nil, nil, fmt.Errorf("composition: tool policy version mismatch")
	}
	selected, err := registration.Factory(append([]byte(nil), canonical...))
	if err != nil {
		return nil, nil, fmt.Errorf("composition: tool policy factory %s: %w", id, err)
	}
	if selected == nil || nilPolicy(selected) {
		return nil, nil, fmt.Errorf("composition: tool policy factory %s returned nil", id)
	}
	return publicToolPolicyAdapter{policy: selected}, &domain.ToolPolicyIdentity{ID: id, Version: registration.Version, ConfigDigest: digest}, nil
}

type publicToolPolicyAdapter struct {
	policy toolpolicy.Policy
}

func (adapter publicToolPolicyAdapter) Decide(ctx context.Context, input corepolicy.Input) (corepolicy.Decision, error) {
	decision, err := adapter.policy.Decide(ctx, toolpolicy.Input{
		Name:        input.Name,
		Risk:        toolpolicy.Risk(input.Risk),
		Mutates:     input.Mutates,
		WorkspaceIn: input.WorkspaceIn,
		PathLiteral: input.PathLiteral,
	})
	return corepolicy.Decision{
		Effect: corepolicy.Effect(decision.Effect),
		RuleID: decision.RuleID,
		Reason: decision.Reason,
	}, err
}
