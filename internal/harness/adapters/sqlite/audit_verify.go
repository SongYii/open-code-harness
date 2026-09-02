package sqlite

import (
	"fmt"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// VerifiedAuditSession is one Session's canonically verified, ordered
// RecordedEvent sequence from a verified audit replica.
type VerifiedAuditSession struct {
	SessionID   string
	HeadVersion uint64
	Events      []domain.RecordedEvent
}

// VerifiedAuditReplica is the canonical, verified read-back of an audit
// replica directory — every layer ImportAuditReplica already checks before
// landing anything (manifest/segment digests, hash-chain continuity,
// canonical event re-encoding, per-session sequence/version continuity,
// known schema versions, and complete domain replay) — exposed without
// requiring a live Store connection or a destination database. It exists so
// a caller outside this package can snapshot an Attempt's audit replica as
// evidence; the milestone 10 evaluation subsystem's design
// (docs/superpowers/specs/2026-09-02-evaluation-design.md §14) requires
// exactly this operation, wrapped by Composition so the evaluation package
// itself never imports sqlite.
type VerifiedAuditReplica struct {
	HeadCommitPosition uint64
	HeadAuditDigest    string
	Sessions           []VerifiedAuditSession
}

// VerifyAuditReplica verifies the audit replica directory and returns its
// canonical decoded contents without landing anything into a database.
// Every verification layer ImportAuditReplica performs before opening a
// destination Store runs here too; VerifyAuditReplica simply stops short of
// landing the result, so it can run against a read-only replica directory
// with no live SQLite connection at all.
func VerifyAuditReplica(directory string) (VerifiedAuditReplica, error) {
	replica, err := readAndVerifyReplica(directory)
	if err != nil {
		return VerifiedAuditReplica{}, err
	}

	sessions := make(map[string]*VerifiedAuditSession)
	order := make([]string, 0)
	for _, batch := range replica.batches {
		session := sessions[batch.SessionID]
		if session == nil {
			session = &VerifiedAuditSession{SessionID: batch.SessionID}
			sessions[batch.SessionID] = session
			order = append(order, batch.SessionID)
		}
		for _, payload := range batch.Events {
			record, err := domain.UnmarshalRecordedEvent(payload)
			if err != nil {
				// readAndVerifyReplica already proved every payload decodes
				// and re-encodes canonically; this can only fail if that
				// invariant were broken, which would itself be a bug.
				return VerifiedAuditReplica{}, fmt.Errorf("sqlite: verify audit replica: re-decode event: %w", err)
			}
			session.Events = append(session.Events, record)
		}
		session.HeadVersion = batch.LastSequence
	}

	result := VerifiedAuditReplica{
		HeadCommitPosition: replica.manifest.HeadCommitPosition,
		HeadAuditDigest:    replica.manifest.HeadAuditDigest,
	}
	for _, sessionID := range order {
		result.Sessions = append(result.Sessions, *sessions[sessionID])
	}
	return result, nil
}
