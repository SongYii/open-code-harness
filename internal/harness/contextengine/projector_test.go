package contextengine

import (
	"errors"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestProjectSourceEventsCompleteTurn(t *testing.T) {
	records := []domain.RecordedEvent{
		record(1, domain.TurnStarted{TurnID: "t1", Input: "hello"}),
		record(2, domain.AssistantMessageCompleted{TurnID: "t1", ItemID: "item1", Text: "hi there"}),
		record(3, domain.TurnCompleted{TurnID: "t1"}), // not source, must not itself add or block a unit
	}
	units, err := ProjectSourceEvents(records)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 {
		t.Fatalf("got %d units, want 2: %+v", len(units), units)
	}
	if units[0].Kind != UnitKindTurn || units[0].Messages[0].Text != "hello" {
		t.Fatalf("unit[0] = %+v, want a turn unit with text 'hello'", units[0])
	}
	if units[1].Kind != UnitKindAssistant || units[1].Messages[0].Text != "hi there" {
		t.Fatalf("unit[1] = %+v, want an assistant unit with text 'hi there'", units[1])
	}
}

func TestProjectSourceEventsFailedAndInterruptedTurns(t *testing.T) {
	records := []domain.RecordedEvent{
		record(1, domain.TurnStarted{TurnID: "t1", Input: "do something"}),
		record(2, domain.AssistantMessageCompleted{TurnID: "t1", ItemID: "item1", ToolCalls: []domain.ToolCallOffer{{ID: "c1", Name: "read_file", Arguments: `{}`}}}),
		record(3, domain.ToolCallStarted{TurnID: "t1", ItemID: "item2", CallID: "c1", Name: "read_file", StepIndex: 1}),
		record(4, domain.ToolCallFailed{TurnID: "t1", ItemID: "item2", CallID: "c1", Code: "exec_timeout", Message: "command timed out"}),
		record(5, domain.TurnFailed{TurnID: "t1"}), // not source
	}
	units, err := ProjectSourceEvents(records)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 {
		t.Fatalf("got %d units, want 2 (turn + step): %+v", len(units), units)
	}
	step := units[1]
	if step.Kind != UnitKindStep || len(step.Messages) != 2 {
		t.Fatalf("step unit = %+v, want a 2-message step unit", step)
	}
	if step.Messages[1].Text != "command timed out" || step.Messages[1].ToolCallID != "c1" || step.Messages[1].Name != "read_file" {
		t.Fatalf("tool result message = %+v", step.Messages[1])
	}

	interrupted := []domain.RecordedEvent{
		record(1, domain.TurnStarted{TurnID: "t2", Input: "do something else"}),
		record(2, domain.AssistantMessageCompleted{TurnID: "t2", ItemID: "item3", ToolCalls: []domain.ToolCallOffer{{ID: "c2", Name: "exec", Arguments: `{}`}}}),
		record(3, domain.ToolCallStarted{TurnID: "t2", ItemID: "item4", CallID: "c2", Name: "exec", StepIndex: 1}),
		record(4, domain.ToolCallInterrupted{TurnID: "t2", ItemID: "item4", CallID: "c2", Code: "caller_canceled", Message: "canceled"}),
		record(5, domain.TurnInterrupted{TurnID: "t2"}), // not source
	}
	units2, err := ProjectSourceEvents(interrupted)
	if err != nil {
		t.Fatal(err)
	}
	if len(units2) != 2 || units2[1].Messages[1].Text != "canceled" {
		t.Fatalf("got %+v, want a step unit whose tool result text is 'canceled'", units2)
	}
}

// TestProjectSourceEventsInterleavedMultiCallStep is the mutation-check
// counterpart for the "tool-pair boundary" mutation-kill target (design
// §22.4): two open calls in one Step, terminal results arriving out of
// call-offer order, must still resolve to exactly one balanced Step unit
// whose Messages are in offered order, not arrival order.
func TestProjectSourceEventsInterleavedMultiCallStep(t *testing.T) {
	records := []domain.RecordedEvent{
		record(1, domain.TurnStarted{TurnID: "t1", Input: "do two things"}),
		record(2, domain.AssistantMessageCompleted{TurnID: "t1", ItemID: "item1", ToolCalls: []domain.ToolCallOffer{
			{ID: "c1", Name: "read_file", Arguments: `{}`},
			{ID: "c2", Name: "list_dir", Arguments: `{}`},
		}}),
		record(3, domain.ToolCallStarted{TurnID: "t1", ItemID: "item2", CallID: "c1", Name: "read_file", StepIndex: 1}),
		record(4, domain.ToolCallStarted{TurnID: "t1", ItemID: "item3", CallID: "c2", Name: "list_dir", StepIndex: 1}),
		// c2's terminal result arrives before c1's, out of offered order.
		record(5, domain.ToolCallCompleted{TurnID: "t1", ItemID: "item3", CallID: "c2", Content: "dir listing"}),
		record(6, domain.ToolCallCompleted{TurnID: "t1", ItemID: "item2", CallID: "c1", Content: "file contents"}),
	}
	units, err := ProjectSourceEvents(records)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 {
		t.Fatalf("got %d units, want 2 (turn + one balanced step): %+v", len(units), units)
	}
	step := units[1]
	if step.Kind != UnitKindStep || len(step.Messages) != 3 {
		t.Fatalf("step = %+v, want one assistant message plus two tool results", step)
	}
	// Offered order was c1 then c2, so the tool results must appear in
	// that order regardless of arrival order.
	if step.Messages[1].ToolCallID != "c1" || step.Messages[1].Text != "file contents" {
		t.Fatalf("Messages[1] = %+v, want c1's result first (offered order)", step.Messages[1])
	}
	if step.Messages[2].ToolCallID != "c2" || step.Messages[2].Text != "dir listing" {
		t.Fatalf("Messages[2] = %+v, want c2's result second (offered order)", step.Messages[2])
	}
}

func TestProjectSourceEventsFailsClosedOnDuplicateOffer(t *testing.T) {
	records := []domain.RecordedEvent{
		record(1, domain.AssistantMessageCompleted{TurnID: "t1", ItemID: "item1", ToolCalls: []domain.ToolCallOffer{{ID: "c1", Name: "read_file", Arguments: `{}`}}}),
		record(2, domain.AssistantMessageCompleted{TurnID: "t1", ItemID: "item2", ToolCalls: []domain.ToolCallOffer{{ID: "c1", Name: "read_file", Arguments: `{}`}}}),
	}
	_, err := ProjectSourceEvents(records)
	if !errors.Is(err, ErrProjectionInvalid) {
		t.Fatalf("got %v, want ErrProjectionInvalid", err)
	}
}

func TestProjectSourceEventsFailsClosedOnUnknownCallID(t *testing.T) {
	tests := []struct {
		name    string
		records []domain.RecordedEvent
	}{
		{
			name: "ToolCallStarted names an unoffered Call ID",
			records: []domain.RecordedEvent{
				record(1, domain.ToolCallStarted{TurnID: "t1", ItemID: "item1", CallID: "ghost", Name: "read_file", StepIndex: 1}),
			},
		},
		{
			name: "ToolCallCompleted names an unoffered Call ID",
			records: []domain.RecordedEvent{
				record(1, domain.ToolCallCompleted{TurnID: "t1", ItemID: "item1", CallID: "ghost", Content: "x"}),
			},
		},
		{
			name: "ToolCallFailed names an unoffered Call ID",
			records: []domain.RecordedEvent{
				record(1, domain.ToolCallFailed{TurnID: "t1", ItemID: "item1", CallID: "ghost", Code: "x", Message: "x"}),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ProjectSourceEvents(test.records)
			if !errors.Is(err, ErrProjectionInvalid) {
				t.Fatalf("got %v, want ErrProjectionInvalid", err)
			}
		})
	}
}

func TestProjectSourceEventsFailsClosedOnDuplicateTerminal(t *testing.T) {
	records := []domain.RecordedEvent{
		record(1, domain.AssistantMessageCompleted{TurnID: "t1", ItemID: "item1", ToolCalls: []domain.ToolCallOffer{{ID: "c1", Name: "read_file", Arguments: `{}`}}}),
		record(2, domain.ToolCallCompleted{TurnID: "t1", ItemID: "item2", CallID: "c1", Content: "first"}),
		record(3, domain.ToolCallCompleted{TurnID: "t1", ItemID: "item2", CallID: "c1", Content: "second"}),
	}
	_, err := ProjectSourceEvents(records)
	if !errors.Is(err, ErrProjectionInvalid) {
		t.Fatalf("got %v, want ErrProjectionInvalid", err)
	}
}

func TestProjectSourceEventsNeverPanics(t *testing.T) {
	// A defensive smoke test: every fail-closed path above returns an
	// error rather than panicking, checked here by construction (each
	// case above already ran to completion without a panic under `go
	// test`, which would otherwise report it) plus one more adversarial
	// case: a terminal result with no preceding offer at all.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ProjectSourceEvents panicked: %v", r)
		}
	}()
	_, _ = ProjectSourceEvents([]domain.RecordedEvent{
		record(1, domain.ToolCallInterrupted{TurnID: "t1", ItemID: "item1", CallID: "never-offered", Code: "x", Message: "x"}),
	})
}
