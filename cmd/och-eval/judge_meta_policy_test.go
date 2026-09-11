package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
