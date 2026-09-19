package domain

import (
	"encoding/hex"
	"encoding/json"
	"strings"
)

// ToolPolicyIdentity attributes a durable decision to the startup-selected
// policy implementation and its non-secret canonical configuration. It is
// replay evidence, not executable policy state.
type ToolPolicyIdentity struct {
	ID           string `json:"id"`
	Version      string `json:"version"`
	ConfigDigest string `json:"configDigest"`
}

// ValidateToolPolicyIdentity validates an optional durable policy identity.
func ValidateToolPolicyIdentity(identity *ToolPolicyIdentity) error {
	return validateToolPolicyIdentity(identity, CodeInvalidEvent)
}

func validateToolPolicyIdentity(identity *ToolPolicyIdentity, code ErrorCode) error {
	if identity == nil {
		return nil
	}
	digest, err := hex.DecodeString(identity.ConfigDigest)
	if !validPolicyLabel(identity.ID) || !validPolicyLabel(identity.Version) || err != nil || len(digest) != 32 || strings.ToLower(identity.ConfigDigest) != identity.ConfigDigest {
		return domainError(code, "tool policy identity is invalid")
	}
	return nil
}

// CloneToolPolicyIdentity returns a detached copy of identity.
func CloneToolPolicyIdentity(identity *ToolPolicyIdentity) *ToolPolicyIdentity {
	if identity == nil {
		return nil
	}
	cloned := *identity
	return &cloned
}

func validateToolPolicyIdentityJSON(data []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return invalidEventError("invalid tool policy payload")
	}
	if raw, ok := object["policy"]; ok {
		return validateStrictJSONObject(raw, "id", "version", "configDigest")
	}
	return nil
}
