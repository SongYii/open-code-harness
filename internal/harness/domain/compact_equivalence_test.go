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
				full, err := Replay(records[:prefix])
				if err != nil {
					t.Fatalf("Replay() prefix %d error = %v", prefix, err)
				}
				compact, err := ReplayCompact(records[:prefix])
				if err != nil {
					t.Fatalf("ReplayCompact() prefix %d error = %v", prefix, err)
				}
				for _, command := range freshCommandsForPrefix(full, prefix) {
					assertDecisionEquivalent(t, full, compact, command)
				}
			})
		}
	}
}

func TestCompactHistoricalDuplicateRequiresStoreIdentityIndex(t *testing.T) {
	records := fixtureRecords(t, "testdata/assistant_lifecycle.jsonl")
	full, err := Replay(records[:5])
	if err != nil {
		t.Fatal(err)
	}
	compact, err := ReplayCompact(records[:5])
	if err != nil {
		t.Fatal(err)
	}

	fullEvents, fullErr := Decide(full, StartTurn{SessionID: full.ID, TurnID: "turn-1", Input: "duplicate"})
	compactEvents, compactErr := DecideCompact(compact, StartTurn{SessionID: compact.ID, TurnID: "turn-1", Input: "duplicate"})
	if !IsCode(fullErr, CodeTurnAlreadyExists) || fullEvents != nil {
		t.Fatalf("full historical turn duplicate = (%#v, %v), want %q", fullEvents, fullErr, CodeTurnAlreadyExists)
	}
	if compactErr != nil || !reflect.DeepEqual(compactEvents, []UncommittedEvent{{Event: TurnStarted{TurnID: "turn-1", Input: "duplicate"}}}) {
		t.Fatalf("compact historical turn duplicate = (%#v, %v), want admission pending Store identity index", compactEvents, compactErr)
	}

	fullEvents, fullErr = Decide(full, StartAssistantTurn{SessionID: full.ID, TurnID: "turn-new", ItemID: "item-1", Input: "duplicate item"})
	compactEvents, compactErr = DecideCompact(compact, StartAssistantTurn{SessionID: compact.ID, TurnID: "turn-new", ItemID: "item-1", Input: "duplicate item"})
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
	full, err := Replay(base)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := ReplayCompact(base)
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
			_, fullErr := Apply(full, record)
			_, compactErr := ApplyCompact(compact, record)
			if errorCode(fullErr) != CodeInvalidEvent || errorCode(compactErr) != errorCode(fullErr) {
				t.Fatalf("chronology rejection compact = %v, full = %v", compactErr, fullErr)
			}
		})
	}
}

func TestApplyCompactMatchesV1DuplicateSessionCreatedPreflightOrder(t *testing.T) {
	base := compactChronologyRecords()[:1]
	full, err := Replay(base)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := ReplayCompact(base)
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
	_, fullErr := Apply(full, record)
	_, compactErr := ApplyCompact(compact, record)
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
		first, firstErr := ReplayCompact(records)
		second, secondErr := ReplayCompact(records)
		if errorCode(firstErr) != errorCode(secondErr) || !reflect.DeepEqual(first, second) {
			t.Fatalf("non-deterministic replay: (%#v,%v) then (%#v,%v)", first, firstErr, second, secondErr)
		}
	})
}

func assertDecisionEquivalent(t *testing.T, full Session, compact CompactSession, command Command) {
	t.Helper()
	wantEvents, wantErr := Decide(full, command)
	gotEvents, gotErr := DecideCompact(compact, command)
	if errorCode(gotErr) != errorCode(wantErr) || !reflect.DeepEqual(gotEvents, wantEvents) {
		t.Fatalf("command %T compact decision = (%#v,%v), full = (%#v,%v)", command, gotEvents, gotErr, wantEvents, wantErr)
	}
}

func freshCommandsForPrefix(state Session, prefix int) []Command {
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
		CompleteTurn{SessionID: sessionID, TurnID: turnID},
		FailTurn{SessionID: sessionID, TurnID: turnID, Code: "failed", Message: "failed"},
		InterruptTurn{SessionID: sessionID, TurnID: turnID, Reason: "interrupted"},
		StartAssistantMessage{SessionID: sessionID, TurnID: turnID, ItemID: ItemID(fmt.Sprintf("item-fresh-%d", prefix))},
		CompleteAssistantTurn{SessionID: sessionID, TurnID: turnID, ItemID: itemID, Text: "done"},
		FailAssistantTurn{SessionID: sessionID, TurnID: turnID, ItemID: itemID, Code: "failed", Message: "failed"},
		InterruptAssistantTurn{SessionID: sessionID, TurnID: turnID, ItemID: itemID, Code: "interrupted", Message: "interrupted"},
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
