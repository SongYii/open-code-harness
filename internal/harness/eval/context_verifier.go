package eval

import (
	"fmt"
	"strings"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// Context mechanism verifier IDs. Each names one focused behavior rather
// than one Scenario-ID-aware mega-verifier, so a Scenario declares exactly
// the mechanisms it exercises and a failure names the mechanism that failed.
const (
	VerifierContextManualReset     = "context-manual-reset-v1"
	VerifierContextManualSummary   = "context-manual-summary-v1"
	VerifierContextPreTurnSummary  = "context-pre-turn-summary-v1"
	VerifierContextCheckpointReuse = "context-checkpoint-reused-v1"
	VerifierContextBudgetBounds    = "context-budget-bounds-v1"
	VerifierContextProjection      = "context-projection-present-v1"
)

// contextCriterion is the shared entry point every Context verifier uses. It
// builds the trace once and maps a build failure to indeterminate.
//
// That mapping is the whole point of routing through here. "The evidence
// does not hold together" and "the evidence is intact and shows this did not
// happen" are different claims, and a verifier that collapsed the first into
// the second would report a subject failure for what is really a collection
// or correlation problem.
func contextCriterion(id string, reader *ArtifactReader, evaluate func(*contextTrace) CriterionResult) CriterionResult {
	trace, err := buildContextTrace(reader)
	if err != nil {
		return indeterminate(id, err.Error())
	}
	return evaluate(trace)
}

func indeterminate(id, detail string) CriterionResult {
	return CriterionResult{ID: id, Status: ScoreIndeterminate, Detail: boundedDetail(detail)}
}

func failed(id, detail string) CriterionResult {
	return CriterionResult{ID: id, Status: ScoreFail, Detail: boundedDetail(detail)}
}

func passed(id, detail string) CriterionResult {
	return CriterionResult{ID: id, Status: ScorePass, Detail: boundedDetail(detail)}
}

// boundedDetail keeps verifier explanations inside the Score document's own
// cap. Detail names counts and coordinates, never content, so truncation
// here should be unreachable in practice; it exists so a future verifier
// cannot publish an unbounded string by accident.
func boundedDetail(detail string) string {
	return boundedString(detail, maxCriterionDetailBytes)
}

// completedCompactions returns every closed, successful compaction bracket
// whose start matches trigger and strategy, in canonical order.
func completedCompactions(trace *contextTrace, trigger, strategy string) []*contextCompaction {
	var matched []*contextCompaction
	for _, compaction := range trace.CompactionsInOrder() {
		if compaction.Started.Trigger != trigger || compaction.Started.Strategy != strategy {
			continue
		}
		if compaction.Completed == nil {
			continue
		}
		matched = append(matched, compaction)
	}
	return matched
}

// verifyContextManualReset requires a manual reset bracket that completed
// with a source_tail_reset_v1 checkpoint whose coverage actually advanced.
// A checkpoint covering nothing would mean the reset discarded no history.
func verifyContextManualReset(reader *ArtifactReader, _ Scenario) CriterionResult {
	return contextCriterion(VerifierContextManualReset, reader, func(trace *contextTrace) CriterionResult {
		matched := completedCompactions(trace, domain.ContextTriggerManual, domain.ContextStrategyReset)
		if len(matched) == 0 {
			return failed(VerifierContextManualReset,
				fmt.Sprintf("no completed manual/reset compaction in %d brackets", len(trace.CompactionsInOrder())))
		}
		for _, compaction := range matched {
			checkpoint := compaction.Completed.Checkpoint
			if checkpoint.Kind != domain.ContextCheckpointKindSourceTailReset {
				continue
			}
			if checkpoint.CoveredEventCount == 0 || checkpoint.ThroughSequence == 0 {
				return failed(VerifierContextManualReset, fmt.Sprintf(
					"manual reset checkpoint %q covers nothing (events=%d throughSequence=%d)",
					checkpoint.ID, checkpoint.CoveredEventCount, checkpoint.ThroughSequence))
			}
			return passed(VerifierContextManualReset, fmt.Sprintf(
				"manual reset completed checkpoint %q covering %d events through sequence %d",
				checkpoint.ID, checkpoint.CoveredEventCount, checkpoint.ThroughSequence))
		}
		return failed(VerifierContextManualReset,
			fmt.Sprintf("%d manual/reset brackets completed, none with a %s checkpoint",
				len(matched), domain.ContextCheckpointKindSourceTailReset))
	})
}

// verifyContextManualSummary requires a manual summary bracket that
// completed with a rolling_summary_v1 checkpoint carrying valid version
// fields and a non-empty summary. Version fields matter because a
// checkpoint that did not record which prompt produced it cannot be
// reproduced later.
func verifyContextManualSummary(reader *ArtifactReader, _ Scenario) CriterionResult {
	return contextCriterion(VerifierContextManualSummary, reader, func(trace *contextTrace) CriterionResult {
		matched := completedCompactions(trace, domain.ContextTriggerManual, domain.ContextStrategySummary)
		if len(matched) == 0 {
			return failed(VerifierContextManualSummary,
				fmt.Sprintf("no completed manual/summary compaction in %d brackets", len(trace.CompactionsInOrder())))
		}
		for _, compaction := range matched {
			checkpoint := compaction.Completed.Checkpoint
			if checkpoint.Kind != domain.ContextCheckpointKindRollingSummary {
				continue
			}
			if strings.TrimSpace(checkpoint.Summary) == "" {
				return failed(VerifierContextManualSummary,
					fmt.Sprintf("rolling summary checkpoint %q carries an empty summary", checkpoint.ID))
			}
			if checkpoint.SummaryFormat == "" || checkpoint.PromptVersion == "" {
				return failed(VerifierContextManualSummary, fmt.Sprintf(
					"rolling summary checkpoint %q names summaryFormat=%q promptVersion=%q",
					checkpoint.ID, checkpoint.SummaryFormat, checkpoint.PromptVersion))
			}
			return passed(VerifierContextManualSummary, fmt.Sprintf(
				"manual summary completed checkpoint %q (%s/%s) covering %d events",
				checkpoint.ID, checkpoint.SummaryFormat, checkpoint.PromptVersion, checkpoint.CoveredEventCount))
		}
		return failed(VerifierContextManualSummary,
			fmt.Sprintf("%d manual/summary brackets completed, none with a %s checkpoint",
				len(matched), domain.ContextCheckpointKindRollingSummary))
	})
}

// verifyContextPreTurnSummary requires an automatic pre-turn compaction that
// completed before a later preparation, and that preparation to actually
// name the new checkpoint. Completing a compaction that no subsequent
// request then used would prove admission ran, not that it took effect.
func verifyContextPreTurnSummary(reader *ArtifactReader, _ Scenario) CriterionResult {
	return contextCriterion(VerifierContextPreTurnSummary, reader, func(trace *contextTrace) CriterionResult {
		matched := completedCompactions(trace, domain.ContextTriggerPreTurn, domain.ContextStrategySummary)
		if len(matched) == 0 {
			return failed(VerifierContextPreTurnSummary,
				fmt.Sprintf("no completed pre_turn/summary compaction in %d brackets", len(trace.CompactionsInOrder())))
		}
		for _, compaction := range matched {
			checkpointID := compaction.Completed.Checkpoint.ID
			for _, decision := range trace.DecisionsInOrder() {
				if decision.Prepared.CheckpointID != checkpointID {
					continue
				}
				if decision.Request == nil {
					return failed(VerifierContextPreTurnSummary, fmt.Sprintf(
						"decision %q names checkpoint %q but no request was dispatched from it",
						decision.Prepared.ContextDecisionID, checkpointID))
				}
				return passed(VerifierContextPreTurnSummary, fmt.Sprintf(
					"pre-turn checkpoint %q was used by decision %q on turn %s",
					checkpointID, decision.Prepared.ContextDecisionID, decision.Prepared.TurnID))
			}
		}
		return failed(VerifierContextPreTurnSummary, fmt.Sprintf(
			"%d pre-turn checkpoints completed, none named by any of %d later preparations",
			len(matched), len(trace.DecisionsInOrder())))
	})
}

// verifyContextCheckpointReuse requires a preparation that names a
// checkpoint this Attempt's own evidence established, proving the post-
// restart request continued from the chain rather than replaying raw
// history. It deliberately accepts a valid successor: a restart followed by
// further compaction still continues the same chain.
func verifyContextCheckpointReuse(reader *ArtifactReader, _ Scenario) CriterionResult {
	return contextCriterion(VerifierContextCheckpointReuse, reader, func(trace *contextTrace) CriterionResult {
		checkpoints := trace.CheckpointsInOrder()
		if len(checkpoints) == 0 {
			return failed(VerifierContextCheckpointReuse, "no checkpoint was ever established")
		}
		for _, decision := range trace.DecisionsInOrder() {
			checkpointID := decision.Prepared.CheckpointID
			if checkpointID == "" {
				continue
			}
			if _, known := trace.Checkpoint(checkpointID); !known {
				return failed(VerifierContextCheckpointReuse, fmt.Sprintf(
					"decision %q names checkpoint %q that this Attempt's evidence never established",
					decision.Prepared.ContextDecisionID, checkpointID))
			}
			if decision.Request == nil {
				continue
			}
			return passed(VerifierContextCheckpointReuse, fmt.Sprintf(
				"decision %q dispatched a request from checkpoint %q (%s)",
				decision.Prepared.ContextDecisionID, checkpointID, decision.Prepared.CheckpointKind))
		}
		return failed(VerifierContextCheckpointReuse, fmt.Sprintf(
			"%d checkpoints exist but no dispatched request was prepared from any of them", len(checkpoints)))
	})
}

// verifyContextBudgetBounds holds every dispatched conversation request to
// the Context budget invariants. The trace already refuses unordered or zero
// budgets, so reaching a fail here means a request was admitted whose own
// estimate exceeded the hard input bound it recorded.
func verifyContextBudgetBounds(reader *ArtifactReader, _ Scenario) CriterionResult {
	return contextCriterion(VerifierContextBudgetBounds, reader, func(trace *contextTrace) CriterionResult {
		dispatched := 0
		for _, decision := range trace.DecisionsInOrder() {
			if decision.Request == nil {
				continue
			}
			dispatched++
			prepared := decision.Prepared
			if prepared.EstimatedTotalTokens > prepared.BudgetHardInput {
				return failed(VerifierContextBudgetBounds, fmt.Sprintf(
					"decision %q estimated %d tokens against hard input %d",
					prepared.ContextDecisionID, prepared.EstimatedTotalTokens, prepared.BudgetHardInput))
			}
			if prepared.SerializedEnvelopeBytes == 0 {
				return failed(VerifierContextBudgetBounds, fmt.Sprintf(
					"decision %q recorded a zero serialized envelope size", prepared.ContextDecisionID))
			}
		}
		if dispatched == 0 {
			return failed(VerifierContextBudgetBounds, "no conversation request was dispatched through the Context Engine")
		}
		return passed(VerifierContextBudgetBounds,
			fmt.Sprintf("%d dispatched requests stayed within their recorded hard-input budgets", dispatched))
	})
}

// verifyContextProjection requires both public evidence surfaces to carry
// the Context lifecycle. The transcript intentionally omits
// model.request.recorded, so it is checked for lifecycle facts only.
func verifyContextProjection(reader *ArtifactReader, _ Scenario) CriterionResult {
	return contextCriterion(VerifierContextProjection, reader, func(trace *contextTrace) CriterionResult {
		if len(trace.DecisionsInOrder()) == 0 {
			return failed(VerifierContextProjection, "audit evidence carries no context.prepared record")
		}
		transcript, ok := readTranscriptEventTypes(reader)
		if !ok {
			return indeterminate(VerifierContextProjection, "transcript evidence is missing or unreadable")
		}
		if !transcript[domain.EventContextPreparedRecorded] {
			return failed(VerifierContextProjection, "transcript carries no context.prepared projection")
		}
		if transcript[domain.EventModelRequestRecorded] {
			return failed(VerifierContextProjection,
				"transcript exposed model.request.recorded, which it must never project")
		}
		compactions := trace.CompactionsInOrder()
		if len(compactions) > 0 && !transcript[domain.EventContextCompactionStarted] {
			return failed(VerifierContextProjection, fmt.Sprintf(
				"audit carries %d compaction brackets but the transcript projects none", len(compactions)))
		}
		return passed(VerifierContextProjection, fmt.Sprintf(
			"transcript and audit both carry the Context lifecycle (%d decisions, %d compactions)",
			len(trace.DecisionsInOrder()), len(compactions)))
	})
}

// readTranscriptEventTypes returns the set of event types the collected
// transcript projects. Unreadable transcript evidence is reported as
// unavailable, never as an absent event type.
func readTranscriptEventTypes(reader *ArtifactReader) (map[string]bool, bool) {
	entries := reader.Entries("transcript")
	if !hasCollectedEntry(entries) {
		return nil, false
	}
	types := map[string]bool{}
	for _, entry := range entries {
		if entry.State != EntryCollected {
			continue
		}
		data, err := reader.ReadEntry(entry.Path)
		if err != nil {
			return nil, false
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var envelope struct {
				Type string `json:"type"`
			}
			if err := decodeAuditPayload([]byte(line), &envelope); err != nil {
				return nil, false
			}
			if envelope.Type != "" {
				types[envelope.Type] = true
			}
		}
	}
	return types, true
}
