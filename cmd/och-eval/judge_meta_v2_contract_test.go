package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

func TestCheckedInJudgeMetaV2BindingsAndHoldoutSeparation(t *testing.T) {
	root := repoRootDir(t)
	deepseek := loadBoundMetaFixture(t, root,
		"judges/semantic-meta-judge-deepseek-priced-v2.json",
		"prices/deepseek-v4-pro-peak-2026-09-11.json",
		"judge-meta/semantic-calibration-v2-deepseek.json")
	deepseekHoldout := loadJudgeMetaSetAt(t, root, "judge-meta/semantic-validation-v2-deepseek.json")
	if err := eval.VerifyJudgeMetaSetBinding(deepseekHoldout, deepseek.config); err != nil {
		t.Fatal(err)
	}
	openai := loadBoundMetaFixture(t, root,
		"judges/semantic-meta-judge-openai-v1.json",
		"prices/openai-gpt-5.4-mini-standard-2026-09-11.json",
		"judge-meta/semantic-validation-v2-openai.json")
	calibrationDigest, err := eval.JudgeMetaSetDigest(deepseek.set)
	if err != nil {
		t.Fatal(err)
	}
	holdoutDigest, err := eval.JudgeMetaSetDigest(deepseekHoldout)
	if err != nil {
		t.Fatal(err)
	}
	if calibrationDigest == holdoutDigest {
		t.Fatal("calibration and holdout sets unexpectedly share an identity")
	}
	if !reflect.DeepEqual(deepseekHoldout.Cases, openai.set.Cases) || deepseekHoldout.RepetitionCount != openai.set.RepetitionCount {
		t.Fatal("provider holdout corpora drifted")
	}
	if len(deepseek.set.Cases) != 6 || len(deepseekHoldout.Cases) != 6 || deepseek.set.CallCount() != 18 || deepseekHoldout.CallCount() != 18 {
		t.Fatalf("v2 corpus sizes: calibration=%d/%d holdout=%d/%d", len(deepseek.set.Cases), deepseek.set.CallCount(), len(deepseekHoldout.Cases), deepseekHoldout.CallCount())
	}
}

func TestCheckedInJudgeMetaV3UsesFreshCasesAndEquivalentProviderHoldouts(t *testing.T) {
	root := repoRootDir(t)
	deepseek := loadBoundMetaFixture(t, root,
		"judges/semantic-meta-judge-deepseek-v3.json",
		"prices/deepseek-v4-pro-peak-2026-09-11.json",
		"judge-meta/semantic-calibration-v3-deepseek.json")
	deepseekHoldout := loadJudgeMetaSetAt(t, root, "judge-meta/semantic-validation-v3-deepseek.json")
	if err := eval.VerifyJudgeMetaSetBinding(deepseekHoldout, deepseek.config); err != nil {
		t.Fatal(err)
	}
	openai := loadBoundMetaFixture(t, root,
		"judges/semantic-meta-judge-openai-v2.json",
		"prices/openai-gpt-5.4-mini-standard-2026-09-11.json",
		"judge-meta/semantic-validation-v3-openai.json")
	if !reflect.DeepEqual(deepseekHoldout.Cases, openai.set.Cases) || deepseekHoldout.RepetitionCount != openai.set.RepetitionCount {
		t.Fatal("v3 provider holdout corpora drifted")
	}
	if len(deepseek.set.Cases) != 6 || len(deepseekHoldout.Cases) != 6 || deepseek.set.CallCount() != 18 || deepseekHoldout.CallCount() != 18 {
		t.Fatalf("v3 corpus sizes: calibration=%d/%d holdout=%d/%d", len(deepseek.set.Cases), deepseek.set.CallCount(), len(deepseekHoldout.Cases), deepseekHoldout.CallCount())
	}

	seen := make(map[string]string)
	sets := []struct {
		name string
		set  eval.JudgeMetaSet
	}{
		{"v1-seed", loadJudgeMetaSetAt(t, root, "judge-meta/semantic-seed-v1.json")},
		{"v2-calibration", loadJudgeMetaSetAt(t, root, "judge-meta/semantic-calibration-v2-deepseek.json")},
		{"v2-holdout", loadJudgeMetaSetAt(t, root, "judge-meta/semantic-validation-v2-deepseek.json")},
		{"v3-calibration", deepseek.set},
		{"v3-holdout", deepseekHoldout},
	}
	for _, item := range sets {
		for _, metaCase := range item.set.Cases {
			if previous, ok := seen[metaCase.ID]; ok {
				t.Fatalf("case %q appears in both %s and %s", metaCase.ID, previous, item.name)
			}
			seen[metaCase.ID] = item.name
		}
	}

	runMetaSetKeylessAtItsLabels(t, deepseek.set, deepseek.config, &deepseek.table)
}

func runMetaSetKeylessAtItsLabels(t *testing.T, set eval.JudgeMetaSet, config eval.JudgeConfig, table *eval.PriceTable) {
	t.Helper()
	expected := make(map[string]eval.ScoreVerdict, len(set.Cases))
	for _, metaCase := range set.Cases {
		expected[metaCase.ID] = metaCase.Label.Verdict
	}
	caller := func(_ context.Context, prompt, bundle string) (string, eval.ScorerUsage, error) {
		if prompt != eval.QualityJudgePromptV2 {
			t.Fatalf("v3 set used the wrong prompt")
		}
		var caseID string
		for id := range expected {
			if strings.Contains(bundle, "cases/"+id+"/") {
				caseID = id
				break
			}
		}
		if caseID == "" {
			t.Fatalf("could not identify case from bundle")
		}
		verdict := expected[caseID]
		payload := map[string]any{
			"verdict":            verdict,
			"criteria":           []map[string]any{{"id": config.Criteria[0].ID, "status": verdict}},
			"evidenceReferences": bundlePaths(bundle),
			"rationale":          "fixture follows the reviewed label",
		}
		if verdict == eval.ScoreIndeterminate {
			payload["unresolvedContradictoryEvidence"] = bundlePaths(bundle)
		}
		data, err := json.Marshal(payload)
		return string(data), eval.ScorerUsage{}, err
	}
	report, err := eval.RunJudgeMetaSet(context.Background(), set, config, caller, table)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Complete || report.Summary.ExactMatches != set.CallCount() {
		t.Fatalf("keyless v3 report = %+v", report.Summary)
	}
}

type boundMetaFixture struct {
	config eval.JudgeConfig
	set    eval.JudgeMetaSet
	table  eval.PriceTable
}

func loadBoundMetaFixture(t *testing.T, root, configPath, pricePath, setPath string) boundMetaFixture {
	t.Helper()
	config, err := loadJudgeConfig(filepath.Join(root, "eval", configPath))
	if err != nil {
		t.Fatal(err)
	}
	priceData, err := os.ReadFile(filepath.Join(root, "eval", pricePath))
	if err != nil {
		t.Fatal(err)
	}
	table, err := eval.DecodePriceTable(priceData)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := eval.PriceTableDigest(table)
	if err != nil {
		t.Fatal(err)
	}
	if digest != config.PriceTableDigest {
		t.Fatalf("price digest = %q, config binds %q", digest, config.PriceTableDigest)
	}
	set := loadJudgeMetaSetAt(t, root, setPath)
	if err := eval.VerifyJudgeMetaSetBinding(set, config); err != nil {
		t.Fatal(err)
	}
	return boundMetaFixture{config: config, set: set, table: table}
}

func loadJudgeMetaSetAt(t *testing.T, root, relative string) eval.JudgeMetaSet {
	t.Helper()
	set, err := loadJudgeMetaSet(filepath.Join(root, "eval", relative))
	if err != nil {
		t.Fatal(err)
	}
	return set
}
