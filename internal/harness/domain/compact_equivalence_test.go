package domain

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestCompactDecisionsMatchFullStateForFixturePrefixes(t *testing.T) {
	for _, path := range []string{
		"testdata/assistant_lifecycle.jsonl",
		"testdata/session_lifecycle.jsonl",
	} {
		records := fixtureRecords(t, path)
		for prefix := 0; prefix <= len(records); prefix++ {
			t.Run(fmt.Sprintf("%s/prefix-%d", path, prefix), func(t *testing.T) {
				full, err := HistoricalReplay(records[:prefix])
				if err != nil {
					t.Fatalf("HistoricalReplay() prefix %d error = %v", prefix, err)
				}
				compact, err := Replay(records[:prefix])
				if err != nil {
					t.Fatalf("Replay() prefix %d error = %v", prefix, err)
				}
				for _, command := range freshCommandsForPrefix(full, prefix) {
					assertDecisionEquivalent(t, full, compact, command)
				}
			})
		}
	}
}

func TestCompactDecisionsMatchFullStateForModelFacts(t *testing.T) {
	created := RecordedEvent{SchemaVersion: schemaVersion, ID: "model-fact-1", CommandID: "model-fact-1", SessionID: "model-fact-session", Sequence: 1, OccurredAt: time.Date(2026, 8, 15, 0, 0, 1, 0, time.UTC), Event: SessionCreated{WorkspaceRoot: "/workspace"}}
	full, err := HistoricalApply(HistoricalSession{}, created)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := Apply(Session{}, created)
	if err != nil {
		t.Fatal(err)
	}

	command := StartAssistantTurn{SessionID: "model-fact-session", TurnID: "turn-1", ItemID: "item-1", Input: "inspect", Request: validModelRequestSpec("inspect")}
	assertDecisionEquivalent(t, full, compact, command)
	events, err := Decide(compact, command)
	if err != nil || len(events) != 3 {
		t.Fatalf("Decide() = (%#v, %v)", events, err)
	}
	when := time.Date(2026, 8, 15, 0, 0, 2, 0, time.UTC)
	for index, event := range events {
		record := RecordedEvent{
			SchemaVersion: schemaVersion,
			ID:            EventID(fmt.Sprintf("model-fact-%d", index+2)),
			CommandID:     "model-fact-admit",
			SessionID:     "model-fact-session",
			Sequence:      uint64(index + 2),
			OccurredAt:    when,
			Event:         event.Event,
		}
		full, err = HistoricalApply(full, record)
		if err != nil {
			t.Fatalf("HistoricalApply(%d) error = %v", index, err)
		}
		compact, err = Apply(compact, record)
		if err != nil {
			t.Fatalf("Apply(%d) error = %v", index, err)
		}
	}

	usage := RecordModelUsage{SessionID: "model-fact-session", ModelUsageRecorded: validModelUsageRecorded("turn-1", "item-1")}
	assertDecisionEquivalent(t, full, compact, usage)
	usageEvents, err := Decide(compact, usage)
	if err != nil || len(usageEvents) != 1 {
		t.Fatalf("Decide(usage) = (%#v, %v)", usageEvents, err)
	}
	usageRecord := RecordedEvent{
		SchemaVersion: schemaVersion,
		ID:            "model-fact-usage",
		CommandID:     "model-fact-usage",
		SessionID:     "model-fact-session",
		Sequence:      5,
		OccurredAt:    when,
		Event:         usageEvents[0].Event,
	}
	full, err = HistoricalApply(full, usageRecord)
	if err != nil {
		t.Fatal(err)
	}
	compact, err = Apply(compact, usageRecord)
	if err != nil {
		t.Fatal(err)
	}
	for _, next := range freshCommandsForPrefix(full, 99) {
		assertDecisionEquivalent(t, full, compact, next)
	}

	complete := CompleteAssistantTurn{SessionID: "model-fact-session", TurnID: "turn-1", ItemID: "item-1", Text: "done"}
	assertDecisionEquivalent(t, full, compact, complete)
	completeEvents, err := Decide(compact, complete)
	if err != nil {
		t.Fatal(err)
	}
	for index, event := range completeEvents {
		record := RecordedEvent{
			SchemaVersion: schemaVersion,
			ID:            EventID(fmt.Sprintf("model-fact-term-%d", index)),
			CommandID:     "model-fact-term",
			SessionID:     "model-fact-session",
			Sequence:      uint64(6 + index),
			OccurredAt:    when,
			Event:         event.Event,
		}
		full, err = HistoricalApply(full, record)
		if err != nil {
			t.Fatal(err)
		}
		compact, err = Apply(compact, record)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, next := range freshCommandsForPrefix(full, 100) {
		assertDecisionEquivalent(t, full, compact, next)
	}
}

func TestCompactHistoricalDuplicateRequiresStoreIdentityIndex(t *testing.T) {
	records := fixtureRecords(t, "testdata/assistant_lifecycle.jsonl")
	full, err := HistoricalReplay(records[:5])
	if err != nil {
		t.Fatal(err)
	}
	compact, err := Replay(records[:5])
	if err != nil {
		t.Fatal(err)
	}

	fullEvents, fullErr := HistoricalDecide(full, StartTurn{SessionID: full.ID, TurnID: "turn-1", Input: "duplicate"})
	compactEvents, compactErr := Decide(compact, StartTurn{SessionID: compact.ID, TurnID: "turn-1", Input: "duplicate"})
	if !IsCode(fullErr, CodeTurnAlreadyExists) || fullEvents != nil {
		t.Fatalf("full historical turn duplicate = (%#v, %v), want %q", fullEvents, fullErr, CodeTurnAlreadyExists)
	}
	if compactErr != nil || !reflect.DeepEqual(compactEvents, []UncommittedEvent{{Event: TurnStarted{TurnID: "turn-1", Input: "duplicate"}}}) {
		t.Fatalf("compact historical turn duplicate = (%#v, %v), want admission pending Store identity index", compactEvents, compactErr)
	}

	fullEvents, fullErr = HistoricalDecide(full, StartAssistantTurn{SessionID: full.ID, TurnID: "turn-new", ItemID: "item-1", Input: "duplicate item"})
	compactEvents, compactErr = Decide(compact, StartAssistantTurn{SessionID: compact.ID, TurnID: "turn-new", ItemID: "item-1", Input: "duplicate item"})
	if !IsCode(fullErr, CodeItemAlreadyExists) || fullEvents != nil {
		t.Fatalf("full historical item duplicate = (%#v, %v), want %q", fullEvents, fullErr, CodeItemAlreadyExists)
	}
	wantCompact := []UncommittedEvent{
		{Event: TurnStarted{TurnID: "turn-new", Input: "duplicate item"}},
		{Event: AssistantMessageStarted{TurnID: "turn-new", ItemID: "item-1"}},
	}
	if compactErr != nil || !reflect.DeepEqual(compactEvents, wantCompact) {
		t.Fatalf("compact historical item duplicate = (%#v, %v), want admission pending Store identity index", compactEvents, compactErr)
	}
}

func TestApplyCompactMatchesV1ChronologyRejections(t *testing.T) {
	base := compactChronologyRecords()
	full, err := HistoricalReplay(base)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := Replay(base)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		event Event
	}{
		{
			name:  "later item starts before completed item terminal",
			event: AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-2"},
		},
		{
			name:  "turn terminal precedes completed item terminal",
			event: TurnCompleted{TurnID: "turn-1"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := RecordedEvent{
				SchemaVersion: schemaVersion,
				ID:            "chronology-invalid",
				CommandID:     "chronology-invalid",
				SessionID:     "chronology-session",
				Sequence:      5,
				OccurredAt:    time.Date(2026, 8, 13, 0, 0, 3, 0, time.UTC),
				Event:         test.event,
			}
			_, fullErr := HistoricalApply(full, record)
			_, compactErr := Apply(compact, record)
			if errorCode(fullErr) != CodeInvalidEvent || errorCode(compactErr) != errorCode(fullErr) {
				t.Fatalf("chronology rejection compact = %v, full = %v", compactErr, fullErr)
			}
		})
	}
}

func TestApplyCompactMatchesV1DuplicateSessionCreatedPreflightOrder(t *testing.T) {
	base := compactChronologyRecords()[:1]
	full, err := HistoricalReplay(base)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := Replay(base)
	if err != nil {
		t.Fatal(err)
	}
	record := RecordedEvent{
		SchemaVersion: schemaVersion,
		ID:            "duplicate-session-created",
		CommandID:     "duplicate-session-created",
		SessionID:     "chronology-session",
		Sequence:      99,
		OccurredAt:    time.Date(2026, 8, 13, 0, 0, 5, 0, time.UTC),
		Event:         SessionCreated{WorkspaceRoot: "/workspace"},
	}
	_, fullErr := HistoricalApply(full, record)
	_, compactErr := Apply(compact, record)
	if errorCode(fullErr) != CodeSessionAlreadyExists || errorCode(compactErr) != errorCode(fullErr) {
		t.Fatalf("duplicate create preflight compact = %v, full = %v", compactErr, fullErr)
	}
}

func FuzzReplayCompact(f *testing.F) {
	f.Add(fixtureBytesForFuzz("testdata/assistant_lifecycle.jsonl"))
	f.Add(fixtureBytesForFuzz("testdata/session_lifecycle.jsonl"))
	f.Fuzz(func(t *testing.T, data []byte) {
		records, err := DecodeJSONL(bytes.NewReader(data))
		if err != nil {
			return
		}
		first, firstErr := Replay(records)
		second, secondErr := Replay(records)
		if errorCode(firstErr) != errorCode(secondErr) || !reflect.DeepEqual(first, second) {
			t.Fatalf("non-deterministic replay: (%#v,%v) then (%#v,%v)", first, firstErr, second, secondErr)
		}
	})
}

func assertDecisionEquivalent(t *testing.T, full HistoricalSession, compact Session, command Command) {
	t.Helper()
	wantEvents, wantErr := HistoricalDecide(full, command)
	gotEvents, gotErr := Decide(compact, command)
	if errorCode(gotErr) != errorCode(wantErr) || !reflect.DeepEqual(gotEvents, wantEvents) {
		t.Fatalf("command %T compact decision = (%#v,%v), full = (%#v,%v)", command, gotEvents, gotErr, wantEvents, wantErr)
	}
}

func freshCommandsForPrefix(state HistoricalSession, prefix int) []Command {
	sessionID := state.ID
	turnID := TurnID(fmt.Sprintf("turn-fresh-%d", prefix))
	itemID := ItemID(fmt.Sprintf("item-fresh-%d", prefix))
	if state.ActiveTurnID != "" {
		turnID = state.ActiveTurnID
		if item := state.Turns[turnID].ActiveItemID; item != "" {
			itemID = item
		}
	}
	return []Command{
		CreateSession{SessionID: "session-fresh", WorkspaceRoot: "/fresh"},
		StartTurn{SessionID: sessionID, TurnID: TurnID(fmt.Sprintf("turn-fresh-%d", prefix)), Input: "fresh"},
		StartAssistantTurn{SessionID: sessionID, TurnID: TurnID(fmt.Sprintf("assistant-turn-fresh-%d", prefix)), ItemID: ItemID(fmt.Sprintf("assistant-item-fresh-%d", prefix)), Input: "fresh"},
		StartAssistantTurn{SessionID: sessionID, TurnID: TurnID(fmt.Sprintf("assistant-turn-request-%d", prefix)), ItemID: ItemID(fmt.Sprintf("assistant-item-request-%d", prefix)), Input: "fresh", Request: validModelRequestSpec("fresh")},
		RecordModelUsage{SessionID: sessionID, ModelUsageRecorded: ModelUsageRecorded{TurnID: turnID, ItemID: itemID, FinishReason: FinishReasonStop}},
		CompleteTurn{SessionID: sessionID, TurnID: turnID},
		FailTurn{SessionID: sessionID, TurnID: turnID, Code: "failed", Message: "failed"},
		InterruptTurn{SessionID: sessionID, TurnID: turnID, Reason: "interrupted"},
		StartAssistantMessage{SessionID: sessionID, TurnID: turnID, ItemID: ItemID(fmt.Sprintf("item-fresh-%d", prefix))},
		CompleteAssistantMessage{SessionID: sessionID, TurnID: turnID, ItemID: itemID, Text: "done", ToolCalls: []ToolCallOffer{validToolCallOffer()}},
		CompleteAssistantTurn{SessionID: sessionID, TurnID: turnID, ItemID: itemID, Text: "done"},
		FailAssistantTurn{SessionID: sessionID, TurnID: turnID, ItemID: itemID, Code: "failed", Message: "failed"},
		InterruptAssistantTurn{SessionID: sessionID, TurnID: turnID, ItemID: itemID, Code: "interrupted", Message: "interrupted"},
		RecordModelRequest{SessionID: sessionID, ModelRequestRecorded: validModelRequestRecorded(turnID, itemID, "fresh")},
		StartToolCall{SessionID: sessionID, TurnID: turnID, ItemID: ItemID(fmt.Sprintf("tool-fresh-%d", prefix)), CallID: "call-1", Name: "read_file", Arguments: `{}`, StepIndex: 1},
		CompleteToolCall{SessionID: sessionID, TurnID: turnID, ItemID: itemID, CallID: "call-1", Content: "ok"},
		FailToolCall{SessionID: sessionID, TurnID: turnID, ItemID: itemID, CallID: "call-1", Code: "policy_denied", Message: "policy denied this tool"},
		InterruptToolTurn{SessionID: sessionID, TurnID: turnID, ItemID: itemID, CallID: "call-1", Code: InterruptionCallerCanceled, Message: ""},
		FailToolTurn{SessionID: sessionID, TurnID: turnID, ItemID: itemID, CallID: "call-1", Code: "failed", Message: "failed"},
		RecordPolicyDecision{SessionID: sessionID, TurnID: turnID, ItemID: itemID, CallID: "call-1", Name: "read_file", Effect: PolicyEffectAllow, RuleID: "default.read", Reason: "in_workspace"},
		RequestApproval{SessionID: sessionID, TurnID: turnID, ItemID: itemID, ApprovalID: "approval-1", CallID: "call-1", Name: "write_file", Reason: "write_requires_approval"},
		ResolveApproval{SessionID: sessionID, TurnID: turnID, ItemID: itemID, ApprovalID: "approval-1", Decision: ApprovalDecisionGranted},
		CloseSession{SessionID: sessionID},
	}
}

func errorCode(err error) ErrorCode {
	var domainErr *DomainError
	if errors.As(err, &domainErr) {
		return domainErr.Code
	}
	return ""
}

func fixtureBytesForFuzz(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return data
}

func compactChronologyRecords() []RecordedEvent {
	return []RecordedEvent{
		{SchemaVersion: schemaVersion, ID: "chronology-1", CommandID: "chronology-1", SessionID: "chronology-session", Sequence: 1, OccurredAt: time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC), Event: SessionCreated{WorkspaceRoot: "/workspace"}},
		{SchemaVersion: schemaVersion, ID: "chronology-2", CommandID: "chronology-2", SessionID: "chronology-session", Sequence: 2, OccurredAt: time.Date(2026, 8, 13, 0, 0, 1, 0, time.UTC), Event: TurnStarted{TurnID: "turn-1", Input: "inspect"}},
		{SchemaVersion: schemaVersion, ID: "chronology-3", CommandID: "chronology-3", SessionID: "chronology-session", Sequence: 3, OccurredAt: time.Date(2026, 8, 13, 0, 0, 2, 0, time.UTC), Event: AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}},
		{SchemaVersion: schemaVersion, ID: "chronology-4", CommandID: "chronology-4", SessionID: "chronology-session", Sequence: 4, OccurredAt: time.Date(2026, 8, 13, 0, 0, 4, 0, time.UTC), Event: AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "done"}},
	}
}
