package eval

import (
	"context"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func testApprovalScript() []ApprovalScriptEntry {
	return []ApprovalScriptEntry{
		{PromptActionID: "prompt-1", Ordinal: 0, ToolName: "read_file", Answer: ApprovalAllow},
		{PromptActionID: "prompt-1", Ordinal: 1, ToolName: "write_file", Answer: ApprovalDeny},
		{PromptActionID: "prompt-2", Ordinal: 0, ToolName: "read_file", Answer: ApprovalAllow},
	}
}

func TestApprovalMatcherMatchesDeclaredAllowAndDeny(t *testing.T) {
	matcher := NewApprovalMatcher(testApprovalScript())
	matcher.BeginPrompt("prompt-1")

	first := matcher.Decide("read_file", "call-1")
	if first.Answer != ApprovalAllow || first.Violation != "" {
		t.Fatalf("first decision = %+v, want allow with no violation", first)
	}
	second := matcher.Decide("write_file", "call-2")
	if second.Answer != ApprovalDeny || second.Violation != "" {
		t.Fatalf("second decision = %+v, want deny with no violation", second)
	}
}

func TestApprovalMatcherResetsOrdinalOnNextPrompt(t *testing.T) {
	matcher := NewApprovalMatcher(testApprovalScript())
	matcher.BeginPrompt("prompt-1")
	matcher.Decide("read_file", "call-1")
	matcher.Decide("write_file", "call-2")

	matcher.BeginPrompt("prompt-2")
	decision := matcher.Decide("read_file", "call-3")
	if decision.Answer != ApprovalAllow || decision.Violation != "" {
		t.Fatalf("decision after BeginPrompt reset = %+v, want allow with no violation", decision)
	}
}

func TestApprovalMatcherDeniesRequestBeforeAnyPrompt(t *testing.T) {
	matcher := NewApprovalMatcher(testApprovalScript())
	decision := matcher.Decide("read_file", "call-1")
	if decision.Answer != ApprovalDeny || decision.Violation == "" {
		t.Fatalf("decision before BeginPrompt = %+v, want deny with a violation", decision)
	}
}

func TestApprovalMatcherDeniesExhaustedOrdinal(t *testing.T) {
	matcher := NewApprovalMatcher(testApprovalScript())
	matcher.BeginPrompt("prompt-1")
	matcher.Decide("read_file", "call-1")
	matcher.Decide("write_file", "call-2")

	decision := matcher.Decide("read_file", "call-3") // no ordinal 2 declared for prompt-1
	if decision.Answer != ApprovalDeny || decision.Violation == "" {
		t.Fatalf("decision past the declared script = %+v, want deny with a violation", decision)
	}
}

func TestApprovalMatcherDeniesToolNameMismatch(t *testing.T) {
	matcher := NewApprovalMatcher(testApprovalScript())
	matcher.BeginPrompt("prompt-1")
	decision := matcher.Decide("delete_file", "call-1") // ordinal 0 declares read_file
	if decision.Answer != ApprovalDeny || decision.Violation == "" {
		t.Fatalf("decision on tool mismatch = %+v, want deny with a violation", decision)
	}
}

func TestApprovalMatcherMismatchDoesNotConsumeOrdinal(t *testing.T) {
	matcher := NewApprovalMatcher(testApprovalScript())
	matcher.BeginPrompt("prompt-1")
	matcher.Decide("delete_file", "call-1") // violates; must not advance the ordinal

	// The correctly-named request for ordinal 0 must still be able to match.
	decision := matcher.Decide("read_file", "call-2")
	if decision.Answer != ApprovalAllow || decision.Violation != "" {
		t.Fatalf("decision after a prior mismatch = %+v, want allow with no violation (ordinal must not have advanced)", decision)
	}
}

func TestApprovalMatcherRepeatedToolNamesAtDifferentOrdinals(t *testing.T) {
	script := []ApprovalScriptEntry{
		{PromptActionID: "prompt-1", Ordinal: 0, ToolName: "read_file", Answer: ApprovalAllow},
		{PromptActionID: "prompt-1", Ordinal: 1, ToolName: "read_file", Answer: ApprovalDeny},
	}
	matcher := NewApprovalMatcher(script)
	matcher.BeginPrompt("prompt-1")
	first := matcher.Decide("read_file", "call-1")
	second := matcher.Decide("read_file", "call-2")
	if first.Answer != ApprovalAllow || second.Answer != ApprovalDeny {
		t.Fatalf("repeated tool decisions = %+v, %+v; want allow then deny", first, second)
	}
}

func TestApprovalMatcherObservationsRecordEveryRequestInOrder(t *testing.T) {
	matcher := NewApprovalMatcher(testApprovalScript())
	matcher.BeginPrompt("prompt-1")
	matcher.Decide("read_file", "call-1")
	matcher.Decide("delete_file", "call-2") // violation
	matcher.Decide("write_file", "call-3")

	observations := matcher.Observations()
	if len(observations) != 3 {
		t.Fatalf("Observations() returned %d entries, want 3", len(observations))
	}
	if observations[0].Answer != ApprovalAllow || observations[0].Violation != "" {
		t.Fatalf("observations[0] = %+v, want allow with no violation", observations[0])
	}
	if observations[1].Violation == "" {
		t.Fatalf("observations[1] = %+v, want a recorded violation", observations[1])
	}
	if observations[2].Answer != ApprovalDeny || observations[2].Violation != "" {
		t.Fatalf("observations[2] = %+v, want the script's declared deny with no violation", observations[2])
	}
	if observations[0].CallID != "call-1" || observations[2].CallID != "call-3" {
		t.Fatalf("observations did not retain CallID as evidence: %+v", observations)
	}
}

func TestApprovalMatcherObservationsAreACopy(t *testing.T) {
	matcher := NewApprovalMatcher(testApprovalScript())
	matcher.BeginPrompt("prompt-1")
	matcher.Decide("read_file", "call-1")

	observations := matcher.Observations()
	observations[0].ToolName = "tampered"

	fresh := matcher.Observations()
	if fresh[0].ToolName == "tampered" {
		t.Fatal("mutating a returned Observations slice affected matcher state")
	}
}

func TestNewApproverGrantsOnlyDeclaredAllow(t *testing.T) {
	matcher := NewApprovalMatcher(testApprovalScript())
	matcher.BeginPrompt("prompt-1")
	approver := NewApprover(matcher)

	allowed, err := approver.Decide(context.Background(), tools.ApprovalRequest{Name: "read_file", CallID: "call-1"})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !allowed.Granted {
		t.Fatal("Granted = false, want true for a declared allow")
	}

	denied, err := approver.Decide(context.Background(), tools.ApprovalRequest{Name: "write_file", CallID: "call-2"})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if denied.Granted {
		t.Fatal("Granted = true, want false for a declared deny")
	}
}

func TestNewApproverDeniesUndeclaredRequest(t *testing.T) {
	matcher := NewApprovalMatcher(testApprovalScript())
	matcher.BeginPrompt("prompt-1")
	approver := NewApprover(matcher)

	answer, err := approver.Decide(context.Background(), tools.ApprovalRequest{Name: "delete_file", CallID: "call-1"})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if answer.Granted {
		t.Fatal("Granted = true, want false: fail-closed on an undeclared tool")
	}
	if len(matcher.Observations()) != 1 || matcher.Observations()[0].Violation == "" {
		t.Fatal("approver Decide did not record the violation on the underlying matcher")
	}
}

var _ tools.Approver = approverAdapter{}
