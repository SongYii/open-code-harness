package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// pruneFixtureContent is the large Tool Result the pruning Scenario reads.
// It has to be genuinely large: projection only happens when the content
// costs more than the marker frame that replaces it, and the verifier
// correctly refuses a "projection" that grew. The projected frame's declared
// original_bytes and sha256 must resolve back to exactly these bytes in
// collected workspace evidence.
var pruneFixtureContent = strings.Repeat("large tool result line that must be projected rather than sent whole\n", 600)

func projectedFrame(callID string) string {
	digest := sha256.Sum256([]byte(pruneFixtureContent))
	return fmt.Sprintf("%s\nevent_id: event-1\noriginal_bytes: %d\nsha256: %s\ncontent_head:\nlarge tool\ncontent_tail:\nwhole\n%s",
		projectedToolResultOpen, len(pruneFixtureContent), hex.EncodeToString(digest[:]), projectedToolResultClose)
}

// pruningEvents is a healthy mid-turn pruning Attempt: attempt 1 issues the
// tool call, attempt 2 is prepared mid-turn carrying the projected frame, and
// the fixture confirms it received the complete projection.
func pruningEvents() []traceEvent {
	second := tracePrepared("decision-2", "turn-1", "item-1", 2, domain.ContextTriggerMidTurn)
	second.PrunedToolResultCount = 1

	request := traceRequest("decision-2", "turn-1", "item-1", 2)
	request.Messages = []domain.ModelPromptMessage{
		{Role: "user", Text: "read it OCH_EVAL_CONTEXT_PRUNE"},
		{Role: "tool", ToolCallID: "call_context_prune", Text: projectedFrame("call_context_prune")},
	}

	return []traceEvent{
		{domain.EventContextPreparedRecorded, tracePrepared("decision-1", "turn-1", "item-1", 1, domain.ContextTriggerPreTurn)},
		{domain.EventModelRequestRecorded, traceRequest("decision-1", "turn-1", "item-1", 1)},
		{domain.EventContextPreparedRecorded, second},
		{domain.EventModelRequestRecorded, request},
		{domain.EventAssistantMessageCompleted, domain.AssistantMessageCompleted{
			TurnID: "turn-1", ItemID: "item-1", Text: contextPruneSuccessSentinel,
		}},
		{domain.EventTurnCompleted, domain.TurnCompleted{TurnID: "turn-1"}},
	}
}

func pruningReader(t *testing.T, events []traceEvent) *ArtifactReader {
	t.Helper()
	reader := traceReaderFor(t, events...)
	writeExtraEvidenceEntry(t, reader, "workspace/large-tool-result.txt", "workspace", pruneFixtureContent)
	addTranscriptEntry(t, reader, []string{domain.EventContextPreparedRecorded})
	return reader
}

func overflowEvents() []traceEvent {
	first := tracePrepared("decision-1", "turn-1", "item-1", 1, domain.ContextTriggerPreTurn)
	first.EstimatedTotalTokens = 6000
	second := tracePrepared("decision-2", "turn-1", "item-1", 2, domain.ContextTriggerOverflowRetry)
	second.EstimatedTotalTokens = 2000
	second.CheckpointID = "checkpoint-overflow"
	second.CheckpointKind = domain.ContextCheckpointKindRollingSummary

	return []traceEvent{
		{domain.EventContextPreparedRecorded, first},
		{domain.EventModelRequestRecorded, traceRequest("decision-1", "turn-1", "item-1", 1)},
		{domain.EventContextCompactionStarted, domain.ContextCompactionStarted{
			ID: "compaction-overflow", Trigger: domain.ContextTriggerOverflowRetry, Strategy: domain.ContextStrategySummary,
			BaseSourceHead: 20, SourceSchema: "och_context_source_v1", MeterID: "och_wire_estimate_v1",
		}},
		{domain.EventContextCompactionCompleted, domain.ContextCompactionCompleted{
			ID: "compaction-overflow", Checkpoint: traceCheckpoint("checkpoint-overflow", "", domain.ContextCheckpointKindRollingSummary),
		}},
		{domain.EventContextPreparedRecorded, second},
		{domain.EventModelRequestRecorded, traceRequest("decision-2", "turn-1", "item-1", 2)},
		{domain.EventAssistantMessageCompleted, domain.AssistantMessageCompleted{
			TurnID: "turn-1", ItemID: "item-1", Text: contextPostCompactionResult,
		}},
		{domain.EventTurnCompleted, domain.TurnCompleted{TurnID: "turn-1"}},
	}
}

func multiChunkEvents(chunks uint32, depth int) []traceEvent {
	checkpoint := traceCheckpoint("checkpoint-chunked", "", domain.ContextCheckpointKindRollingSummary)
	checkpoint.SummaryChunks = chunks
	checkpoint.Summary = strings.Join([]string{
		"## Objective", "Exercise summarization.",
		"## Established Facts", fmt.Sprintf("fixture_chunk_depth: %d", depth),
		"## Continuation", "Proceed.",
	}, "\n")
	return []traceEvent{
		{domain.EventContextCompactionStarted, domain.ContextCompactionStarted{
			ID: "compaction-chunked", Trigger: domain.ContextTriggerPreTurn, Strategy: domain.ContextStrategySummary,
			BaseSourceHead: 30, SourceSchema: "och_context_source_v1", MeterID: "och_wire_estimate_v1",
		}},
		{domain.EventContextCompactionCompleted, domain.ContextCompactionCompleted{
			ID: "compaction-chunked", Checkpoint: checkpoint,
		}},
	}
}

func usageAnchorEvents() []traceEvent {
	anchored := tracePrepared("decision-2", "turn-2", "item-2", 1, domain.ContextTriggerPreTurn)
	anchored.UsageAnchorApplied = true
	anchored.UsageAnchorTokens = 60000
	anchored.CheckpointID = "checkpoint-anchor"
	anchored.CheckpointKind = domain.ContextCheckpointKindRollingSummary

	return []traceEvent{
		{domain.EventContextPreparedRecorded, tracePrepared("decision-1", "turn-1", "item-1", 1, domain.ContextTriggerPreTurn)},
		{domain.EventModelRequestRecorded, traceRequest("decision-1", "turn-1", "item-1", 1)},
		{domain.EventModelUsageRecorded, domain.ModelUsageRecorded{
			TurnID: "turn-1", ItemID: "item-1", AttemptIndex: 1, InputTokens: 60000, OutputTokens: 2,
		}},
		{domain.EventContextCompactionStarted, domain.ContextCompactionStarted{
			ID: "compaction-anchor", Trigger: domain.ContextTriggerPreTurn, Strategy: domain.ContextStrategySummary,
			BaseSourceHead: 24, SourceSchema: "och_context_source_v1", MeterID: "och_wire_estimate_v1",
		}},
		{domain.EventContextCompactionCompleted, domain.ContextCompactionCompleted{
			ID: "compaction-anchor", Checkpoint: traceCheckpoint("checkpoint-anchor", "", domain.ContextCheckpointKindRollingSummary),
		}},
		{domain.EventContextPreparedRecorded, anchored},
		{domain.EventModelRequestRecorded, traceRequest("decision-2", "turn-2", "item-2", 1)},
		{domain.EventTurnCompleted, domain.TurnCompleted{TurnID: "turn-2"}},
	}
}

func TestContextMechanismVerifiersPassOnHealthyEvidence(t *testing.T) {
	t.Run(VerifierContextMidTurn, func(t *testing.T) {
		requireStatus(t, runContextVerifier(t, VerifierContextMidTurn, pruningReader(t, pruningEvents())), ScorePass)
	})
	t.Run(VerifierContextToolResultPruned, func(t *testing.T) {
		requireStatus(t, runContextVerifier(t, VerifierContextToolResultPruned, pruningReader(t, pruningEvents())), ScorePass)
	})
	t.Run(VerifierContextOverflowRecover, func(t *testing.T) {
		requireStatus(t, runContextVerifier(t, VerifierContextOverflowRecover, contextVerifierReader(t, overflowEvents())), ScorePass)
	})
	t.Run(VerifierContextMultiChunk, func(t *testing.T) {
		requireStatus(t, runContextVerifier(t, VerifierContextMultiChunk, contextVerifierReader(t, multiChunkEvents(3, 3))), ScorePass)
	})
	t.Run(VerifierContextUsageAnchor, func(t *testing.T) {
		requireStatus(t, runContextVerifier(t, VerifierContextUsageAnchor, contextVerifierReader(t, usageAnchorEvents())), ScorePass)
	})
}

// TestContextMechanismMutationsStopTheirCriterion is the design's own
// required mutation set. Each removes exactly one observed fact from
// otherwise healthy evidence, and each must stop its criterion from passing.
// Configuration is never proof: every mutation below leaves the Scenario and
// Subject untouched and changes only what was actually recorded.
func TestContextMechanismMutationsStopTheirCriterion(t *testing.T) {
	t.Run("removing the pruning count", func(t *testing.T) {
		events := pruningEvents()
		prepared := events[2].Data.(domain.ContextPreparedRecorded)
		prepared.PrunedToolResultCount = 0
		events[2].Data = prepared
		got := runContextVerifier(t, VerifierContextToolResultPruned, pruningReader(t, events))
		if got.Status == ScorePass {
			t.Fatalf("pruning criterion still passed with no recorded pruning count: %s", got.Detail)
		}
	})

	t.Run("losing the original tool call id", func(t *testing.T) {
		events := pruningEvents()
		request := events[3].Data.(domain.ModelRequestRecorded)
		request.Messages[1].ToolCallID = ""
		events[3].Data = request
		got := runContextVerifier(t, VerifierContextToolResultPruned, pruningReader(t, events))
		if got.Status == ScorePass {
			t.Fatalf("pruning criterion still passed after the call id was lost: %s", got.Detail)
		}
	})

	t.Run("a frame naming content nothing collected", func(t *testing.T) {
		events := pruningEvents()
		request := events[3].Data.(domain.ModelRequestRecorded)
		request.Messages[1].Text = strings.Replace(request.Messages[1].Text,
			fmt.Sprintf("original_bytes: %d", len(pruneFixtureContent)), "original_bytes: 999999", 1)
		events[3].Data = request
		got := runContextVerifier(t, VerifierContextToolResultPruned, pruningReader(t, events))
		if got.Status == ScorePass {
			t.Fatalf("pruning criterion still passed for content no workspace file matches: %s", got.Detail)
		}
	})

	t.Run("removing the overflow retry", func(t *testing.T) {
		var events []traceEvent
		for _, event := range overflowEvents() {
			if started, ok := event.Data.(domain.ContextCompactionStarted); ok && started.Trigger == domain.ContextTriggerOverflowRetry {
				continue
			}
			if completed, ok := event.Data.(domain.ContextCompactionCompleted); ok && completed.ID == "compaction-overflow" {
				continue
			}
			events = append(events, event)
		}
		got := runContextVerifier(t, VerifierContextOverflowRecover, contextVerifierReader(t, events))
		if got.Status == ScorePass {
			t.Fatalf("overflow criterion still passed with no overflow_retry compaction: %s", got.Detail)
		}
	})

	t.Run("a retry that did not shrink", func(t *testing.T) {
		events := overflowEvents()
		second := events[4].Data.(domain.ContextPreparedRecorded)
		second.EstimatedTotalTokens = 6000
		events[4].Data = second
		got := runContextVerifier(t, VerifierContextOverflowRecover, contextVerifierReader(t, events))
		if got.Status == ScorePass {
			t.Fatalf("overflow criterion still passed for a non-decreasing retry: %s", got.Detail)
		}
	})

	t.Run("reducing the chunk count to one", func(t *testing.T) {
		got := runContextVerifier(t, VerifierContextMultiChunk, contextVerifierReader(t, multiChunkEvents(1, 1)))
		if got.Status == ScorePass {
			t.Fatalf("multi-chunk criterion still passed for a single chunk: %s", got.Detail)
		}
	})

	t.Run("a chunk count the fixture never observed", func(t *testing.T) {
		got := runContextVerifier(t, VerifierContextMultiChunk, contextVerifierReader(t, multiChunkEvents(3, 1)))
		if got.Status == ScorePass {
			t.Fatalf("multi-chunk criterion still passed when depth disagreed with chunks: %s", got.Detail)
		}
	})

	t.Run("clearing the applied usage anchor", func(t *testing.T) {
		events := usageAnchorEvents()
		anchored := events[5].Data.(domain.ContextPreparedRecorded)
		anchored.UsageAnchorApplied = false
		anchored.UsageAnchorTokens = 0
		events[5].Data = anchored
		got := runContextVerifier(t, VerifierContextUsageAnchor, contextVerifierReader(t, events))
		if got.Status == ScorePass {
			t.Fatalf("usage-anchor criterion still passed with no applied anchor: %s", got.Detail)
		}
	})

	t.Run("an anchor no earlier provider usage supports", func(t *testing.T) {
		var events []traceEvent
		for _, event := range usageAnchorEvents() {
			if _, ok := event.Data.(domain.ModelUsageRecorded); ok {
				continue
			}
			events = append(events, event)
		}
		got := runContextVerifier(t, VerifierContextUsageAnchor, contextVerifierReader(t, events))
		if got.Status == ScorePass {
			t.Fatalf("usage-anchor criterion still passed with no provider usage to establish it: %s", got.Detail)
		}
	})
}

// TestContextMechanismVerifiersAreIndeterminateOnBrokenEvidence keeps the
// mechanism criteria to the same verdict rule as the core ones.
func TestContextMechanismVerifiersAreIndeterminateOnBrokenEvidence(t *testing.T) {
	ids := []string{
		VerifierContextMidTurn, VerifierContextToolResultPruned, VerifierContextOverflowRecover,
		VerifierContextMultiChunk, VerifierContextUsageAnchor,
	}
	reader := &ArtifactReader{evidenceRoot: t.TempDir(), manifest: EvidenceManifest{}}
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			requireStatus(t, runContextVerifier(t, id, reader), ScoreIndeterminate)
		})
	}
}

// TestContextPruningRefusesAFixtureContractFailure proves the fixture's own
// refusal is honoured: a run where the fixture reported it never received the
// complete projected frame must not pass.
func TestContextPruningRefusesAFixtureContractFailure(t *testing.T) {
	events := pruningEvents()
	events[4].Data = domain.AssistantMessageCompleted{
		TurnID: "turn-1", ItemID: "item-1",
		Text: contextFixtureFailureMarker + ": tool result was not the projected frame",
	}
	got := runContextVerifier(t, VerifierContextToolResultPruned, pruningReader(t, events))
	if got.Status == ScorePass {
		t.Fatalf("pruning criterion passed despite the fixture reporting a contract failure: %s", got.Detail)
	}
}
