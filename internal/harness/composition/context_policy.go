package composition

import (
	"fmt"
	"reflect"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/sdk/contextpolicy"
)

func resolveContextPolicy(config Config) (contextpolicy.Policy, *domain.ContextPolicyIdentity, error) {
	registry := make(map[string]contextpolicy.Registration, len(config.ContextPolicies))
	for _, registration := range config.ContextPolicies {
		if !contextpolicy.ValidLabel(registration.ID) || !contextpolicy.ValidLabel(registration.Version) || registration.ID == contextpolicy.DefaultID || registration.Factory == nil {
			return nil, nil, fmt.Errorf("composition: invalid context policy registration %q", registration.ID)
		}
		if _, exists := registry[registration.ID]; exists {
			return nil, nil, fmt.Errorf("composition: duplicate context policy %q", registration.ID)
		}
		registry[registration.ID] = registration
	}
	canonical, digest, err := contextpolicy.CanonicalConfig([]byte(config.Context.PolicyConfig))
	if err != nil {
		return nil, nil, fmt.Errorf("composition: context policy config: %w", err)
	}
	id := config.Context.PolicyID
	if id == "" || id == contextpolicy.DefaultID {
		if string(canonical) != "{}" || config.Context.PolicyVersion != "" {
			return nil, nil, fmt.Errorf("composition: builtin context policy accepts no custom config/version")
		}
		return nil, nil, nil // Omit attribution to preserve legacy bytes.
	}
	registration, ok := registry[id]
	if !ok {
		return nil, nil, fmt.Errorf("composition: unknown context policy %q", id)
	}
	if config.Context.PolicyVersion != "" && config.Context.PolicyVersion != registration.Version {
		return nil, nil, fmt.Errorf("composition: context policy version mismatch")
	}
	policy, err := registration.Factory(append([]byte(nil), canonical...))
	if err != nil {
		return nil, nil, fmt.Errorf("composition: context policy factory %s: %w", id, err)
	}
	if policy == nil || nilPolicy(policy) {
		return nil, nil, fmt.Errorf("composition: context policy factory %s returned nil", id)
	}
	return policy, &domain.ContextPolicyIdentity{ID: id, Version: registration.Version, ConfigDigest: digest}, nil
}

func nilPolicy(value any) bool {
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
