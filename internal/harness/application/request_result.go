package application

import (
	"fmt"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// ReconstructRequestResult rebuilds the public result from one pinned session
// view. It rejects malformed admission or terminal pairs rather than guessing.
func ReconstructRequestResult(record CommandRequestRecord, records []domain.RecordedEvent) (RunTurnResult, error) {
	start := -1
	for index := range records {
		if records[index].SessionID != record.SessionID || records[index].CommandID != record.CommandID {
			continue
		}
		turnStarted, ok := records[index].Event.(domain.TurnStarted)
		if !ok || turnStarted.TurnID != record.TurnID || index+1 >= len(records) {
			continue
		}
		itemStarted, ok := records[index+1].Event.(domain.AssistantMessageStarted)
		if !ok || records[index+1].SessionID != record.SessionID || records[index+1].CommandID != record.CommandID || itemStarted.TurnID != record.TurnID || itemStarted.ItemID != record.ItemID {
			return RunTurnResult{}, corruptRequestResult("admission does not contain the required start pair")
		}
		start = index
		break
	}
	if start < 0 {
		return RunTurnResult{}, corruptRequestResult("admission start pair is absent")
	}
	result := RunTurnResult{SessionID: record.SessionID, TurnID: record.TurnID, ItemID: record.ItemID, Status: domain.TurnStatusRunning, Records: append([]domain.RecordedEvent(nil), records[start:start+2]...)}
	for index := start + 2; index+1 < len(records); index++ {
		if records[index].SessionID != record.SessionID || records[index+1].SessionID != record.SessionID || records[index].CommandID != record.CommandID || records[index+1].CommandID != record.CommandID {
			continue
		}
		switch terminal := records[index].Event.(type) {
		case domain.AssistantMessageCompleted:
			turn, ok := records[index+1].Event.(domain.TurnCompleted)
			if !ok || terminal.TurnID != record.TurnID || terminal.ItemID != record.ItemID || turn.TurnID != record.TurnID {
				return RunTurnResult{}, corruptRequestResult("completed terminal pair is invalid")
			}
			result.Status, result.Text, result.TerminalCommitted = domain.TurnStatusCompleted, terminal.Text, true
		case domain.AssistantMessageFailed:
			turn, ok := records[index+1].Event.(domain.TurnFailed)
			if !ok || terminal.TurnID != record.TurnID || terminal.ItemID != record.ItemID || turn.TurnID != record.TurnID {
				return RunTurnResult{}, corruptRequestResult("failed terminal pair is invalid")
			}
			result.Status, result.TerminalCommitted = domain.TurnStatusFailed, true
		case domain.AssistantMessageInterrupted:
			turn, ok := records[index+1].Event.(domain.TurnInterrupted)
			if !ok || terminal.TurnID != record.TurnID || terminal.ItemID != record.ItemID || turn.TurnID != record.TurnID {
				return RunTurnResult{}, corruptRequestResult("interrupted terminal pair is invalid")
			}
			result.Status, result.TerminalCommitted = domain.TurnStatusInterrupted, true
		default:
			continue
		}
		result.Records = append(result.Records, records[index], records[index+1])
		return cloneRunTurnResult(result), nil
	}
	return cloneRunTurnResult(result), nil
}

func corruptRequestResult(message string) error {
	err, newErr := NewStoreError(StoreError{Code: StoreCodeCorrupt, Cause: fmt.Errorf("%s", message)})
	if newErr != nil {
		return fmt.Errorf("store corrupt: %s", message)
	}
	return err
}
