package eval

import (
	"context"
	"testing"
)

func completeJudgeMetaReport(t *testing.T) JudgeMetaReport {
	t.Helper()
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
	return report
}

func TestCalibrateAndEvaluateJudgeMetaPolicyOnDisjointHoldout(t *testing.T) {
	calibration := completeJudgeMetaReport(t)
	validationSet := validJudgeMetaSet(t)
	validationSet.ID = "holdout"
	validationDigest, err := JudgeMetaSetDigest(validationSet)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := CalibrateJudgeMetaPolicy(calibration, validationSet, "semantic-judge", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if policy.Calibration != CalibrationCalibrated || policy.MaxUnsafePasses != calibration.Summary.UnsafePasses {
		t.Fatalf("policy = %+v", policy)
	}

	holdout := calibration
	holdout.SetID = "holdout"
	holdout.SetVersion = "v1"
	holdout.SetDigest = validationDigest
	result, err := EvaluateJudgeMetaPolicy(policy, holdout)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || len(result.Breaches) != 0 {
		t.Fatalf("result = %+v", result)
	}
	substituted := holdout
	substituted.SetDigest = mustDigest(t, 'c')
	if _, err := EvaluateJudgeMetaPolicy(policy, substituted); err == nil {
		t.Fatal("EvaluateJudgeMetaPolicy accepted a post-selected substitute holdout")
	}

	holdout.Observations[0].ExpectedVerdict = ScoreFail
	holdout = finishJudgeMetaReport(holdout)
	result, err = EvaluateJudgeMetaPolicy(policy, holdout)
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed || len(result.Breaches) == 0 {
		t.Fatalf("unsafe holdout result = %+v", result)
	}
}

func TestJudgeMetaPolicyRejectsCalibrationReportAsItsOwnHoldout(t *testing.T) {
	report := completeJudgeMetaReport(t)
	validationSet := validJudgeMetaSet(t)
	validationSet.ID = "holdout"
	policy, err := CalibrateJudgeMetaPolicy(report, validationSet, "semantic-judge", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EvaluateJudgeMetaPolicy(policy, report); err == nil {
		t.Fatal("EvaluateJudgeMetaPolicy accepted calibration data as validation")
	}
}

func TestJudgeMetaPolicyRejectsIncompleteCalibration(t *testing.T) {
	report := completeJudgeMetaReport(t)
	report.Complete = false
	report.StopReason = "stopped"
	report.PlannedCalls++
	validationSet := validJudgeMetaSet(t)
	validationSet.ID = "holdout"
	if _, err := CalibrateJudgeMetaPolicy(report, validationSet, "semantic-judge", "v1"); err == nil {
		t.Fatal("CalibrateJudgeMetaPolicy accepted incomplete report")
	}
}
