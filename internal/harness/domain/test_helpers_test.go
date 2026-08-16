package domain

import (
	"fmt"
	"testing"
	"time"
)

func recordedForTest(state HistoricalSession, event Event) RecordedEvent {
	sequence := state.Version + 1
	return RecordedEvent{
		SchemaVersion: 1,
		ID:            EventID(fmt.Sprintf("event-%d", sequence)),
		CommandID:     CommandID(fmt.Sprintf("command-%d", sequence)),
		SessionID:     SessionID("session-1"),
		Sequence:      sequence,
		OccurredAt:    time.Date(2026, 8, 11, 0, 0, int(sequence), 0, time.UTC),
		Event:         event,
	}
}

func activeSessionForTest(t *testing.T) HistoricalSession {
	t.Helper()
	state, err := HistoricalApply(HistoricalSession{}, recordedForTest(HistoricalSession{}, SessionCreated{WorkspaceRoot: "/workspace"}))
	if err != nil {
		t.Fatalf("create test session: %v", err)
	}
	return state
}

func validModelRequestSpec(input string) *ModelRequestSpec {
	return &ModelRequestSpec{
		AdapterFamily:    "openai_compat",
		ModelID:          "test-model",
		EndpointID:       "api.example.com",
		NativeTools:      "unsupported",
		Images:           "unsupported",
		StructuredOutput: "unsupported",
		ReasoningFields:  "unsupported",
		PromptCache:      "unsupported",
		IncludeUsage:     true,
		Messages:         []ModelPromptMessage{{Role: PromptRoleUser, Text: input}},
	}
}

func validModelRequestRecorded(turnID TurnID, itemID ItemID, input string) ModelRequestRecorded {
	return modelRequestRecordedFromSpec(turnID, itemID, *validModelRequestSpec(input))
}

func validModelUsageRecorded(turnID TurnID, itemID ItemID) ModelUsageRecorded {
	return ModelUsageRecorded{
		TurnID:            turnID,
		ItemID:            itemID,
		InputTokens:       3,
		OutputTokens:      5,
		CachedInputTokens: 1,
		LatencyMs:         12,
		FinishReason:      FinishReasonStop,
		ProviderRequestID: "req-1",
	}
}

func validToolCallStarted(turnID TurnID, itemID ItemID) ToolCallStarted {
	return ToolCallStarted{
		TurnID: turnID, ItemID: itemID, CallID: "call-1",
		Name: "read_file", Arguments: `{"path":"README.md"}`, StepIndex: 1,
	}
}

func validToolCallOffer() ToolCallOffer {
	return ToolCallOffer{ID: "call-1", Name: "read_file", Arguments: `{"path":"README.md"}`}
}

func runningTurnForTest(t *testing.T) HistoricalSession {
	t.Helper()
	state := activeSessionForTest(t)
	state, err := HistoricalApply(state, recordedForTest(state, TurnStarted{
		TurnID: TurnID("turn-1"), Input: "inspect repository",
	}))
	if err != nil {
		t.Fatalf("start test turn: %v", err)
	}
	return state
}
