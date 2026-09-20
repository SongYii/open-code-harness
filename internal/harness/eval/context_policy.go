package eval

import (
	"encoding/json"
	"fmt"

	"github.com/SongYii/open-code-harness/sdk/contextpolicy"
)

// SubjectContextPolicy freezes both implementation and nonsecret configuration.
// Custom policies are supported through a hashed ACP launcher binary only.
type SubjectContextPolicy struct {
	ID           string          `json:"id"`
	Version      string          `json:"version"`
	ConfigDigest string          `json:"configDigest"`
	Config       json.RawMessage `json:"config"`
}

func (policy SubjectContextPolicy) validate() error {
	_, digest, err := contextpolicy.CanonicalConfig(policy.Config)
	if err != nil || !contextpolicy.ValidLabel(policy.ID) || policy.ID == contextpolicy.DefaultID || !contextpolicy.ValidLabel(policy.Version) || policy.ConfigDigest != digest {
		return fmt.Errorf("%w: invalid context policy identity/configuration", errInvalidDocument)
	}
	return nil
}

func (policy SubjectContextPolicy) MarshalJSON() ([]byte, error) {
	canonical, _, err := contextpolicy.CanonicalConfig(policy.Config)
	if err != nil {
		return nil, err
	}
	policy.Config = canonical
	type plain SubjectContextPolicy
	return json.Marshal(plain(policy))
}
