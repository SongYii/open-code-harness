package eval

import (
	"encoding/json"
	"strings"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// verifyReadFileCompleted requires canonical audit evidence that a
// read_file tool call actually started and completed -- not merely that
// the Scenario declares one.
func verifyReadFileCompleted(reader *ArtifactReader, _ Scenario) CriterionResult {
	events, ok := readAuditEvents(reader)
	if !ok {
		return CriterionResult{ID: "read-file-completed-v1", Status: ScoreIndeterminate}
	}
	started := make(map[string]bool)
	completed := make(map[string]bool)
	for _, event := range events {
		switch event.Type {
		case domain.EventToolCallStarted:
			var data domain.ToolCallStarted
			if json.Unmarshal(event.Data, &data) == nil && data.Name == "read_file" {
				started[data.CallID] = true
			}
		case domain.EventToolCallCompleted:
			var data domain.ToolCallCompleted
			if json.Unmarshal(event.Data, &data) == nil {
				completed[data.CallID] = true
			}
		}
	}
	for callID := range started {
		if completed[callID] {
			return CriterionResult{ID: "read-file-completed-v1", Status: ScorePass}
		}
	}
	return CriterionResult{ID: "read-file-completed-v1", Status: ScoreFail}
}

// toolCallRedactionSecretMarker is the exact fake-secret substring
// eval/scenarios/tool-exec-redaction's own fixture-triggered exec call
// writes to stdout (cmd/och-eval/fixture.go's own execRedactionMarker
// branch) -- present here only to confirm it is ABSENT from committed
// evidence, never to trigger anything.
const toolCallRedactionSecretMarker = "sk-test-should-not-appear-in-evidence"

// verifyRedactionObserved requires canonical audit evidence that a tool
// result's own content was actually redacted (contains the "[redacted]"
// marker internal/harness/redact's own Text function writes) and that
// the raw secret value never appears anywhere in committed evidence -- a
// Scenario merely triggering a tool call that outputs a secret-shaped
// string is not proof by itself; the redaction must be observed to have
// actually happened.
func verifyRedactionObserved(reader *ArtifactReader, _ Scenario) CriterionResult {
	events, ok := readAuditEvents(reader)
	if !ok {
		return CriterionResult{ID: "redaction-observed-v1", Status: ScoreIndeterminate}
	}
	found := false
	for _, event := range events {
		if event.Type != domain.EventToolCallCompleted {
			continue
		}
		var data domain.ToolCallCompleted
		if json.Unmarshal(event.Data, &data) != nil {
			continue
		}
		if strings.Contains(data.Content, toolCallRedactionSecretMarker) {
			return CriterionResult{ID: "redaction-observed-v1", Status: ScoreFail}
		}
		if strings.Contains(data.Content, "[redacted]") {
			found = true
		}
	}
	if found {
		return CriterionResult{ID: "redaction-observed-v1", Status: ScorePass}
	}
	return CriterionResult{ID: "redaction-observed-v1", Status: ScoreFail}
}

// expectedMissingFileFailureCode is internal/harness/application's own
// CodeInvalidArgs value (errors.go), read directly rather than assumed: a
// read_file call for a workspace-relative path that does not exist fails
// through invokeTool's own generic non-nil-error path (pipeline.go),
// which runToolBody maps to exactly this code, not a dedicated
// "not_found" one. Hardcoded here rather than importing
// internal/harness/application (which eval may import) since this is
// the one constant this file needs from it.
const expectedMissingFileFailureCode = "invalid_args"

// verifyExpectedToolFailureObserved requires canonical audit evidence of
// a tool call that failed for the specific, expected reason a missing
// workspace file produces.
func verifyExpectedToolFailureObserved(reader *ArtifactReader, _ Scenario) CriterionResult {
	events, ok := readAuditEvents(reader)
	if !ok {
		return CriterionResult{ID: "expected-tool-failure-observed-v1", Status: ScoreIndeterminate}
	}
	for _, event := range events {
		if event.Type != domain.EventToolCallFailed {
			continue
		}
		var data domain.ToolCallFailed
		if json.Unmarshal(event.Data, &data) == nil && data.Code == expectedMissingFileFailureCode {
			return CriterionResult{ID: "expected-tool-failure-observed-v1", Status: ScorePass}
		}
	}
	return CriterionResult{ID: "expected-tool-failure-observed-v1", Status: ScoreFail}
}

// scopeDeniedFailureCode is internal/harness/application's own
// CodeScopeDenied value (errors.go), read directly rather than assumed.
// An out-of-workspace path is refused by
// internal/harness/application/pipeline.go's own lexical
// tools.CheckScopeLexical check, executed before a tool call's argument
// is ever resolved against the real filesystem and before Policy.Decide
// is ever called at all -- verified directly by inspecting a real
// Attempt's own committed audit evidence for this exact Scenario, not
// assumed from reading the policy engine alone: no
// policy.decision.recorded event is emitted for this path at all, only
// tool.call.failed with this code.
const scopeDeniedFailureCode = "scope_denied"

// verifyContainmentRefused requires canonical audit evidence that an
// out-of-workspace path was refused (design's own containment
// guarantee) with the specific, expected failure code that refusal
// produces, not merely that the resulting tool call failed for some
// other reason.
func verifyContainmentRefused(reader *ArtifactReader, _ Scenario) CriterionResult {
	events, ok := readAuditEvents(reader)
	if !ok {
		return CriterionResult{ID: "containment-refused-v1", Status: ScoreIndeterminate}
	}
	for _, event := range events {
		if event.Type != domain.EventToolCallFailed {
			continue
		}
		var data domain.ToolCallFailed
		if json.Unmarshal(event.Data, &data) == nil && data.Code == scopeDeniedFailureCode {
			return CriterionResult{ID: "containment-refused-v1", Status: ScorePass}
		}
	}
	return CriterionResult{ID: "containment-refused-v1", Status: ScoreFail}
}
