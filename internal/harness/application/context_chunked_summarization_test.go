package application_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

// numberedSummaryText renders a minimal but structurally valid
// och_context_summary_v1 document (matching validSummaryText's own exact
// 8 required headings) with a per-call marker woven into its own
// "Continuation" section, so a test can prove which chunk's own output
// ended up where.
func numberedSummaryText(n int) string {
	return strings.Join([]string{
		"## Objective", fmt.Sprintf("Ship chunk %d of the context engine.", n),
		"## User Constraints", "None.",
		"## Established Facts", "The session has several prior turns.",
		"## Work Completed", "Prior turns ran successfully.",
		"## Files and Commands", "None.",
		"## Open Work", "None.",
		"## Risks and Unknowns", "None.",
		"## Continuation", fmt.Sprintf("Proceed with the next turn after chunk %d.", n),
	}, "\n")
}

// sequencedSummarizer returns texts[i] for its i-th call (repeating the
// last entry if called more times than texts provided), recording every
// request it received -- used to prove design §11.2's rolling-chunk shape
// directly: each call's own Content must contain the PREVIOUS call's own
// returned text as its "PREVIOUS CHECKPOINT" section, not just a
// disconnected fixed string.
type sequencedSummarizer struct {
	mu    sync.Mutex
	calls []application.ContextSummarizeRequest
	texts []string
}

func (summarizer *sequencedSummarizer) Summarize(ctx context.Context, request application.ContextSummarizeRequest) (application.ContextSummarizeResult, error) {
	summarizer.mu.Lock()
	defer summarizer.mu.Unlock()
	index := len(summarizer.calls)
	summarizer.calls = append(summarizer.calls, request)
	text := summarizer.texts[len(summarizer.texts)-1]
	if index < len(summarizer.texts) {
		text = summarizer.texts[index]
	}
	return application.ContextSummarizeResult{Text: text, Usage: engine.TokenUsage{OutputTokens: 20}}, nil
}

func (summarizer *sequencedSummarizer) callCount() int {
	summarizer.mu.Lock()
	defer summarizer.mu.Unlock()
	return len(summarizer.calls)
}

func (summarizer *sequencedSummarizer) callContents() []string {
	summarizer.mu.Lock()
	defer summarizer.mu.Unlock()
	var contents []string
	for _, call := range summarizer.calls {
		contents = append(contents, call.Content)
	}
	return contents
}

// TestPrepareContextChunkedSummarizationRollsMultipleCallsIntoOneCheckpoint
// is the regression test for closing MaxSummaryChunks' own disclosed
// "accepted but inert" gap: source material too large for one summarizer
// call must now be split into design §11.2's own rolling chunks rather
// than immediately failing, and the checkpoint's own evidence
// (SummaryChunks, Summary, coverage, digest) must reflect the complete,
// multi-call result correctly.
func TestPrepareContextChunkedSummarizationRollsMultipleCallsIntoOneCheckpoint(t *testing.T) {
	store, state, scan, historyIDs := buildHistorySession(t, 30)
	fullEstimate := contextengine.WireEstimateMeter{}.EstimateMessages(flattenScanMessages(scan))
	summarizer := &sequencedSummarizer{texts: []string{numberedSummaryText(1), numberedSummaryText(2), numberedSummaryText(3)}}
	deps := application.ContextOrchestratorDeps{
		Store: store, IDs: historyIDs, Clock: testkit.FixedClock{Time: acceptanceTime},
		Authority:       application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1},
		CheckpointStore: &fakeCheckpointStore{}, Summarizer: summarizer, Meter: contextengine.WireEstimateMeter{},
		Budget: contextengine.Budget{
			// HardInput deliberately smaller than what rendering all
			// covered Turns in one summarizer call would need (forcing at
			// least two chunks), but generous enough that the actually
			// dispatched request (a short checkpoint + a small retained
			// tail + current input) and each chunk's own real content
			// still fit comfortably.
			HardInput: fullEstimate * 6 / 10, Trigger: fullEstimate / 4, Target: fullEstimate / 8,
			ProtectedTail: fullEstimate / 50, SummaryOutputCap: 4000,
		},
		MaxSummaryChunks: 5,
	}
	result, err := application.PrepareContext(context.Background(), deps, state, application.PrepareContextInput{
		SessionID: state.ID, TurnID: "turn-pending", ItemID: "item-pending", Trigger: domain.ContextTriggerPreTurn,
		CurrentInput: domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: "next"},
	})
	if err != nil {
		t.Fatalf("PrepareContext() error = %v", err)
	}
	if !result.CompactionRan {
		t.Fatal("CompactionRan = false, want true")
	}
	if result.Prepared.CheckpointKind != contextengine.CheckpointKindRollingSummary {
		t.Fatalf("CheckpointKind = %q, want rolling_summary_v1", result.Prepared.CheckpointKind)
	}

	callCount := summarizer.callCount()
	if callCount < 2 {
		t.Fatalf("summarizer.callCount() = %d, want at least 2 -- this fixture's own HardInput was sized specifically to require multiple chunks", callCount)
	}

	contents := summarizer.callContents()
	for i := 1; i < len(contents); i++ {
		previousMarker := fmt.Sprintf("chunk %d", i) // texts[i-1] is numberedSummaryText(i)
		if !strings.Contains(contents[i], previousMarker) {
			t.Fatalf("chunk %d's own request Content does not contain chunk %d's own returned text (marker %q); rolling chunks are not actually chained: %q", i+1, i, previousMarker, contents[i])
		}
	}
	// The fixed prompt template itself mentions "PREVIOUS CHECKPOINT" as
	// instructional text regardless of whether that section is actually
	// rendered (contextengine/prompt.md), so this checks for the rendered
	// heading followed by real content, not the bare substring.
	if strings.Contains(contents[0], "\n\n## PREVIOUS CHECKPOINT\n\n") {
		t.Fatal("the very first chunk's own request Content renders a PREVIOUS CHECKPOINT section, want none (this Session had no prior checkpoint)")
	}

	records, err := application.ReadWholeStreamPinned(context.Background(), store, state.ID, 256)
	if err != nil {
		t.Fatal(err)
	}
	var completed domain.ContextCompactionCompleted
	var sawFailed bool
	for _, record := range records {
		switch event := record.Event.(type) {
		case domain.ContextCompactionCompleted:
			completed = event
		case domain.ContextCompactionFailed:
			sawFailed = true
		}
	}
	if sawFailed {
		t.Fatal("a context.compaction.failed event was committed; want the chunked summary to succeed outright")
	}
	if int(completed.Checkpoint.SummaryChunks) != callCount {
		t.Fatalf("Checkpoint.SummaryChunks = %d, want %d (exactly one per summarizer call)", completed.Checkpoint.SummaryChunks, callCount)
	}
	wantFinalText := numberedSummaryText(callCount)
	if completed.Checkpoint.Summary != wantFinalText {
		t.Fatalf("Checkpoint.Summary = %q, want the LAST chunk's own validated output %q", completed.Checkpoint.Summary, wantFinalText)
	}
}

// TestPrepareContextChunkCapExhaustedFallsBackToDeterministicReset proves
// design's own context_compaction_limit failure code: when the covered
// source material needs more chunks than MaxSummaryChunks allows, the
// compaction bracket fails closed with that code (never silently
// truncating source material or exceeding the configured cap) and falls
// through to the same deterministic-reset ladder any other summary
// failure above hardInput already uses.
func TestPrepareContextChunkCapExhaustedFallsBackToDeterministicReset(t *testing.T) {
	store, state, scan, historyIDs := buildHistorySession(t, 30)
	fullEstimate := contextengine.WireEstimateMeter{}.EstimateMessages(flattenScanMessages(scan))
	summarizer := &sequencedSummarizer{texts: []string{numberedSummaryText(1), numberedSummaryText(2), numberedSummaryText(3)}}
	deps := application.ContextOrchestratorDeps{
		Store: store, IDs: historyIDs, Clock: testkit.FixedClock{Time: acceptanceTime},
		Authority:       application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1},
		CheckpointStore: &fakeCheckpointStore{}, Summarizer: summarizer, Meter: contextengine.WireEstimateMeter{},
		Budget: contextengine.Budget{
			HardInput: fullEstimate * 6 / 10, Trigger: fullEstimate / 4, Target: fullEstimate / 8,
			ProtectedTail: fullEstimate / 50, SummaryOutputCap: 4000,
		},
		// The same fixture as the success test above needs at least 2
		// chunks; capping at 1 here must fail the summary attempt rather
		// than silently accepting a partial, single-chunk covered range.
		MaxSummaryChunks: 1,
	}
	result, err := application.PrepareContext(context.Background(), deps, state, application.PrepareContextInput{
		SessionID: state.ID, TurnID: "turn-pending", ItemID: "item-pending", Trigger: domain.ContextTriggerPreTurn,
		CurrentInput: domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: "next"},
	})
	if err != nil {
		t.Fatalf("PrepareContext() error = %v", err)
	}
	if !result.CompactionRan {
		t.Fatal("CompactionRan = false, want true (a deterministic reset should still run)")
	}
	if result.Prepared.CheckpointKind != contextengine.CheckpointKindSourceTailReset {
		t.Fatalf("CheckpointKind = %q, want source_tail_reset_v1", result.Prepared.CheckpointKind)
	}
	if summarizer.callCount() != 1 {
		t.Fatalf("summarizer.callCount() = %d, want exactly 1 (only the first chunk's own call before the cap was found exhausted)", summarizer.callCount())
	}

	records, err := application.ReadWholeStreamPinned(context.Background(), store, state.ID, 256)
	if err != nil {
		t.Fatal(err)
	}
	var failedEvent domain.ContextCompactionFailed
	var sawFailed, sawCompleted bool
	for _, record := range records {
		switch event := record.Event.(type) {
		case domain.ContextCompactionFailed:
			sawFailed = true
			failedEvent = event
		case domain.ContextCompactionCompleted:
			sawCompleted = true
		}
	}
	if !sawFailed || !sawCompleted {
		t.Fatalf("sawFailed=%t sawCompleted=%t, want both (a failed summary attempt, then a completed reset)", sawFailed, sawCompleted)
	}
	if failedEvent.Code != application.CodeContextCompactionLimit {
		t.Fatalf("ContextCompactionFailed.Code = %q, want %q", failedEvent.Code, application.CodeContextCompactionLimit)
	}
}

// TestPrepareContextMaxSummaryChunksDefaultsToSingleShot proves the
// backward-compatibility default this task's own design depends on: a
// ContextOrchestratorDeps that never sets MaxSummaryChunks at all (every
// caller written before this field existed) must keep failing exactly as
// it always did on source material too large for one call -- never
// silently start chunking just because the underlying mechanism now
// exists.
func TestPrepareContextMaxSummaryChunksDefaultsToSingleShot(t *testing.T) {
	store, state, scan, historyIDs := buildHistorySession(t, 30)
	fullEstimate := contextengine.WireEstimateMeter{}.EstimateMessages(flattenScanMessages(scan))
	summarizer := &sequencedSummarizer{texts: []string{numberedSummaryText(1), numberedSummaryText(2), numberedSummaryText(3)}}
	deps := application.ContextOrchestratorDeps{
		Store: store, IDs: historyIDs, Clock: testkit.FixedClock{Time: acceptanceTime},
		Authority:       application.WriterAuthority{RuntimeID: "concurrency-runtime", FencingToken: 1},
		CheckpointStore: &fakeCheckpointStore{}, Summarizer: summarizer, Meter: contextengine.WireEstimateMeter{},
		Budget: contextengine.Budget{
			HardInput: fullEstimate * 6 / 10, Trigger: fullEstimate / 4, Target: fullEstimate / 8,
			ProtectedTail: fullEstimate / 50, SummaryOutputCap: 4000,
		},
		// MaxSummaryChunks intentionally left unset (zero value).
	}
	result, err := application.PrepareContext(context.Background(), deps, state, application.PrepareContextInput{
		SessionID: state.ID, TurnID: "turn-pending", ItemID: "item-pending", Trigger: domain.ContextTriggerPreTurn,
		CurrentInput: domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: "next"},
	})
	if err != nil {
		t.Fatalf("PrepareContext() error = %v", err)
	}
	if result.Prepared.CheckpointKind != contextengine.CheckpointKindSourceTailReset {
		t.Fatalf("CheckpointKind = %q, want source_tail_reset_v1 (single-shot summary should have failed and fallen back to reset)", result.Prepared.CheckpointKind)
	}
	if summarizer.callCount() != 1 {
		t.Fatalf("summarizer.callCount() = %d, want exactly 1 (single-shot only, matching every caller before MaxSummaryChunks existed)", summarizer.callCount())
	}
}
