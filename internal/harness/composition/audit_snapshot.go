package composition

import (
	"fmt"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// AuditSession is one Session's canonical, verified, ordered RecordedEvent
// sequence from a verified audit replica.
type AuditSession struct {
	SessionID   string
	HeadVersion uint64
	Events      []domain.RecordedEvent
}

// AuditSnapshot is the canonical, verified read-back of one audit replica
// directory (docs/superpowers/specs/2026-09-02-evaluation-design.md §14):
// the audit replica is not a second transcript, it is existing canonical
// append evidence supplying facts the transcript deliberately omits,
// including model.request.recorded and policy.decision.recorded.
//
// This is the Composition-owned wrapper design §14 requires: Composition is
// the only package allowed to import an adapter, so it is the only place
// that may call sqlite.VerifyAuditReplica directly. The evaluation
// subsystem depends only on Composition's own AuditSnapshot/AuditSession
// types, never on sqlite's, so a change to sqlite's internal verification
// representation cannot silently ripple into eval.
type AuditSnapshot struct {
	HeadCommitPosition uint64
	HeadAuditDigest    string
	Sessions           []AuditSession
}

// VerifyAuditSnapshot verifies the audit replica directory and returns its
// canonical decoded contents. It requires no live SQLite connection and
// lands nothing into a database; callers collect it only after the writer
// that produced the replica has stopped (design §14/§15).
func VerifyAuditSnapshot(directory string) (AuditSnapshot, error) {
	replica, err := sqlite.VerifyAuditReplica(directory)
	if err != nil {
		return AuditSnapshot{}, fmt.Errorf("composition: verify audit snapshot: %w", err)
	}
	snapshot := AuditSnapshot{
		HeadCommitPosition: replica.HeadCommitPosition,
		HeadAuditDigest:    replica.HeadAuditDigest,
		Sessions:           make([]AuditSession, 0, len(replica.Sessions)),
	}
	for _, session := range replica.Sessions {
		snapshot.Sessions = append(snapshot.Sessions, AuditSession{
			SessionID:   session.SessionID,
			HeadVersion: session.HeadVersion,
			Events:      session.Events,
		})
	}
	return snapshot, nil
}
