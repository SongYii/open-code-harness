package eval

import (
	"context"
	"testing"
)

// liveJudgeAttemptFor builds a collected live-lane Attempt whose
// deterministic verifiers will reach exactly want. No Subject is ever
// launched: the verdict is steered through the Scenario's own declared
// verifiers and the published Outcome, both of which are real evidence
// the real verifiers read.
func liveJudgeAttemptFor(t *testing.T, want ScoreVerdict) (AttemptRootDirectories, JudgeConfig) {
	t.Helper()
	config := validJudgeConfig()
	// The only role this synthetic Attempt collects is the frozen identity
	// evidence staging always writes. Declaring a role nothing collected
	// would (correctly) stop RunJudge before the caller, which is a
	// different test's subject.
	config.Criteria = []JudgeCriterion{{
		ID: "constraint-preservation", Rubric: "Judge the frozen scenario.", EvidenceRoles: []string{"scenario"},
	}}
	options := identityAttemptOptions{
		VerifierIDs:           []string{"manifest-complete-v1", "outcome-not-infra-failed-v1"},
		RequiredEvidenceRoles: []string{"scenario"},
	}
	switch want {
	case ScorePass:
	case ScoreFail:
		// outcome-not-infra-failed-v1 reports Fail for an infra-failed Outcome.
		options.OutcomeStatus = OutcomeInfraFailed
	case ScoreIndeterminate:
		// manifest-complete-v1 reports Indeterminate for a required role
		// nothing collected.
		options.RequiredEvidenceRoles = []string{"scenario", "never-collected"}
	default:
		t.Fatalf("unsupported prerequisite verdict %q", want)
	}
	directories, _ := collectedIdentityAttempt(t, LaneLive, liveTestSubject(), &config, options)
	return directories, config
}

func failingJudgeCaller(t *testing.T, called *bool) JudgeCaller {
	t.Helper()
	return func(context.Context, string, string) (string, ScorerUsage, error) {
		*called = true
		return "", ScorerUsage{}, nil
	}
}

func grantedConsent() LiveConsent {
	return LiveConsent{Flag: true, Environment: LiveConfirmValue}
}

func TestEvaluateJudgeAttemptStopsBeforeCallerOnPrerequisiteFailure(t *testing.T) {
	for _, prerequisite := range []ScoreVerdict{ScoreFail, ScoreIndeterminate} {
		t.Run(string(prerequisite), func(t *testing.T) {
			directories, config := liveJudgeAttemptFor(t, prerequisite)
			called := false
			result, err := EvaluateJudgeAttempt(context.Background(), directories, config,
				grantedConsent(), failingJudgeCaller(t, &called), nil)
			if err != nil {
				t.Fatalf("EvaluateJudgeAttempt: %v", err)
			}
			if called {
				t.Fatal("a non-Pass deterministic prerequisite still reached the provider")
			}
			if result.PrerequisiteVerdict != prerequisite {
				t.Fatalf("PrerequisiteVerdict = %q, want %q", result.PrerequisiteVerdict, prerequisite)
			}
			if result.Score.Verdict != ScoreIndeterminate {
				t.Fatalf("Score.Verdict = %q, want %q", result.Score.Verdict, ScoreIndeterminate)
			}
			if result.Score.Lane != LaneLive {
				t.Fatalf("Score.Lane = %q, want %q", result.Score.Lane, LaneLive)
			}
		})
	}
}

func TestEvaluateJudgeAttemptChecksConsentBeforeCaller(t *testing.T) {
	directories, config := liveJudgeAttemptFor(t, ScorePass)
	for _, testCase := range []struct {
		name    string
		consent LiveConsent
	}{
		{"no consent at all", LiveConsent{}},
		{"flag without the environment confirmation", LiveConsent{Flag: true}},
		{"environment confirmation without the flag", LiveConsent{Environment: LiveConfirmValue}},
		{"wrong environment value", LiveConsent{Flag: true, Environment: "true"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			called := false
			if _, err := EvaluateJudgeAttempt(context.Background(), directories, config,
				testCase.consent, failingJudgeCaller(t, &called), nil); err == nil {
				t.Fatal("EvaluateJudgeAttempt proceeded without live consent")
			}
			if called {
				t.Fatal("EvaluateJudgeAttempt reached the provider without live consent")
			}
		})
	}
}

// TestEvaluateJudgeAttemptRefusesAConfigTheEvidenceDoesNotProve is the
// binding half of the gate: the operator-supplied configuration must be
// byte-identical to what the Attempt froze, or nothing runs.
func TestEvaluateJudgeAttemptRefusesAConfigTheEvidenceDoesNotProve(t *testing.T) {
	directories, config := liveJudgeAttemptFor(t, ScorePass)
	supplied := config
	supplied.Version = "v2"

	called := false
	if _, err := EvaluateJudgeAttempt(context.Background(), directories, supplied,
		grantedConsent(), failingJudgeCaller(t, &called), nil); err == nil {
		t.Fatal("EvaluateJudgeAttempt accepted a config the Attempt's evidence does not prove")
	}
	if called {
		t.Fatal("EvaluateJudgeAttempt reached the provider with an unproven config")
	}
}

func TestEvaluateJudgeAttemptPublishesAppendOnlyLiveScore(t *testing.T) {
	directories, config := liveJudgeAttemptFor(t, ScorePass)
	caller := fixedJudgeCaller(t, judgeRawOutput{
		Verdict:            "pass",
		Criteria:           []judgeRawCriterion{{ID: config.Criteria[0].ID, Status: "pass"}},
		EvidenceReferences: []string{"scenario.json"},
		Rationale:          "the frozen scenario supports the claim",
	})

	first, err := EvaluateJudgeAttempt(context.Background(), directories, config, grantedConsent(), caller, nil)
	if err != nil {
		t.Fatalf("EvaluateJudgeAttempt: %v", err)
	}
	if first.PrerequisiteVerdict != ScorePass {
		t.Fatalf("PrerequisiteVerdict = %q, want %q", first.PrerequisiteVerdict, ScorePass)
	}
	if first.Score.Verdict != ScorePass {
		t.Fatalf("Score.Verdict = %q, want %q (rationale: %s)", first.Score.Verdict, ScorePass, first.Score.Rationale)
	}
	if first.Score.Lane != LaneLive {
		t.Fatalf("Score.Lane = %q, want %q", first.Score.Lane, LaneLive)
	}
	if first.Score.ScorerID != config.ID || first.Score.ScorerVersion != config.Version {
		t.Fatalf("Score names scorer %q/%q, want the frozen config's %q/%q",
			first.Score.ScorerID, first.Score.ScorerVersion, config.ID, config.Version)
	}
	configDigest, err := JudgeConfigDigest(config)
	if err != nil {
		t.Fatalf("JudgeConfigDigest: %v", err)
	}
	if first.Score.ScorerConfigDigest != configDigest {
		t.Fatalf("ScorerConfigDigest = %q, want %q", first.Score.ScorerConfigDigest, configDigest)
	}

	// Appending, never replacing: a second judge run over the same Attempt
	// gets its own Score ID and leaves the first one intact.
	second, err := EvaluateJudgeAttempt(context.Background(), directories, config, grantedConsent(), caller, nil)
	if err != nil {
		t.Fatalf("second EvaluateJudgeAttempt: %v", err)
	}
	if second.Score.ID == first.Score.ID {
		t.Fatal("a second judge run reused the first Score's ID")
	}
	scores, err := ReadScores(directories.Root)
	if err != nil {
		t.Fatalf("ReadScores: %v", err)
	}
	if len(scores) != 2 {
		t.Fatalf("ReadScores returned %d scores, want 2", len(scores))
	}
}

// TestEvaluateJudgeAttemptReportsCostAvailabilityExplicitly pins the rule
// that an unresolvable price is never published as a computed zero.
func TestEvaluateJudgeAttemptReportsCostAvailabilityExplicitly(t *testing.T) {
	directories, config := liveJudgeAttemptFor(t, ScorePass)
	caller := fixedJudgeCaller(t, judgeRawOutput{
		Verdict:            "pass",
		Criteria:           []judgeRawCriterion{{ID: config.Criteria[0].ID, Status: "pass"}},
		EvidenceReferences: []string{"scenario.json"},
		Rationale:          "ok",
	})

	withoutPrices, err := EvaluateJudgeAttempt(context.Background(), directories, config, grantedConsent(), caller, nil)
	if err != nil {
		t.Fatalf("EvaluateJudgeAttempt: %v", err)
	}
	if withoutPrices.Score.ScorerUsage == nil || withoutPrices.Score.ScorerUsage.CostStatus != CostStatusUnavailable {
		t.Fatalf("ScorerUsage = %+v, want an explicit %q cost status", withoutPrices.Score.ScorerUsage, CostStatusUnavailable)
	}

	table := PriceTable{Currency: "USD", Entries: []PriceEntry{
		{ModelID: config.Provider.ModelID, InputMicrounitsPerToken: 2, OutputMicrounitsPerToken: 10},
	}}
	withPrices, err := EvaluateJudgeAttempt(context.Background(), directories, config, grantedConsent(), caller, &table)
	if err != nil {
		t.Fatalf("EvaluateJudgeAttempt with prices: %v", err)
	}
	usage := withPrices.Score.ScorerUsage
	if usage == nil || usage.CostStatus != CostStatusComputed || usage.CostCurrency != "USD" {
		t.Fatalf("ScorerUsage = %+v, want a computed USD cost", usage)
	}
	// fixedJudgeCaller reports 42 input and 7 output tokens.
	if want := int64(42*2 + 7*10); usage.CostMicrounits != want {
		t.Fatalf("CostMicrounits = %d, want %d", usage.CostMicrounits, want)
	}
}

// TestEvaluateJudgeAttemptRefusesALegacyAttempt pins the honest outcome
// for an Attempt collected before judge evidence existed: it can still be
// regraded deterministically, but it can never be live-judged.
func TestEvaluateJudgeAttemptRefusesALegacyAttempt(t *testing.T) {
	directories, _, _ := collectedHappyAttempt(t)
	degradeToLegacyAttempt(t, directories)

	called := false
	if _, err := EvaluateJudgeAttempt(context.Background(), directories, validJudgeConfig(),
		grantedConsent(), failingJudgeCaller(t, &called), nil); err == nil {
		t.Fatal("EvaluateJudgeAttempt accepted a legacy Attempt")
	}
	if called {
		t.Fatal("EvaluateJudgeAttempt reached the provider for a legacy Attempt")
	}
}
