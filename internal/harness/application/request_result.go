package application

import (
	"fmt"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

// ReconstructRequestResult rebuilds one durable request result from a pinned,
// contiguous Session view. RecordedEvent carries no AppendID, so admission
// linkage is established by the unique CommandID plus adjacent start pair.
func ReconstructRequestResult(record CommandRequestRecord, records []domain.RecordedEvent) (RunTurnResult, error) {
	if err := validateCommandRequestRecord(record); err != nil {
		return RunTurnResult{}, corruptRequestResult(err.Error())
	}
	if err := validateRequestView(record.SessionID, records); err != nil {
		return RunTurnResult{}, corruptRequestResult(err.Error())
	}
	matching := make([]domain.RecordedEvent, 0, 6)
	for _, candidate := range records {
		if candidate.CommandID == record.CommandID {
			matching = append(matching, candidate)
		}
	}
	if err := validateRequestCompanions(matching, record); err != nil {
		return RunTurnResult{}, corruptRequestResult(err.Error())
	}
	shape, ok := requestResultShape(matching)
	if !ok {
		return RunTurnResult{}, corruptRequestResult("relevant records do not match an admitted request shape")
	}
	start, itemStart := matching[0], matching[1]
	turn, turnOK := start.Event.(domain.TurnStarted)
	item, itemOK := itemStart.Event.(domain.AssistantMessageStarted)
	if !turnOK || !itemOK || turn.TurnID != record.TurnID || item.TurnID != record.TurnID || item.ItemID != record.ItemID {
		return RunTurnResult{}, corruptRequestResult("admission pair is malformed")
	}
	digest, err := DigestRunTurnRequestV1(record.SessionID, turn.Input)
	if err != nil || digest != record.RequestDigest {
		return RunTurnResult{}, corruptRequestResult("admission input digest mismatches record")
	}
	result := RunTurnResult{SessionID: record.SessionID, TurnID: record.TurnID, ItemID: record.ItemID, Status: domain.TurnStatusRunning, Records: matching}
	if !shape.terminal {
		return cloneRunTurnResult(result), nil
	}
	terminalRecord, turnRecord := matching[shape.itemTerminal], matching[shape.turnTerminal]
	terminal, ok, status, text, stableCode := requestTerminal(terminalRecord.Event, record)
	if !ok {
		return RunTurnResult{}, corruptRequestResult("terminal pair is malformed")
	}
	if err := validateTerminalTurn(turnRecord.Event, record, stableCode); err != nil {
		return RunTurnResult{}, corruptRequestResult(err.Error())
	}
	if message, ok := terminal.(domain.AssistantMessageFailed); ok {
		if turnRecord.Event.(domain.TurnFailed).Message != message.Message {
			return RunTurnResult{}, corruptRequestResult("failed terminal messages do not match")
		}
	}
	result.Status, result.Text, result.TerminalCommitted = status, text, true
	return cloneRunTurnResult(result), nil
}

type requestShape struct {
	terminal     bool
	itemTerminal int
	turnTerminal int
}

func requestResultShape(records []domain.RecordedEvent) (requestShape, bool) {
	if len(records) < 2 {
		return requestShape{}, false
	}
	if _, ok := records[0].Event.(domain.TurnStarted); !ok {
		return requestShape{}, false
	}
	if _, ok := records[1].Event.(domain.AssistantMessageStarted); !ok {
		return requestShape{}, false
	}
	switch len(records) {
	case 2:
		return requestShape{}, true
	case 3:
		_, ok := records[2].Event.(domain.ModelRequestRecorded)
		return requestShape{}, ok
	case 4:
		if !isRequestItemTerminal(records[2].Event) || !isRequestTurnTerminal(records[3].Event) {
			return requestShape{}, false
		}
		return requestShape{terminal: true, itemTerminal: 2, turnTerminal: 3}, true
	case 5:
		if _, ok := records[2].Event.(domain.ModelRequestRecorded); !ok {
			return requestShape{}, false
		}
		if !isRequestItemTerminal(records[3].Event) || !isRequestTurnTerminal(records[4].Event) {
			return requestShape{}, false
		}
		return requestShape{terminal: true, itemTerminal: 3, turnTerminal: 4}, true
	case 6:
		if _, ok := records[2].Event.(domain.ModelRequestRecorded); !ok {
			return requestShape{}, false
		}
		if _, ok := records[3].Event.(domain.ModelUsageRecorded); !ok {
			return requestShape{}, false
		}
		if !isRequestItemTerminal(records[4].Event) || !isRequestTurnTerminal(records[5].Event) {
			return requestShape{}, false
		}
		return requestShape{terminal: true, itemTerminal: 4, turnTerminal: 5}, true
	default:
		return requestShape{}, false
	}
}

func isRequestItemTerminal(event domain.Event) bool {
	switch event.(type) {
	case domain.AssistantMessageCompleted, domain.AssistantMessageFailed, domain.AssistantMessageInterrupted:
		return true
	default:
		return false
	}
}

func isRequestTurnTerminal(event domain.Event) bool {
	switch event.(type) {
	case domain.TurnCompleted, domain.TurnFailed, domain.TurnInterrupted:
		return true
	default:
		return false
	}
}

func validateRequestCompanions(records []domain.RecordedEvent, record CommandRequestRecord) error {
	var seenRequest, seenUsage bool
	for _, candidate := range records {
		switch event := candidate.Event.(type) {
		case domain.TurnStarted, domain.TurnCompleted, domain.TurnFailed, domain.TurnInterrupted,
			domain.AssistantMessageStarted, domain.AssistantMessageCompleted, domain.AssistantMessageFailed, domain.AssistantMessageInterrupted:
		case domain.ModelRequestRecorded:
			if seenRequest {
				return fmt.Errorf("duplicate model request fact")
			}
			if event.TurnID != record.TurnID || event.ItemID != record.ItemID {
				return fmt.Errorf("model request identity does not match record")
			}
			seenRequest = true
		case domain.ModelUsageRecorded:
			if seenUsage {
				return fmt.Errorf("duplicate model usage fact")
			}
			if event.TurnID != record.TurnID || event.ItemID != record.ItemID {
				return fmt.Errorf("model usage identity does not match record")
			}
			seenUsage = true
		default:
			return fmt.Errorf("unknown same-command event type")
		}
	}
	return nil
}

func referencesRequestIdentity(event domain.Event, record CommandRequestRecord) bool {
	switch value := event.(type) {
	case domain.TurnStarted:
		return value.TurnID == record.TurnID
	case domain.TurnCompleted:
		return value.TurnID == record.TurnID
	case domain.TurnFailed:
		return value.TurnID == record.TurnID
	case domain.TurnInterrupted:
		return value.TurnID == record.TurnID
	case domain.AssistantMessageStarted:
		return value.TurnID == record.TurnID || value.ItemID == record.ItemID
	case domain.AssistantMessageCompleted:
		return value.TurnID == record.TurnID || value.ItemID == record.ItemID
	case domain.AssistantMessageFailed:
		return value.TurnID == record.TurnID || value.ItemID == record.ItemID
	case domain.AssistantMessageInterrupted:
		return value.TurnID == record.TurnID || value.ItemID == record.ItemID
	default:
		return false
	}
}

func relevantRequestTerminal(event domain.Event, record CommandRequestRecord) bool {
	switch terminal := event.(type) {
	case domain.AssistantMessageCompleted:
		return terminal.TurnID == record.TurnID || terminal.ItemID == record.ItemID
	case domain.AssistantMessageFailed:
		return terminal.TurnID == record.TurnID || terminal.ItemID == record.ItemID
	case domain.AssistantMessageInterrupted:
		return terminal.TurnID == record.TurnID || terminal.ItemID == record.ItemID
	default:
		return false
	}
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

func requestTerminal(event domain.Event, record CommandRequestRecord) (domain.Event, bool, domain.TurnStatus, string, string) {
	switch terminal := event.(type) {
	case domain.AssistantMessageCompleted:
		if terminal.TurnID == record.TurnID && terminal.ItemID == record.ItemID {
			return terminal, true, domain.TurnStatusCompleted, terminal.Text, ""
		}
	case domain.AssistantMessageFailed:
		if terminal.TurnID == record.TurnID && terminal.ItemID == record.ItemID && allowedFailureCode(terminal.Code) {
			return terminal, true, domain.TurnStatusFailed, "", terminal.Code
		}
	case domain.AssistantMessageInterrupted:
		if terminal.TurnID == record.TurnID && terminal.ItemID == record.ItemID && allowedInterruptionCode(terminal.Code) {
			return terminal, true, domain.TurnStatusInterrupted, "", terminal.Code
		}
	}
	return nil, false, "", "", ""
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
	case string(engine.CodeModelStartup), string(engine.CodeModelStream), string(engine.CodeOutputLimit), string(engine.CodeInvalidStream), "model_failure":
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

func corruptRequestResult(message string) error {
	err, newErr := NewStoreError(StoreError{Code: StoreCodeCorrupt, Cause: fmt.Errorf("%s", message)})
	if newErr != nil {
		return fmt.Errorf("store corrupt: %s", message)
	}
	return err
}
