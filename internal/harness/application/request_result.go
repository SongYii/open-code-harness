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
	start := -1
	for index := 0; index+1 < len(records); index++ {
		turn, turnOK := records[index].Event.(domain.TurnStarted)
		item, itemOK := records[index+1].Event.(domain.AssistantMessageStarted)
		if !turnOK && !itemOK {
			continue
		}
		if turnOK && turn.TurnID == record.TurnID || itemOK && (item.TurnID == record.TurnID || item.ItemID == record.ItemID) {
			if !turnOK || !itemOK || records[index].CommandID != record.CommandID || records[index+1].CommandID != record.CommandID || turn.TurnID != record.TurnID || item.TurnID != record.TurnID || item.ItemID != record.ItemID {
				return RunTurnResult{}, corruptRequestResult("relevant admission pair is malformed")
			}
			digest, err := DigestRunTurnRequestV1(record.SessionID, turn.Input)
			if err != nil || digest != record.RequestDigest {
				return RunTurnResult{}, corruptRequestResult("admission input digest mismatches record")
			}
			if start >= 0 {
				return RunTurnResult{}, corruptRequestResult("multiple admission pairs")
			}
			start = index
		}
	}
	if start < 0 {
		return RunTurnResult{}, corruptRequestResult("admission start pair is absent")
	}
	result := RunTurnResult{SessionID: record.SessionID, TurnID: record.TurnID, ItemID: record.ItemID, Status: domain.TurnStatusRunning, Records: append([]domain.RecordedEvent(nil), records[start:start+2]...)}
	terminals := 0
	for index := start + 2; index+1 < len(records); index++ {
		terminal, terminalOK, status, text, stableCode := requestTerminal(records[index].Event, record)
		_, turnOK := records[index+1].Event.(domain.TurnCompleted)
		if !turnOK {
			switch records[index+1].Event.(type) {
			case domain.TurnFailed, domain.TurnInterrupted:
				turnOK = true
			}
		}
		if !terminalOK {
			if relevantRequestTerminal(records[index].Event, record) {
				return RunTurnResult{}, corruptRequestResult("relevant terminal event is malformed")
			}
			continue
		}
		if records[index].CommandID != record.CommandID || records[index+1].CommandID != record.CommandID || !turnOK || terminals != 0 {
			return RunTurnResult{}, corruptRequestResult("terminal pair is malformed")
		}
		if err := validateTerminalTurn(records[index+1].Event, record, stableCode); err != nil {
			return RunTurnResult{}, corruptRequestResult(err.Error())
		}
		if message, ok := terminal.(domain.AssistantMessageFailed); ok {
			turn := records[index+1].Event.(domain.TurnFailed)
			if message.Message != turn.Message {
				return RunTurnResult{}, corruptRequestResult("failed terminal messages do not match")
			}
		}
		_ = terminal
		terminals++
		result.Status, result.Text, result.TerminalCommitted = status, text, true
		result.Records = append(result.Records, records[index], records[index+1])
	}
	return cloneRunTurnResult(result), nil
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
		if record.SessionID != sessionID || (index > 0 && record.Sequence != previous+1) || (index == 0 && record.Sequence == 0) {
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
	case domain.InterruptionCallerCanceled, domain.InterruptionDeliveryFailed:
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
