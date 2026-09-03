package eval

// Verifier is one deterministic, versioned check design §20 runs against
// an Attempt's committed evidence (design §7's DeterministicVerifierIDs).
// A Verifier never has network, filesystem, or Subject-execution access
// beyond what its ArtifactReader exposes, and never observes anything
// beyond one Attempt's own evidence and Scenario.
type Verifier func(reader *ArtifactReader, scenario Scenario) CriterionResult

// verifierCatalog is a fixed, compiled-in table of every known Verifier,
// keyed by its versioned ID (design §20: "unknown verifier IDs fail
// EvalSet validation rather than executing data-file code" — the catalog
// is Go code registered here, never loaded from a Scenario- or
// EvalSet-supplied file).
var verifierCatalog = map[string]Verifier{
	"manifest-complete-v1":              verifyManifestComplete,
	"tool-approval-failure-observed-v1": verifyToolApprovalFailureObserved,
	"context-compaction-observed-v1":    verifyContextCompactionObserved,
	"transcript-present-v1":             verifyTranscriptPresent,
	"audit-included-v1":                 verifyAuditIncluded,
	"outcome-not-infra-failed-v1":       verifyOutcomeNotInfraFailed,
	"read-file-completed-v1":            verifyReadFileCompleted,
	"redaction-observed-v1":             verifyRedactionObserved,
	"expected-tool-failure-observed-v1": verifyExpectedToolFailureObserved,
	"containment-refused-v1":            verifyContainmentRefused,
}

// LookupVerifier returns the compiled Verifier for id, and whether one is
// registered. A Scenario or Scorer referencing an unregistered ID is
// invalid input, not a runtime failure to recover from.
func LookupVerifier(id string) (Verifier, bool) {
	verifier, ok := verifierCatalog[id]
	return verifier, ok
}

// verifyManifestComplete checks that every one of the Scenario's declared
// RequiredEvidenceRoles has at least one EntryCollected manifest entry
// (design §20's "manifest completeness" check). A missing required role
// is Indeterminate, never Fail — the Attempt's own execution may have
// been entirely healthy; only its evidence is incomplete, which this
// package's own stated rule ("missing... required evidence... yields
// indeterminate... never pass") gives an explicit verdict for.
func verifyManifestComplete(reader *ArtifactReader, scenario Scenario) CriterionResult {
	for _, role := range scenario.RequiredEvidenceRoles {
		if !hasCollectedEntry(reader.Entries(role)) {
			return CriterionResult{ID: "manifest-complete-v1", Status: ScoreIndeterminate}
		}
	}
	return CriterionResult{ID: "manifest-complete-v1", Status: ScorePass}
}

// verifyTranscriptPresent checks that a "transcript" role entry is
// collected and its bytes still match the manifest (ReadEntry
// re-verifies size and SHA-256, and refuses a symlink/hard link/type
// change since collection).
func verifyTranscriptPresent(reader *ArtifactReader, _ Scenario) CriterionResult {
	return verifyRoleReadable(reader, "transcript-present-v1", "transcript")
}

// verifyAuditIncluded checks that at least one "audit" role entry is
// collected and its bytes still match the manifest.
func verifyAuditIncluded(reader *ArtifactReader, _ Scenario) CriterionResult {
	return verifyRoleReadable(reader, "audit-included-v1", "audit")
}

func verifyRoleReadable(reader *ArtifactReader, criterionID, role string) CriterionResult {
	entries := reader.Entries(role)
	if !hasCollectedEntry(entries) {
		return CriterionResult{ID: criterionID, Status: ScoreIndeterminate}
	}
	for _, entry := range entries {
		if entry.State != EntryCollected {
			continue
		}
		if _, err := reader.ReadEntry(entry.Path); err != nil {
			return CriterionResult{ID: criterionID, Status: ScoreIndeterminate}
		}
	}
	return CriterionResult{ID: criterionID, Status: ScorePass}
}

// verifyOutcomeNotInfraFailed checks that the Attempt's own execution
// authority stayed sound: infra_failed and indeterminate both mean the
// runner or its evidence could not be trusted, which is a known, definite
// fact once Outcome is published — a real Fail, not an unknown.
// subject_failed and completed both leave runner authority intact
// (design §13: "Outcome is not quality"), so both pass this check.
func verifyOutcomeNotInfraFailed(reader *ArtifactReader, _ Scenario) CriterionResult {
	switch reader.Outcome().Status {
	case OutcomeInfraFailed, OutcomeIndeterminate:
		return CriterionResult{ID: "outcome-not-infra-failed-v1", Status: ScoreFail}
	default:
		return CriterionResult{ID: "outcome-not-infra-failed-v1", Status: ScorePass}
	}
}

func hasCollectedEntry(entries []ManifestEntry) bool {
	for _, entry := range entries {
		if entry.State == EntryCollected {
			return true
		}
	}
	return false
}
