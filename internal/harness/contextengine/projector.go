package contextengine

import (
	"errors"
	"fmt"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// ContextUnitKind classifies one ContextUnit (design §7.1). TurnUnit,
// AssistantUnit, and StepUnit are represented by this Kind rather than as
// separate Go types, since all three share the fields the planner (Task 3)
// needs; CurrentInputUnit is not produced by ProjectSourceEvents at all —
// it is the not-yet-committed incoming input the Application orchestrator
// (Task 9) supplies directly at plan time, never a historical source event.
type ContextUnitKind string

const (
	// UnitKindTurn is one TurnStarted's own user message, standalone.
	UnitKindTurn ContextUnitKind = "turn"
	// UnitKindAssistant is one AssistantMessageCompleted with no Tool
	// Calls, standalone.
	UnitKindAssistant ContextUnitKind = "assistant"
	// UnitKindStep is one AssistantMessageCompleted that offered one or
	// more Tool Calls, plus every one of those calls' terminal results —
	// design §7.1's StepUnit. A StepUnit is emitted only once it is
	// Balanced.
	UnitKindStep ContextUnitKind = "step"
)

// ContextUnit is one design §7.1 "ContextUnit": the smallest boundary the
// planner (Task 3) may retain or cover.
type ContextUnit struct {
	Kind ContextUnitKind
	// TurnID is the unit's owning Turn.
	TurnID domain.TurnID
	// FirstSequence and LastSequence bound the unit's own source events
	// (record.Sequence values), inclusive, for boundary/coverage bookkeeping.
	FirstSequence uint64
	LastSequence  uint64
	// Messages is the unit's projected content, in a fixed deterministic
	// order: for UnitKindStep, the assistant message first, then each
	// offered Tool Call's terminal result in the order the calls were
	// offered — never arrival order, which is a race/network artifact a
	// deterministic projection must not depend on.
	Messages []domain.ModelPromptMessage
}

// ErrProjectionInvalid reports a source event sequence that cannot form a
// valid tool-pair-balanced projection: a Call ID offered twice, a terminal
// result (ToolCallStarted/Completed/Failed/Interrupted) naming a Call ID
// that was never offered, or a second terminal result for one Call ID
// (design §7.1: "An incomplete historical tool pair is a store/domain
// contract violation"). The Application layer maps this to
// context_projection_invalid (design §16).
var ErrProjectionInvalid = errors.New("contextengine: source event sequence is not tool-pair-balanced")

type openStep struct {
	turnID          domain.TurnID
	firstSequence   uint64
	assistant       domain.ModelPromptMessage
	offeredOrder    []string
	offeredNames    map[string]string
	terminalResults map[string]domain.ModelPromptMessage
	lastSequence    uint64
}

func (step *openStep) balanced() bool {
	return len(step.terminalResults) == len(step.offeredOrder)
}

func (step *openStep) toUnit() ContextUnit {
	messages := make([]domain.ModelPromptMessage, 0, len(step.offeredOrder)+1)
	messages = append(messages, step.assistant)
	for _, callID := range step.offeredOrder {
		messages = append(messages, step.terminalResults[callID])
	}
	return ContextUnit{
		Kind:          UnitKindStep,
		TurnID:        step.turnID,
		FirstSequence: step.firstSequence,
		LastSequence:  step.lastSequence,
		Messages:      messages,
	}
}

// ProjectSourceEvents folds records — an unfiltered slice; this function
// applies the IsSourceEvent filter itself — into an ordered []ContextUnit
// per the projection grammar (design §9.1): TurnStarted becomes a
// UnitKindTurn; AssistantMessageCompleted with no Tool Calls becomes a
// UnitKindAssistant; AssistantMessageCompleted offering one or more Tool
// Calls opens a UnitKindStep that ToolCallCompleted/Failed/Interrupted
// close, in whatever order their terminal results actually arrive — a
// Step is emitted, in offered-call order, only once every one of its
// offered Call IDs has exactly one terminal result. ToolCallStarted
// contributes no message; it exists in the grammar only to validate that
// every Call ID it names was actually offered (an unknown Call ID here
// fails closed).
//
// Matching this project's existing (application.projectPriorTurns)
// behavior exactly: a TurnStarted with empty Input, and an
// AssistantMessageCompleted with neither Text nor Tool Calls, are both
// skipped rather than emitted as an empty unit.
//
// ProjectSourceEvents returns ErrProjectionInvalid, never a panic and
// never a silently dropped event, for a duplicate Call ID offer, a
// ToolCallStarted/Completed/Failed/Interrupted naming a Call ID that was
// never offered, or a second terminal result for one Call ID.
func ProjectSourceEvents(records []domain.RecordedEvent) ([]ContextUnit, error) {
	var units []ContextUnit
	offeredCallIDs := make(map[string]bool)
	openSteps := make(map[string]*openStep) // keyed by CallID; every CallID of one Step points to the same *openStep

	for _, record := range records {
		switch event := record.Event.(type) {
		case domain.TurnStarted:
			if event.Input == "" {
				continue
			}
			units = append(units, ContextUnit{
				Kind:          UnitKindTurn,
				TurnID:        event.TurnID,
				FirstSequence: record.Sequence,
				LastSequence:  record.Sequence,
				Messages:      []domain.ModelPromptMessage{{Role: domain.PromptRoleUser, Text: event.Input}},
			})

		case domain.AssistantMessageCompleted:
			if len(event.ToolCalls) == 0 {
				if event.Text == "" {
					continue
				}
				units = append(units, ContextUnit{
					Kind:          UnitKindAssistant,
					TurnID:        event.TurnID,
					FirstSequence: record.Sequence,
					LastSequence:  record.Sequence,
					Messages: []domain.ModelPromptMessage{{
						Role:      domain.PromptRoleAssistant,
						Text:      event.Text,
						ToolCalls: cloneToolCallOffersForProjector(event.ToolCalls),
					}},
				})
				continue
			}
			step := &openStep{
				turnID:        event.TurnID,
				firstSequence: record.Sequence,
				lastSequence:  record.Sequence,
				assistant: domain.ModelPromptMessage{
					Role:      domain.PromptRoleAssistant,
					Text:      event.Text,
					ToolCalls: cloneToolCallOffersForProjector(event.ToolCalls),
				},
				offeredNames:    make(map[string]string, len(event.ToolCalls)),
				terminalResults: make(map[string]domain.ModelPromptMessage, len(event.ToolCalls)),
			}
			for _, call := range event.ToolCalls {
				if offeredCallIDs[call.ID] {
					return nil, fmt.Errorf("%w: Call ID %q offered more than once", ErrProjectionInvalid, call.ID)
				}
				offeredCallIDs[call.ID] = true
				step.offeredOrder = append(step.offeredOrder, call.ID)
				step.offeredNames[call.ID] = call.Name
				openSteps[call.ID] = step
			}

		case domain.ToolCallStarted:
			step, ok := openSteps[event.CallID]
			if !ok {
				return nil, fmt.Errorf("%w: ToolCallStarted names unknown Call ID %q", ErrProjectionInvalid, event.CallID)
			}
			if record.Sequence > step.lastSequence {
				step.lastSequence = record.Sequence
			}

		case domain.ToolCallCompleted:
			if err := closeToolCall(openSteps, offeredCallIDs, &units, event.CallID, record.Sequence,
				domain.ModelPromptMessage{Role: domain.PromptRoleTool, Text: event.Content, ToolCallID: event.CallID}); err != nil {
				return nil, err
			}

		case domain.ToolCallFailed:
			if err := closeToolCall(openSteps, offeredCallIDs, &units, event.CallID, record.Sequence,
				domain.ModelPromptMessage{Role: domain.PromptRoleTool, Text: event.Message, ToolCallID: event.CallID}); err != nil {
				return nil, err
			}

		case domain.ToolCallInterrupted:
			if err := closeToolCall(openSteps, offeredCallIDs, &units, event.CallID, record.Sequence,
				domain.ModelPromptMessage{Role: domain.PromptRoleTool, Text: event.Message, ToolCallID: event.CallID}); err != nil {
				return nil, err
			}
		}
	}

	return units, nil
}

// closeToolCall records one Call ID's terminal result and, once its owning
// Step is fully balanced, appends the Step's ContextUnit to *units and
// removes every one of that Step's Call IDs from openSteps so a later
// duplicate terminal for the same Call ID is detected as "unknown" (its
// entry no longer exists) rather than silently overwriting the first.
func closeToolCall(openSteps map[string]*openStep, offeredCallIDs map[string]bool, units *[]ContextUnit, callID string, sequence uint64, message domain.ModelPromptMessage) error {
	step, ok := openSteps[callID]
	if !ok {
		if offeredCallIDs[callID] {
			return fmt.Errorf("%w: Call ID %q already has a terminal result", ErrProjectionInvalid, callID)
		}
		return fmt.Errorf("%w: terminal result names unknown Call ID %q", ErrProjectionInvalid, callID)
	}
	message.Name = step.offeredNames[callID]
	step.terminalResults[callID] = message
	if sequence > step.lastSequence {
		step.lastSequence = sequence
	}
	delete(openSteps, callID)
	if step.balanced() {
		*units = append(*units, step.toUnit())
	}
	return nil
}

func cloneToolCallOffersForProjector(offers []domain.ToolCallOffer) []domain.ToolCallOffer {
	if len(offers) == 0 {
		return nil
	}
	cloned := make([]domain.ToolCallOffer, len(offers))
	copy(cloned, offers)
	return cloned
}
