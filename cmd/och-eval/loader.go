package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

// documentTree is one loaded EvalSet plus every document it references,
// resolved from disk by this command's own checked-in layout convention
// (not part of the eval package's own contract, which only ever accepts
// already-decoded documents in memory):
//
//	<root>/sets/<id>.json
//	<root>/scenarios/<id>/scenario.json
//	<root>/scenarios/<id>/fixture/            (a Scenario's fixture source directory)
//	<root>/subjects/<id>.json
//	<root>/executors/<id>.json
//
// <root> is the EvalSet file's own parent's parent directory — e.g. a set
// at eval/sets/pr-inprocess.json resolves scenarios under
// eval/scenarios/<id>/.
type documentTree struct {
	Set            eval.EvalSet
	Scenarios      map[eval.ScenarioID]eval.Scenario
	Subjects       map[eval.SubjectID]eval.Subject
	Executors      map[eval.ExecutorID]eval.Executor
	FixtureSources map[eval.ScenarioID]string
}

func loadDocumentTree(setPath string) (documentTree, error) {
	setData, err := os.ReadFile(setPath)
	if err != nil {
		return documentTree{}, fmt.Errorf("read eval set: %w", err)
	}
	set, err := eval.DecodeEvalSet(setData)
	if err != nil {
		return documentTree{}, fmt.Errorf("decode eval set: %w", err)
	}

	absSetPath, err := filepath.Abs(setPath)
	if err != nil {
		return documentTree{}, fmt.Errorf("resolve eval set path: %w", err)
	}
	root := filepath.Dir(filepath.Dir(absSetPath))

	tree := documentTree{
		Set:            set,
		Scenarios:      make(map[eval.ScenarioID]eval.Scenario, len(set.Scenarios)),
		Subjects:       make(map[eval.SubjectID]eval.Subject, len(set.Subjects)),
		Executors:      make(map[eval.ExecutorID]eval.Executor, len(set.Executors)),
		FixtureSources: make(map[eval.ScenarioID]string, len(set.Scenarios)),
	}

	for _, ref := range set.Scenarios {
		scenarioDir := filepath.Join(root, "scenarios", string(ref.ID))
		data, err := os.ReadFile(filepath.Join(scenarioDir, "scenario.json"))
		if err != nil {
			return documentTree{}, fmt.Errorf("read scenario %q: %w", ref.ID, err)
		}
		scenario, err := eval.DecodeScenario(data)
		if err != nil {
			return documentTree{}, fmt.Errorf("decode scenario %q: %w", ref.ID, err)
		}
		tree.Scenarios[scenario.ID] = scenario
		tree.FixtureSources[scenario.ID] = filepath.Join(scenarioDir, "fixture")
	}
	for _, ref := range set.Subjects {
		data, err := os.ReadFile(filepath.Join(root, "subjects", string(ref.ID)+".json"))
		if err != nil {
			return documentTree{}, fmt.Errorf("read subject %q: %w", ref.ID, err)
		}
		subject, err := eval.DecodeSubject(data)
		if err != nil {
			return documentTree{}, fmt.Errorf("decode subject %q: %w", ref.ID, err)
		}
		tree.Subjects[subject.ID] = subject
	}
	for _, ref := range set.Executors {
		data, err := os.ReadFile(filepath.Join(root, "executors", string(ref.ID)+".json"))
		if err != nil {
			return documentTree{}, fmt.Errorf("read executor %q: %w", ref.ID, err)
		}
		executor, err := eval.DecodeExecutor(data)
		if err != nil {
			return documentTree{}, fmt.Errorf("decode executor %q: %w", ref.ID, err)
		}
		tree.Executors[executor.ID] = executor
	}
	return tree, nil
}

// loadJudgeConfig reads and strictly decodes one frozen
// `och.eval.judge-config` document. It never resolves the credential the
// document names — only its name is ever read from the file, and the
// value is looked up much later, inside the provider call, after every
// consent and binding check has already passed.
func loadJudgeConfig(path string) (eval.JudgeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return eval.JudgeConfig{}, fmt.Errorf("read judge config: %w", err)
	}
	config, err := eval.DecodeJudgeConfig(data)
	if err != nil {
		return eval.JudgeConfig{}, fmt.Errorf("decode judge config: %w", err)
	}
	return config, nil
}

// loadJudgePriceTable resolves the optional price table a JudgeConfig may
// pin. A config that names a priceTableDigest requires the table and
// requires it to match: a Score that claimed a computed cost against a
// table nobody can identify would be worse than one that honestly
// reported the price as unavailable.
func loadJudgePriceTable(config eval.JudgeConfig, path string) (*eval.PriceTable, error) {
	if config.PriceTableDigest == "" {
		if path == "" {
			return nil, nil
		}
		return nil, fmt.Errorf("-price-table was given but this judge config names no priceTableDigest")
	}
	if path == "" {
		return nil, fmt.Errorf("this judge config names priceTableDigest %q, so -price-table is required", config.PriceTableDigest)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read price table: %w", err)
	}
	var table eval.PriceTable
	if err := json.Unmarshal(data, &table); err != nil {
		return nil, fmt.Errorf("decode price table: %w", err)
	}
	digest, err := eval.PriceTableDigest(table)
	if err != nil {
		return nil, err
	}
	if digest != config.PriceTableDigest {
		return nil, fmt.Errorf("price table digest %q disagrees with the frozen judge config's %q", digest, config.PriceTableDigest)
	}
	return &table, nil
}

// resolveRunJudgeConfig turns `och-eval run`'s own -judge-config flag into
// the value RunnerInputs needs, applying the same lane rule the EvalSet
// document itself carries: a live set requires the judge configuration its
// judgeConfigDigest names, and a fixture set must not be given one.
//
// The runner enforces this too, but doing it here first names the flag an
// operator actually has to pass instead of reporting a generic whole-set
// validation failure. The digest is verified before the run starts, which
// is what keeps a mismatched configuration from costing anything.
func resolveRunJudgeConfig(set eval.EvalSet, path string) (*eval.JudgeConfig, error) {
	if set.Lane != eval.LaneLive {
		if path != "" {
			return nil, fmt.Errorf("-judge-config was given but this EvalSet's own lane is %q", set.Lane)
		}
		return nil, nil
	}
	if path == "" {
		return nil, fmt.Errorf("this EvalSet's own lane is %q and it names judgeConfigDigest %q: -judge-config is required",
			set.Lane, set.JudgeConfigDigest)
	}
	config, err := loadJudgeConfig(path)
	if err != nil {
		return nil, err
	}
	digest, err := eval.JudgeConfigDigest(config)
	if err != nil {
		return nil, err
	}
	if digest != set.JudgeConfigDigest {
		return nil, fmt.Errorf("judge config digest %q disagrees with this EvalSet's own judgeConfigDigest %q",
			digest, set.JudgeConfigDigest)
	}
	return &config, nil
}
