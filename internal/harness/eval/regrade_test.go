package eval

import "testing"

func TestRegradeAttemptPublishesScoreAndNeverReplacesEarlier(t *testing.T) {
	directories, _, _ := collectedHappyAttempt(t)
	scenario := validScenario()
	scenario.DeterministicVerifierIDs = allCatalogVerifierIDs

	first, err := RegradeAttempt(directories, Scorer{ID: "scorer-1", Version: "v1", VerifierIDs: allCatalogVerifierIDs})
	if err != nil {
		t.Fatalf("first RegradeAttempt: %v", err)
	}
	second, err := RegradeAttempt(directories, Scorer{ID: "scorer-1", Version: "v2", VerifierIDs: allCatalogVerifierIDs})
	if err != nil {
		t.Fatalf("second RegradeAttempt: %v", err)
	}
	if first.ID == second.ID {
		t.Fatal("both regrades published the same Score ID")
	}
	if first.AttemptID != second.AttemptID {
		t.Fatalf("AttemptID mismatch: %q vs %q", first.AttemptID, second.AttemptID)
	}
	if first.ManifestDigest != second.ManifestDigest || first.OutcomeDigest != second.OutcomeDigest {
		t.Fatal("regrading the same evidence twice produced different manifest/outcome digests")
	}

	scores, err := ReadScores(directories.Root)
	if err != nil {
		t.Fatalf("ReadScores: %v", err)
	}
	if len(scores) != 2 {
		t.Fatalf("len(scores) = %d, want 2 (both regrades kept, neither replaced)", len(scores))
	}
}

func TestRegradeAttemptRefusesWithoutACommittedManifest(t *testing.T) {
	directories, execution, _ := runHappyAttempt(t)
	_ = execution // Outcome/Manifest deliberately not published for this test.

	_, err := RegradeAttempt(directories, Scorer{ID: "scorer-1", Version: "v1", VerifierIDs: allCatalogVerifierIDs})
	if err == nil {
		t.Fatal("RegradeAttempt() error = nil, want a refusal: no manifest has been published for this Attempt yet")
	}
}

func TestAssembleEvaluationResultReadsPublishedScores(t *testing.T) {
	directories, _, _ := collectedHappyAttempt(t)
	scenario := validScenario()
	scenario.DeterministicVerifierIDs = allCatalogVerifierIDs
	if _, err := RegradeAttempt(directories, Scorer{ID: "scorer-1", Version: "v1", VerifierIDs: allCatalogVerifierIDs}); err != nil {
		t.Fatalf("RegradeAttempt: %v", err)
	}

	result, err := AssembleEvaluationResult(directories)
	if err != nil {
		t.Fatalf("AssembleEvaluationResult: %v", err)
	}
	if len(result.Scores) != 1 {
		t.Fatalf("len(result.Scores) = %d, want 1", len(result.Scores))
	}
	if result.AttemptID != result.Outcome.AttemptID {
		t.Fatalf("AttemptID mismatch: %q vs %q", result.AttemptID, result.Outcome.AttemptID)
	}
}
