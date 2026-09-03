package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Digest is design §6's canonical identity digest shape: "sha256:" followed
// by 64 lowercase hex characters, matching the string convention
// internal/harness/adapters/sqlite/auditcodec.go already uses for audit
// replica digests.
type Digest string

// ScenarioDigest is the design §6 canonical identity digest of a validated
// Scenario: SHA-256 over the exact canonical JSON bytes of the whole
// document. Scenario carries no credential, absolute path, or timestamp
// field, so nothing to exclude survives to the digest by construction.
func ScenarioDigest(scenario Scenario) (Digest, error) {
	if err := scenario.Validate(); err != nil {
		return "", fmt.Errorf("eval: scenario digest: %w", err)
	}
	return canonicalDigest(scenario)
}

// SubjectDigest is the design §6 canonical identity digest of a validated
// Subject. Subject carries only a credential *name* (never a value) and no
// absolute path or timestamp field, so credentials/paths/timestamps cannot
// leak into it.
func SubjectDigest(subject Subject) (Digest, error) {
	if err := subject.Validate(); err != nil {
		return "", fmt.Errorf("eval: subject digest: %w", err)
	}
	return canonicalDigest(subject)
}

// ExecutorDigest is the design §6 canonical identity digest of a validated
// Executor.
func ExecutorDigest(executor Executor) (Digest, error) {
	if err := executor.Validate(); err != nil {
		return "", fmt.Errorf("eval: executor digest: %w", err)
	}
	return canonicalDigest(executor)
}

// OutcomeDigest is the SHA-256 canonical digest of a validated Outcome
// (design §14: EvidenceManifest.OutcomeDigest; design §20: Score
// records "manifest digest and Outcome digest").
func OutcomeDigest(outcome Outcome) (Digest, error) {
	if err := outcome.Validate(); err != nil {
		return "", fmt.Errorf("eval: outcome digest: %w", err)
	}
	return canonicalDigest(outcome)
}

// EvidenceManifestDigest is the SHA-256 digest of a validated
// EvidenceManifest's exact published JSON bytes (design §14: "the manifest
// does not hash itself; a Score references SHA-256 of the exact published
// manifest bytes"). Score.ManifestDigest records this value.
func EvidenceManifestDigest(manifest EvidenceManifest) (Digest, error) {
	if err := manifest.Validate(); err != nil {
		return "", fmt.Errorf("eval: evidence manifest digest: %w", err)
	}
	return canonicalDigest(manifest)
}

// canonicalDigest marshals value with encoding/json (which emits struct
// fields in a fixed declaration order and sorts map keys, so the output is
// already deterministic without a separate canonicalization pass — the same
// approach adapters/sqlite/auditcodec.go uses for audit envelope digests)
// and SHA-256s the exact resulting bytes.
func canonicalDigest(value any) (Digest, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("eval: canonical encode: %w", err)
	}
	sum := sha256.Sum256(data)
	return Digest("sha256:" + hex.EncodeToString(sum[:])), nil
}
