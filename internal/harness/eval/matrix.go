package eval

import "fmt"

// Cell identifies one Scenario × Subject × Executor combination within an
// EvalSet (design §4).
type Cell struct {
	ScenarioID ScenarioID
	SubjectID  SubjectID
	ExecutorID ExecutorID
}

// CellAttempt is one Cell paired with a repetition index, in design §9's
// fixed expansion order (Scenario, Subject, Executor, then repetition
// index). Constructing the real Attempt document — assigning a generated
// AttemptID and the referenced Scenario/Subject/Executor digests — is the
// runner's job (not yet implemented), not this expansion step's.
type CellAttempt struct {
	Cell            Cell
	RepetitionIndex int
}

// ExpandCells returns the Cartesian product of set's Scenario, Subject, and
// Executor references in design §9's fixed nesting order. It performs no
// capability checking; ExpandAttempts does, because that needs the full
// Scenario and Executor documents, not just their references.
func (set EvalSet) ExpandCells() []Cell {
	cells := make([]Cell, 0, len(set.Scenarios)*len(set.Subjects)*len(set.Executors))
	for _, scenario := range set.Scenarios {
		for _, subject := range set.Subjects {
			for _, executor := range set.Executors {
				cells = append(cells, Cell{ScenarioID: scenario.ID, SubjectID: subject.ID, ExecutorID: executor.ID})
			}
		}
	}
	return cells
}

// ExpandAttempts resolves set's references against the full documents they
// name, confirms every resolved document's own digest still matches the
// digest set froze (a changed checked-in Scenario or a changed Subject/
// Executor snapshot must not silently expand under a stale EvalSet), checks
// that every Cell's Scenario-required capability is present on its
// Executor — a missing capability fails the whole set, not one skipped row
// (design §9) — and then, only if every check passes, returns the ordered
// (Cell, repetition index) list. It fails before returning anything if the
// total would exceed limits.maxExpandedAttempts.
//
// scenarios, subjects, and executors must contain every document set
// references, keyed by ID; ExpandAttempts does not load them itself (design
// §7: Scenarios are checked-in files, and this package does not open the
// filesystem on their behalf).
func ExpandAttempts(set EvalSet, scenarios map[ScenarioID]Scenario, subjects map[SubjectID]Subject, executors map[ExecutorID]Executor) ([]CellAttempt, error) {
	if err := set.Validate(); err != nil {
		return nil, fmt.Errorf("eval: expand attempts: %w", err)
	}

	for _, ref := range set.Scenarios {
		scenario, ok := scenarios[ref.ID]
		if !ok {
			return nil, fmt.Errorf("eval: expand attempts: scenario %q was not provided", ref.ID)
		}
		digest, err := ScenarioDigest(scenario)
		if err != nil {
			return nil, fmt.Errorf("eval: expand attempts: scenario %q: %w", ref.ID, err)
		}
		if digest != ref.Digest {
			return nil, fmt.Errorf("eval: expand attempts: scenario %q digest changed since the EvalSet was frozen", ref.ID)
		}
	}
	for _, ref := range set.Subjects {
		subject, ok := subjects[ref.ID]
		if !ok {
			return nil, fmt.Errorf("eval: expand attempts: subject %q was not provided", ref.ID)
		}
		digest, err := SubjectDigest(subject)
		if err != nil {
			return nil, fmt.Errorf("eval: expand attempts: subject %q: %w", ref.ID, err)
		}
		if digest != ref.Digest {
			return nil, fmt.Errorf("eval: expand attempts: subject %q digest changed since the EvalSet was frozen", ref.ID)
		}
	}
	for _, ref := range set.Executors {
		executor, ok := executors[ref.ID]
		if !ok {
			return nil, fmt.Errorf("eval: expand attempts: executor %q was not provided", ref.ID)
		}
		digest, err := ExecutorDigest(executor)
		if err != nil {
			return nil, fmt.Errorf("eval: expand attempts: executor %q: %w", ref.ID, err)
		}
		if digest != ref.Digest {
			return nil, fmt.Errorf("eval: expand attempts: executor %q digest changed since the EvalSet was frozen", ref.ID)
		}
	}

	for _, scenarioRef := range set.Scenarios {
		scenario := scenarios[scenarioRef.ID]
		for _, executorRef := range set.Executors {
			executor := executors[executorRef.ID]
			if missing := missingCapability(scenario.RequiredCapabilities, executor.Capabilities); missing != "" {
				return nil, fmt.Errorf("eval: expand attempts: executor %q lacks capability %q required by scenario %q",
					executorRef.ID, missing, scenarioRef.ID)
			}
		}
	}

	cells := set.ExpandCells()
	limits := set.Limits.withDefaults()
	totalAttempts := len(cells) * set.RepetitionCount
	if totalAttempts > limits.MaxExpandedAttempts {
		return nil, fmt.Errorf("eval: expand attempts: %d attempts exceeds limits.maxExpandedAttempts (%d)",
			totalAttempts, limits.MaxExpandedAttempts)
	}

	attempts := make([]CellAttempt, 0, totalAttempts)
	for _, cell := range cells {
		for repetition := 0; repetition < set.RepetitionCount; repetition++ {
			attempts = append(attempts, CellAttempt{Cell: cell, RepetitionIndex: repetition})
		}
	}
	return attempts, nil
}

func missingCapability(required, available []string) string {
	have := make(map[string]struct{}, len(available))
	for _, capability := range available {
		have[capability] = struct{}{}
	}
	for _, capability := range required {
		if _, ok := have[capability]; !ok {
			return capability
		}
	}
	return ""
}
