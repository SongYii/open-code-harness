package composition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/transcript"
)

// EvaluationSessionTerminalFacts is Composition's own copy of the bounded
// Session/Turn/compaction terminal facts InspectEvaluationStore returns
// (design §14), so a caller never needs sqlite's package in scope to read
// them.
type EvaluationSessionTerminalFacts struct {
	Status           string
	Open             bool
	Running          bool
	ActiveTurnID     string
	CompactionActive bool
}

// EvaluationInspection is Composition's own copy of the pinned identity
// InspectEvaluationStore returns (design §14): a database identity, a
// Session head sequence, the commit position of the append that introduced
// it, and the store's own global head commit position. The two head
// numbers are different coordinate systems -- one counts this Session's own
// events, the other every committed append across the whole database -- and
// this type never compares them to one another; only
// SessionHeadAppendCommitPosition links them, and only in one direction (an
// inclusion check, never a numeric equality). This is the only "identity"
// a caller in another package (eventually eval) receives; the concrete
// sqlite.EvaluationInspection type it wraps never crosses the package
// boundary, so eval, which must never import sqlite, still never needs to.
type EvaluationInspection struct {
	DatabasePath                    string
	SessionID                       string
	StoreHeadCommitPosition         uint64
	SessionHeadSequence             uint64
	SessionHeadAppendCommitPosition uint64
	Terminal                        EvaluationSessionTerminalFacts
}

// InspectEvaluationStore opens databasePath cold -- no migration, no writer
// lease -- verifies sessionID's canonical event chain against the
// maintained session-heads projection, and pins its identity for a later
// ExportEvaluationEvidence call (design §14). It refuses while a Runtime
// Host lease is currently live rather than waiting for, taking over, or
// signaling it, and it creates no destination; the database is closed
// before this function returns either way.
func InspectEvaluationStore(ctx context.Context, databasePath string, sessionID domain.SessionID) (EvaluationInspection, error) {
	inspection, err := sqlite.InspectEvaluationStore(ctx, databasePath, sessionID)
	if err != nil {
		return EvaluationInspection{}, fmt.Errorf("composition: inspect evaluation store: %w", err)
	}
	return toEvaluationInspection(inspection), nil
}

func toEvaluationInspection(inspection sqlite.EvaluationInspection) EvaluationInspection {
	return EvaluationInspection{
		DatabasePath:                    inspection.DatabasePath,
		SessionID:                       inspection.SessionID,
		StoreHeadCommitPosition:         inspection.StoreHeadCommitPosition,
		SessionHeadSequence:             inspection.SessionHeadSequence,
		SessionHeadAppendCommitPosition: inspection.SessionHeadAppendCommitPosition,
		Terminal: EvaluationSessionTerminalFacts{
			Status:           inspection.Terminal.Status,
			Open:             inspection.Terminal.Open,
			Running:          inspection.Terminal.Running,
			ActiveTurnID:     inspection.Terminal.ActiveTurnID,
			CompactionActive: inspection.Terminal.CompactionActive,
		},
	}
}

func (inspection EvaluationInspection) toSQLite() sqlite.EvaluationInspection {
	return sqlite.EvaluationInspection{
		DatabasePath:                    inspection.DatabasePath,
		SessionID:                       inspection.SessionID,
		StoreHeadCommitPosition:         inspection.StoreHeadCommitPosition,
		SessionHeadSequence:             inspection.SessionHeadSequence,
		SessionHeadAppendCommitPosition: inspection.SessionHeadAppendCommitPosition,
	}
}

// EvaluationExportDestinations names the two empty, caller-owned staging
// paths ExportEvaluationEvidence writes into: TranscriptPath is a single
// file it creates (refusing to overwrite one that already exists);
// AuditDirectory must not yet exist or must be empty, matching
// ExportConsistent's own requirement.
type EvaluationExportDestinations struct {
	TranscriptPath string
	AuditDirectory string
}

// EvaluationEvidence is what ExportEvaluationEvidence produced and verified
// (design §14): a transcript digest, an audit head digest, and the pinned
// Session/store identity the export re-verified.
type EvaluationEvidence struct {
	TranscriptDigest                string
	TranscriptResult                transcript.Result
	AuditHeadDigest                 string
	SessionHeadSequence             uint64
	StoreHeadCommitPosition         uint64
	SessionHeadAppendCommitPosition uint64
}

// ExportEvaluationEvidence reopens inspection's database cold, refuses if a
// Runtime Host lease is currently live or if the database's current
// identity/heads disagree with what inspection pinned (design §14: a
// database that changed since InspectEvaluationStore ran is refused, never
// silently exported against a moving target), exports a complete native
// transcript to destinations.TranscriptPath, regenerates a canonical audit
// replica directly from database append records into
// destinations.AuditDirectory, and verifies the generated replica. It then
// proves the regenerated audit reaches the append that introduced the
// pinned Session head -- design §14's inclusion proof -- before returning;
// a replica that stops short of that append is not usable evidence and is
// refused rather than returned as a partial result. It never copies an
// already-exported live replica and never touches the live exporter's
// checkpoint or outbox.
func ExportEvaluationEvidence(ctx context.Context, inspection EvaluationInspection, destinations EvaluationExportDestinations) (EvaluationEvidence, error) {
	if destinations.TranscriptPath == "" || destinations.AuditDirectory == "" {
		return EvaluationEvidence{}, fmt.Errorf("composition: export evaluation evidence: transcript path and audit directory are required")
	}
	sessionID, err := domain.ParseSessionID(inspection.SessionID)
	if err != nil {
		return EvaluationEvidence{}, fmt.Errorf("composition: export evaluation evidence: %w", err)
	}

	export, err := sqlite.OpenEvaluationExport(ctx, inspection.toSQLite())
	if err != nil {
		return EvaluationEvidence{}, fmt.Errorf("composition: export evaluation evidence: %w", err)
	}
	defer export.Close()

	transcriptFile, err := os.OpenFile(destinations.TranscriptPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return EvaluationEvidence{}, fmt.Errorf("composition: export evaluation evidence: create transcript destination: %w", err)
	}
	hasher := sha256.New()
	result, writeErr := transcript.WriteSession(ctx, export, sessionID, time.Now().UTC(), io.MultiWriter(transcriptFile, hasher))
	closeErr := transcriptFile.Close()
	if writeErr != nil {
		return EvaluationEvidence{}, fmt.Errorf("composition: export evaluation evidence: write transcript: %w", writeErr)
	}
	if closeErr != nil {
		return EvaluationEvidence{}, fmt.Errorf("composition: export evaluation evidence: close transcript: %w", closeErr)
	}
	transcriptDigest := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	current := export.Inspection()
	if result.HeadSequence != current.SessionHeadSequence {
		return EvaluationEvidence{}, fmt.Errorf(
			"composition: export evaluation evidence: transcript head sequence %d disagrees with the pinned session head %d",
			result.HeadSequence, current.SessionHeadSequence)
	}

	verified, err := export.RegenerateAudit(ctx, destinations.AuditDirectory)
	if err != nil {
		return EvaluationEvidence{}, fmt.Errorf("composition: export evaluation evidence: %w", err)
	}
	if err := verifyAuditIncludesSessionHead(verified.HeadCommitPosition, current.SessionHeadAppendCommitPosition); err != nil {
		return EvaluationEvidence{}, err
	}

	return EvaluationEvidence{
		TranscriptDigest:                transcriptDigest,
		TranscriptResult:                result,
		AuditHeadDigest:                 verified.HeadAuditDigest,
		SessionHeadSequence:             current.SessionHeadSequence,
		StoreHeadCommitPosition:         current.StoreHeadCommitPosition,
		SessionHeadAppendCommitPosition: current.SessionHeadAppendCommitPosition,
	}, nil
}

// verifyAuditIncludesSessionHead is design §14's inclusion proof, isolated
// as its own function so it is directly testable: a regenerated audit
// replica whose head commit position falls short of the append that
// introduced the pinned Session head is not usable evidence, however
// successfully it verified on its own terms. auditHeadCommitPosition and
// sessionHeadAppendCommitPosition are the same coordinate system (both
// global commit positions), so this is an ordinary inequality, not the
// cross-coordinate comparison EvaluationInspection's own doc comment warns
// against.
func verifyAuditIncludesSessionHead(auditHeadCommitPosition, sessionHeadAppendCommitPosition uint64) error {
	if auditHeadCommitPosition < sessionHeadAppendCommitPosition {
		return fmt.Errorf(
			"composition: export evaluation evidence: regenerated audit head %d does not include the session-head append at %d",
			auditHeadCommitPosition, sessionHeadAppendCommitPosition)
	}
	return nil
}
