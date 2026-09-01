package domain

import (
	"encoding/json"
	"testing"
)

func validContextCompactionStarted(id ContextCompactionID, trigger, strategy string) ContextCompactionStarted {
	return ContextCompactionStarted{
		ID:             id,
		Trigger:        trigger,
		Strategy:       strategy,
		BaseSourceHead: 10,
		SourceSchema:   "och_context_source_v1",
		MeterID:        "och_wire_estimate_v1",
	}
}

func validContextCheckpointRecord(id string) ContextCheckpointRecord {
	return ContextCheckpointRecord{
		ID:                     id,
		Kind:                   ContextCheckpointKindRollingSummary,
		SourceSchema:           "och_context_source_v1",
		SummaryFormat:          "och_context_summary_v1",
		PromptVersion:          "och_context_summary_prompt_v1",
		CoveredEventCount:      5,
		CoveredTurnCount:       2,
		ThroughSequence:        10,
		SourceDigestHex:        "deadbeef",
		Summary:                "## Objective\nsomething",
		TokensBefore:           1000,
		CheckpointTokens:       100,
		RetainedTailTokens:     50,
		EstimatedRequestTokens: 150,
	}
}

func validContextPreparedRecorded(turnID TurnID, itemID ItemID) ContextPreparedRecorded {
	return ContextPreparedRecorded{
		TurnID:                    turnID,
		ItemID:                    itemID,
		AttemptIndex:              1,
		Trigger:                   ContextTriggerPreTurn,
		SourceHeadVersion:         10,
		BudgetHardInput:           6656,
		BudgetTrigger:             5324,
		BudgetTarget:              3660,
		EstimatedMessageTokens:    100,
		EstimatedToolSchemaTokens: 20,
		EstimatedTotalTokens:      120,
		MeterID:                   "och_wire_estimate_v1",
		SerializedEnvelopeBytes:   500,
	}
}

// --- Decide: StartContextCompaction eligibility ---

func TestDecideStartContextCompactionPreTurnRequiresNoActiveTurn(t *testing.T) {
	idle := compactActiveSession(t)
	if _, err := Decide(idle, StartContextCompaction{
		SessionID:                idle.ID,
		ContextCompactionStarted: validContextCompactionStarted("ctx-1", ContextTriggerPreTurn, ContextStrategySummary),
	}); err != nil {
		t.Fatalf("expected pre_turn start on an idle session to be eligible, got %v", err)
	}

	running := applyCompactRecord(t, idle, TurnStarted{TurnID: "turn-1", Input: "hi"})
	if _, err := Decide(running, StartContextCompaction{
		SessionID:                running.ID,
		ContextCompactionStarted: validContextCompactionStarted("ctx-1", ContextTriggerPreTurn, ContextStrategySummary),
	}); !IsCode(err, CodeTurnAlreadyRunning) {
		t.Fatalf("got %v, want CodeTurnAlreadyRunning", err)
	}
}

func TestDecideStartContextCompactionMidTurnRequiresActiveTurn(t *testing.T) {
	idle := compactActiveSession(t)
	if _, err := Decide(idle, StartContextCompaction{
		SessionID:                idle.ID,
		ContextCompactionStarted: validContextCompactionStarted("ctx-1", ContextTriggerMidTurn, ContextStrategySummary),
	}); !IsCode(err, CodeTurnNotRunning) {
		t.Fatalf("got %v, want CodeTurnNotRunning", err)
	}

	running := applyCompactRecord(t, idle, TurnStarted{TurnID: "turn-1", Input: "hi"})
	if _, err := Decide(running, StartContextCompaction{
		SessionID:                running.ID,
		ContextCompactionStarted: validContextCompactionStarted("ctx-1", ContextTriggerMidTurn, ContextStrategySummary),
	}); err != nil {
		t.Fatalf("expected mid_turn start with an active turn to be eligible, got %v", err)
	}
}

func TestDecideStartContextCompactionOverflowRetryRequiresActiveTurn(t *testing.T) {
	idle := compactActiveSession(t)
	if _, err := Decide(idle, StartContextCompaction{
		SessionID:                idle.ID,
		ContextCompactionStarted: validContextCompactionStarted("ctx-1", ContextTriggerOverflowRetry, ContextStrategyReset),
	}); !IsCode(err, CodeTurnNotRunning) {
		t.Fatalf("got %v, want CodeTurnNotRunning", err)
	}
}

func TestDecideStartContextCompactionManualRequiresNoActiveTurn(t *testing.T) {
	idle := compactActiveSession(t)
	running := applyCompactRecord(t, idle, TurnStarted{TurnID: "turn-1", Input: "hi"})
	if _, err := Decide(running, StartContextCompaction{
		SessionID:                running.ID,
		ContextCompactionStarted: validContextCompactionStarted("ctx-1", ContextTriggerManual, ContextStrategySummary),
	}); !IsCode(err, CodeTurnAlreadyRunning) {
		t.Fatalf("got %v, want CodeTurnAlreadyRunning", err)
	}
}

func TestDecideStartContextCompactionRejectsSecondWhileOneActive(t *testing.T) {
	idle := compactActiveSession(t)
	active := applyCompactRecord(t, idle, validContextCompactionStarted("ctx-1", ContextTriggerPreTurn, ContextStrategySummary))
	if _, err := Decide(active, StartContextCompaction{
		SessionID:                active.ID,
		ContextCompactionStarted: validContextCompactionStarted("ctx-2", ContextTriggerPreTurn, ContextStrategySummary),
	}); !IsCode(err, CodeCompactionAlreadyRunning) {
		t.Fatalf("got %v, want CodeCompactionAlreadyRunning", err)
	}
}

func TestDecideStartContextCompactionRejectsInvalidTriggerAndStrategy(t *testing.T) {
	idle := compactActiveSession(t)
	if _, err := Decide(idle, StartContextCompaction{
		SessionID:                idle.ID,
		ContextCompactionStarted: validContextCompactionStarted("ctx-1", "not_a_real_trigger", ContextStrategySummary),
	}); !IsCode(err, CodeInvalidCommand) {
		t.Fatalf("got %v, want CodeInvalidCommand for an invalid trigger", err)
	}
	if _, err := Decide(idle, StartContextCompaction{
		SessionID:                idle.ID,
		ContextCompactionStarted: validContextCompactionStarted("ctx-1", ContextTriggerPreTurn, "not_a_real_strategy"),
	}); !IsCode(err, CodeInvalidCommand) {
		t.Fatalf("got %v, want CodeInvalidCommand for an invalid strategy", err)
	}
}

// --- Decide: CompleteContextCompaction / FailContextCompaction ---

func TestDecideCompleteContextCompactionRequiresActiveCompaction(t *testing.T) {
	idle := compactActiveSession(t)
	if _, err := Decide(idle, CompleteContextCompaction{
		SessionID: idle.ID,
		ContextCompactionCompleted: ContextCompactionCompleted{
			ID: "ctx-1", Checkpoint: validContextCheckpointRecord("ckpt-1"),
		},
	}); !IsCode(err, CodeCompactionNotRunning) {
		t.Fatalf("got %v, want CodeCompactionNotRunning", err)
	}
}

func TestDecideCompleteContextCompactionRejectsIDMismatch(t *testing.T) {
	idle := compactActiveSession(t)
	active := applyCompactRecord(t, idle, validContextCompactionStarted("ctx-1", ContextTriggerPreTurn, ContextStrategySummary))
	if _, err := Decide(active, CompleteContextCompaction{
		SessionID: active.ID,
		ContextCompactionCompleted: ContextCompactionCompleted{
			ID: "ctx-WRONG", Checkpoint: validContextCheckpointRecord("ckpt-1"),
		},
	}); !IsCode(err, CodeCompactionMismatch) {
		t.Fatalf("got %v, want CodeCompactionMismatch", err)
	}
}

func TestDecideCompleteContextCompactionAccepts(t *testing.T) {
	idle := compactActiveSession(t)
	active := applyCompactRecord(t, idle, validContextCompactionStarted("ctx-1", ContextTriggerPreTurn, ContextStrategySummary))
	if _, err := Decide(active, CompleteContextCompaction{
		SessionID: active.ID,
		ContextCompactionCompleted: ContextCompactionCompleted{
			ID: "ctx-1", Checkpoint: validContextCheckpointRecord("ckpt-1"),
		},
	}); err != nil {
		t.Fatalf("expected a valid completion to be accepted, got %v", err)
	}
}

func TestDecideFailContextCompactionRequiresActiveCompaction(t *testing.T) {
	idle := compactActiveSession(t)
	if _, err := Decide(idle, FailContextCompaction{
		SessionID:               idle.ID,
		ContextCompactionFailed: ContextCompactionFailed{ID: "ctx-1", Code: "context_summary_failed", Message: "safe message"},
	}); !IsCode(err, CodeCompactionNotRunning) {
		t.Fatalf("got %v, want CodeCompactionNotRunning", err)
	}
}

// --- Decide: RecordContextPreparation ---

func TestDecideRecordContextPreparationRequiresRunningAssistantItem(t *testing.T) {
	idle := compactActiveSession(t)
	if _, err := Decide(idle, RecordContextPreparation{
		SessionID:               idle.ID,
		ContextPreparedRecorded: validContextPreparedRecorded("turn-1", "item-1"),
	}); err == nil {
		t.Fatal("expected an error when no turn/item is running")
	}

	running := applyCompactRecord(t, idle, TurnStarted{TurnID: "turn-1", Input: "hi"})
	started := applyCompactRecord(t, running, AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"})
	if _, err := Decide(started, RecordContextPreparation{
		SessionID:               started.ID,
		ContextPreparedRecorded: validContextPreparedRecorded("turn-1", "item-1"),
	}); err != nil {
		t.Fatalf("expected acceptance with a running assistant item, got %v", err)
	}
}

// --- Apply: mirrored eligibility, terminal-timestamp ordering, and the
// "completed checkpoint never enters the bounded aggregate" rule ---

func TestApplyContextCompactionStartedSetsAndClearsAggregateField(t *testing.T) {
	state := compactActiveSession(t)
	state = applyCompactRecord(t, state, validContextCompactionStarted("ctx-1", ContextTriggerPreTurn, ContextStrategySummary))
	if state.ContextCompaction == nil || state.ContextCompaction.ID != "ctx-1" {
		t.Fatalf("ContextCompaction = %+v, want an active compaction with ID ctx-1", state.ContextCompaction)
	}

	state = applyCompactRecord(t, state, ContextCompactionCompleted{ID: "ctx-1", Checkpoint: validContextCheckpointRecord("ckpt-1")})
	if state.ContextCompaction != nil {
		t.Fatalf("ContextCompaction = %+v, want nil after completion (completed checkpoints never enter the bounded aggregate)", state.ContextCompaction)
	}
}

func TestApplyContextCompactionFailedClearsAggregateField(t *testing.T) {
	state := compactActiveSession(t)
	state = applyCompactRecord(t, state, validContextCompactionStarted("ctx-1", ContextTriggerPreTurn, ContextStrategySummary))
	state = applyCompactRecord(t, state, ContextCompactionFailed{ID: "ctx-1", Code: "context_summary_failed", Message: "safe message"})
	if state.ContextCompaction != nil {
		t.Fatalf("ContextCompaction = %+v, want nil after failure", state.ContextCompaction)
	}
}

func TestApplyContextCompactionRejectsSecondWhileOneActive(t *testing.T) {
	state := compactActiveSession(t)
	state = applyCompactRecord(t, state, validContextCompactionStarted("ctx-1", ContextTriggerPreTurn, ContextStrategySummary))
	_, err := Apply(state, compactRecord(state, validContextCompactionStarted("ctx-2", ContextTriggerPreTurn, ContextStrategySummary)))
	if !IsCode(err, CodeCompactionAlreadyRunning) {
		t.Fatalf("got %v, want CodeCompactionAlreadyRunning", err)
	}
}

func TestApplyContextCompactionTerminalTimestampCannotPrecedeStart(t *testing.T) {
	state := compactActiveSession(t)
	state = applyCompactRecord(t, state, validContextCompactionStarted("ctx-1", ContextTriggerPreTurn, ContextStrategySummary))
	record := compactRecord(state, ContextCompactionCompleted{ID: "ctx-1", Checkpoint: validContextCheckpointRecord("ckpt-1")})
	record.OccurredAt = state.ContextCompaction.StartedAt.Add(-1)
	if _, err := Apply(state, record); !IsCode(err, CodeInvalidEvent) {
		t.Fatalf("got %v, want CodeInvalidEvent for a terminal timestamp preceding start", err)
	}
}

func TestApplyContextPreparedRecordedRequiresRunningAssistantItem(t *testing.T) {
	state := compactActiveSession(t)
	state = applyCompactRecord(t, state, TurnStarted{TurnID: "turn-1", Input: "hi"})
	state = applyCompactRecord(t, state, AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"})
	next, err := Apply(state, compactRecord(state, validContextPreparedRecorded("turn-1", "item-1")))
	if err != nil {
		t.Fatalf("expected acceptance, got %v", err)
	}
	if next.Version != state.Version+1 {
		t.Fatalf("Version = %d, want %d", next.Version, state.Version+1)
	}
}

// --- Full command -> Decide -> Apply/Replay round trip ---

func TestContextCompactionFullLifecycleRoundTrip(t *testing.T) {
	var records []RecordedEvent

	decideAndApply := func(state Session, command Command) Session {
		t.Helper()
		events, err := Decide(state, command)
		if err != nil {
			t.Fatalf("Decide(%T) error = %v", command, err)
		}
		for _, uncommitted := range events {
			record := compactRecord(state, uncommitted.Event)
			var applyErr error
			state, applyErr = Apply(state, record)
			if applyErr != nil {
				t.Fatalf("Apply(%T) error = %v", uncommitted.Event, applyErr)
			}
			records = append(records, record)
		}
		return state
	}

	// Build the session through Decide/Apply from scratch, like every
	// other step below, so records contains the complete sequence Replay
	// can reconstruct from -- compactActiveSession's own SessionCreated
	// record is internal to that helper and would otherwise be silently
	// missing from records, breaking Replay at sequence 2.
	state := decideAndApply(Session{}, CreateSession{SessionID: "compact-session", WorkspaceRoot: "/workspace"})

	state = decideAndApply(state, StartContextCompaction{
		SessionID:                state.ID,
		ContextCompactionStarted: validContextCompactionStarted("ctx-1", ContextTriggerPreTurn, ContextStrategySummary),
	})
	if state.ContextCompaction == nil {
		t.Fatal("expected an active compaction after start")
	}
	state = decideAndApply(state, CompleteContextCompaction{
		SessionID: state.ID,
		ContextCompactionCompleted: ContextCompactionCompleted{
			ID: "ctx-1", Checkpoint: validContextCheckpointRecord("ckpt-1"),
		},
	})
	if state.ContextCompaction != nil {
		t.Fatal("expected no active compaction after completion")
	}

	state = decideAndApply(state, StartTurn{SessionID: state.ID, TurnID: "turn-1", Input: "hi"})
	state = decideAndApply(state, StartAssistantMessage{SessionID: state.ID, TurnID: "turn-1", ItemID: "item-1"})
	state = decideAndApply(state, RecordContextPreparation{
		SessionID:               state.ID,
		ContextPreparedRecorded: validContextPreparedRecorded("turn-1", "item-1"),
	})

	// A full Replay from scratch over the same records must reach the
	// identical final state — the same command->event->state round trip
	// this project's other command families are held to.
	replayed, err := Replay(records)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if replayed.Version != state.Version {
		t.Fatalf("Replay().Version = %d, want %d", replayed.Version, state.Version)
	}
	if (replayed.ContextCompaction == nil) != (state.ContextCompaction == nil) {
		t.Fatalf("Replay().ContextCompaction = %+v, want %+v", replayed.ContextCompaction, state.ContextCompaction)
	}
}

// --- Codec golden round-trips ---

func TestCodecContextCompactionStartedRoundTrip(t *testing.T) {
	record := compactRecord(compactActiveSession(t), validContextCompactionStarted("ctx-1", ContextTriggerMidTurn, ContextStrategyReset))
	record.SessionID = "compact-session"
	encoded, err := MarshalRecordedEvent(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalRecordedEvent(encoded)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := decoded.Event.(ContextCompactionStarted)
	if !ok {
		t.Fatalf("decoded event type = %T, want ContextCompactionStarted", decoded.Event)
	}
	want := record.Event.(ContextCompactionStarted)
	if got != want {
		t.Fatalf("round-tripped = %+v, want %+v", got, want)
	}
}

func TestCodecContextCompactionCompletedRoundTrip(t *testing.T) {
	event := ContextCompactionCompleted{ID: "ctx-1", Checkpoint: validContextCheckpointRecord("ckpt-1")}
	record := compactRecord(compactActiveSession(t), event)
	record.SessionID = "compact-session"
	encoded, err := MarshalRecordedEvent(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalRecordedEvent(encoded)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := decoded.Event.(ContextCompactionCompleted)
	if !ok {
		t.Fatalf("decoded event type = %T, want ContextCompactionCompleted", decoded.Event)
	}
	if got != event {
		t.Fatalf("round-tripped = %+v, want %+v", got, event)
	}
}

func TestCodecContextCompactionFailedRoundTrip(t *testing.T) {
	event := ContextCompactionFailed{ID: "ctx-1", Code: "context_summary_failed", Message: "safe message"}
	record := compactRecord(compactActiveSession(t), event)
	record.SessionID = "compact-session"
	encoded, err := MarshalRecordedEvent(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalRecordedEvent(encoded)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := decoded.Event.(ContextCompactionFailed)
	if !ok {
		t.Fatalf("decoded event type = %T, want ContextCompactionFailed", decoded.Event)
	}
	if got != event {
		t.Fatalf("round-tripped = %+v, want %+v", got, event)
	}
}

func TestCodecContextPreparedRecordedRoundTrip(t *testing.T) {
	event := validContextPreparedRecorded("turn-1", "item-1")
	record := compactRecord(compactActiveSession(t), event)
	record.SessionID = "compact-session"
	encoded, err := MarshalRecordedEvent(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalRecordedEvent(encoded)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := decoded.Event.(ContextPreparedRecorded)
	if !ok {
		t.Fatalf("decoded event type = %T, want ContextPreparedRecorded", decoded.Event)
	}
	if got != event {
		t.Fatalf("round-tripped = %+v, want %+v", got, event)
	}
}

// TestCodecContextEventsRejectUnknownFields matches this project's own
// existing strict-unknown-field convention for every other event type.
func TestCodecContextEventsRejectUnknownFields(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{
			name: "ContextCompactionStarted",
			json: `{"schemaVersion":1,"id":"e1","commandId":"c1","sessionId":"s1","sequence":1,"occurredAt":"2026-08-13T01:02:01Z","type":"context.compaction.started","data":{"id":"ctx-1","trigger":"pre_turn","strategy":"summary","baseSourceHead":10,"sourceSchema":"och_context_source_v1","meterID":"och_wire_estimate_v1","extra":"nope"}}`,
		},
		{
			name: "ContextCompactionFailed",
			json: `{"schemaVersion":1,"id":"e1","commandId":"c1","sessionId":"s1","sequence":1,"occurredAt":"2026-08-13T01:02:01Z","type":"context.compaction.failed","data":{"id":"ctx-1","code":"x","message":"y","extra":"nope"}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := UnmarshalRecordedEvent([]byte(test.json))
			if !IsCode(err, CodeInvalidEvent) {
				t.Fatalf("got %v, want CodeInvalidEvent for an unknown field", err)
			}
		})
	}
}

// TestModelRequestRecordedNewFieldsRoundTrip confirms Purpose/AttemptIndex/
// ContextDecisionID round-trip, and that an event constructed without them
// (the pre-Task-7 shape) still encodes and decodes identically -- the
// backward-compatibility property every existing caller depends on.
func TestModelRequestRecordedNewFieldsRoundTrip(t *testing.T) {
	withNewFields := validModelRequestRecorded("turn-1", "item-1", "hi")
	withNewFields.Purpose = ModelRequestPurposeCompaction
	withNewFields.AttemptIndex = 2
	withNewFields.ContextDecisionID = "decision-1"

	record := compactRecord(compactActiveSession(t), withNewFields)
	record.SessionID = "compact-session"
	encoded, err := MarshalRecordedEvent(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalRecordedEvent(encoded)
	if err != nil {
		t.Fatal(err)
	}
	got := decoded.Event.(ModelRequestRecorded)
	if got.Purpose != ModelRequestPurposeCompaction || got.AttemptIndex != 2 || got.ContextDecisionID != "decision-1" {
		t.Fatalf("round-tripped new fields = %+v", got)
	}

	// Old-style: no new fields set at all.
	old := validModelRequestRecorded("turn-1", "item-1", "hi")
	oldRecord := compactRecord(compactActiveSession(t), old)
	oldRecord.SessionID = "compact-session"
	oldEncoded, err := MarshalRecordedEvent(oldRecord)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(oldEncoded, &wire); err != nil {
		t.Fatal(err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(wire["data"], &data); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"purpose", "attemptIndex", "contextDecisionID"} {
		if _, present := data[key]; present {
			t.Fatalf("old-style event unexpectedly encoded key %q (omitempty broken, changes the wire shape for every existing caller)", key)
		}
	}
}
