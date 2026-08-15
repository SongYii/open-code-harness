package application

import (
	"fmt"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

// ReconstructRequestResult rebuilds one durable request result from a pinned,
// contiguous Session view. RecordedEvent carries no AppendID, so admission
// linkage is established by the unique CommandID plus adjacent start pair.
//
// The walk is Apply-equivalent (admit_turn → open_assistant → idle_in_turn →
// open_tool → terminal) and does not call Apply: there is no compact Session
// here. Illegal order is store_corrupt. Admission ItemID is only the stable
// RunTurnResult.ItemID; later Steps and tools use new ItemIDs.
func ReconstructRequestResult(record CommandRequestRecord, records []domain.RecordedEvent) (RunTurnResult, error) {
	if err := validateCommandRequestRecord(record); err != nil {
		return RunTurnResult{}, corruptRequestResult(err.Error())
	}
	if err := validateRequestView(record.SessionID, records); err != nil {
		return RunTurnResult{}, corruptRequestResult(err.Error())
	}
	matching := make([]domain.RecordedEvent, 0, 8)
	for _, candidate := range records {
		if candidate.CommandID == record.CommandID {
			matching = append(matching, candidate)
		}
	}
	walk, err := walkRequestCommand(matching, record)
	if err != nil {
		return RunTurnResult{}, corruptRequestResult(err.Error())
	}
	result := RunTurnResult{
		SessionID:         record.SessionID,
		TurnID:            record.TurnID,
		ItemID:            record.ItemID,
		Status:            walk.status,
		Text:              walk.text,
		TerminalCommitted: walk.terminal,
		Records:           matching,
	}
	return cloneRunTurnResult(result), nil
}

type reconstructState int

const (
	reconstructOpenAssistant reconstructState = iota
	reconstructIdleInTurn
	reconstructOpenTool
	reconstructTerminal
)

type requestWalk struct {
	record           CommandRequestRecord
	state            reconstructState
	openItem         domain.ItemID
	seenItems        map[domain.ItemID]struct{}
	sawRequest       bool
	sawUsage         bool
	sawPolicy        bool
	sawApprovalReq   bool
	sawApprovalRes   bool
	stepHadToolCalls bool
	text             string
	status           domain.TurnStatus
	terminal         bool
}

func walkRequestCommand(records []domain.RecordedEvent, record CommandRequestRecord) (requestWalk, error) {
	walk, err := admitRequestCommand(records, record)
	if err != nil {
		return requestWalk{}, err
	}
	for index := 2; index < len(records); {
		if walk.state == reconstructTerminal {
			return requestWalk{}, fmt.Errorf("events after request terminal")
		}
		next, err := walk.step(records, index)
		if err != nil {
			return requestWalk{}, err
		}
		if next <= index {
			return requestWalk{}, fmt.Errorf("request reconstruction did not advance")
		}
		index = next
	}
	switch walk.state {
	case reconstructOpenAssistant, reconstructIdleInTurn, reconstructOpenTool:
		walk.status = domain.TurnStatusRunning
		walk.terminal = false
	case reconstructTerminal:
		walk.terminal = true
	default:
		return requestWalk{}, fmt.Errorf("request walk ended in an illegal state")
	}
	return walk, nil
}

func admitRequestCommand(records []domain.RecordedEvent, record CommandRequestRecord) (requestWalk, error) {
	if len(records) < 2 {
		return requestWalk{}, fmt.Errorf("admission pair is malformed")
	}
	turn, turnOK := records[0].Event.(domain.TurnStarted)
	item, itemOK := records[1].Event.(domain.AssistantMessageStarted)
	if !turnOK || !itemOK || turn.TurnID != record.TurnID || item.TurnID != record.TurnID || item.ItemID != record.ItemID {
		return requestWalk{}, fmt.Errorf("admission pair is malformed")
	}
	digest, err := DigestRunTurnRequestV1(record.SessionID, turn.Input)
	if err != nil || digest != record.RequestDigest {
		return requestWalk{}, fmt.Errorf("admission input digest mismatches record")
	}
	return requestWalk{
		record:    record,
		state:     reconstructOpenAssistant,
		openItem:  item.ItemID,
		seenItems: map[domain.ItemID]struct{}{item.ItemID: {}},
	}, nil
}

func (walk *requestWalk) step(records []domain.RecordedEvent, index int) (int, error) {
	switch walk.state {
	case reconstructOpenAssistant:
		return walk.stepOpenAssistant(records, index)
	case reconstructIdleInTurn:
		return walk.stepIdleInTurn(records, index)
	case reconstructOpenTool:
		return walk.stepOpenTool(records, index)
	default:
		return 0, fmt.Errorf("illegal request reconstruction state")
	}
}

func (walk *requestWalk) stepOpenAssistant(records []domain.RecordedEvent, index int) (int, error) {
	switch event := records[index].Event.(type) {
	case domain.ModelRequestRecorded:
		if err := walk.matchOpenItem(event.TurnID, event.ItemID); err != nil {
			return 0, fmt.Errorf("model request identity does not match open assistant")
		}
		if walk.sawRequest {
			return 0, fmt.Errorf("duplicate model request fact")
		}
		if walk.sawUsage {
			return 0, fmt.Errorf("model request follows usage on the same item")
		}
		walk.sawRequest = true
		return index + 1, nil
	case domain.ModelUsageRecorded:
		if err := walk.matchOpenItem(event.TurnID, event.ItemID); err != nil {
			return 0, fmt.Errorf("model usage identity does not match open assistant")
		}
		if walk.sawUsage {
			return 0, fmt.Errorf("duplicate model usage fact")
		}
		walk.sawUsage = true
		return index + 1, nil
	case domain.AssistantMessageCompleted:
		if err := walk.matchOpenItem(event.TurnID, event.ItemID); err != nil {
			return 0, fmt.Errorf("assistant complete does not match open item")
		}
		walk.stepHadToolCalls = len(event.ToolCalls) > 0
		if !walk.stepHadToolCalls {
			walk.text = event.Text
		}
		walk.closeItem()
		walk.state = reconstructIdleInTurn
		return index + 1, nil
	case domain.AssistantMessageFailed:
		if err := walk.matchOpenItem(event.TurnID, event.ItemID); err != nil {
			return 0, fmt.Errorf("assistant failure does not match open item")
		}
		if !allowedFailureCode(event.Code) {
			return 0, fmt.Errorf("assistant failure code is not allowed")
		}
		if err := walk.consumeFollowingTurnFailed(records, index+1, event.Code, event.Message); err != nil {
			return 0, err
		}
		return index + 2, nil
	case domain.AssistantMessageInterrupted:
		if err := walk.matchOpenItem(event.TurnID, event.ItemID); err != nil {
			return 0, fmt.Errorf("assistant interruption does not match open item")
		}
		if !allowedInterruptionCode(event.Code) {
			return 0, fmt.Errorf("assistant interruption code is not allowed")
		}
		if err := walk.consumeFollowingTurnInterrupted(records, index+1, event.Code); err != nil {
			return 0, err
		}
		return index + 2, nil
	default:
		return 0, fmt.Errorf("illegal event while assistant item is open")
	}
}

func (walk *requestWalk) stepIdleInTurn(records []domain.RecordedEvent, index int) (int, error) {
	switch event := records[index].Event.(type) {
	case domain.ToolCallStarted:
		if !walk.stepHadToolCalls {
			return 0, fmt.Errorf("tool call started without a prior item-only assistant complete")
		}
		if err := walk.openNewItem(event.TurnID, event.ItemID); err != nil {
			return 0, err
		}
		walk.resetToolCompanions()
		walk.state = reconstructOpenTool
		return index + 1, nil
	case domain.AssistantMessageStarted:
		if err := walk.openNewItem(event.TurnID, event.ItemID); err != nil {
			return 0, err
		}
		walk.sawRequest, walk.sawUsage = false, false
		walk.stepHadToolCalls = false
		walk.state = reconstructOpenAssistant
		return index + 1, nil
	case domain.TurnCompleted:
		if event.TurnID != walk.record.TurnID {
			return 0, fmt.Errorf("completed turn is malformed")
		}
		walk.finish(domain.TurnStatusCompleted)
		return index + 1, nil
	case domain.TurnFailed:
		if event.TurnID != walk.record.TurnID || !allowedFailureCode(event.Code) {
			return 0, fmt.Errorf("failed turn does not match message")
		}
		walk.finish(domain.TurnStatusFailed)
		return index + 1, nil
	case domain.TurnInterrupted:
		if event.TurnID != walk.record.TurnID || !allowedInterruptionCode(event.Reason) {
			return 0, fmt.Errorf("interrupted turn does not match message")
		}
		walk.finish(domain.TurnStatusInterrupted)
		return index + 1, nil
	default:
		return 0, fmt.Errorf("illegal event while turn is idle")
	}
}

func (walk *requestWalk) stepOpenTool(records []domain.RecordedEvent, index int) (int, error) {
	switch event := records[index].Event.(type) {
	case domain.PolicyDecisionRecorded:
		if err := walk.matchOpenItem(event.TurnID, event.ItemID); err != nil {
			return 0, fmt.Errorf("policy decision does not match open tool")
		}
		if walk.sawPolicy {
			return 0, fmt.Errorf("duplicate policy decision fact")
		}
		walk.sawPolicy = true
		return index + 1, nil
	case domain.ApprovalRequested:
		if err := walk.matchOpenItem(event.TurnID, event.ItemID); err != nil {
			return 0, fmt.Errorf("approval request does not match open tool")
		}
		if !walk.sawPolicy {
			return 0, fmt.Errorf("approval requested without policy decision")
		}
		if walk.sawApprovalReq {
			return 0, fmt.Errorf("duplicate approval request fact")
		}
		walk.sawApprovalReq = true
		return index + 1, nil
	case domain.ApprovalResolved:
		if err := walk.matchOpenItem(event.TurnID, event.ItemID); err != nil {
			return 0, fmt.Errorf("approval resolution does not match open tool")
		}
		if !walk.sawApprovalReq {
			return 0, fmt.Errorf("approval resolved without request")
		}
		if walk.sawApprovalRes {
			return 0, fmt.Errorf("duplicate approval resolution fact")
		}
		walk.sawApprovalRes = true
		return index + 1, nil
	case domain.ToolCallCompleted, domain.ToolCallFailed:
		if err := walk.matchToolTerminal(event); err != nil {
			return 0, err
		}
		walk.closeItem()
		walk.state = reconstructIdleInTurn
		return index + 1, nil
	case domain.ToolCallInterrupted:
		if err := walk.matchOpenItem(event.TurnID, event.ItemID); err != nil {
			return 0, fmt.Errorf("tool interruption does not match open item")
		}
		if !allowedInterruptionCode(event.Code) {
			return 0, fmt.Errorf("tool interruption code is not allowed")
		}
		if err := walk.consumeFollowingTurnInterrupted(records, index+1, event.Code); err != nil {
			return 0, err
		}
		return index + 2, nil
	default:
		return 0, fmt.Errorf("illegal event while tool item is open")
	}
}

func (walk *requestWalk) matchToolTerminal(event domain.Event) error {
	switch event := event.(type) {
	case domain.ToolCallCompleted:
		return walk.matchOpenItem(event.TurnID, event.ItemID)
	case domain.ToolCallFailed:
		return walk.matchOpenItem(event.TurnID, event.ItemID)
	default:
		return fmt.Errorf("tool terminal is malformed")
	}
}

func (walk *requestWalk) consumeFollowingTurnFailed(records []domain.RecordedEvent, index int, code, message string) error {
	if index >= len(records) {
		return fmt.Errorf("assistant failure is not followed by turn.failed")
	}
	turn, ok := records[index].Event.(domain.TurnFailed)
	if !ok {
		return fmt.Errorf("assistant failure is not followed by turn.failed")
	}
	if err := validateTerminalTurn(turn, walk.record, code); err != nil {
		return err
	}
	if turn.Message != message {
		return fmt.Errorf("failed terminal messages do not match")
	}
	walk.closeItem()
	walk.finish(domain.TurnStatusFailed)
	return nil
}

func (walk *requestWalk) consumeFollowingTurnInterrupted(records []domain.RecordedEvent, index int, code string) error {
	if index >= len(records) {
		return fmt.Errorf("item interruption is not followed by turn.interrupted")
	}
	turn, ok := records[index].Event.(domain.TurnInterrupted)
	if !ok {
		return fmt.Errorf("item interruption is not followed by turn.interrupted")
	}
	if err := validateTerminalTurn(turn, walk.record, code); err != nil {
		return err
	}
	walk.closeItem()
	walk.finish(domain.TurnStatusInterrupted)
	return nil
}

func (walk *requestWalk) matchOpenItem(turnID domain.TurnID, itemID domain.ItemID) error {
	if turnID != walk.record.TurnID || itemID != walk.openItem {
		return fmt.Errorf("event identity does not match open item")
	}
	return nil
}

func (walk *requestWalk) openNewItem(turnID domain.TurnID, itemID domain.ItemID) error {
	if turnID != walk.record.TurnID {
		return fmt.Errorf("item start turn does not match record")
	}
	if _, seen := walk.seenItems[itemID]; seen {
		return fmt.Errorf("item ID reused without a terminal")
	}
	walk.seenItems[itemID] = struct{}{}
	walk.openItem = itemID
	return nil
}

func (walk *requestWalk) closeItem() {
	walk.openItem = ""
	walk.sawRequest = false
	walk.sawUsage = false
	walk.resetToolCompanions()
}

func (walk *requestWalk) resetToolCompanions() {
	walk.sawPolicy = false
	walk.sawApprovalReq = false
	walk.sawApprovalRes = false
}

func (walk *requestWalk) finish(status domain.TurnStatus) {
	walk.state = reconstructTerminal
	walk.status = status
}

func validateCommandRequestRecord(record CommandRequestRecord) error {
	if _, err := domain.ParseRunTurnRequestID(string(record.RunTurnRequestID)); err != nil {
		return err
	}
	if _, err := domain.ParseSessionID(string(record.SessionID)); err != nil {
		return err
	}
	if _, err := domain.ParseCommandID(string(record.CommandID)); err != nil {
		return err
	}
	if _, err := domain.ParseTurnID(string(record.TurnID)); err != nil {
		return err
	}
	if _, err := domain.ParseItemID(string(record.ItemID)); err != nil {
		return err
	}
	if _, err := domain.ParseAppendID(string(record.AdmissionAppendID)); err != nil {
		return err
	}
	if record.RequestDigest == (Digest{}) {
		return fmt.Errorf("request digest is zero")
	}
	return nil
}

func validateRequestView(sessionID domain.SessionID, records []domain.RecordedEvent) error {
	var previous uint64
	for index, record := range records {
		if record.SessionID != sessionID || (index > 0 && record.Sequence != previous+1) || (index == 0 && record.Sequence != 1) {
			return fmt.Errorf("records are not one contiguous session view")
		}
		if _, err := domain.MarshalRecordedEvent(record); err != nil {
			return err
		}
		previous = record.Sequence
	}
	return nil
}

func validateTerminalTurn(event domain.Event, record CommandRequestRecord, code string) error {
	switch turn := event.(type) {
	case domain.TurnCompleted:
		if code != "" || turn.TurnID != record.TurnID {
			return fmt.Errorf("completed turn is malformed")
		}
	case domain.TurnFailed:
		if turn.TurnID != record.TurnID || turn.Code != code || !allowedFailureCode(turn.Code) {
			return fmt.Errorf("failed turn does not match message")
		}
	case domain.TurnInterrupted:
		if turn.TurnID != record.TurnID || turn.Reason != code || !allowedInterruptionCode(turn.Reason) {
			return fmt.Errorf("interrupted turn does not match message")
		}
	default:
		return fmt.Errorf("terminal turn event missing")
	}
	return nil
}

func allowedFailureCode(code string) bool {
	switch code {
	case string(engine.CodeModelStartup), string(engine.CodeModelStream), string(engine.CodeOutputLimit), string(engine.CodeInvalidStream), "model_failure",
		"provider_auth", "provider_quota", "provider_rate_limit", "provider_transient", "provider_permanent",
		"capability_mismatch", "context_overflow", "empty_response",
		CodeStepLimit, CodeEnvelopeLimit:
		return true
	default:
		return false
	}
}

func allowedInterruptionCode(code string) bool {
	switch code {
	case domain.InterruptionCallerCanceled, domain.InterruptionDeliveryFailed, domain.InterruptionRequestAbandoned:
		return true
	default:
		return false
	}
}

// requestOutcomeEvent prefers the turn terminal so cancel-during-tool and
// idle_in_turn failures classify without an assistant item terminal.
func requestOutcomeEvent(records []domain.RecordedEvent) domain.Event {
	for index := len(records) - 1; index >= 0; index-- {
		switch event := records[index].Event.(type) {
		case domain.TurnCompleted, domain.TurnFailed, domain.TurnInterrupted:
			return event
		}
	}
	return itemTerminalEvent(records)
}

func corruptRequestResult(message string) error {
	err, newErr := NewStoreError(StoreError{Code: StoreCodeCorrupt, Cause: fmt.Errorf("%s", message)})
	if newErr != nil {
		return fmt.Errorf("store corrupt: %s", message)
	}
	return err
}
