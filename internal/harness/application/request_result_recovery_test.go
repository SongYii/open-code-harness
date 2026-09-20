package application

import (
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestReconstructRecoveredCompaction(t *testing.T) {
	for _, idle := range []bool{false, true} {
		name := "open_assistant"
		if idle {
			name = "idle_after_tool"
		}
		t.Run(name, func(t *testing.T) {
			events := []domain.Event{domain.TurnStarted{TurnID: "turn-1", Input: "input"}, domain.AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}}
			if idle {
				events = append(events, domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", ToolCalls: []domain.ToolCallOffer{{ID: "call-1", Name: "read_file", Arguments: `{"path":"x"}`}}}, domain.ToolCallStarted{TurnID: "turn-1", ItemID: "tool-1", CallID: "call-1", Name: "read_file", Arguments: `{"path":"x"}`, StepIndex: 1}, domain.ToolCallCompleted{TurnID: "turn-1", ItemID: "tool-1", CallID: "call-1", Content: "x"})
			}
			startIndex := len(events) + 1 // Recorded view begins with SessionCreated.
			events = append(events, domain.ContextCompactionStarted{ID: "compaction-1", Trigger: domain.ContextTriggerMidTurn, Strategy: domain.ContextStrategySummary, SourceSchema: "och_source_v1", MeterID: "fixture"}, domain.ContextCompactionFailed{ID: "compaction-1", Code: "runtime_recovered", Message: "interrupted compaction"})
			if !idle {
				events = append(events, domain.AssistantMessageInterrupted{TurnID: "turn-1", ItemID: "item-1", Code: "process_crash"})
			}
			events = append(events, domain.TurnInterrupted{TurnID: "turn-1", Reason: "process_crash"})
			record, records := requestViewFromEvents(t, events)
			records[startIndex].CommandID = "compaction-command"
			result, err := ReconstructRequestResult(record, records)
			if err != nil || result.Status != domain.TurnStatusInterrupted || !result.TerminalCommitted {
				t.Fatalf("recovered compaction request: %v", err)
			}
			// The cross-command start is not projected into the request result,
			// but the recovery batch's context failure must remain in evidence.
			found := false
			for _, r := range result.Records {
				if _, ok := r.Event.(domain.ContextCompactionFailed); ok {
					found = true
				}
			}
			if !found {
				t.Fatal("request projection discarded recovery evidence")
			}
			for _, mutation := range []string{"foreign_start", "foreign_terminal", "wrong_code", "no_crash"} {
				t.Run(mutation, func(t *testing.T) {
					bad := append([]domain.RecordedEvent(nil), records...)
					switch mutation {
					case "foreign_start":
						e := bad[startIndex].Event.(domain.ContextCompactionStarted)
						e.ID = "foreign"
						bad[startIndex].Event = e
					case "foreign_terminal":
						e := bad[startIndex+1].Event.(domain.ContextCompactionFailed)
						e.ID = "foreign"
						bad[startIndex+1].Event = e
					case "wrong_code":
						e := bad[startIndex+1].Event.(domain.ContextCompactionFailed)
						e.Code = CodeContextSummaryFailed
						bad[startIndex+1].Event = e
					case "no_crash":
						for i := startIndex + 2; i < len(bad); i++ {
							switch e := bad[i].Event.(type) {
							case domain.AssistantMessageInterrupted:
								e.Code = domain.InterruptionCallerCanceled
								bad[i].Event = e
							case domain.TurnInterrupted:
								e.Reason = domain.InterruptionCallerCanceled
								bad[i].Event = e
							}
						}
					}
					for _, r := range bad {
						if _, err := domain.MarshalRecordedEvent(r); err != nil {
							t.Fatalf("invalid mutation fixture: %v", err)
						}
					}
					if _, err := ReconstructRequestResult(record, bad); !IsStoreCode(err, StoreCodeCorrupt) {
						t.Fatalf("invalid cross-command recovery accepted: %v", err)
					}
				})
			}
		})
	}
}

func TestReconstructRejectsInvalidOverflowAttempt(t *testing.T) {
	p1 := domain.ContextPreparedRecorded{TurnID: "turn-1", ItemID: "item-1", AttemptIndex: 1, ContextDecisionID: "decision-1", Trigger: domain.ContextTriggerPreTurn}
	p2 := p1
	p2.AttemptIndex = 2
	p2.ContextDecisionID = "decision-2"
	p2.Trigger = domain.ContextTriggerOverflowRetry
	r1 := modelRequestRecordedForTest("item-1", []domain.ModelPromptMessage{{Role: "user", Text: "input"}})
	r1.ContextDecisionID = p1.ContextDecisionID
	r1.AttemptIndex = 1
	for _, name := range []string{"valid", "same_attempt", "skipped_attempt", "reused_decision", "wrong_trigger", "after_usage", "no_prior_request"} {
		t.Run(name, func(t *testing.T) {
			retry := p2
			switch name {
			case "same_attempt":
				retry.AttemptIndex = 1
			case "skipped_attempt":
				retry.AttemptIndex = 3
			case "reused_decision":
				retry.ContextDecisionID = p1.ContextDecisionID
			case "wrong_trigger":
				retry.Trigger = domain.ContextTriggerPreTurn
			}
			events := []domain.Event{domain.TurnStarted{TurnID: "turn-1", Input: "input"}, domain.AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}, p1}
			if name != "no_prior_request" {
				events = append(events, r1)
			}
			if name == "after_usage" {
				events = append(events, domain.ModelUsageRecorded{TurnID: "turn-1", ItemID: "item-1", InputTokens: 1})
			}
			r2 := r1
			r2.ContextDecisionID = retry.ContextDecisionID
			r2.AttemptIndex = retry.AttemptIndex
			events = append(events, retry, r2, domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "done"}, domain.TurnCompleted{TurnID: "turn-1"})
			record, records := requestViewFromEvents(t, events)
			if _, err := domain.Replay(records); err != nil {
				t.Fatalf("invalid lifecycle fixture: %v", err)
			}
			result, err := ReconstructRequestResult(record, records)
			if name == "valid" {
				if err != nil || result.Status != domain.TurnStatusCompleted {
					t.Fatalf("valid attempt rejected: %v", err)
				}
			} else if !IsStoreCode(err, StoreCodeCorrupt) {
				t.Fatalf("invalid attempt accepted: %v", err)
			}
		})
	}
}
