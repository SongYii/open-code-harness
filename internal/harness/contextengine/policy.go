package contextengine

import (
	"context"
	"errors"
	"fmt"

	"github.com/SongYii/open-code-harness/sdk/contextpolicy"
)

var ErrPolicy = errors.New("contextengine: invalid context policy decision")

// Plan delegates only trigger/cut selection. Candidate construction and
// validation remain core responsibilities. A nil policy preserves legacy
// planning byte-for-byte; the default is never used to conceal plugin errors.
func Plan(ctx context.Context, policy contextpolicy.Policy, input PlanInput, trigger string, previousThrough, previousTokens uint64) (PlanResult, error) {
	if err := ctx.Err(); err != nil {
		return PlanResult{}, err
	}
	if policy == nil {
		return SelectCutPoint(input)
	}
	forced := input
	forced.Force = true
	floor, err := SelectCutPoint(forced)
	if err != nil {
		return PlanResult{}, err
	}
	all := input
	all.Force = false
	all.Budget.Trigger = ^uint64(0)
	uncut, err := SelectCutPoint(all)
	if err != nil {
		return PlanResult{}, err
	}
	metadata := contextpolicy.Input{
		Trigger: trigger, Force: input.Force || uncut.EstimatedTokens+previousTokens > input.Budget.HardInput,
		Budget:          contextpolicy.Budget{HardInput: input.Budget.HardInput, Trigger: input.Budget.Trigger, Target: input.Budget.Target, ProtectedTail: input.Budget.ProtectedTail, SummaryOutputCap: input.Budget.SummaryOutputCap},
		EstimatedTokens: uncut.EstimatedTokens + previousTokens, PreviousThroughSequence: previousThrough,
	}
	// Walk once: no extra store reads or quadratic materialization per cut.
	retainedTokens := uncut.EstimatedTokens
	var turns uint64
	for _, unit := range input.Units {
		if unit.Kind == UnitKindTurn {
			turns++
		}
	}
	boundaries := make(map[uint64]int)
	for index, unit := range input.Units {
		if index > 0 && index <= len(floor.CoveredUnits) && unit.Kind == UnitKindTurn {
			id := uint64(index)
			boundaries[id] = index
			metadata.Candidates = append(metadata.Candidates, contextpolicy.Candidate{ID: id, CoveredUnits: id, RetainedUnits: uint64(len(input.Units) - index), RetainedTurns: turns, EstimatedRetainedTokens: retainedTokens})
		}
		tokens := input.Meter.EstimateMessages(unit.Messages)
		if tokens < retainedTokens {
			retainedTokens -= tokens
		} else {
			retainedTokens = 0
		}
		if unit.Kind == UnitKindTurn {
			turns--
		}
	}
	// Input is passed by value: Force and the Candidates slice header (and
	// therefore its length) are isolated already; these locals are copies
	// for validation, not an additional mutation barrier. Candidate elements
	// share backing storage, so eligibility is checked against the separate
	// core-owned boundaries map, never against the callback-visible IDs.
	force, candidates := metadata.Force, len(metadata.Candidates)
	decision, err := policy.Plan(ctx, metadata)
	if ctx.Err() != nil {
		return PlanResult{}, ctx.Err()
	}
	if err != nil {
		return PlanResult{}, fmt.Errorf("%w: callback: %w", ErrPolicy, err)
	}
	if !decision.Compact {
		if decision.CandidateID != 0 || (force && candidates > 0) {
			return PlanResult{}, fmt.Errorf("%w: forced compaction cannot be vetoed", ErrPolicy)
		}
		return uncut, nil
	}
	index, ok := boundaries[decision.CandidateID]
	if !ok {
		return PlanResult{}, fmt.Errorf("%w: candidate was not offered", ErrPolicy)
	}
	retained := input.Units[index:]
	selected := all
	selected.Units = retained
	estimate, err := SelectCutPoint(selected)
	if err != nil {
		return PlanResult{}, err
	}
	return PlanResult{NeedsCompaction: true, CoveredThroughSequence: input.Units[index-1].LastSequence, CoveredUnits: input.Units[:index], RetainedUnits: retained, EstimatedTokens: estimate.EstimatedTokens}, nil
}
