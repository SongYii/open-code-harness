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

func TestCheckedInJudgeMetaV3DeepSeekEvidenceBinds(t *testing.T) {
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

	config, err := loadJudgeConfig(filepath.Join(root, "eval", "judges", "semantic-meta-judge-deepseek-v3.json"))
	if err != nil {
		t.Fatal(err)
	}
	calibrationSet := loadJudgeMetaSetAt(t, root, "judge-meta/semantic-calibration-v3-deepseek.json")
	validationSet := loadJudgeMetaSetAt(t, root, "judge-meta/semantic-validation-v3-deepseek.json")
	calibration, err := eval.DecodeJudgeMetaReport(read("eval/reports/judge-semantic-meta-v3-deepseek-calibration-2026-09-11.json", "c8ee3b77e19b3ae516544a3495c04b78d642ac7850592ddba1ef2324aea5f170"))
	if err != nil {
		t.Fatal(err)
	}
	if err := eval.VerifyJudgeMetaReportBinding(calibration, calibrationSet, config); err != nil {
		t.Fatal(err)
	}
	policy, err := eval.DecodeJudgeMetaPolicy(read("eval/policies/judge-semantic-meta-v3-deepseek.json", "5d74f3328080acaa2f5fcdec4937853205ba93ab7d95f9f28b8f3505d7b72fa1"))
	if err != nil {
		t.Fatal(err)
	}
	wantPolicy, err := eval.CalibrateJudgeMetaPolicy(calibration, validationSet, "deepseek-semantic-v3", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(policy, wantPolicy) {
		t.Fatalf("checked-in v3 policy differs from calibration: got %+v want %+v", policy, wantPolicy)
	}
	validation, err := eval.DecodeJudgeMetaReport(read("eval/reports/judge-semantic-meta-v3-deepseek-validation-2026-09-11.json", "250e82316ecff015d910f3d75ef9b6af93489320711f520da0918b537e4a8541"))
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
	var result eval.JudgeMetaPolicyResult
	if err := json.Unmarshal(read("eval/reports/judge-semantic-meta-v3-deepseek-policy-result-2026-09-11.json", "9d69cad2a9226b1b281d7039c34757dc1aec7edf567aa4e42ebf6b8f73fd58be"), &result); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result, wantResult) || result.Passed {
		t.Fatalf("checked-in v3 result = %+v, recomputed = %+v", result, wantResult)
	}
}

func TestCheckedInJudgeMetaV4DeepSeekEvidenceBinds(t *testing.T) {
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

	config, err := loadJudgeConfig(filepath.Join(root, "eval", "judges", "semantic-meta-judge-deepseek-v4.json"))
	if err != nil {
		t.Fatal(err)
	}
	calibrationSet := loadJudgeMetaSetAt(t, root, "judge-meta/semantic-calibration-v4-deepseek.json")
	validationSet := loadJudgeMetaSetAt(t, root, "judge-meta/semantic-validation-v4-deepseek.json")
	calibration, err := eval.DecodeJudgeMetaReport(read("eval/reports/judge-semantic-meta-v4-deepseek-calibration-2026-09-12.json", "86c026caef4ac6cedf319a713ecf344f94f3d5ddc4aa871a931b42ba48e154a3"))
	if err != nil {
		t.Fatal(err)
	}
	if err := eval.VerifyJudgeMetaReportBinding(calibration, calibrationSet, config); err != nil {
		t.Fatal(err)
	}
	policy, err := eval.DecodeJudgeMetaPolicy(read("eval/policies/judge-semantic-meta-v4-deepseek.json", "10e204e8d7f928ab74da734453bdc00e06fdb6dfa2c6f7dd627848f60fea4da6"))
	if err != nil {
		t.Fatal(err)
	}
	wantPolicy, err := eval.CalibrateJudgeMetaPolicy(calibration, validationSet, "deepseek-semantic-v4", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(policy, wantPolicy) {
		t.Fatalf("checked-in v4 policy differs from calibration: got %+v want %+v", policy, wantPolicy)
	}
	validation, err := eval.DecodeJudgeMetaReport(read("eval/reports/judge-semantic-meta-v4-deepseek-validation-2026-09-12.json", "6722352b7bbb73d608f1fae4c7f2906dec8fc69b95ff398f2fd4ec09c9db8358"))
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
	var result eval.JudgeMetaPolicyResult
	if err := json.Unmarshal(read("eval/reports/judge-semantic-meta-v4-deepseek-policy-result-2026-09-12.json", "ac2f0105adf928d9afc99f801052c0d80571a06f5b7418620ffcf5f774194a58"), &result); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result, wantResult) || !result.Passed {
		t.Fatalf("checked-in v4 result = %+v, recomputed = %+v", result, wantResult)
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
