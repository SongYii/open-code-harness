package eval

import (
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// contextVerifierEvents builds a complete, healthy Context Attempt: a manual
// reset, a manual summary, an automatic pre-turn summary whose checkpoint a
// later request actually uses, and the dispatched requests for each.
func contextVerifierEvents() []traceEvent {
	return []traceEvent{
		{domain.EventContextCompactionStarted, domain.ContextCompactionStarted{
			ID: "compaction-reset", Trigger: domain.ContextTriggerManual, Strategy: domain.ContextStrategyReset,
			BaseSourceHead: 10, SourceSchema: "och_context_source_v1", MeterID: "och_wire_estimate_v1",
		}},
		{domain.EventContextCompactionCompleted, domain.ContextCompactionCompleted{
			ID: "compaction-reset", Checkpoint: traceCheckpoint("checkpoint-reset", "", domain.ContextCheckpointKindSourceTailReset),
		}},
		{domain.EventContextCompactionStarted, domain.ContextCompactionStarted{
			ID: "compaction-manual", Trigger: domain.ContextTriggerManual, Strategy: domain.ContextStrategySummary,
			BaseSourceHead: 14, SourceSchema: "och_context_source_v1", MeterID: "och_wire_estimate_v1",
		}},
		{domain.EventContextCompactionCompleted, domain.ContextCompactionCompleted{
			ID: "compaction-manual", Checkpoint: traceCheckpoint("checkpoint-manual", "checkpoint-reset", domain.ContextCheckpointKindRollingSummary),
		}},
		{domain.EventContextCompactionStarted, domain.ContextCompactionStarted{
			ID: "compaction-preturn", Trigger: domain.ContextTriggerPreTurn, Strategy: domain.ContextStrategySummary,
			BaseSourceHead: 20, SourceSchema: "och_context_source_v1", MeterID: "och_wire_estimate_v1",
		}},
		{domain.EventContextCompactionCompleted, domain.ContextCompactionCompleted{
			ID: "compaction-preturn", Checkpoint: traceCheckpoint("checkpoint-preturn", "checkpoint-manual", domain.ContextCheckpointKindRollingSummary),
		}},
		{domain.EventContextPreparedRecorded, func() domain.ContextPreparedRecorded {
			prepared := tracePrepared("decision-1", "turn-1", "item-1", 1, domain.ContextTriggerPreTurn)
			prepared.CheckpointID = "checkpoint-preturn"
			prepared.CheckpointKind = domain.ContextCheckpointKindRollingSummary
			return prepared
		}()},
		{domain.EventModelRequestRecorded, traceRequest("decision-1", "turn-1", "item-1", 1)},
		{domain.EventModelUsageRecorded, domain.ModelUsageRecorded{
			TurnID: "turn-1", ItemID: "item-1", AttemptIndex: 1, InputTokens: 4000, OutputTokens: 30,
		}},
	}
}

// contextVerifierReader pairs healthy audit with a transcript projecting the
// Context lifecycle, which is what verifyContextProjection reads.
func contextVerifierReader(t *testing.T, events []traceEvent, transcriptTypes ...string) *ArtifactReader {
	t.Helper()
	reader := traceReaderFor(t, events...)
	if len(transcriptTypes) == 0 {
		transcriptTypes = []string{
			domain.EventContextCompactionStarted, domain.EventContextCompactionCompleted,
			domain.EventContextPreparedRecorded, domain.EventTurnCompleted,
		}
	}
	addTranscriptEntry(t, reader, transcriptTypes)
	return reader
}

func addTranscriptEntry(t *testing.T, reader *ArtifactReader, types []string) {
	t.Helper()
	lines := make([]string, 0, len(types))
	for _, eventType := range types {
		lines = append(lines, `{"formatVersion":1,"schema":"och.session.transcript","type":"`+eventType+`","payload":{}}`)
	}
	writeExtraEvidenceEntry(t, reader, "transcript.jsonl", "transcript", strings.Join(lines, "\n")+"\n")
}

func runContextVerifier(t *testing.T, id string, reader *ArtifactReader) CriterionResult {
	t.Helper()
	verifier, ok := LookupVerifier(id)
	if !ok {
		t.Fatalf("verifier %q is not registered", id)
	}
	return verifier(reader, validScenario())
}

func requireStatus(t *testing.T, got CriterionResult, want ScoreVerdict) {
	t.Helper()
	if got.Status != want {
		t.Fatalf("%s status = %q (detail: %s), want %q", got.ID, got.Status, got.Detail, want)
	}
	if got.Detail == "" {
		t.Fatalf("%s produced no detail text", got.ID)
	}
	if len(got.Detail) > maxCriterionDetailBytes {
		t.Fatalf("%s detail is %d bytes, over the cap", got.ID, len(got.Detail))
	}
}

func TestContextVerifiersPassOnHealthyEvidence(t *testing.T) {
	for _, id := range []string{
		VerifierContextManualReset, VerifierContextManualSummary, VerifierContextPreTurnSummary,
		VerifierContextCheckpointReuse, VerifierContextBudgetBounds, VerifierContextProjection,
	} {
		t.Run(id, func(t *testing.T) {
			requireStatus(t, runContextVerifier(t, id, contextVerifierReader(t, contextVerifierEvents())), ScorePass)
		})
	}
}

// TestContextVerifiersFailOnIntactEvidenceShowingTheBehaviourDidNotHappen is
// the other half of the verdict rule: evidence that is complete and readable
// but shows the mechanism did not run is a fail, never an indeterminate.
func TestContextVerifiersFailOnIntactEvidenceShowingTheBehaviourDidNotHappen(t *testing.T) {
	withoutReset := func() []traceEvent {
		var kept []traceEvent
		for _, event := range contextVerifierEvents() {
			if started, ok := event.Data.(domain.ContextCompactionStarted); ok && started.Strategy == domain.ContextStrategyReset {
				continue
			}
			if completed, ok := event.Data.(domain.ContextCompactionCompleted); ok && completed.ID == "compaction-reset" {
				continue
			}
			kept = append(kept, event)
		}
		return kept
	}

	cases := []struct {
		name   string
		id     string
		events []traceEvent
	}{
		{"no manual reset ever ran", VerifierContextManualReset, withoutReset()},
		{"no manual summary ever ran", VerifierContextManualSummary, []traceEvent{
			{domain.EventContextCompactionStarted, domain.ContextCompactionStarted{
				ID: "compaction-reset", Trigger: domain.ContextTriggerManual, Strategy: domain.ContextStrategyReset,
				BaseSourceHead: 10, SourceSchema: "och_context_source_v1", MeterID: "och_wire_estimate_v1",
			}},
			{domain.EventContextCompactionCompleted, domain.ContextCompactionCompleted{
				ID: "compaction-reset", Checkpoint: traceCheckpoint("checkpoint-reset", "", domain.ContextCheckpointKindSourceTailReset),
			}},
		}},
		{"a pre-turn checkpoint nothing then used", VerifierContextPreTurnSummary, []traceEvent{
			{domain.EventContextCompactionStarted, domain.ContextCompactionStarted{
				ID: "compaction-preturn", Trigger: domain.ContextTriggerPreTurn, Strategy: domain.ContextStrategySummary,
				BaseSourceHead: 20, SourceSchema: "och_context_source_v1", MeterID: "och_wire_estimate_v1",
			}},
			{domain.EventContextCompactionCompleted, domain.ContextCompactionCompleted{
				ID: "compaction-preturn", Checkpoint: traceCheckpoint("checkpoint-preturn", "", domain.ContextCheckpointKindRollingSummary),
			}},
			{domain.EventContextPreparedRecorded, tracePrepared("decision-1", "turn-1", "item-1", 1, domain.ContextTriggerPreTurn)},
			{domain.EventModelRequestRecorded, traceRequest("decision-1", "turn-1", "item-1", 1)},
		}},
		{"no request was ever prepared from a checkpoint", VerifierContextCheckpointReuse, []traceEvent{
			{domain.EventContextCompactionStarted, domain.ContextCompactionStarted{
				ID: "compaction-manual", Trigger: domain.ContextTriggerManual, Strategy: domain.ContextStrategySummary,
				BaseSourceHead: 14, SourceSchema: "och_context_source_v1", MeterID: "och_wire_estimate_v1",
			}},
			{domain.EventContextCompactionCompleted, domain.ContextCompactionCompleted{
				ID: "compaction-manual", Checkpoint: traceCheckpoint("checkpoint-manual", "", domain.ContextCheckpointKindRollingSummary),
			}},
			{domain.EventContextPreparedRecorded, tracePrepared("decision-1", "turn-1", "item-1", 1, domain.ContextTriggerPreTurn)},
			{domain.EventModelRequestRecorded, traceRequest("decision-1", "turn-1", "item-1", 1)},
		}},
		{"a request exceeded its own recorded hard input", VerifierContextBudgetBounds, []traceEvent{
			{domain.EventContextPreparedRecorded, func() domain.ContextPreparedRecorded {
				prepared := tracePrepared("decision-1", "turn-1", "item-1", 1, domain.ContextTriggerPreTurn)
				prepared.EstimatedTotalTokens = prepared.BudgetHardInput + 1
				return prepared
			}()},
			{domain.EventModelRequestRecorded, traceRequest("decision-1", "turn-1", "item-1", 1)},
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			requireStatus(t, runContextVerifier(t, testCase.id, contextVerifierReader(t, testCase.events)), ScoreFail)
		})
	}
}

// TestContextVerifiersAreIndeterminateOnBrokenEvidence pins the rule that
// matters most: unavailable, malformed, or self-contradictory evidence must
// never resolve to pass or to a behavioural fail.
func TestContextVerifiersAreIndeterminateOnBrokenEvidence(t *testing.T) {
	ids := []string{
		VerifierContextManualReset, VerifierContextManualSummary, VerifierContextPreTurnSummary,
		VerifierContextCheckpointReuse, VerifierContextBudgetBounds, VerifierContextProjection,
	}

	t.Run("audit evidence missing", func(t *testing.T) {
		reader := &ArtifactReader{evidenceRoot: t.TempDir(), manifest: EvidenceManifest{}}
		for _, id := range ids {
			requireStatus(t, runContextVerifier(t, id, reader), ScoreIndeterminate)
		}
	})

	t.Run("audit evidence malformed", func(t *testing.T) {
		lines := append(traceLines(t, contextVerifierEvents()...), `{"events":[{"type":"context.prepared","data":{ broken`)
		reader := traceReaderFromLines(t, lines)
		addTranscriptEntry(t, reader, []string{domain.EventContextPreparedRecorded})
		for _, id := range ids {
			requireStatus(t, runContextVerifier(t, id, reader), ScoreIndeterminate)
		}
	})

	t.Run("evidence contradicts itself", func(t *testing.T) {
		events := append(contextVerifierEvents(), traceEvent{
			domain.EventContextCompactionFailed,
			domain.ContextCompactionFailed{ID: "compaction-manual", Code: "context_summary_failed", Message: "boom"},
		})
		reader := contextVerifierReader(t, events)
		for _, id := range ids {
			requireStatus(t, runContextVerifier(t, id, reader), ScoreIndeterminate)
		}
	})

	t.Run("transcript unavailable is indeterminate, not a projection failure", func(t *testing.T) {
		reader := traceReaderFor(t, contextVerifierEvents()...)
		requireStatus(t, runContextVerifier(t, VerifierContextProjection, reader), ScoreIndeterminate)
	})
}

// TestContextProjectionRefusesALeakedModelRequest pins the transcript's own
// boundary: it must never project model.request.recorded.
func TestContextProjectionRefusesALeakedModelRequest(t *testing.T) {
	reader := contextVerifierReader(t, contextVerifierEvents(),
		domain.EventContextPreparedRecorded, domain.EventContextCompactionStarted, domain.EventModelRequestRecorded)
	requireStatus(t, runContextVerifier(t, VerifierContextProjection, reader), ScoreFail)
}

// TestContextCheckpointReuseRefusesAnUnknownCheckpoint proves a preparation
// cannot claim a checkpoint this Attempt's own evidence never established.
func TestContextCheckpointReuseRefusesAnUnknownCheckpoint(t *testing.T) {
	events := []traceEvent{
		{domain.EventContextCompactionStarted, domain.ContextCompactionStarted{
			ID: "compaction-manual", Trigger: domain.ContextTriggerManual, Strategy: domain.ContextStrategySummary,
			BaseSourceHead: 14, SourceSchema: "och_context_source_v1", MeterID: "och_wire_estimate_v1",
		}},
		{domain.EventContextCompactionCompleted, domain.ContextCompactionCompleted{
			ID: "compaction-manual", Checkpoint: traceCheckpoint("checkpoint-manual", "", domain.ContextCheckpointKindRollingSummary),
		}},
		{domain.EventContextPreparedRecorded, func() domain.ContextPreparedRecorded {
			prepared := tracePrepared("decision-1", "turn-1", "item-1", 1, domain.ContextTriggerPreTurn)
			prepared.CheckpointID = "checkpoint-that-never-existed"
			prepared.CheckpointKind = domain.ContextCheckpointKindRollingSummary
			return prepared
		}()},
		{domain.EventModelRequestRecorded, traceRequest("decision-1", "turn-1", "item-1", 1)},
	}
	requireStatus(t, runContextVerifier(t, VerifierContextCheckpointReuse, contextVerifierReader(t, events)), ScoreFail)
}
