package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

func cliJudgeMetaFixture(t *testing.T) (eval.JudgeMetaSet, eval.JudgeConfig) {
	t.Helper()
	config, err := loadJudgeConfig(checkedInJudgeConfigPath(t))
	if err != nil {
		t.Fatal(err)
	}
	digest, err := eval.JudgeConfigDigest(config)
	if err != nil {
		t.Fatal(err)
	}
	roles := make(map[string]bool)
	for _, criterion := range config.Criteria {
		for _, role := range criterion.EvidenceRoles {
			roles[role] = true
		}
	}
	orderedRoles := make([]string, 0, len(roles))
	for role := range roles {
		orderedRoles = append(orderedRoles, role)
	}
	sort.Strings(orderedRoles)
	var evidence []eval.JudgeMetaEvidence
	for _, role := range orderedRoles {
		evidence = append(evidence, eval.JudgeMetaEvidence{Path: role + "/record.txt", Role: role, Text: role + " supports the expected result"})
	}
	return eval.JudgeMetaSet{
		FormatVersion: eval.FormatVersion, Schema: eval.SchemaJudgeMetaSet,
		ID: "cli-meta", Version: "v1", JudgeConfigDigest: digest, RepetitionCount: 2,
		Cases: []eval.JudgeMetaCase{{
			ID: "pass", Label: eval.JudgeMetaLabel{Verdict: eval.ScorePass, Rationale: "clear fixture pass"}, Evidence: evidence,
		}},
	}, config
}

func TestRunJudgeMetaAndReportRequiresConsentAndExactCallBudgetBeforeCaller(t *testing.T) {
	set, config := cliJudgeMetaFixture(t)
	for _, test := range []struct {
		name     string
		live     bool
		env      string
		maxCalls int
	}{
		{name: "missing flag", env: eval.LiveConfirmValue, maxCalls: 2},
		{name: "missing environment", live: true, maxCalls: 2},
		{name: "wrong call budget", live: true, env: "1", maxCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("OCH_EVAL_LIVE_CONFIRM", test.env)
			called := false
			caller := func(context.Context, string, string) (string, eval.ScorerUsage, error) {
				called = true
				return "", eval.ScorerUsage{}, nil
			}
			var stdout, stderr bytes.Buffer
			if code := runJudgeMetaAndReport(context.Background(), set, config, test.live, test.maxCalls, caller, nil, &stdout, &stderr); code != exitValidation {
				t.Fatalf("exit=%d stderr=%s", code, stderr.String())
			}
			if called {
				t.Fatal("caller reached before consent and budget validation")
			}
		})
	}
}

func TestRunJudgeMetaAndReportWritesOneBoundReport(t *testing.T) {
	set, config := cliJudgeMetaFixture(t)
	t.Setenv("OCH_EVAL_LIVE_CONFIRM", eval.LiveConfirmValue)
	calls := 0
	caller := func(_ context.Context, _, bundle string) (string, eval.ScorerUsage, error) {
		calls++
		var criteria []map[string]any
		for _, criterion := range config.Criteria {
			criteria = append(criteria, map[string]any{"id": criterion.ID, "status": "pass"})
		}
		payload := map[string]any{
			"verdict": "pass", "score": 1, "criteria": criteria,
			"evidenceReferences": bundlePaths(bundle), "rationale": "all evidence supports pass",
		}
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		return string(data), eval.ScorerUsage{InputTokens: 5, OutputTokens: 2}, nil
	}
	var stdout, stderr bytes.Buffer
	if code := runJudgeMetaAndReport(context.Background(), set, config, true, 2, caller, nil, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if calls != 2 {
		t.Fatalf("calls=%d, want 2", calls)
	}
	var report eval.JudgeMetaReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Schema != eval.SchemaJudgeMetaReport || report.SetDigest == "" || report.JudgeConfigDigest != set.JudgeConfigDigest {
		t.Fatalf("report identity = %+v", report)
	}
	if report.Summary.ExactMatches != 2 || report.Summary.UnsafePasses != 0 {
		t.Fatalf("summary=%+v", report.Summary)
	}
}

func TestRunJudgeMetaAndReportWritesIncompleteReportOnCancellation(t *testing.T) {
	set, config := cliJudgeMetaFixture(t)
	t.Setenv("OCH_EVAL_LIVE_CONFIRM", eval.LiveConfirmValue)
	ctx, cancel := context.WithCancel(context.Background())
	caller := func(_ context.Context, _, bundle string) (string, eval.ScorerUsage, error) {
		cancel()
		var criteria []map[string]any
		for _, criterion := range config.Criteria {
			criteria = append(criteria, map[string]any{"id": criterion.ID, "status": "pass"})
		}
		data, err := json.Marshal(map[string]any{
			"verdict": "pass", "score": nil, "criteria": criteria,
			"evidenceReferences": bundlePaths(bundle), "rationale": "paid first result",
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(data), eval.ScorerUsage{InputTokens: 9}, nil
	}
	var stdout, stderr bytes.Buffer
	if code := runJudgeMetaAndReport(ctx, set, config, true, 2, caller, nil, &stdout, &stderr); code != exitIndeterminate {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	report, err := eval.DecodeJudgeMetaReport(stdout.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if report.Complete || report.CompletedCalls != 1 || report.Observations[0].ScorerUsage.InputTokens != 9 {
		t.Fatalf("partial report=%+v", report)
	}
}

func bundlePaths(bundle string) []string {
	var paths []string
	for _, line := range strings.Split(bundle, "\n") {
		if strings.HasPrefix(line, "--- path: ") {
			paths = append(paths, strings.SplitN(strings.TrimPrefix(line, "--- path: "), " ", 2)[0])
		}
	}
	return paths
}

func TestCheckedInJudgeMetaSeedBindsAndRunsKeyless(t *testing.T) {
	config, err := loadJudgeConfig(filepath.Join(repoRootDir(t), "eval", "judges", "semantic-meta-judge.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	setPath := filepath.Join(repoRootDir(t), "eval", "judge-meta", "semantic-seed-v1.json")
	data, err := os.ReadFile(setPath)
	if err != nil {
		t.Fatal(err)
	}
	set, err := eval.DecodeJudgeMetaSet(data)
	if err != nil {
		t.Fatal(err)
	}
	actualDigest, err := eval.JudgeConfigDigest(config)
	if err != nil {
		t.Fatal(err)
	}
	if set.JudgeConfigDigest != actualDigest {
		t.Fatalf("checked-in judgeConfigDigest = %q, want %q", set.JudgeConfigDigest, actualDigest)
	}
	if len(set.Cases) != 6 || set.CallCount() != 18 {
		t.Fatalf("cases=%d calls=%d, want 6 and 18", len(set.Cases), set.CallCount())
	}

	expected := make(map[string]eval.ScoreVerdict, len(set.Cases))
	for _, metaCase := range set.Cases {
		expected[metaCase.ID] = metaCase.Label.Verdict
	}
	calls := 0
	caller := func(_ context.Context, _, bundle string) (string, eval.ScorerUsage, error) {
		calls++
		caseID := ""
		for id := range expected {
			if strings.Contains(bundle, "cases/"+id+"/") {
				caseID = id
				break
			}
		}
		if caseID == "" {
			t.Fatalf("could not identify checked-in case from bundle: %s", bundle)
		}
		verdict := expected[caseID]
		criteria := []map[string]any{{"id": config.Criteria[0].ID, "status": verdict}}
		payload := map[string]any{
			"verdict": verdict, "score": nil, "criteria": criteria,
			"evidenceReferences": bundlePaths(bundle), "rationale": "fixture follows the reviewed label",
		}
		if verdict == eval.ScoreIndeterminate {
			payload["contradictoryEvidence"] = []string{"cases/unresolved-contradiction/audit.txt"}
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded), eval.ScorerUsage{}, nil
	}
	report, err := eval.RunJudgeMetaSet(context.Background(), set, config, caller, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 18 || report.Summary.ExactMatches != 18 || report.Summary.UnsafePasses != 0 || report.Summary.Overclaims != 0 {
		t.Fatalf("calls=%d summary=%+v", calls, report.Summary)
	}
}
