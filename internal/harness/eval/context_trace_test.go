package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// traceEvent is one canonical audit event a fixture writes.
type traceEvent struct {
	Type string
	Data any
}

// traceReaderFor writes events as one canonical audit segment and returns a
// reader over it. The envelope shape matches what the SQLite audit replica
// actually produces, so these fixtures exercise the same parse path real
// evidence does.
func traceReaderFor(t *testing.T, events ...traceEvent) *ArtifactReader {
	t.Helper()
	return traceReaderFromLines(t, traceLines(t, events...))
}

func traceLines(t *testing.T, events ...traceEvent) []string {
	t.Helper()
	lines := make([]string, 0, len(events))
	for _, event := range events {
		payload, err := json.Marshal(event.Data)
		if err != nil {
			t.Fatalf("marshal %s: %v", event.Type, err)
		}
		envelope := map[string]any{"events": []map[string]any{{"type": event.Type, "data": json.RawMessage(payload)}}}
		line, err := json.Marshal(envelope)
		if err != nil {
			t.Fatalf("marshal envelope: %v", err)
		}
		lines = append(lines, string(line))
	}
	return lines
}

func traceReaderFromLines(t *testing.T, lines []string) *ArtifactReader {
	t.Helper()
	root := t.TempDir()
	data := []byte(strings.Join(lines, "\n") + "\n")
	path := "audit/segments/000000000001-000000000999-fixture.jsonl"
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(path)), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, path), data, 0o600); err != nil {
		t.Fatalf("write audit: %v", err)
	}
	sum := sha256.Sum256(data)
	return &ArtifactReader{evidenceRoot: root, manifest: EvidenceManifest{Entries: []ManifestEntry{{
		Path: path, Role: "audit", MediaType: "application/x-ndjson", Required: true,
		State: EntryCollected, SHA256: hex.EncodeToString(sum[:]), ByteLength: int64(len(data)),
		ProducedBy: "test",
	}}}}
}

func tracePrepared(decisionID, turnID, itemID string, attempt uint32, trigger string) domain.ContextPreparedRecorded {
	return domain.ContextPreparedRecorded{
		TurnID: domain.TurnID(turnID), ItemID: domain.ItemID(itemID), AttemptIndex: attempt,
		ContextDecisionID: domain.ContextDecisionID(decisionID), Trigger: trigger,
		SourceHeadVersion: 10,
		BudgetTarget:      3660, BudgetTrigger: 5324, BudgetHardInput: 6656,
		EstimatedMessageTokens: 100, EstimatedToolSchemaTokens: 20, EstimatedTotalTokens: 120,
		MeterID: "och_wire_estimate_v1", SerializedEnvelopeBytes: 500,
	}
}

func traceRequest(decisionID, turnID, itemID string, attempt uint32) domain.ModelRequestRecorded {
	return domain.ModelRequestRecorded{
		TurnID: domain.TurnID(turnID), ItemID: domain.ItemID(itemID), AttemptIndex: attempt,
		ContextDecisionID: domain.ContextDecisionID(decisionID), Purpose: "conversation",
		Messages: []domain.ModelPromptMessage{{Role: "user", Text: "hello"}},
	}
}

func traceCheckpoint(id, previous, kind string) domain.ContextCheckpointRecord {
	record := domain.ContextCheckpointRecord{
		ID: id, Kind: kind, SourceSchema: "och_context_source_v1",
		CoveredEventCount: 8, CoveredTurnCount: 2, ThroughSequence: 18,
		SourceDigestHex: strings.Repeat("a", 64), PreviousCheckpointID: previous,
		TokensBefore: 900, CheckpointTokens: 200, RetainedTailTokens: 100, EstimatedRequestTokens: 300,
	}
	if kind == domain.ContextCheckpointKindRollingSummary {
		record.Summary = "a summary"
		record.SummaryFormat = "och_context_summary_v1"
		record.PromptVersion = "och_context_summary_prompt_v1"
	}
	return record
}

// healthyTraceEvents is one well-formed manual-summary Attempt: a compaction
// bracket that closes, then a prepared decision paired with its request.
func healthyTraceEvents() []traceEvent {
	return []traceEvent{
		{domain.EventContextCompactionStarted, domain.ContextCompactionStarted{
			ID: "compaction-1", Trigger: domain.ContextTriggerManual, Strategy: domain.ContextStrategySummary,
			BaseSourceHead: 18, SourceSchema: "och_context_source_v1", MeterID: "och_wire_estimate_v1",
		}},
		{domain.EventContextCompactionCompleted, domain.ContextCompactionCompleted{
			ID: "compaction-1", Checkpoint: traceCheckpoint("checkpoint-1", "", domain.ContextCheckpointKindRollingSummary),
		}},
		{domain.EventContextPreparedRecorded, tracePrepared("decision-1", "turn-1", "item-1", 1, domain.ContextTriggerPreTurn)},
		{domain.EventModelRequestRecorded, traceRequest("decision-1", "turn-1", "item-1", 1)},
		{domain.EventModelUsageRecorded, domain.ModelUsageRecorded{
			TurnID: "turn-1", ItemID: "item-1", AttemptIndex: 1, InputTokens: 4000, OutputTokens: 30,
		}},
	}
}

func TestBuildContextTraceIndexesAHealthyAttempt(t *testing.T) {
	trace, err := buildContextTrace(traceReaderFor(t, healthyTraceEvents()...))
	if err != nil {
		t.Fatalf("buildContextTrace: %v", err)
	}
	compaction, ok := trace.Compaction("compaction-1")
	if !ok || compaction.Completed == nil || compaction.Failed != nil {
		t.Fatalf("compaction-1 = %+v, want one completed bracket", compaction)
	}
	if compaction.Started.Trigger != domain.ContextTriggerManual {
		t.Fatalf("start trigger = %q, want %q", compaction.Started.Trigger, domain.ContextTriggerManual)
	}
	decision, ok := trace.Decision("decision-1")
	if !ok || decision.Request == nil {
		t.Fatalf("decision-1 = %+v, want a paired request", decision)
	}
	if decision.Prepared.Trigger != domain.ContextTriggerPreTurn {
		t.Fatalf("decision trigger = %q, want %q", decision.Prepared.Trigger, domain.ContextTriggerPreTurn)
	}
	if len(trace.Usages) != 1 || trace.Usages[0].InputTokens != 4000 {
		t.Fatalf("Usages = %+v, want the one provider usage record", trace.Usages)
	}
	checkpoint, ok := trace.Checkpoint("checkpoint-1")
	if !ok || checkpoint.Kind != domain.ContextCheckpointKindRollingSummary {
		t.Fatalf("checkpoint-1 = %+v", checkpoint)
	}
	// Canonical order must survive: the compaction bracket precedes the decision.
	if got := trace.DecisionsInOrder(); len(got) != 1 || got[0].Prepared.ContextDecisionID != "decision-1" {
		t.Fatalf("DecisionsInOrder = %+v", got)
	}
}

// TestBuildContextTraceFailsClosed is the heart of section 8: every way the
// evidence can be broken must refuse to build a trace, so a dependent
// criterion resolves indeterminate instead of silently passing or degrading
// to a behavioural fail.
func TestBuildContextTraceFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		events []traceEvent
	}{
		{"terminal compaction with no earlier start", []traceEvent{
			{domain.EventContextCompactionCompleted, domain.ContextCompactionCompleted{
				ID: "compaction-orphan", Checkpoint: traceCheckpoint("checkpoint-1", "", domain.ContextCheckpointKindRollingSummary),
			}},
		}},
		{"failed compaction with no earlier start", []traceEvent{
			{domain.EventContextCompactionFailed, domain.ContextCompactionFailed{
				ID: "compaction-orphan", Code: "context_summary_failed", Message: "boom",
			}},
		}},
		{"two terminal events for one compaction", append(healthyTraceEvents(),
			traceEvent{domain.EventContextCompactionFailed, domain.ContextCompactionFailed{
				ID: "compaction-1", Code: "context_summary_failed", Message: "boom",
			}})},
		{"duplicate decision id", append(healthyTraceEvents(),
			traceEvent{domain.EventContextPreparedRecorded, tracePrepared("decision-1", "turn-1", "item-1", 2, domain.ContextTriggerMidTurn)})},
		{"request names a decision that was never prepared", []traceEvent{
			{domain.EventModelRequestRecorded, traceRequest("decision-missing", "turn-1", "item-1", 1)},
		}},
		{"request pairs a decision from a different turn", []traceEvent{
			{domain.EventContextPreparedRecorded, tracePrepared("decision-1", "turn-1", "item-1", 1, domain.ContextTriggerPreTurn)},
			{domain.EventModelRequestRecorded, traceRequest("decision-1", "turn-2", "item-1", 1)},
		}},
		{"request precedes its own prepared decision", []traceEvent{
			{domain.EventModelRequestRecorded, traceRequest("decision-1", "turn-1", "item-1", 1)},
			{domain.EventContextPreparedRecorded, tracePrepared("decision-1", "turn-1", "item-1", 1, domain.ContextTriggerPreTurn)},
		}},
		{"attempt index zero", []traceEvent{
			{domain.EventContextPreparedRecorded, tracePrepared("decision-1", "turn-1", "item-1", 0, domain.ContextTriggerPreTurn)},
		}},
		{"attempt indices out of order for one item", []traceEvent{
			{domain.EventContextPreparedRecorded, tracePrepared("decision-1", "turn-1", "item-1", 2, domain.ContextTriggerPreTurn)},
			{domain.EventContextPreparedRecorded, tracePrepared("decision-2", "turn-1", "item-1", 1, domain.ContextTriggerMidTurn)},
		}},
		{"budgets out of order", []traceEvent{
			func() traceEvent {
				prepared := tracePrepared("decision-1", "turn-1", "item-1", 1, domain.ContextTriggerPreTurn)
				prepared.BudgetTrigger = prepared.BudgetHardInput + 1
				return traceEvent{domain.EventContextPreparedRecorded, prepared}
			}(),
		}},
		{"zero budget", []traceEvent{
			func() traceEvent {
				prepared := tracePrepared("decision-1", "turn-1", "item-1", 1, domain.ContextTriggerPreTurn)
				prepared.BudgetTarget = 0
				return traceEvent{domain.EventContextPreparedRecorded, prepared}
			}(),
		}},
		{"missing meter id", []traceEvent{
			func() traceEvent {
				prepared := tracePrepared("decision-1", "turn-1", "item-1", 1, domain.ContextTriggerPreTurn)
				prepared.MeterID = ""
				return traceEvent{domain.EventContextPreparedRecorded, prepared}
			}(),
		}},
		{"duplicate checkpoint id", []traceEvent{
			{domain.EventContextCompactionStarted, domain.ContextCompactionStarted{
				ID: "compaction-1", Trigger: domain.ContextTriggerManual, Strategy: domain.ContextStrategySummary,
				BaseSourceHead: 18, SourceSchema: "och_context_source_v1", MeterID: "och_wire_estimate_v1",
			}},
			{domain.EventContextCompactionCompleted, domain.ContextCompactionCompleted{
				ID: "compaction-1", Checkpoint: traceCheckpoint("checkpoint-1", "", domain.ContextCheckpointKindRollingSummary),
			}},
			{domain.EventContextCompactionStarted, domain.ContextCompactionStarted{
				ID: "compaction-2", Trigger: domain.ContextTriggerPreTurn, Strategy: domain.ContextStrategySummary,
				BaseSourceHead: 20, SourceSchema: "och_context_source_v1", MeterID: "och_wire_estimate_v1",
			}},
			{domain.EventContextCompactionCompleted, domain.ContextCompactionCompleted{
				ID: "compaction-2", Checkpoint: traceCheckpoint("checkpoint-1", "", domain.ContextCheckpointKindRollingSummary),
			}},
		}},
		{"forked checkpoint predecessor", []traceEvent{
			{domain.EventContextCompactionStarted, domain.ContextCompactionStarted{
				ID: "compaction-1", Trigger: domain.ContextTriggerManual, Strategy: domain.ContextStrategySummary,
				BaseSourceHead: 18, SourceSchema: "och_context_source_v1", MeterID: "och_wire_estimate_v1",
			}},
			{domain.EventContextCompactionCompleted, domain.ContextCompactionCompleted{
				ID: "compaction-1", Checkpoint: traceCheckpoint("checkpoint-2", "checkpoint-root", domain.ContextCheckpointKindRollingSummary),
			}},
			{domain.EventContextCompactionStarted, domain.ContextCompactionStarted{
				ID: "compaction-2", Trigger: domain.ContextTriggerPreTurn, Strategy: domain.ContextStrategySummary,
				BaseSourceHead: 20, SourceSchema: "och_context_source_v1", MeterID: "och_wire_estimate_v1",
			}},
			{domain.EventContextCompactionCompleted, domain.ContextCompactionCompleted{
				ID: "compaction-2", Checkpoint: traceCheckpoint("checkpoint-3", "checkpoint-root", domain.ContextCheckpointKindRollingSummary),
			}},
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := buildContextTrace(traceReaderFor(t, testCase.events...)); err == nil {
				t.Fatalf("buildContextTrace accepted %s", testCase.name)
			}
		})
	}
}

func TestBuildContextTraceRefusesUnreadableEvidence(t *testing.T) {
	t.Run("no audit entry at all", func(t *testing.T) {
		reader := &ArtifactReader{evidenceRoot: t.TempDir(), manifest: EvidenceManifest{}}
		if _, err := buildContextTrace(reader); err == nil {
			t.Fatal("buildContextTrace accepted an Attempt with no audit evidence")
		}
	})
	t.Run("malformed json line", func(t *testing.T) {
		lines := traceLines(t, healthyTraceEvents()...)
		lines = append(lines, `{"events":[{"type":"context.prepared","data":{ this is not json`)
		if _, err := buildContextTrace(traceReaderFromLines(t, lines)); err == nil {
			t.Fatal("buildContextTrace accepted a malformed audit line")
		}
	})
	t.Run("malformed event payload", func(t *testing.T) {
		lines := traceLines(t, healthyTraceEvents()...)
		lines = append(lines, `{"events":[{"type":"context.prepared","data":"not-an-object"}]}`)
		if _, err := buildContextTrace(traceReaderFromLines(t, lines)); err == nil {
			t.Fatal("buildContextTrace accepted a malformed context.prepared payload")
		}
	})
}

// TestBuildContextTraceIgnoresSummarizerRequests proves the pairing rule
// applies to conversation requests only. A summarizer request carries no
// Context decision and must not be demanded to have one.
func TestBuildContextTraceIgnoresSummarizerRequests(t *testing.T) {
	events := append(healthyTraceEvents(), traceEvent{domain.EventModelRequestRecorded, domain.ModelRequestRecorded{
		TurnID: "turn-1", ItemID: "item-summary", Purpose: "compaction",
		Messages: []domain.ModelPromptMessage{{Role: "user", Text: "summarize"}},
	}})
	if _, err := buildContextTrace(traceReaderFor(t, events...)); err != nil {
		t.Fatalf("buildContextTrace refused a summarizer request: %v", err)
	}
}

// TestBuildContextTraceOnRealCollectedEvidence runs a real Attempt through
// the production in-process path and builds a trace from whatever canonical
// audit it actually wrote. Hand-built fixtures prove the correlation rules;
// only this proves the builder agrees with what OCH really emits.
func TestBuildContextTraceOnRealCollectedEvidence(t *testing.T) {
	directories, _, _ := collectedHappyAttempt(t)
	reader, err := NewArtifactReader(directories)
	if err != nil {
		t.Fatalf("NewArtifactReader: %v", err)
	}
	trace, err := buildContextTrace(reader)
	if err != nil {
		t.Fatalf("buildContextTrace on real evidence: %v", err)
	}
	decisions := trace.DecisionsInOrder()
	if len(decisions) == 0 {
		t.Fatal("a real Attempt produced no context.prepared evidence")
	}
	paired := 0
	for _, decision := range decisions {
		if decision.Request != nil {
			paired++
		}
		if decision.Prepared.MeterID == "" {
			t.Fatalf("decision %q names no meter", decision.Prepared.ContextDecisionID)
		}
	}
	if paired == 0 {
		t.Fatal("no real context decision paired with its own model request")
	}
	if len(trace.Usages) == 0 {
		t.Fatal("a real Attempt produced no provider usage evidence")
	}
}
