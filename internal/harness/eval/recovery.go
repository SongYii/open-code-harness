package eval

import "context"

// AttemptRecoveryState classifies what one Attempt directory needs, if
// anything, after an interruption (design §18): exactly one of four
// mutually exclusive cases, determined solely by which documents this
// directory has already durably published. Determining this never reads
// or mutates anything outside the directory itself.
type AttemptRecoveryState string

const (
	// RecoveryTerminal means a valid Outcome and EvidenceManifest already
	// exist: this Attempt is immutable and done (design §12). Nothing to
	// do — in particular, never republish or reinterpret either document.
	RecoveryTerminal AttemptRecoveryState = "terminal"
	// RecoveryResumeCollectionOnly means a valid Outcome exists but no
	// EvidenceManifest does: only evidence collection may resume — never
	// the Subject, and never a second Outcome.
	RecoveryResumeCollectionOnly AttemptRecoveryState = "resume_collection_only"
	// RecoveryInspectRequired means a valid Attempt exists but no Outcome
	// does: the cold database must be inspected to determine terminal
	// facts before anything can be published (design §18). Publishing
	// that recovered Outcome requires discovering the Attempt's Session
	// ID from its own isolated database first — no API for that exists
	// yet (composition.InspectEvaluationStore requires an already-known
	// Session ID) — so this package classifies this case but does not
	// yet perform the recovery publish itself; see RecoverOutcome's own
	// doc comment.
	RecoveryInspectRequired AttemptRecoveryState = "inspect_required"
	// RecoveryUncommitted means no valid Attempt exists: only
	// uncommitted temporary state, if anything, is present (design §12:
	// Attempt is the first document written, before the Subject ever
	// starts). CleanupStaleTempFiles is the only cleanup this case needs.
	RecoveryUncommitted AttemptRecoveryState = "uncommitted"
)

// ClassifyAttemptDirectory reads directory (one Attempt's publication
// root) and reports which of design §18's four recovery cases applies. It
// never mutates directory and never touches the live database, Runtime
// Host lease, or any subprocess — classification is a pure read of
// already-published documents.
func ClassifyAttemptDirectory(directory string) (AttemptRecoveryState, error) {
	if _, err := ReadAttempt(directory); err != nil {
		return RecoveryUncommitted, nil
	}
	if _, err := ReadOutcome(directory); err != nil {
		return RecoveryInspectRequired, nil
	}
	if _, err := ReadEvidenceManifest(directory); err != nil {
		return RecoveryResumeCollectionOnly, nil
	}
	return RecoveryTerminal, nil
}

// ResumeCollection performs design §18's "Outcome without Manifest"
// recovery step: it re-stages evidence and publishes the manifest against
// the Outcome that already exists, without ever republishing or mutating
// that Outcome. execution must still report a provably stopped writer,
// exactly as a normal CollectEvidence call would require — recovery never
// reopens a writer to get one.
//
// Unlike CollectEvidence, ResumeCollection does not accept a tentative
// Outcome to finalize and publish; it reads the one already published in
// directories.Root and treats it as immutable input.
func ResumeCollection(ctx context.Context, directories AttemptRootDirectories, execution ExecutionOutcome, documents EvidenceDocuments, limits CollectionLimits) (Outcome, EvidenceManifest, error) {
	outcome, err := ReadOutcome(directories.Root)
	if err != nil {
		return Outcome{}, EvidenceManifest{}, err
	}
	return stageAndPublishManifestForExistingOutcome(ctx, directories, execution, outcome, documents, limits)
}
