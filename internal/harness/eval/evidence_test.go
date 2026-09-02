package eval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// runHappyAttempt runs a minimal, real Attempt (an approved write_file
// call, so there's a real workspace artifact) and returns everything
// CollectEvidence needs.
func runHappyAttempt(t *testing.T) (directories AttemptRootDirectories, execution ExecutionOutcome, scenario Scenario) {
	t.Helper()
	server := newApprovalProvider(t)
	subject := testSubject(t, server.Server)
	attemptID := testAttemptID(t)
	directories = testDirectories(t, attemptID)

	scenario = validScenario()
	scenario.Actions = []ScenarioAction{
		newEchoScenarioAction("prompt-1", "write the file"),
		{ID: "collect-1", Type: ActionCollect, Collect: &CollectAction{WorkspacePath: "output.txt"}},
	}
	scenario.ApprovalScript = []ApprovalScriptEntry{
		{PromptActionID: "prompt-1", Ordinal: 0, ToolName: "write_file", Answer: ApprovalAllow},
	}
	scenario.RequiredEvidenceRoles = []string{"transcript", "audit", "workspace"}
	scenario.OptionalEvidenceRoles = nil

	matcher := NewApprovalMatcher(scenario.ApprovalScript)
	var err error
	execution, err = RunAttempt(context.Background(), attemptID, subject, directories, scenario, matcher)
	if err != nil {
		t.Fatalf("RunAttempt() error = %v", err)
	}
	if !execution.WriterStopped {
		t.Fatal("RunAttempt() WriterStopped = false, want true")
	}
	return directories, execution, scenario
}

func TestCollectEvidenceHappyPath(t *testing.T) {
	directories, execution, scenario := runHappyAttempt(t)

	outcome, manifest, err := CollectEvidence(context.Background(), directories, execution, execution.Outcome, scenario, CollectionLimits{})
	if err != nil {
		t.Fatalf("CollectEvidence() error = %v", err)
	}
	if outcome.CollectionStatus != CollectionComplete {
		t.Fatalf("CollectionStatus = %q, want %q", outcome.CollectionStatus, CollectionComplete)
	}

	roles := map[string]int{}
	for _, entry := range manifest.Entries {
		if entry.State != EntryCollected {
			t.Fatalf("entry %+v not collected in the happy path", entry)
		}
		roles[entry.Role]++
	}
	if roles["transcript"] != 1 {
		t.Fatalf("transcript entries = %d, want 1", roles["transcript"])
	}
	if roles["audit"] < 1 {
		t.Fatalf("audit entries = %d, want at least 1", roles["audit"])
	}
	if roles["workspace"] != 1 {
		t.Fatalf("workspace entries = %d, want 1", roles["workspace"])
	}
	if roles["outcome"] != 1 {
		t.Fatalf("outcome entries = %d, want 1", roles["outcome"])
	}

	// The published documents must actually be readable back.
	readOutcome, err := ReadOutcome(directories.Root)
	if err != nil {
		t.Fatalf("ReadOutcome: %v", err)
	}
	if readOutcome.CollectionStatus != CollectionComplete {
		t.Fatalf("read-back Outcome.CollectionStatus = %q, want %q", readOutcome.CollectionStatus, CollectionComplete)
	}
	readManifest, err := ReadEvidenceManifest(directories.Root)
	if err != nil {
		t.Fatalf("ReadEvidenceManifest: %v", err)
	}
	if len(readManifest.Entries) != len(manifest.Entries) {
		t.Fatalf("read-back manifest has %d entries, want %d", len(readManifest.Entries), len(manifest.Entries))
	}

	if _, err := os.Stat(filepath.Join(directories.Evidence, "transcript.jsonl")); err != nil {
		t.Fatalf("transcript.jsonl not on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directories.Evidence, "workspace", "output.txt")); err != nil {
		t.Fatalf("workspace/output.txt not on disk: %v", err)
	}
}

func TestCollectEvidenceMissingRequiredWorkspaceArtifactIsPartial(t *testing.T) {
	server := newEchoProvider(t)
	subject := testSubject(t, server.Server)
	attemptID := testAttemptID(t)
	directories := testDirectories(t, attemptID)

	scenario := validScenario()
	scenario.Actions = []ScenarioAction{
		newEchoScenarioAction("prompt-1", "hello"),
		{ID: "collect-1", Type: ActionCollect, Collect: &CollectAction{WorkspacePath: "never-created.txt"}},
	}
	scenario.ApprovalScript = nil
	scenario.RequiredEvidenceRoles = []string{"transcript", "audit", "workspace"}
	scenario.OptionalEvidenceRoles = nil

	matcher := NewApprovalMatcher(scenario.ApprovalScript)
	execution, err := RunAttempt(context.Background(), attemptID, subject, directories, scenario, matcher)
	if err != nil {
		t.Fatalf("RunAttempt() error = %v", err)
	}

	outcome, manifest, err := CollectEvidence(context.Background(), directories, execution, execution.Outcome, scenario, CollectionLimits{})
	if err != nil {
		t.Fatalf("CollectEvidence() error = %v", err)
	}
	if outcome.CollectionStatus != CollectionPartial {
		t.Fatalf("CollectionStatus = %q, want %q", outcome.CollectionStatus, CollectionPartial)
	}
	found := false
	for _, entry := range manifest.Entries {
		if entry.Role != "workspace" {
			continue
		}
		found = true
		if entry.State != EntryMissing || entry.ReasonCode != "workspace_file_absent" {
			t.Fatalf("workspace entry = %+v, want missing/workspace_file_absent", entry)
		}
	}
	if !found {
		t.Fatal("no workspace entry recorded for the missing artifact")
	}
}

func TestCollectEvidenceRejectsSymlinkArtifact(t *testing.T) {
	server := newEchoProvider(t)
	subject := testSubject(t, server.Server)
	attemptID := testAttemptID(t)
	directories := testDirectories(t, attemptID)

	scenario := validScenario()
	scenario.Actions = []ScenarioAction{
		newEchoScenarioAction("prompt-1", "hello"),
		{ID: "collect-1", Type: ActionCollect, Collect: &CollectAction{WorkspacePath: "escape-link"}},
	}
	scenario.ApprovalScript = nil
	scenario.RequiredEvidenceRoles = []string{"transcript", "audit", "workspace"}
	scenario.OptionalEvidenceRoles = nil

	matcher := NewApprovalMatcher(scenario.ApprovalScript)
	execution, err := RunAttempt(context.Background(), attemptID, subject, directories, scenario, matcher)
	if err != nil {
		t.Fatalf("RunAttempt() error = %v", err)
	}

	outsideTarget := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outsideTarget, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write outside target: %v", err)
	}
	if err := os.Symlink(outsideTarget, filepath.Join(directories.Workspace, "escape-link")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	outcome, manifest, err := CollectEvidence(context.Background(), directories, execution, execution.Outcome, scenario, CollectionLimits{})
	if err != nil {
		t.Fatalf("CollectEvidence() error = %v", err)
	}
	if outcome.CollectionStatus != CollectionPartial {
		t.Fatalf("CollectionStatus = %q, want %q", outcome.CollectionStatus, CollectionPartial)
	}
	for _, entry := range manifest.Entries {
		if entry.Role != "workspace" {
			continue
		}
		if entry.State != EntryRejected || entry.ReasonCode != "workspace_file_rejected" {
			t.Fatalf("symlinked workspace entry = %+v, want rejected/workspace_file_rejected", entry)
		}
	}
	if _, err := os.Stat(filepath.Join(directories.Evidence, "workspace", "escape-link")); err == nil {
		t.Fatal("a rejected symlink must not have been copied into evidence")
	}
}

func TestCollectEvidenceRejectsHardLinkedArtifact(t *testing.T) {
	server := newEchoProvider(t)
	subject := testSubject(t, server.Server)
	attemptID := testAttemptID(t)
	directories := testDirectories(t, attemptID)

	scenario := validScenario()
	scenario.Actions = []ScenarioAction{
		newEchoScenarioAction("prompt-1", "hello"),
		{ID: "collect-1", Type: ActionCollect, Collect: &CollectAction{WorkspacePath: "linked.txt"}},
	}
	scenario.ApprovalScript = nil
	scenario.RequiredEvidenceRoles = []string{"transcript", "audit", "workspace"}
	scenario.OptionalEvidenceRoles = nil

	matcher := NewApprovalMatcher(scenario.ApprovalScript)
	execution, err := RunAttempt(context.Background(), attemptID, subject, directories, scenario, matcher)
	if err != nil {
		t.Fatalf("RunAttempt() error = %v", err)
	}

	original := filepath.Join(directories.Workspace, "original.txt")
	if err := os.WriteFile(original, []byte("shared"), 0o600); err != nil {
		t.Fatalf("write original: %v", err)
	}
	linked := filepath.Join(directories.Workspace, "linked.txt")
	if err := os.Link(original, linked); err != nil {
		t.Fatalf("create hard link: %v", err)
	}

	outcome, manifest, err := CollectEvidence(context.Background(), directories, execution, execution.Outcome, scenario, CollectionLimits{})
	if err != nil {
		t.Fatalf("CollectEvidence() error = %v", err)
	}
	if outcome.CollectionStatus != CollectionPartial {
		t.Fatalf("CollectionStatus = %q, want %q", outcome.CollectionStatus, CollectionPartial)
	}
	for _, entry := range manifest.Entries {
		if entry.Role != "workspace" {
			continue
		}
		if entry.State != EntryRejected || entry.ReasonCode != "workspace_file_rejected" {
			t.Fatalf("hard-linked workspace entry = %+v, want rejected/workspace_file_rejected", entry)
		}
	}
}

func TestCollectEvidenceOversizedWorkspaceArtifactIsTruncated(t *testing.T) {
	server := newEchoProvider(t)
	subject := testSubject(t, server.Server)
	attemptID := testAttemptID(t)
	directories := testDirectories(t, attemptID)

	scenario := validScenario()
	scenario.Actions = []ScenarioAction{
		newEchoScenarioAction("prompt-1", "hello"),
		{ID: "collect-1", Type: ActionCollect, Collect: &CollectAction{WorkspacePath: "big.txt"}},
	}
	scenario.ApprovalScript = nil
	scenario.RequiredEvidenceRoles = []string{"transcript", "audit"}
	scenario.OptionalEvidenceRoles = []string{"workspace"}

	matcher := NewApprovalMatcher(scenario.ApprovalScript)
	execution, err := RunAttempt(context.Background(), attemptID, subject, directories, scenario, matcher)
	if err != nil {
		t.Fatalf("RunAttempt() error = %v", err)
	}

	big := filepath.Join(directories.Workspace, "big.txt")
	if err := os.WriteFile(big, make([]byte, 1024), 0o600); err != nil {
		t.Fatalf("write big file: %v", err)
	}

	outcome, manifest, err := CollectEvidence(context.Background(), directories, execution, execution.Outcome, scenario, CollectionLimits{MaxWorkspaceFileBytes: 16})
	if err != nil {
		t.Fatalf("CollectEvidence() error = %v", err)
	}
	// "workspace" is optional here, so a truncated optional artifact does
	// not, on its own, prevent CollectionComplete -- it is recorded
	// honestly either way.
	if outcome.CollectionStatus != CollectionComplete {
		t.Fatalf("CollectionStatus = %q, want %q (workspace role is optional)", outcome.CollectionStatus, CollectionComplete)
	}
	for _, entry := range manifest.Entries {
		if entry.Role != "workspace" {
			continue
		}
		if entry.State != EntryTruncated || entry.ReasonCode != "workspace_file_too_large" {
			t.Fatalf("oversized workspace entry = %+v, want truncated/workspace_file_too_large", entry)
		}
	}
}

func TestCollectEvidenceRefusesWhenWriterNotStopped(t *testing.T) {
	directories, execution, scenario := runHappyAttempt(t)
	execution.WriterStopped = false

	_, _, err := CollectEvidence(context.Background(), directories, execution, execution.Outcome, scenario, CollectionLimits{})
	if err == nil {
		t.Fatal("CollectEvidence() error = nil, want a refusal when the writer was not provably stopped")
	}
	if _, statErr := os.Stat(filepath.Join(directories.Root, outcomeFilename)); statErr == nil {
		t.Fatal("outcome.json was published despite the writer-stopped refusal")
	}
}

func TestCollectEvidenceSecondCallFailsCleanlyAndNeverReplacesOutcome(t *testing.T) {
	directories, execution, scenario := runHappyAttempt(t)

	first, _, err := CollectEvidence(context.Background(), directories, execution, execution.Outcome, scenario, CollectionLimits{})
	if err != nil {
		t.Fatalf("first CollectEvidence() error = %v", err)
	}

	_, _, err = CollectEvidence(context.Background(), directories, execution, execution.Outcome, scenario, CollectionLimits{})
	if err == nil {
		t.Fatal("second CollectEvidence() error = nil, want errAlreadyPublished (outcome.json already exists)")
	}
	if !errors.Is(err, errAlreadyPublished) {
		t.Fatalf("second CollectEvidence() error = %v, want wrapping errAlreadyPublished", err)
	}

	readBack, err := ReadOutcome(directories.Root)
	if err != nil {
		t.Fatalf("ReadOutcome after failed second call: %v", err)
	}
	firstDigest, err := OutcomeDigest(first)
	if err != nil {
		t.Fatalf("OutcomeDigest(first): %v", err)
	}
	readDigest, err := OutcomeDigest(readBack)
	if err != nil {
		t.Fatalf("OutcomeDigest(readBack): %v", err)
	}
	if firstDigest != readDigest {
		t.Fatal("the first call's Outcome was replaced by the failed second call")
	}
}

func TestCollectEvidenceCrashBeforeManifestLeavesOutcomeIntactAndManifestAbsent(t *testing.T) {
	directories, execution, scenario := runHappyAttempt(t)

	injected := errors.New("injected: crash before manifest link")
	original := linkTempFile
	linkTempFile = func(oldname, newname string) error {
		if filepath.Base(newname) == manifestFilename {
			return injected
		}
		return original(oldname, newname)
	}
	t.Cleanup(func() { linkTempFile = original })

	_, _, err := CollectEvidence(context.Background(), directories, execution, execution.Outcome, scenario, CollectionLimits{})
	if !errors.Is(err, injected) {
		t.Fatalf("CollectEvidence() error = %v, want wrapping the injected manifest-link failure", err)
	}

	if _, err := ReadOutcome(directories.Root); err != nil {
		t.Fatalf("ReadOutcome after a crash before manifest publication: %v", err)
	}
	if _, err := ReadEvidenceManifest(directories.Root); err == nil {
		t.Fatal("ReadEvidenceManifest succeeded despite the injected crash before its own publication")
	}
}
