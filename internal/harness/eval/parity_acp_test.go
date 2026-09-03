//go:build unix

package eval

import (
	"context"
	"testing"
	"time"
)

// publishParityTestEvidenceDocuments mirrors evidence_test.go's own
// publishTestEvidenceDocuments, parameterized by executor: this file's own
// tests need both an in_process and an acp_subprocess Attempt document for
// the same logical Scenario, which that shared helper's own hardcoded
// in-process executor cannot produce.
func publishParityTestEvidenceDocuments(t *testing.T, directories AttemptRootDirectories, attemptID AttemptID, scenario Scenario, subject Subject, executor Executor) EvidenceDocuments {
	t.Helper()
	attempt, err := buildAttemptDocument(
		"parity-test-set",
		CellAttempt{Cell: Cell{ScenarioID: scenario.ID, SubjectID: subject.ID, ExecutorID: executor.ID}},
		attemptID, directories, scenario, subject, executor,
	)
	if err != nil {
		t.Fatalf("buildAttemptDocument: %v", err)
	}
	if err := PublishAttempt(directories.Root, attempt); err != nil {
		t.Fatalf("PublishAttempt: %v", err)
	}
	return EvidenceDocuments{Scenario: scenario, Subject: subject, Executor: executor, Attempt: attempt}
}

func parityApprovalScenario() Scenario {
	scenario := validScenario()
	scenario.Actions = []ScenarioAction{
		newEchoScenarioAction("prompt-1", "write the file"),
		{ID: "collect-1", Type: ActionCollect, Collect: &CollectAction{WorkspacePath: "output.txt"}},
	}
	scenario.RequiredEvidenceRoles = []string{"transcript", "audit", "workspace"}
	scenario.OptionalEvidenceRoles = nil
	scenario.DeterministicVerifierIDs = nil
	return scenario
}

// TestComparePairedArmsRealInProcessVsACPYieldsNoMismatchForIdenticalScenario
// is design §25.1's own executor parity fixture, run for real: the same
// deterministic Scenario and Subject semantics driven through both
// executors against independently fresh fixture providers, loaded via
// LoadParityArm from each side's own real, collected evidence, and
// compared with ComparePairedArms. A lifecycle-only difference (a
// different httptest.Server port baked into each Subject's own
// NormalizedEndpoint, a different Executor kind/binary, different
// AttemptIDs/paths/timestamps) must never surface as a mismatch.
func TestComparePairedArmsRealInProcessVsACPYieldsNoMismatchForIdenticalScenario(t *testing.T) {
	ochBin := buildOchBinary(t)
	binary, err := ResolveACPBinary(ochBin)
	if err != nil {
		t.Fatalf("ResolveACPBinary: %v", err)
	}

	approvalScript := []ApprovalScriptEntry{
		{PromptActionID: "prompt-1", Ordinal: 0, ToolName: "write_file", Answer: ApprovalAllow},
	}

	baselineServer := newApprovalProvider(t)
	baselineSubject := testSubject(t, baselineServer.Server)
	baselineAttemptID, baselineDirectories := acpTestDirectories(t)
	baselineScenario := parityApprovalScenario()
	baselineScenario.ApprovalScript = approvalScript
	baselineDocuments := publishParityTestEvidenceDocuments(t, baselineDirectories, baselineAttemptID, baselineScenario, baselineSubject, validExecutorInProcess())
	baselineMatcher := NewApprovalMatcher(baselineScenario.ApprovalScript)
	baselineExecution, err := RunAttempt(context.Background(), baselineAttemptID, baselineSubject, baselineDirectories, baselineScenario, baselineMatcher)
	if err != nil {
		t.Fatalf("RunAttempt() (baseline) error = %v", err)
	}
	if _, _, err := CollectEvidence(context.Background(), baselineDirectories, baselineExecution, baselineExecution.Outcome, baselineDocuments, CollectionLimits{}); err != nil {
		t.Fatalf("CollectEvidence (baseline): %v", err)
	}
	baselineArm, err := LoadParityArm(baselineDirectories)
	if err != nil {
		t.Fatalf("LoadParityArm (baseline): %v", err)
	}

	candidateServer := newApprovalProvider(t)
	candidateSubject := testSubject(t, candidateServer.Server)
	candidateAttemptID, candidateDirectories := acpTestDirectories(t)
	candidateScenario := parityApprovalScenario()
	candidateScenario.ApprovalScript = approvalScript
	candidateDocuments := publishParityTestEvidenceDocuments(t, candidateDirectories, candidateAttemptID, candidateScenario, candidateSubject, validExecutorACPSubprocess())
	candidateMatcher := NewApprovalMatcher(candidateScenario.ApprovalScript)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	candidateExecution, err := RunACPAttempt(ctx, candidateAttemptID, candidateSubject, candidateDirectories, candidateScenario, ACPLaunchConfig{Binary: binary}, candidateMatcher)
	if err != nil {
		t.Fatalf("RunACPAttempt() (candidate) error = %v", err)
	}
	if _, _, err := CollectEvidence(context.Background(), candidateDirectories, candidateExecution, candidateExecution.Outcome, candidateDocuments, CollectionLimits{}); err != nil {
		t.Fatalf("CollectEvidence (candidate): %v", err)
	}
	candidateArm, err := LoadParityArm(candidateDirectories)
	if err != nil {
		t.Fatalf("LoadParityArm (candidate): %v", err)
	}

	if baselineArm.ExecutorKind != ExecutorInProcess {
		t.Fatalf("baseline ExecutorKind = %q, want %q", baselineArm.ExecutorKind, ExecutorInProcess)
	}
	if candidateArm.ExecutorKind != ExecutorACPSubprocess {
		t.Fatalf("candidate ExecutorKind = %q, want %q", candidateArm.ExecutorKind, ExecutorACPSubprocess)
	}
	if baselineArm.SubjectDigest == candidateArm.SubjectDigest {
		t.Fatal("baseline and candidate Subject digests are equal; each Subject's own NormalizedEndpoint (a distinct httptest.Server port) should have made them differ")
	}
	if len(baselineArm.Facts.ToolCalls) == 0 {
		t.Fatal("baseline recorded no tool calls; this test proves nothing without a real approved write_file call on both sides")
	}

	mismatches := ComparePairedArms(baselineArm, candidateArm)
	if len(mismatches) != 0 {
		t.Fatalf("mismatches = %+v, want none: a lifecycle-only difference must not surface as a parity failure", mismatches)
	}
}

// TestComparePairedArmsRealDivergenceIsDetected proves the mirror case: a
// genuine semantic divergence between the two arms (a scripted deny on the
// candidate side where the baseline allows) is caught, not silently
// absorbed as "just another lifecycle difference."
func TestComparePairedArmsRealDivergenceIsDetected(t *testing.T) {
	ochBin := buildOchBinary(t)
	binary, err := ResolveACPBinary(ochBin)
	if err != nil {
		t.Fatalf("ResolveACPBinary: %v", err)
	}

	baselineServer := newApprovalProvider(t)
	baselineSubject := testSubject(t, baselineServer.Server)
	baselineAttemptID, baselineDirectories := acpTestDirectories(t)
	baselineScenario := parityApprovalScenario()
	baselineScenario.ApprovalScript = []ApprovalScriptEntry{
		{PromptActionID: "prompt-1", Ordinal: 0, ToolName: "write_file", Answer: ApprovalAllow},
	}
	baselineDocuments := publishParityTestEvidenceDocuments(t, baselineDirectories, baselineAttemptID, baselineScenario, baselineSubject, validExecutorInProcess())
	baselineMatcher := NewApprovalMatcher(baselineScenario.ApprovalScript)
	baselineExecution, err := RunAttempt(context.Background(), baselineAttemptID, baselineSubject, baselineDirectories, baselineScenario, baselineMatcher)
	if err != nil {
		t.Fatalf("RunAttempt() (baseline) error = %v", err)
	}
	if _, _, err := CollectEvidence(context.Background(), baselineDirectories, baselineExecution, baselineExecution.Outcome, baselineDocuments, CollectionLimits{}); err != nil {
		t.Fatalf("CollectEvidence (baseline): %v", err)
	}
	baselineArm, err := LoadParityArm(baselineDirectories)
	if err != nil {
		t.Fatalf("LoadParityArm (baseline): %v", err)
	}

	candidateServer := newApprovalProvider(t)
	candidateSubject := testSubject(t, candidateServer.Server)
	candidateAttemptID, candidateDirectories := acpTestDirectories(t)
	candidateScenario := parityApprovalScenario()
	candidateScenario.ApprovalScript = []ApprovalScriptEntry{
		{PromptActionID: "prompt-1", Ordinal: 0, ToolName: "write_file", Answer: ApprovalDeny},
	}
	candidateDocuments := publishParityTestEvidenceDocuments(t, candidateDirectories, candidateAttemptID, candidateScenario, candidateSubject, validExecutorACPSubprocess())
	candidateMatcher := NewApprovalMatcher(candidateScenario.ApprovalScript)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	candidateExecution, err := RunACPAttempt(ctx, candidateAttemptID, candidateSubject, candidateDirectories, candidateScenario, ACPLaunchConfig{Binary: binary}, candidateMatcher)
	if err != nil {
		t.Fatalf("RunACPAttempt() (candidate) error = %v", err)
	}
	if _, _, err := CollectEvidence(context.Background(), candidateDirectories, candidateExecution, candidateExecution.Outcome, candidateDocuments, CollectionLimits{}); err != nil {
		t.Fatalf("CollectEvidence (candidate): %v", err)
	}
	candidateArm, err := LoadParityArm(candidateDirectories)
	if err != nil {
		t.Fatalf("LoadParityArm (candidate): %v", err)
	}

	mismatches := ComparePairedArms(baselineArm, candidateArm)
	if len(mismatches) == 0 {
		t.Fatal("mismatches is empty, want at least one: the scripted approval decisions genuinely differ between the two arms")
	}
	foundToolCallMismatch := false
	for _, mismatch := range mismatches {
		if mismatch.Field == "toolCalls" {
			foundToolCallMismatch = true
		}
	}
	if !foundToolCallMismatch {
		t.Fatalf("mismatches = %+v, want a toolCalls mismatch", mismatches)
	}
}
