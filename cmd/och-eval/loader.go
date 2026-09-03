package main

import (
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
