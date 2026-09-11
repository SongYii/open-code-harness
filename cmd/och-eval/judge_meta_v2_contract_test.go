package main

import (
	"os"
	"path/filepath"
	"reflect"
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

type boundMetaFixture struct {
	config eval.JudgeConfig
	set    eval.JudgeMetaSet
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
	return boundMetaFixture{config: config, set: set}
}

func loadJudgeMetaSetAt(t *testing.T, root, relative string) eval.JudgeMetaSet {
	t.Helper()
	set, err := loadJudgeMetaSet(filepath.Join(root, "eval", relative))
	if err != nil {
		t.Fatal(err)
	}
	return set
}
