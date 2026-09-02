package eval

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/composition"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// CollectionLimits bounds one Attempt's post-shutdown evidence collection
// (design §14/§19). Zero fields take a generous, documented default via
// withDefaults, matching composition.Config's own zero-means-default
// convention.
type CollectionLimits struct {
	MaxWorkspaceFileBytes int64
	MaxTotalBytes         int64
	MaxFiles              int
	Timeout               time.Duration
}

const (
	defaultMaxWorkspaceFileBytes = 16 << 20  // 16 MiB
	defaultMaxCollectionBytes    = 256 << 20 // 256 MiB
	defaultMaxCollectionFiles    = 4096
	defaultCollectionTimeout     = 2 * time.Minute
)

func (limits CollectionLimits) withDefaults() CollectionLimits {
	if limits.MaxWorkspaceFileBytes <= 0 {
		limits.MaxWorkspaceFileBytes = defaultMaxWorkspaceFileBytes
	}
	if limits.MaxTotalBytes <= 0 {
		limits.MaxTotalBytes = defaultMaxCollectionBytes
	}
	if limits.MaxFiles <= 0 {
		limits.MaxFiles = defaultMaxCollectionFiles
	}
	if limits.Timeout <= 0 {
		limits.Timeout = defaultCollectionTimeout
	}
	return limits
}

// collectionBudget accumulates ManifestEntry rows and enforces
// CollectionLimits' file-count and total-byte bounds as entries are
// tracked, so a single pass both stages evidence and produces the
// manifest's own inventory.
type collectionBudget struct {
	limits     CollectionLimits
	entries    []ManifestEntry
	totalBytes int64
	fileCount  int
}

// track records one resolved ManifestEntry. A collected entry that would
// push the running total past limits.MaxFiles or limits.MaxTotalBytes is
// instead recorded as rejected, with the original bytes already removed
// from disk by the caller before track is reached (stageWorkspaceArtifact
// and the transcript/audit paths below each remove their own destination
// file on any post-hoc rejection here).
func (budget *collectionBudget) track(entry ManifestEntry) {
	if entry.State == EntryCollected {
		if budget.fileCount+1 > budget.limits.MaxFiles {
			entry = ManifestEntry{Path: entry.Path, Role: entry.Role, MediaType: entry.MediaType,
				Required: entry.Required, State: EntryRejected, ReasonCode: "collection_file_count_exceeded"}
		} else if budget.totalBytes+entry.ByteLength > budget.limits.MaxTotalBytes {
			entry = ManifestEntry{Path: entry.Path, Role: entry.Role, MediaType: entry.MediaType,
				Required: entry.Required, State: EntryRejected, ReasonCode: "collection_total_bytes_exceeded"}
		} else {
			budget.fileCount++
			budget.totalBytes += entry.ByteLength
		}
	}
	budget.entries = append(budget.entries, entry)
}

// CollectEvidence collects bounded evidence for one already-executed
// Attempt after its writer has stopped (design §14), finalizes and
// atomically publishes its Outcome with the resulting collection status,
// and publishes the EvidenceManifest last as the commit marker for a
// scoreable Attempt (design §12/§20).
//
// execution must report a provably stopped writer (RunAttempt's own
// WriterStopped, or a future crash-recovery equivalent) — CollectEvidence
// refuses outright otherwise: every cold evaluation API it calls already
// refuses a live lease on its own, but a well-behaved caller must not even
// try. tentativeOutcome is the Outcome RunAttempt (or recovery) already
// decided from live execution facts; CollectEvidence never changes its
// Status, Code, Message, or TerminalSession — only CollectionStatus,
// finalized from what this call actually collects, and EndedAt is left as
// the execution-time value RunAttempt recorded, since collection is not
// part of the Attempt's own execution window.
//
// Publication order is two-phase (design §14): every evidence file except
// the Outcome's own copy is staged and verified first; the finalized
// Outcome is then atomically published (PublishOutcome, write-once); that
// exact published Outcome is staged as its own manifest entry; and only
// then is the manifest itself published (PublishEvidenceManifest) as the
// commit marker. A failure after Outcome publication still returns
// whatever manifest could be assembled — Outcome is never republished or
// replaced.
func CollectEvidence(ctx context.Context, directories AttemptRootDirectories, execution ExecutionOutcome, tentativeOutcome Outcome, scenario Scenario, limits CollectionLimits) (Outcome, EvidenceManifest, error) {
	if !execution.WriterStopped {
		return Outcome{}, EvidenceManifest{}, fmt.Errorf("eval: collect evidence: refusing to collect: writer was not provably stopped")
	}
	limits = limits.withDefaults()
	collectCtx := ctx
	if limits.Timeout > 0 {
		var cancel context.CancelFunc
		collectCtx, cancel = context.WithTimeout(ctx, limits.Timeout)
		defer cancel()
	}

	startedAt := time.Now().UTC()
	budget := &collectionBudget{limits: limits}

	collectTranscriptAndAudit(collectCtx, directories, execution, budget)
	collectWorkspaceArtifacts(directories, scenario, budget)

	requiredMissing := false
	for _, entry := range budget.entries {
		if entry.Required && entry.State != EntryCollected {
			requiredMissing = true
			break
		}
	}
	collectionStatus := CollectionComplete
	if requiredMissing {
		collectionStatus = CollectionPartial
	}

	finalOutcome := tentativeOutcome
	finalOutcome.CollectionStatus = collectionStatus

	outcomeDirectory := directories.Root
	if err := PublishOutcome(outcomeDirectory, finalOutcome); err != nil {
		return Outcome{}, EvidenceManifest{}, fmt.Errorf("eval: collect evidence: %w", err)
	}

	outcomeDigest, err := OutcomeDigest(finalOutcome)
	if err != nil {
		return finalOutcome, EvidenceManifest{}, fmt.Errorf("eval: collect evidence: digest published outcome: %w", err)
	}

	outcomeCopyPath := filepath.Join(directories.Evidence, "outcome.json")
	outcomeCopyStaged, err := stageOutcomeCopy(outcomeDirectory, outcomeCopyPath)
	if err != nil {
		return finalOutcome, EvidenceManifest{}, fmt.Errorf("eval: collect evidence: stage outcome evidence copy: %w", err)
	}
	budget.track(ManifestEntry{
		Path: "outcome.json", Role: "outcome", MediaType: "application/json",
		Required: true, State: EntryCollected,
		SHA256: outcomeCopyStaged.sha256, ByteLength: outcomeCopyStaged.byteLength,
	})

	endedAt := time.Now().UTC()
	manifest := EvidenceManifest{
		FormatVersion:       FormatVersion,
		Schema:              SchemaEvidenceManifest,
		AttemptID:           finalOutcome.AttemptID,
		OutcomeDigest:       outcomeDigest,
		Entries:             budget.entries,
		TotalBytes:          budget.totalBytes,
		FileCount:           budget.fileCount,
		CollectionStartedAt: startedAt,
		CollectionEndedAt:   endedAt,
	}
	if err := PublishEvidenceManifest(directories.Root, manifest); err != nil {
		return finalOutcome, manifest, fmt.Errorf("eval: collect evidence: %w", err)
	}
	return finalOutcome, manifest, nil
}

// collectTranscriptAndAudit attempts design §14's baseline infrastructure
// evidence: a complete native transcript and a regenerated, verified audit
// replica proven to include the pinned Session head. Both are always
// attempted and always marked Required — they are producible from the
// database alone once the writer has stopped, independent of anything the
// Scenario declares — so any failure here is recorded, never silently
// dropped, and still allows the rest of collection (and Outcome
// publication) to proceed.
func collectTranscriptAndAudit(ctx context.Context, directories AttemptRootDirectories, execution ExecutionOutcome, budget *collectionBudget) {
	sessionID, err := domain.ParseSessionID(execution.SessionID)
	if err != nil {
		budget.track(missingEntry("transcript.jsonl", "transcript", "application/x-ndjson", true, "invalid_session_id", err))
		return
	}
	inspection, err := composition.InspectEvaluationStore(ctx, AttemptDatabasePath(directories), sessionID)
	if err != nil {
		budget.track(missingEntry("transcript.jsonl", "transcript", "application/x-ndjson", true, "inspect_evaluation_store_failed", err))
		return
	}

	transcriptPath := filepath.Join(directories.Evidence, "transcript.jsonl")
	auditDestination := filepath.Join(directories.Evidence, "audit")
	evidence, err := composition.ExportEvaluationEvidence(ctx, inspection, composition.EvaluationExportDestinations{
		TranscriptPath: transcriptPath,
		AuditDirectory: auditDestination,
	})
	if err != nil {
		budget.track(missingEntry("transcript.jsonl", "transcript", "application/x-ndjson", true, "export_evaluation_evidence_failed", err))
		return
	}

	transcriptDigest, err := digestFile(transcriptPath)
	if err != nil {
		budget.track(missingEntry("transcript.jsonl", "transcript", "application/x-ndjson", true, "transcript_unreadable_after_export", err))
	} else if "sha256:"+transcriptDigest.sha256 != evidence.TranscriptDigest {
		budget.track(missingEntry("transcript.jsonl", "transcript", "application/x-ndjson", true, "transcript_digest_mismatch", nil))
	} else {
		budget.track(ManifestEntry{
			Path: "transcript.jsonl", Role: "transcript", MediaType: "application/x-ndjson",
			Required: true, State: EntryCollected,
			SHA256: transcriptDigest.sha256, ByteLength: transcriptDigest.byteLength,
		})
	}

	auditEntries, err := collectAuditEntries(auditDestination)
	if err != nil {
		budget.track(missingEntry("audit", "audit", "application/x-ndjson", true, "audit_replica_unreadable_after_export", err))
		return
	}
	if len(auditEntries) == 0 {
		budget.track(missingEntry("audit", "audit", "application/x-ndjson", true, "audit_replica_empty", nil))
		return
	}
	for _, entry := range auditEntries {
		budget.track(entry)
	}
}

// collectAuditEntries walks an already-regenerated-and-verified audit
// replica directory (composition.ExportEvaluationEvidence's own output,
// never Subject-writable) and returns one collected ManifestEntry per
// regular file within it, rooted at "audit/".
func collectAuditEntries(auditDirectory string) ([]ManifestEntry, error) {
	var entries []ManifestEntry
	walkErr := filepath.WalkDir(auditDirectory, func(walkPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(auditDirectory, walkPath)
		if err != nil {
			return err
		}
		digest, err := digestFile(walkPath)
		if err != nil {
			return err
		}
		entries = append(entries, ManifestEntry{
			Path: path.Join("audit", filepath.ToSlash(relative)), Role: "audit", MediaType: "application/x-ndjson",
			Required: true, State: EntryCollected,
			SHA256: digest.sha256, ByteLength: digest.byteLength,
		})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return entries, nil
}

// collectWorkspaceArtifacts stages every collect action's declared
// WorkspacePath (design §7) from the live Attempt workspace into the
// evidence directory, applying the same fixture-isolation discipline
// CopyFixture enforces on the way in. A Collect action naming a
// VerifierFact rather than a WorkspacePath contributes no manifest entry
// here — it is a scoring-time reference (Task 9), not a collected file.
func collectWorkspaceArtifacts(directories AttemptRootDirectories, scenario Scenario, budget *collectionBudget) {
	required := containsString(scenario.RequiredEvidenceRoles, "workspace")
	for _, action := range scenario.Actions {
		if action.Type != ActionCollect || action.Collect == nil || action.Collect.WorkspacePath == "" {
			continue
		}
		relative := action.Collect.WorkspacePath
		entryPath := path.Join("workspace", relative)
		sourcePath := filepath.Join(directories.Workspace, filepath.FromSlash(relative))
		if !pathWithin(sourcePath, directories.Workspace) {
			budget.track(rejectedEntry(entryPath, "workspace", required, "workspace_path_escapes_root", nil))
			continue
		}
		destinationPath := filepath.Join(directories.Evidence, "workspace", filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(destinationPath), 0o700); err != nil {
			budget.track(missingEntry(entryPath, "workspace", "application/octet-stream", required, "workspace_evidence_dir_create_failed", err))
			continue
		}
		staged, err := stageWorkspaceArtifact(sourcePath, destinationPath, budget.limits.MaxWorkspaceFileBytes)
		switch {
		case err == nil:
			budget.track(ManifestEntry{
				Path: entryPath, Role: "workspace", MediaType: "application/octet-stream",
				Required: required, State: EntryCollected,
				SHA256: staged.sha256, ByteLength: staged.byteLength,
			})
		case errors.Is(err, fs.ErrNotExist):
			budget.track(missingEntry(entryPath, "workspace", "application/octet-stream", required, "workspace_file_absent", nil))
		case errors.Is(err, errArtifactRejected):
			budget.track(rejectedEntry(entryPath, "workspace", required, "workspace_file_rejected", err))
		case errors.Is(err, errArtifactTruncated):
			budget.track(ManifestEntry{
				Path: entryPath, Role: "workspace", MediaType: "application/octet-stream",
				Required: required, State: EntryTruncated, ReasonCode: "workspace_file_too_large",
				Detail: boundedRedactedMessage(err.Error()),
			})
		default:
			budget.track(missingEntry(entryPath, "workspace", "application/octet-stream", required, "workspace_file_read_failed", err))
		}
	}
}

func missingEntry(entryPath, role, mediaType string, required bool, reasonCode string, err error) ManifestEntry {
	entry := ManifestEntry{Path: entryPath, Role: role, MediaType: mediaType, Required: required, State: EntryMissing, ReasonCode: reasonCode}
	if err != nil {
		entry.Detail = boundedRedactedMessage(err.Error())
	}
	return entry
}

func rejectedEntry(entryPath, role string, required bool, reasonCode string, err error) ManifestEntry {
	entry := ManifestEntry{Path: entryPath, Role: role, MediaType: "application/octet-stream", Required: required, State: EntryRejected, ReasonCode: reasonCode}
	if err != nil {
		entry.Detail = boundedRedactedMessage(err.Error())
	}
	return entry
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// stageOutcomeCopy copies the just-published outcome.json byte-for-byte
// into the evidence directory so the manifest can reference it like any
// other collected artifact, and returns its digest. It reads back the
// exact published bytes rather than re-marshaling the Outcome value, so
// the recorded SHA-256 can never disagree with what PublishOutcome wrote.
func stageOutcomeCopy(outcomeDirectory, destinationPath string) (stagedArtifact, error) {
	sourcePath := filepath.Join(outcomeDirectory, outcomeFilename)
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return stagedArtifact{}, err
	}
	if err := os.WriteFile(destinationPath, data, 0o600); err != nil {
		return stagedArtifact{}, err
	}
	return digestFile(destinationPath)
}
