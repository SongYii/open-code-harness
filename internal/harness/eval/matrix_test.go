package eval

import "testing"

func expansionFixtures(t *testing.T) (EvalSet, map[ScenarioID]Scenario, map[SubjectID]Subject, map[ExecutorID]Executor) {
	t.Helper()
	set := validEvalSet(t)
	scenario := validScenario()
	subject := validSubject()
	executor := validExecutorInProcess()
	executor.Capabilities = append([]string{}, scenario.RequiredCapabilities...)
	executorDigest, err := ExecutorDigest(executor)
	if err != nil {
		t.Fatalf("ExecutorDigest: %v", err)
	}
	set.Executors[0].Digest = executorDigest
	return set, map[ScenarioID]Scenario{scenario.ID: scenario},
		map[SubjectID]Subject{subject.ID: subject},
		map[ExecutorID]Executor{executor.ID: executor}
}

func TestExpandCellsIsTheCartesianProductInFixedOrder(t *testing.T) {
	set := validEvalSet(t)
	set.Scenarios = append(set.Scenarios, ScenarioRef{ID: "scenario-2", Digest: set.Scenarios[0].Digest})
	cells := set.ExpandCells()
	want := len(set.Scenarios) * len(set.Subjects) * len(set.Executors)
	if len(cells) != want {
		t.Fatalf("ExpandCells returned %d cells, want %d", len(cells), want)
	}
	if cells[0].ScenarioID != set.Scenarios[0].ID || cells[0].SubjectID != set.Subjects[0].ID || cells[0].ExecutorID != set.Executors[0].ID {
		t.Fatalf("ExpandCells()[0] = %+v, want the first Scenario/Subject/Executor combination", cells[0])
	}
	if cells[len(cells)-1].ScenarioID != set.Scenarios[len(set.Scenarios)-1].ID {
		t.Fatalf("ExpandCells() did not iterate Scenario as the outermost dimension: last cell = %+v", cells[len(cells)-1])
	}
}

func TestExpandAttemptsHappyPath(t *testing.T) {
	set, scenarios, subjects, executors := expansionFixtures(t)
	attempts, err := ExpandAttempts(set, scenarios, subjects, executors)
	if err != nil {
		t.Fatalf("ExpandAttempts: %v", err)
	}
	want := len(set.ExpandCells()) * set.RepetitionCount
	if len(attempts) != want {
		t.Fatalf("ExpandAttempts returned %d attempts, want %d", len(attempts), want)
	}
	seenRepetitions := map[int]bool{}
	for _, attempt := range attempts {
		seenRepetitions[attempt.RepetitionIndex] = true
	}
	for repetition := 0; repetition < set.RepetitionCount; repetition++ {
		if !seenRepetitions[repetition] {
			t.Fatalf("ExpandAttempts never produced repetition index %d", repetition)
		}
	}
}

func TestExpandAttemptsRejectsStaleScenarioDigest(t *testing.T) {
	set, scenarios, subjects, executors := expansionFixtures(t)
	set.Scenarios[0].Digest = mustDigest(t, 9)
	if _, err := ExpandAttempts(set, scenarios, subjects, executors); err == nil {
		t.Fatal("ExpandAttempts accepted a Scenario whose digest no longer matches the frozen reference")
	}
}

func TestExpandAttemptsRejectsMissingCapability(t *testing.T) {
	set, scenarios, subjects, executors := expansionFixtures(t)
	for id, executor := range executors {
		executor.Capabilities = nil
		executors[id] = executor
	}
	// Executor.Validate requires at least one capability, so give it an
	// unrelated one instead of none, then refreeze its digest.
	for id, executor := range executors {
		executor.Capabilities = []string{"unrelated-capability"}
		digest, err := ExecutorDigest(executor)
		if err != nil {
			t.Fatalf("ExecutorDigest: %v", err)
		}
		executors[id] = executor
		for index, ref := range set.Executors {
			if ref.ID == id {
				set.Executors[index].Digest = digest
			}
		}
	}
	if _, err := ExpandAttempts(set, scenarios, subjects, executors); err == nil {
		t.Fatal("ExpandAttempts accepted a Cell whose Executor lacks a Scenario-required capability")
	}
}

func TestExpandAttemptsRejectsUnprovidedDocument(t *testing.T) {
	set, scenarios, subjects, executors := expansionFixtures(t)
	delete(scenarios, set.Scenarios[0].ID)
	if _, err := ExpandAttempts(set, scenarios, subjects, executors); err == nil {
		t.Fatal("ExpandAttempts accepted a set referencing a Scenario that was not provided")
	}
}

func TestExpandAttemptsRejectsExceedingMaxExpandedAttempts(t *testing.T) {
	set, scenarios, subjects, executors := expansionFixtures(t)
	set.Limits.MaxExpandedAttempts = 1
	set.RepetitionCount = 2
	if _, err := ExpandAttempts(set, scenarios, subjects, executors); err == nil {
		t.Fatal("ExpandAttempts accepted an expansion exceeding limits.maxExpandedAttempts")
	}
}
