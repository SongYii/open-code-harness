package eval

import (
	"encoding/json"
	"fmt"

	"github.com/SongYii/open-code-harness/sdk/toolpolicy"
)

// SubjectToolPolicy freezes both implementation and nonsecret configuration.
// Custom policies are supported through a hashed ACP launcher binary only.
type SubjectToolPolicy struct {
	ID           string          `json:"id"`
	Version      string          `json:"version"`
	ConfigDigest string          `json:"configDigest"`
	Config       json.RawMessage `json:"config"`
}

func (policy SubjectToolPolicy) validate() error {
	_, digest, err := toolpolicy.CanonicalConfig(policy.Config)
	if err != nil || !toolpolicy.ValidLabel(policy.ID) || policy.ID == toolpolicy.DefaultID || !toolpolicy.ValidLabel(policy.Version) || policy.ConfigDigest != digest {
		return fmt.Errorf("%w: invalid tool policy identity/configuration", errInvalidDocument)
	}
	return nil
}

func (policy SubjectToolPolicy) MarshalJSON() ([]byte, error) {
	canonical, _, err := toolpolicy.CanonicalConfig(policy.Config)
	if err != nil {
		return nil, err
	}
	policy.Config = canonical
	type plain SubjectToolPolicy
	return json.Marshal(plain(policy))
}
