package eval

import (
	"encoding/json"
	"strings"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

type subagentParentCall struct {
	sessionID string
	index     int
	data      domain.ToolCallStarted
}

// verifySubagentDelegationObserved deliberately requires the whole proof to
// share durable identifiers. Independent observations of a delegate call and
// a child session are not enough: the child lineage, restricted schema,
// actual read, terminal state, and result returned to the parent must all
// describe the same invocation.
func verifySubagentDelegationObserved(reader *ArtifactReader, _ Scenario) CriterionResult {
	result := CriterionResult{ID: VerifierSubagentDelegation, Status: ScoreFail}
	events, present, err := readAuditEventsDetailed(reader)
	if !present || err != nil {
		result.Status = ScoreIndeterminate
		return result
	}

	var parentCalls []subagentParentCall
	for index, event := range events {
		if event.Type != domain.EventToolCallStarted {
			continue
		}
		var started domain.ToolCallStarted
		if json.Unmarshal(event.Data, &started) == nil && started.Name == "delegate_task" {
			parentCalls = append(parentCalls, subagentParentCall{sessionID: event.SessionID, index: index, data: started})
		}
	}
	if len(parentCalls) != 1 {
		return result
	}
	parent := parentCalls[0]

	type childSession struct {
		id    string
		index int
	}
	var children []childSession
	for index, event := range events {
		if event.Type != domain.EventSessionCreated || event.SessionID == "" {
			continue
		}
		var created domain.SessionCreated
		if json.Unmarshal(event.Data, &created) != nil || created.Parent == nil {
			continue
		}
		lineage := created.Parent
		if index > parent.index && string(lineage.SessionID) == parent.sessionID &&
			lineage.TurnID == parent.data.TurnID &&
			lineage.ItemID == parent.data.ItemID &&
			lineage.CallID == parent.data.CallID {
			children = append(children, childSession{id: event.SessionID, index: index})
		}
	}
	if len(children) != 1 {
		return result
	}
	childID := children[0].id
	childCreatedIndex := children[0].index

	var (
		requestCount           int
		firstRequestIndex      = -1
		readStarted            *domain.ToolCallStarted
		readStartedIndex       = -1
		readCompletedIndex     = -1
		assistantCompleteIndex = -1
		turnCompleteIndex      = -1
		parentReceiptIndex     = -1
	)
	for index, event := range events {
		switch {
		case event.SessionID == childID && event.Type == domain.EventModelRequestRecorded:
			var request domain.ModelRequestRecorded
			if index <= childCreatedIndex || turnCompleteIndex >= 0 || json.Unmarshal(event.Data, &request) != nil || !exactReadOnlySchemas(request.Tools) {
				return result
			}
			if firstRequestIndex < 0 {
				firstRequestIndex = index
			}
			requestCount++

		case event.SessionID == childID && event.Type == domain.EventToolCallStarted:
			var started domain.ToolCallStarted
			if firstRequestIndex < 0 || index <= firstRequestIndex || turnCompleteIndex >= 0 || json.Unmarshal(event.Data, &started) != nil || (started.Name != "read_file" && started.Name != "list_dir") {
				return result
			}
			if started.Name == "read_file" {
				if readStarted != nil {
					return result
				}
				copy := started
				readStarted = &copy
				readStartedIndex = index
			}

		case event.SessionID == childID && event.Type == domain.EventToolCallCompleted:
			var completed domain.ToolCallCompleted
			if json.Unmarshal(event.Data, &completed) == nil && readStarted != nil && index > readStartedIndex &&
				completed.TurnID == readStarted.TurnID && completed.ItemID == readStarted.ItemID && completed.CallID == readStarted.CallID {
				readCompletedIndex = index
			}

		case event.SessionID == childID && event.Type == domain.EventAssistantMessageCompleted:
			var completed domain.AssistantMessageCompleted
			if json.Unmarshal(event.Data, &completed) == nil && readStarted != nil && readCompletedIndex >= 0 && index > readCompletedIndex && completed.TurnID == readStarted.TurnID {
				assistantCompleteIndex = index
			}

		case event.SessionID == childID && event.Type == domain.EventTurnCompleted:
			var completed domain.TurnCompleted
			if json.Unmarshal(event.Data, &completed) == nil && readStarted != nil && assistantCompleteIndex >= 0 && index > assistantCompleteIndex && completed.TurnID == readStarted.TurnID {
				turnCompleteIndex = index
			}

		case event.SessionID == parent.sessionID && event.Type == domain.EventToolCallCompleted:
			var completed domain.ToolCallCompleted
			if json.Unmarshal(event.Data, &completed) == nil && turnCompleteIndex >= 0 && index > turnCompleteIndex &&
				completed.TurnID == parent.data.TurnID && completed.ItemID == parent.data.ItemID && completed.CallID == parent.data.CallID &&
				strings.HasPrefix(completed.Content, "child session: "+childID+"\n") {
				parentReceiptIndex = index
			}
		}
	}

	if requestCount > 0 && readStarted != nil && readCompletedIndex >= 0 && assistantCompleteIndex >= 0 && turnCompleteIndex >= 0 && parentReceiptIndex >= 0 {
		result.Status = ScorePass
	}
	return result
}

func exactReadOnlySchemas(schemas []domain.ToolSchema) bool {
	if len(schemas) != 2 {
		return false
	}
	seen := map[string]bool{}
	for _, schema := range schemas {
		if schema.Name != "read_file" && schema.Name != "list_dir" {
			return false
		}
		seen[schema.Name] = true
	}
	return seen["read_file"] && seen["list_dir"]
}
