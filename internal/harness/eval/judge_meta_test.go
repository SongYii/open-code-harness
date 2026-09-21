package eval

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func validJudgeMetaSet(t *testing.T) JudgeMetaSet {
	t.Helper()
	config := testJudgeConfig()
	digest, err := JudgeConfigDigest(config)
	if err != nil {
		t.Fatal(err)
	}
	return JudgeMetaSet{
		FormatVersion:     FormatVersion,
		Schema:            SchemaJudgeMetaSet,
		ID:                "judge-semantic-seed",
		Version:           "v1",
		JudgeConfigDigest: digest,
		RepetitionCount:   2,
		Cases: []JudgeMetaCase{
			{
				ID:    "clear-pass",
				Label: JudgeMetaLabel{Verdict: ScorePass, Rationale: "Both records clearly support completion."},
				Tags:  []string{"control", "pass"},
				Evidence: []JudgeMetaEvidence{
					{Path: "transcript/pass.txt", Role: "transcript", Text: "The requested change was completed and verified."},
					{Path: "audit/pass.txt", Role: "audit", Text: "turn.completed verification=pass"},
				},
			},
			{
				ID:    "clear-fail",
				Label: JudgeMetaLabel{Verdict: ScoreFail, Rationale: "The records explicitly show the required check was skipped."},
				Tags:  []string{"control", "fail"},
				Evidence: []JudgeMetaEvidence{
					{Path: "transcript/fail.txt", Role: "transcript", Text: "I skipped the required verification."},
					{Path: "audit/fail.txt", Role: "audit", Text: "turn.completed verification=missing"},
				},
			},
		},
	}
}

func TestJudgeMetaSetStrictRoundTripDigestAndBinding(t *testing.T) {
	set := validJudgeMetaSet(t)
	data, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJudgeMetaSet(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyJudgeMetaSetBinding(decoded, testJudgeConfig()); err != nil {
		t.Fatal(err)
	}
	first, err := JudgeMetaSetDigest(decoded)
	if err != nil {
		t.Fatal(err)
	}
	decoded.Cases[0].Evidence[0].Text += " changed"
	second, err := JudgeMetaSetDigest(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("digest did not bind labelled evidence bytes")
	}
	if _, err := DecodeJudgeMetaSet(append(data[:len(data)-1], []byte(`,"unknown":true}`)...)); err == nil {
		t.Fatal("DecodeJudgeMetaSet accepted an unknown field")
	}
}

func TestJudgeMetaSetValidationRejectsUnsafeOrAmbiguousCorpus(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*JudgeMetaSet)
	}{
		{"no cases", func(set *JudgeMetaSet) { set.Cases = nil }},
		{"too many repetitions", func(set *JudgeMetaSet) { set.RepetitionCount = 21 }},
		{"duplicate case", func(set *JudgeMetaSet) { set.Cases[1].ID = set.Cases[0].ID }},
		{"indefensible label", func(set *JudgeMetaSet) { set.Cases[0].Label.Rationale = " " }},
		{"duplicate tag", func(set *JudgeMetaSet) { set.Cases[0].Tags = []string{"control", "control"} }},
		{"escaping path", func(set *JudgeMetaSet) { set.Cases[0].Evidence[0].Path = "../secret" }},
		{"duplicate path", func(set *JudgeMetaSet) { set.Cases[0].Evidence[1].Path = set.Cases[0].Evidence[0].Path }},
		{"empty role", func(set *JudgeMetaSet) { set.Cases[0].Evidence[0].Role = "" }},
		{"oversized entry", func(set *JudgeMetaSet) {
			set.Cases[0].Evidence[0].Text = strings.Repeat("x", maxJudgeEvidenceEntryBytes+1)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			set := validJudgeMetaSet(t)
			test.mutate(&set)
			if err := set.Validate(); err == nil {
				t.Fatal("Validate() error = nil")
			}
		})
	}
}

func reviewedJudgeMetaSet(t *testing.T) JudgeMetaSet {
	t.Helper()
	set := validJudgeMetaSet(t)
	set.Version = "v4"
	set.LabelReviewPolicy = JudgeMetaLabelReviewEvidenceV1
	set.Cases[0].Label.Review = &JudgeMetaLabelReview{
		Facts: []JudgeMetaReviewFact{
			{Kind: "task", Claim: "A requested change had to be completed.", EvidencePath: "transcript/pass.txt", ExactExcerpt: "requested change"},
			{Kind: "completion", Claim: "The turn completed.", EvidencePath: "audit/pass.txt", ExactExcerpt: "turn.completed"},
			{Kind: "verification", Claim: "Verification passed.", EvidencePath: "audit/pass.txt", ExactExcerpt: "verification=pass"},
		},
		Counterfactual: "A missing edit or failed verification would change this label to fail.",
	}
	set.Cases[1].Label.Review = &JudgeMetaLabelReview{
		Facts: []JudgeMetaReviewFact{
			{Kind: "task", Claim: "Verification was required.", EvidencePath: "transcript/fail.txt", ExactExcerpt: "required verification"},
			{Kind: "violation", Claim: "The audit records missing verification.", EvidencePath: "audit/fail.txt", ExactExcerpt: "verification=missing"},
		},
		Counterfactual: "A completed passing verification would remove the recorded violation.",
	}
	return set
}

func TestJudgeMetaEvidenceReviewPolicyBindsAuditableLabelSupport(t *testing.T) {
	set := reviewedJudgeMetaSet(t)
	if err := set.Validate(); err != nil {
		t.Fatalf("reviewed set rejected: %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*JudgeMetaSet)
	}{
		{"unsupported policy", func(set *JudgeMetaSet) { set.LabelReviewPolicy = "self-attested" }},
		{"missing review", func(set *JudgeMetaSet) { set.Cases[0].Label.Review = nil }},
		{"invented quote", func(set *JudgeMetaSet) { set.Cases[0].Label.Review.Facts[0].ExactExcerpt = "not in evidence" }},
		{"role not covered", func(set *JudgeMetaSet) { set.Cases[0].Label.Review.Facts[0] = set.Cases[0].Label.Review.Facts[1] }},
		{"pass lacks verification", func(set *JudgeMetaSet) { set.Cases[0].Label.Review.Facts = set.Cases[0].Label.Review.Facts[:2] }},
		{"missing counterfactual", func(set *JudgeMetaSet) { set.Cases[0].Label.Review.Counterfactual = " " }},
		{"review without policy", func(set *JudgeMetaSet) { set.LabelReviewPolicy = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := reviewedJudgeMetaSet(t)
			test.mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("Validate() accepted an unauditable label review")
			}
		})
	}
}

func TestJudgeMetaSetBindingRejectsWrongDigestRoleOrMissingRole(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*JudgeMetaSet)
	}{
		{"wrong digest", func(set *JudgeMetaSet) { set.JudgeConfigDigest = mustDigest(t, 'f') }},
		{"undeclared role", func(set *JudgeMetaSet) { set.Cases[0].Evidence[0].Role = "workspace" }},
		{"missing role", func(set *JudgeMetaSet) { set.Cases[0].Evidence = set.Cases[0].Evidence[:1] }},
	} {
		t.Run(test.name, func(t *testing.T) {
			set := validJudgeMetaSet(t)
			test.mutate(&set)
			if err := VerifyJudgeMetaSetBinding(set, testJudgeConfig()); err == nil {
				t.Fatal("VerifyJudgeMetaSetBinding() error = nil")
			}
		})
	}
}

func TestRunJudgeMetaSetReportsOrderedConfusionAndUnsafePasses(t *testing.T) {
	set := validJudgeMetaSet(t)
	calls := 0
	caller := func(_ context.Context, _, bundle string) (string, ScorerUsage, error) {
		calls++
		verdict := "pass"
		if strings.Contains(bundle, "clear-fail") || strings.Contains(bundle, "verification=missing") {
			// First fail-case repetition is the dangerous false pass; second is
			// an indeterminate observation.
			if calls == 4 {
				verdict = "indeterminate"
			}
		}
		status := verdict
		if verdict == "indeterminate" {
			status = "indeterminate"
		}
		return marshalJudgeRaw(t, judgeRawOutput{
			Verdict: verdict,
			Criteria: []judgeRawCriterion{
				{ID: "quality", Status: status}, {ID: "continuity", Status: status},
			},
			EvidenceReferences: evidencePathsFromBundle(bundle),
			Rationale:          "fixture decision",
		}), ScorerUsage{InputTokens: 10, OutputTokens: 2}, nil
	}

	report, err := RunJudgeMetaSet(context.Background(), set, testJudgeConfig(), caller, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 4 || len(report.Observations) != 4 {
		t.Fatalf("calls=%d observations=%d, want 4 each", calls, len(report.Observations))
	}
	if got := report.Summary; got.Observations != 4 || got.ExactMatches != 2 || got.UnsafePasses != 1 || got.UnexpectedIndeterminates != 1 {
		t.Fatalf("Summary = %+v", got)
	}
	if report.Observations[0].CaseID != "clear-pass" || report.Observations[0].RepetitionIndex != 0 ||
		report.Observations[3].CaseID != "clear-fail" || report.Observations[3].RepetitionIndex != 1 {
		t.Fatalf("observations are not in case/repetition order: %+v", report.Observations)
	}
	if len(report.Confusion) != 9 {
		t.Fatalf("confusion cells = %d, want all 9 including zeroes", len(report.Confusion))
	}
	if !report.Complete || report.PlannedCalls != 4 || report.CompletedCalls != 4 {
		t.Fatalf("completion fields = complete:%t planned:%d completed:%d", report.Complete, report.PlannedCalls, report.CompletedCalls)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeJudgeMetaReport(encoded); err != nil {
		t.Fatalf("DecodeJudgeMetaReport: %v", err)
	}
	if err := VerifyJudgeMetaReportBinding(report, set, testJudgeConfig()); err != nil {
		t.Fatalf("VerifyJudgeMetaReportBinding: %v", err)
	}
}

func TestSummarizeJudgeMetaKeepsErrorDirectionsSeparate(t *testing.T) {
	observations := []JudgeMetaObservation{
		{ExpectedVerdict: ScorePass, ObservedVerdict: ScorePass},
		{ExpectedVerdict: ScoreFail, ObservedVerdict: ScorePass},
		{ExpectedVerdict: ScoreIndeterminate, ObservedVerdict: ScorePass},
		{ExpectedVerdict: ScorePass, ObservedVerdict: ScoreFail},
		{ExpectedVerdict: ScoreFail, ObservedVerdict: ScoreIndeterminate},
		{ExpectedVerdict: ScoreIndeterminate, ObservedVerdict: ScoreFail},
	}
	cells, summary := summarizeJudgeMeta(observations)
	if len(cells) != 9 || summary.Observations != 6 || summary.ExactMatches != 1 ||
		summary.UnsafePasses != 2 || summary.FalseFails != 1 ||
		summary.UnexpectedIndeterminates != 1 || summary.Overclaims != 2 {
		t.Fatalf("cells=%d summary=%+v", len(cells), summary)
	}
}

func TestRunJudgeMetaSetCancellationPreservesPaidPartialObservations(t *testing.T) {
	set := validJudgeMetaSet(t)
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	caller := func(_ context.Context, _, bundle string) (string, ScorerUsage, error) {
		calls++
		cancel()
		return marshalJudgeRaw(t, judgeRawOutput{
			Verdict:            "pass",
			Criteria:           []judgeRawCriterion{{ID: "quality", Status: "pass"}, {ID: "continuity", Status: "pass"}},
			EvidenceReferences: evidencePathsFromBundle(bundle), Rationale: "first paid observation",
		}), ScorerUsage{InputTokens: 10, OutputTokens: 2}, nil
	}
	report, err := RunJudgeMetaSet(ctx, set, testJudgeConfig(), caller, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || report.Complete || report.CompletedCalls != 1 || report.PlannedCalls != 4 {
		t.Fatalf("calls=%d report=%+v", calls, report)
	}
	if !strings.Contains(report.StopReason, "canceled") || report.Observations[0].ScorerUsage.InputTokens != 10 {
		t.Fatalf("partial report lost stop reason or usage: %+v", report)
	}
}

func TestRunJudgeMetaSetPreservesProviderFailureAsIndeterminateObservation(t *testing.T) {
	set := validJudgeMetaSet(t)
	set.Cases = set.Cases[:1]
	set.RepetitionCount = 1
	report, err := RunJudgeMetaSet(context.Background(), set, testJudgeConfig(),
		func(context.Context, string, string) (string, ScorerUsage, error) {
			return "", ScorerUsage{InputTokens: 7}, errors.New("provider unavailable")
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Complete || len(report.Observations) != 1 ||
		report.Observations[0].ObservedVerdict != ScoreIndeterminate ||
		report.Observations[0].ScorerUsage.InputTokens != 7 {
		t.Fatalf("provider failure observation = %+v", report)
	}
}

func TestDecodeJudgeMetaReportRejectsAggregateTampering(t *testing.T) {
	set := validJudgeMetaSet(t)
	caller := func(_ context.Context, _, bundle string) (string, ScorerUsage, error) {
		return marshalJudgeRaw(t, judgeRawOutput{
			Verdict: "pass", Criteria: []judgeRawCriterion{{ID: "quality", Status: "pass"}, {ID: "continuity", Status: "pass"}},
			EvidenceReferences: evidencePathsFromBundle(bundle), Rationale: "fixture",
		}), ScorerUsage{}, nil
	}
	report, err := RunJudgeMetaSet(context.Background(), set, testJudgeConfig(), caller, nil)
	if err != nil {
		t.Fatal(err)
	}
	report.Summary.UnsafePasses++
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeJudgeMetaReport(data); err == nil {
		t.Fatal("DecodeJudgeMetaReport accepted aggregate tampering")
	}
}

func TestVerifyJudgeMetaReportBindingRejectsReorderedOrRelabelledObservation(t *testing.T) {
	set := validJudgeMetaSet(t)
	caller := func(_ context.Context, _, bundle string) (string, ScorerUsage, error) {
		return marshalJudgeRaw(t, judgeRawOutput{
			Verdict: "pass", Criteria: []judgeRawCriterion{{ID: "quality", Status: "pass"}, {ID: "continuity", Status: "pass"}},
			EvidenceReferences: evidencePathsFromBundle(bundle), Rationale: "fixture",
		}), ScorerUsage{}, nil
	}
	report, err := RunJudgeMetaSet(context.Background(), set, testJudgeConfig(), caller, nil)
	if err != nil {
		t.Fatal(err)
	}
	report.Observations[0].CaseID = "clear-fail"
	if err := report.Validate(); err != nil {
		t.Fatalf("internal report validation should not claim set binding: %v", err)
	}
	if err := VerifyJudgeMetaReportBinding(report, set, testJudgeConfig()); err == nil {
		t.Fatal("VerifyJudgeMetaReportBinding accepted an observation moved under another case label")
	}
	report.Observations[0].CaseID = "clear-pass"
	report.Observations[0].Criteria[0].ID = "substituted"
	if err := report.Validate(); err != nil {
		t.Fatalf("internal report validation should not claim config binding: %v", err)
	}
	if err := VerifyJudgeMetaReportBinding(report, set, testJudgeConfig()); err == nil {
		t.Fatal("VerifyJudgeMetaReportBinding accepted a criterion outside its judge config")
	}
}

func TestRunJudgeMetaSetRejectsUnboundPriceTableBeforeCaller(t *testing.T) {
	set := validJudgeMetaSet(t)
	called := false
	caller := func(context.Context, string, string) (string, ScorerUsage, error) {
		called = true
		return "", ScorerUsage{}, nil
	}
	table := &PriceTable{Currency: "USD", Entries: []PriceEntry{{ModelID: testJudgeConfig().Provider.ModelID}}}
	if _, err := RunJudgeMetaSet(context.Background(), set, testJudgeConfig(), caller, table); err == nil {
		t.Fatal("RunJudgeMetaSet accepted a price table the JudgeConfig did not bind")
	}
	if called {
		t.Fatal("caller reached before price-table binding validation")
	}
}

func marshalJudgeRaw(t *testing.T, output judgeRawOutput) string {
	t.Helper()
	data, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func evidencePathsFromBundle(bundle string) []string {
	var paths []string
	for _, line := range strings.Split(bundle, "\n") {
		if !strings.HasPrefix(line, "--- path: ") {
			continue
		}
		path := strings.TrimPrefix(line, "--- path: ")
		paths = append(paths, strings.SplitN(path, " ", 2)[0])
	}
	return paths
}
