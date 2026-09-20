package domain

import (
	"encoding/hex"
	"encoding/json"
	"strings"
)

// ContextPolicyIdentity is optional attribution, not a checkpoint format or
// replay dependency. Absence means the legacy builtin policy. Keep this DTO
// owned by domain, independent of any SDK or executable policy implementation.
type ContextPolicyIdentity struct {
	ID           string `json:"id"`
	Version      string `json:"version"`
	ConfigDigest string `json:"configDigest"`
}

func validateContextPolicyIdentity(identity *ContextPolicyIdentity, code ErrorCode) error {
	if identity == nil {
		return nil
	}
	digest, err := hex.DecodeString(identity.ConfigDigest)
	if !validPolicyLabel(identity.ID) || !validPolicyLabel(identity.Version) || err != nil || len(digest) != 32 || strings.ToLower(identity.ConfigDigest) != identity.ConfigDigest {
		return domainError(code, "context policy identity is invalid")
	}
	return nil
}

func validPolicyLabel(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._-+", char)) {
			return false
		}
	}
	return true
}

func cloneContextPolicy(identity *ContextPolicyIdentity) *ContextPolicyIdentity {
	if identity == nil {
		return nil
	}
	copy := *identity
	return &copy
}

func validateContextPolicyJSON(data []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return invalidEventError("invalid context policy payload")
	}
	if raw, ok := object["policy"]; ok {
		return validateStrictJSONObject(raw, "id", "version", "configDigest")
	}
	return nil
}
