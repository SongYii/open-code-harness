package eval

import (
	"bytes"
	"encoding/json"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

type verifierAuditEnvelope struct {
	Events []json.RawMessage `json:"events"`
}

type verifierAuditEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func readAuditEvents(reader *ArtifactReader) ([]verifierAuditEvent, bool) {
	entries := reader.Entries("audit")
	if !hasCollectedEntry(entries) {
		return nil, false
	}
	var events []verifierAuditEvent
	for _, entry := range entries {
		if entry.State != EntryCollected {
			continue
		}
		data, err := reader.ReadEntry(entry.Path)
		if err != nil {
			return nil, false
		}
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var envelope verifierAuditEnvelope
			if err := json.Unmarshal(line, &envelope); err != nil {
				return nil, false
			}
			for _, raw := range envelope.Events {
				var event verifierAuditEvent
				if err := json.Unmarshal(raw, &event); err != nil || event.Type == "" {
					return nil, false
				}
				events = append(events, event)
			}
		}
	}
	return events, true
}

// verifyToolApprovalFailureObserved requires canonical audit evidence for an
// approval-required write_file call, a denied approval, and a terminal tool
// failure. Configuration or an approval script declaration alone cannot pass.
func verifyToolApprovalFailureObserved(reader *ArtifactReader, _ Scenario) CriterionResult {
	events, ok := readAuditEvents(reader)
	if !ok {
		return CriterionResult{ID: "tool-approval-failure-observed-v1", Status: ScoreIndeterminate}
	}
	var approvalRequired, approvalDenied, toolFailed bool
	for _, event := range events {
		switch event.Type {
		case domain.EventPolicyDecisionRecorded:
			var data struct {
				Name   string `json:"name"`
				Effect string `json:"effect"`
			}
			if json.Unmarshal(event.Data, &data) == nil &&
				data.Name == "write_file" && data.Effect == domain.PolicyEffectRequireApproval {
				approvalRequired = true
			}
		case domain.EventApprovalResolved:
			var data struct {
				Decision string `json:"decision"`
			}
			if json.Unmarshal(event.Data, &data) == nil && data.Decision == domain.ApprovalDecisionDenied {
				approvalDenied = true
			}
		case domain.EventToolCallFailed:
			toolFailed = true
		}
	}
	if approvalRequired && approvalDenied && toolFailed {
		return CriterionResult{ID: "tool-approval-failure-observed-v1", Status: ScorePass}
	}
	return CriterionResult{ID: "tool-approval-failure-observed-v1", Status: ScoreFail}
}

// verifyContextCompactionObserved requires a manual reset bracket and its
// completed source-tail-reset checkpoint in canonical audit evidence.
func verifyContextCompactionObserved(reader *ArtifactReader, _ Scenario) CriterionResult {
	events, ok := readAuditEvents(reader)
	if !ok {
		return CriterionResult{ID: "context-compaction-observed-v1", Status: ScoreIndeterminate}
	}
	var started, completed bool
	for _, event := range events {
		switch event.Type {
		case domain.EventContextCompactionStarted:
			var data struct {
				Trigger  string `json:"trigger"`
				Strategy string `json:"strategy"`
			}
			if json.Unmarshal(event.Data, &data) == nil &&
				data.Trigger == domain.ContextTriggerManual && data.Strategy == domain.ContextStrategyReset {
				started = true
			}
		case domain.EventContextCompactionCompleted:
			var data struct {
				Checkpoint struct {
					Kind string `json:"kind"`
				} `json:"checkpoint"`
			}
			if json.Unmarshal(event.Data, &data) == nil &&
				data.Checkpoint.Kind == domain.ContextCheckpointKindSourceTailReset {
				completed = true
			}
		}
	}
	if started && completed {
		return CriterionResult{ID: "context-compaction-observed-v1", Status: ScorePass}
	}
	return CriterionResult{ID: "context-compaction-observed-v1", Status: ScoreFail}
}
