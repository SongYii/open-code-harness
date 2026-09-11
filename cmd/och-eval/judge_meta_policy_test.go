package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

func checkedInLiveMetaReport(t *testing.T) eval.JudgeMetaReport {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRootDir(t), "eval", "reports", "judge-semantic-meta-deepseek-live-2026-09-11.json"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := eval.DecodeJudgeMetaReport(data)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func TestCheckedInJudgeMetaV2DeepSeekEvidenceBinds(t *testing.T) {
	root := repoRootDir(t)
	read := func(path, wantHash string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != wantHash {
			t.Fatalf("%s digest = %s, want %s", path, got, wantHash)
		}
		return data
	}

	config, err := loadJudgeConfig(filepath.Join(root, "eval", "judges", "semantic-meta-judge-deepseek-priced-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	calibrationSet := loadJudgeMetaSetAt(t, root, "judge-meta/semantic-calibration-v2-deepseek.json")
	validationSet := loadJudgeMetaSetAt(t, root, "judge-meta/semantic-validation-v2-deepseek.json")

	calibrationData := read("eval/reports/judge-semantic-meta-v2-deepseek-calibration-2026-09-11.json", "0a4865947f0c23c04bbb2afe365e4d44fd59a9cc7bf44d7ac29f5dcc8ca109d7")
	calibration, err := eval.DecodeJudgeMetaReport(calibrationData)
	if err != nil {
		t.Fatal(err)
	}
	if err := eval.VerifyJudgeMetaReportBinding(calibration, calibrationSet, config); err != nil {
		t.Fatal(err)
	}

	policyData := read("eval/policies/judge-semantic-meta-v2-deepseek.json", "67b03524dcce216f60c32be772bc2706844611946e7fbc7bfd788019ee6feac2")
	policy, err := eval.DecodeJudgeMetaPolicy(policyData)
	if err != nil {
		t.Fatal(err)
	}
	wantPolicy, err := eval.CalibrateJudgeMetaPolicy(calibration, validationSet, "deepseek-semantic-v2", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(policy, wantPolicy) {
		t.Fatalf("checked-in policy differs from calibration: got %+v want %+v", policy, wantPolicy)
	}

	validationData := read("eval/reports/judge-semantic-meta-v2-deepseek-validation-2026-09-11.json", "7d1b66506e8a8ae52556319961feb73f60ea88aad7ad8f6641a0b2067937f987")
	validation, err := eval.DecodeJudgeMetaReport(validationData)
	if err != nil {
		t.Fatal(err)
	}
	if err := eval.VerifyJudgeMetaReportBinding(validation, validationSet, config); err != nil {
		t.Fatal(err)
	}
	wantResult, err := eval.EvaluateJudgeMetaPolicy(policy, validation)
	if err != nil {
		t.Fatal(err)
	}
	resultData := read("eval/reports/judge-semantic-meta-v2-deepseek-policy-result-2026-09-11.json", "0231439022012c613d36acc97ca36eb52b8fa4616496a80fc5f30d83bb9e08ef")
	var result eval.JudgeMetaPolicyResult
	if err := json.Unmarshal(resultData, &result); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result, wantResult) {
		t.Fatalf("checked-in result differs from evaluation: got %+v want %+v", result, wantResult)
	}
	if result.Passed || !reflect.DeepEqual(result.Breaches, []string{"exact matches fell below calibration", "unexpected indeterminates exceeded calibration"}) {
		t.Fatalf("result = %+v, want the observed two-breach holdout failure", result)
	}
}

func TestJudgeMetaCalibrateAndCheckCommands(t *testing.T) {
	dir := t.TempDir()
	calibration := checkedInLiveMetaReport(t)
	calibrationPath := filepath.Join(dir, "calibration.json")
	writeTestJSON(t, calibrationPath, calibration)
	validationSet := loadJudgeMetaSetAt(t, repoRootDir(t), "judge-meta/semantic-seed-v1.json")
	validationSet.ID = "semantic-holdout"
	validationSetPath := filepath.Join(dir, "validation-set.json")
	writeTestJSON(t, validationSetPath, validationSet)
	validationDigest, err := eval.JudgeMetaSetDigest(validationSet)
	if err != nil {
		t.Fatal(err)
	}

	var policyOutput, stderr bytes.Buffer
	if code := judgeMetaCalibrateCommand([]string{"-report", calibrationPath, "-validation-set", validationSetPath, "-id", "semantic", "-version", "v1"}, &policyOutput, &stderr); code != exitOK {
		t.Fatalf("calibrate code=%d stderr=%s", code, stderr.String())
	}
	policyPath := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(policyPath, policyOutput.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	holdout := calibration
	holdout.SetID = "semantic-holdout"
	holdout.SetVersion = "v1"
	holdout.SetDigest = validationDigest
	holdoutPath := filepath.Join(dir, "holdout.json")
	writeTestJSON(t, holdoutPath, holdout)
	var checkOutput bytes.Buffer
	stderr.Reset()
	if code := judgeMetaCheckCommand([]string{"-report", holdoutPath, "-policy", policyPath}, &checkOutput, &stderr); code != exitOK {
		t.Fatalf("check code=%d stderr=%s", code, stderr.String())
	}
	var result eval.JudgeMetaPolicyResult
	if err := json.Unmarshal(checkOutput.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.FormatVersion != eval.FormatVersion || result.Schema != eval.SchemaJudgeMetaPolicyResult || !result.Passed {
		t.Fatalf("result = %+v", result)
	}
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
