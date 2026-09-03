package eval

import "testing"

func TestParityPairKeyForAttemptUsesScenarioDigestAndRepetitionIndex(t *testing.T) {
	scenarioDigest, err := ScenarioDigest(validScenario())
	if err != nil {
		t.Fatalf("ScenarioDigest: %v", err)
	}
	attempt := Attempt{ScenarioDigest: scenarioDigest, RepetitionIndex: 2}
	key := ParityPairKeyForAttempt(attempt)
	if key.ScenarioDigest != scenarioDigest || key.RepetitionIndex != 2 {
		t.Fatalf("key = %+v, want {%s 2}", key, scenarioDigest)
	}

	other := Attempt{ScenarioDigest: scenarioDigest, RepetitionIndex: 3}
	if ParityPairKeyForAttempt(other) == key {
		t.Fatal("keys with different repetition indices compared equal")
	}
}

func identicalParityFacts() ParitySemanticFacts {
	return ParitySemanticFacts{
		OutcomeStatus:     OutcomeCompleted,
		TerminalTurnCount: 1,
		TerminalOpen:      true,
		ToolCalls: []ParityToolCall{
			{Name: "write_file", Arguments: `{"path":"output.txt"}`, PolicyEffect: "require_approval", ApprovalDecision: "granted", Result: "completed"},
		},
		Usage: []ParityUsage{
			{InputTokens: 10, OutputTokens: 5, FinishReason: "tool_calls"},
		},
		RequestEnvelopes: []ParityRequestEnvelope{
			{ModelID: "smoke-model", ContextWindowTokens: 4096, MaxOutputTokens: 512},
		},
		Workspace: []ParityWorkspaceEntry{
			{Path: "output.txt", SHA256: "abc123"},
		},
	}
}

func TestComparePairedArmsFindsNoMismatchForIdenticalFacts(t *testing.T) {
	baseline := ParityArm{ExecutorKind: ExecutorInProcess, SubjectDigest: "sha256:aaa", Facts: identicalParityFacts()}
	candidate := ParityArm{ExecutorKind: ExecutorACPSubprocess, SubjectDigest: "sha256:bbb", Facts: identicalParityFacts()}

	if mismatches := ComparePairedArms(baseline, candidate); len(mismatches) != 0 {
		t.Fatalf("mismatches = %+v, want none for identical facts (executor kind and subject digest are expected to differ and are never compared)", mismatches)
	}
}

func TestComparePairedArmsReportsEachDifferingField(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ParitySemanticFacts)
		field  string
	}{
		{"status", func(f *ParitySemanticFacts) { f.OutcomeStatus = OutcomeSubjectFailed }, "outcomeStatus"},
		{"turnCount", func(f *ParitySemanticFacts) { f.TerminalTurnCount = 2 }, "terminalTurnCount"},
		{"open", func(f *ParitySemanticFacts) { f.TerminalOpen = false }, "terminalOpen"},
		{"toolCalls", func(f *ParitySemanticFacts) { f.ToolCalls[0].Result = "failed:denied" }, "toolCalls"},
		{"usage", func(f *ParitySemanticFacts) { f.Usage[0].OutputTokens = 999 }, "usage"},
		{"requestEnvelope", func(f *ParitySemanticFacts) { f.RequestEnvelopes[0].ModelID = "other-model" }, "requestEnvelopes"},
		{"workspace", func(f *ParitySemanticFacts) { f.Workspace[0].SHA256 = "different" }, "workspace"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			candidateFacts := identicalParityFacts()
			testCase.mutate(&candidateFacts)
			baseline := ParityArm{ExecutorKind: ExecutorInProcess, Facts: identicalParityFacts()}
			candidate := ParityArm{ExecutorKind: ExecutorACPSubprocess, Facts: candidateFacts}

			mismatches := ComparePairedArms(baseline, candidate)
			if len(mismatches) != 1 || mismatches[0].Field != testCase.field {
				t.Fatalf("mismatches = %+v, want exactly one on field %q", mismatches, testCase.field)
			}
		})
	}
}
