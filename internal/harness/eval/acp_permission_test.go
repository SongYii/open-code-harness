package eval

import (
	"context"
	"encoding/json"
	"testing"
)

func standardPermissionOptions() []acpPermissionOption {
	return []acpPermissionOption{
		{OptionID: "allow-once", Name: "Allow", Kind: "allow_once"},
		{OptionID: "reject-once", Name: "Reject", Kind: "reject_once"},
	}
}

func TestACPKnownTwoOptionShapeAcceptsEitherOrder(t *testing.T) {
	allowID, rejectID, ok := acpKnownTwoOptionShape(standardPermissionOptions())
	if !ok || allowID != "allow-once" || rejectID != "reject-once" {
		t.Fatalf("acpKnownTwoOptionShape = (%q, %q, %v), want (allow-once, reject-once, true)", allowID, rejectID, ok)
	}

	reversed := []acpPermissionOption{
		{OptionID: "reject-once", Name: "Reject", Kind: "reject_once"},
		{OptionID: "allow-once", Name: "Allow", Kind: "allow_once"},
	}
	allowID, rejectID, ok = acpKnownTwoOptionShape(reversed)
	if !ok || allowID != "allow-once" || rejectID != "reject-once" {
		t.Fatalf("acpKnownTwoOptionShape(reversed) = (%q, %q, %v), want (allow-once, reject-once, true)", allowID, rejectID, ok)
	}
}

func TestACPKnownTwoOptionShapeRejectsUnrecognizedOptions(t *testing.T) {
	cases := [][]acpPermissionOption{
		nil,
		{{OptionID: "allow-once"}},
		{{OptionID: "allow-once"}, {OptionID: "allow-once"}},
		{{OptionID: "allow-always"}, {OptionID: "reject-once"}},
		{{OptionID: "allow-once"}, {OptionID: "reject-once"}, {OptionID: "allow-always"}},
	}
	for i, options := range cases {
		if _, _, ok := acpKnownTwoOptionShape(options); ok {
			t.Fatalf("case %d: acpKnownTwoOptionShape(%+v) = ok, want rejected", i, options)
		}
	}
}

func newPermissionRequestParams(t *testing.T, toolName, callID string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(acpPermissionRequestParams{
		SessionID: "session-1",
		ToolCall:  acpPermissionToolCall{ToolCallID: callID, Title: toolName, Kind: "edit", Status: "pending"},
		Options:   standardPermissionOptions(),
	})
	if err != nil {
		t.Fatalf("marshal request params: %v", err)
	}
	return raw
}

func decodePermissionResult(t *testing.T, raw json.RawMessage) acpPermissionResult {
	t.Helper()
	var result acpPermissionResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal permission result: %v", err)
	}
	return result
}

func TestNewACPPermissionHandlerSelectsAllowOptionForScriptedAllow(t *testing.T) {
	matcher := NewApprovalMatcher([]ApprovalScriptEntry{
		{PromptActionID: "prompt-1", Ordinal: 0, ToolName: "read_file", Answer: ApprovalAllow},
	})
	matcher.BeginPrompt("prompt-1")
	handler := NewACPPermissionHandler(matcher)

	raw, err := handler(context.Background(), newPermissionRequestParams(t, "read_file", "call-1"))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	result := decodePermissionResult(t, raw)
	if result.Outcome.Outcome != "selected" || result.Outcome.OptionID != "allow-once" {
		t.Fatalf("outcome = %+v, want selected/allow-once", result.Outcome)
	}
	observations := matcher.Observations()
	if len(observations) != 1 || observations[0].Answer != ApprovalAllow || observations[0].CallID != "call-1" || observations[0].ToolName != "read_file" {
		t.Fatalf("observations = %+v, want one clean allow retaining CallID and ToolName as evidence", observations)
	}
}

func TestNewACPPermissionHandlerSelectsRejectOptionForScriptedDeny(t *testing.T) {
	matcher := NewApprovalMatcher([]ApprovalScriptEntry{
		{PromptActionID: "prompt-1", Ordinal: 0, ToolName: "write_file", Answer: ApprovalDeny},
	})
	matcher.BeginPrompt("prompt-1")
	handler := NewACPPermissionHandler(matcher)

	raw, err := handler(context.Background(), newPermissionRequestParams(t, "write_file", "call-1"))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	result := decodePermissionResult(t, raw)
	if result.Outcome.Outcome != "selected" || result.Outcome.OptionID != "reject-once" {
		t.Fatalf("outcome = %+v, want selected/reject-once", result.Outcome)
	}
}

func TestNewACPPermissionHandlerFailsClosedOnUndeclaredRequest(t *testing.T) {
	matcher := NewApprovalMatcher(nil)
	matcher.BeginPrompt("prompt-1")
	handler := NewACPPermissionHandler(matcher)

	raw, err := handler(context.Background(), newPermissionRequestParams(t, "delete_file", "call-1"))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	result := decodePermissionResult(t, raw)
	if result.Outcome.OptionID != "reject-once" {
		t.Fatalf("outcome = %+v, want reject-once for an undeclared request (fail closed)", result.Outcome)
	}
	if observations := matcher.Observations(); len(observations) != 1 || observations[0].Violation == "" {
		t.Fatalf("observations = %+v, want one recorded violation", observations)
	}
}

func TestNewACPPermissionHandlerRejectsMalformedParams(t *testing.T) {
	matcher := NewApprovalMatcher(nil)
	handler := NewACPPermissionHandler(matcher)
	if _, err := handler(context.Background(), json.RawMessage(`{not json`)); err == nil {
		t.Fatal("handler() error = nil, want a decode error for malformed params")
	}
}

func TestNewACPPermissionHandlerRejectsUnrecognizedOptionsShape(t *testing.T) {
	matcher := NewApprovalMatcher([]ApprovalScriptEntry{
		{PromptActionID: "prompt-1", Ordinal: 0, ToolName: "read_file", Answer: ApprovalAllow},
	})
	matcher.BeginPrompt("prompt-1")
	handler := NewACPPermissionHandler(matcher)

	raw, err := json.Marshal(acpPermissionRequestParams{
		SessionID: "session-1",
		ToolCall:  acpPermissionToolCall{ToolCallID: "call-1", Title: "read_file"},
		Options:   []acpPermissionOption{{OptionID: "allow-always"}, {OptionID: "reject-once"}},
	})
	if err != nil {
		t.Fatalf("marshal request params: %v", err)
	}
	if _, err := handler(context.Background(), raw); err == nil {
		t.Fatal("handler() error = nil, want a refusal for an unrecognized options shape")
	}
	if observations := matcher.Observations(); len(observations) != 0 {
		t.Fatalf("observations = %+v, want none: a refused request must never reach the matcher", observations)
	}
}
