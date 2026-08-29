package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// verifiedReplica is a fully verified, ordered decoding of an audit replica.
type verifiedReplica struct {
	manifest manifestGeneration
	batches  []auditBatch
}

// readAndVerifyReplica performs the parent's verification layers over a
// replica directory before anything is imported: manifest and segment
// digests; continuous commit positions and batch hash chain; event payload
// canonicality; per-session sequence continuity; expected-version
// transitions; known schema; complete domain replay. The heads projection
// is verified after landing (layer eight).
func readAndVerifyReplica(directory string) (*verifiedReplica, error) {
	entries, err := os.ReadDir(filepath.Join(directory, "manifests"))
	if err != nil {
		return nil, fmt.Errorf("audit import: read manifests: %w", err)
	}
	var generations []manifestGeneration
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(directory, "manifests", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("audit import: read manifest: %w", err)
		}
		var generation manifestGeneration
		if err := json.Unmarshal(raw, &generation); err != nil {
			return nil, fmt.Errorf("audit import: manifest %s is damaged: %w", entry.Name(), err)
		}
		if generation.FormatVersion != manifestFormatVersion {
			return nil, fmt.Errorf("audit import: manifest format version %d is unsupported", generation.FormatVersion)
		}
		generations = append(generations, generation)
	}
	if len(generations) == 0 {
		return nil, fmt.Errorf("audit import: no manifest generation found")
	}
	sort.Slice(generations, func(i, j int) bool {
		return generations[i].HeadCommitPosition > generations[j].HeadCommitPosition
	})
	manifest := generations[0]

	// Layer 1: manifest and segment digests.
	var batches []auditBatch
	expectedPosition := uint64(1)
	chain := auditGenesisDigest
	perSession := make(map[string]*sessionReplayAccumulator)
	for _, segment := range manifest.Segments {
		if segment.FirstCommitPosition != expectedPosition {
			return nil, fmt.Errorf("audit import: manifest segment ranges are not continuous")
		}
		raw, err := os.ReadFile(filepath.Join(directory, "segments", segment.File))
		if err != nil {
			return nil, fmt.Errorf("audit import: sealed segment missing: %w", err)
		}
		digest := sha256.Sum256(raw)
		if hex.EncodeToString(digest[:]) != segment.SHA256 || int64(len(raw)) != segment.Bytes {
			return nil, fmt.Errorf("audit import: segment %s disagrees with manifest", segment.File)
		}
		for _, line := range splitSegmentLines(raw) {
			codec, err := auditCodecFor(auditFormatVersionV1)
			if err != nil {
				return nil, err
			}
			batch, err := codec.Decode([]byte(line))
			if err != nil {
				// A torn or tampered line refuses the whole import; nothing
				// is silently discarded.
				return nil, fmt.Errorf("audit import: envelope at position %d fails verification: %w", expectedPosition, err)
			}
			// Layer 2: continuous positions and hash chain.
			if batch.CommitPosition != expectedPosition {
				return nil, fmt.Errorf("audit import: commit position %d breaks continuity at %d", batch.CommitPosition, expectedPosition)
			}
			if batch.PreviousDigest != chain {
				return nil, fmt.Errorf("audit import: hash chain break at position %d", batch.CommitPosition)
			}
			chain = batch.BatchDigest
			// Layer 3: canonical event payloads.
			records := make([]domain.RecordedEvent, 0, len(batch.Events))
			for i, payload := range batch.Events {
				record, err := domain.UnmarshalRecordedEvent(payload)
				if err != nil {
					return nil, fmt.Errorf("audit import: event %d of position %d does not decode: %w", i, batch.CommitPosition, err)
				}
				recanonical, err := domain.MarshalRecordedEvent(record)
				if err != nil || !bytesEqual(recanonical, payload) {
					return nil, fmt.Errorf("audit import: event %d of position %d is not canonical", i, batch.CommitPosition)
				}
				if auditEventPayloadDigest(payload) != sha256.Sum256(recanonical) {
					return nil, fmt.Errorf("audit import: event %d of position %d fails its payload digest", i, batch.CommitPosition)
				}
				// Layer 6: known schema versions.
				if record.SchemaVersion != 1 {
					return nil, fmt.Errorf("audit import: event schema version %d is unknown", record.SchemaVersion)
				}
				records = append(records, record)
			}
			session := perSession[batch.SessionID]
			if session == nil {
				session = &sessionReplayAccumulator{}
				perSession[batch.SessionID] = session
			}
			// Layer 4: continuous per-session sequences.
			for i, record := range records {
				want := session.version + uint64(i) + 1
				if record.Sequence != want {
					return nil, fmt.Errorf("audit import: session %s sequence %d breaks continuity at %d", batch.SessionID, record.Sequence, want)
				}
				if record.SessionID != domain.SessionID(batch.SessionID) {
					return nil, fmt.Errorf("audit import: record session identity disagrees with envelope")
				}
			}
			// Layer 5: expected-version transitions.
			if batch.ExpectedVersion != session.version {
				return nil, fmt.Errorf("audit import: session %s expected version %d does not follow version %d", batch.SessionID, batch.ExpectedVersion, session.version)
			}
			session.records = append(session.records, records...)
			session.version = batch.LastSequence
			expectedPosition++
			batches = append(batches, batch)
		}
	}
	if manifest.HeadCommitPosition != expectedPosition-1 {
		return nil, fmt.Errorf("audit import: manifest head disagrees with segment contents")
	}
	if hex.EncodeToString(chain[:]) != manifest.HeadAuditDigest {
		return nil, fmt.Errorf("audit import: manifest head digest disagrees with the chain")
	}
	// Layer 7: complete domain replay invariants.
	for sessionID, session := range perSession {
		if _, err := domain.Replay(session.records); err != nil {
			return nil, fmt.Errorf("audit import: session %s fails domain replay: %w", sessionID, err)
		}
	}
	return &verifiedReplica{manifest: manifest, batches: batches}, nil
}

type sessionReplayAccumulator struct {
	records []domain.RecordedEvent
	version uint64
}

func splitSegmentLines(raw []byte) []string {
	var lines []string
	start := 0
	for i, b := range raw {
		if b == '\n' {
			lines = append(lines, string(raw[start:i]))
			start = i + 1
		}
	}
	if start < len(raw) {
		// A torn final line without its newline refuses the import.
		lines = append(lines, string(raw[start:])+"\x00torn")
	}
	return lines
}

// ImportAuditReplica verifies a replica directory and lands it in a new or
// empty database. Automatic merge into an active database is forbidden.
func ImportAuditReplica(ctx context.Context, sourceDirectory string, config Config) (*Store, error) {
	replica, err := readAndVerifyReplica(sourceDirectory)
	if err != nil {
		return nil, err
	}
	store, err := Open(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := store.landReplica(ctx, replica); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

func (store *Store) landReplica(ctx context.Context, replica *verifiedReplica) error {
	var existing int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM events").Scan(&existing); err != nil {
		return mapStorageError(err, "")
	}
	if existing != 0 {
		return fmt.Errorf("audit import: destination is not empty; automatic merge into an active database is forbidden")
	}

	store.writeMu.Lock()
	defer store.writeMu.Unlock()
	conn := store.writer
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return mapStorageError(err, "")
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	position := uint64(0)
	heads := make(map[string]sessionHeadState)
	for _, batch := range replica.batches {
		position = batch.CommitPosition
		eventCount := 0
		if err := upsertStream(ctx, conn, batch.SessionID, batch.LastSequence, position); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO event_appends (append_id, commit_position, session_id, expected_version, first_sequence, last_sequence, event_count, command_id, request_digest, writer_runtime_id, writer_fencing_token, audit_format_version, previous_audit_digest, batch_audit_digest, committed_at_unix) "+
				"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'imported', 1, ?, ?, ?, ?)",
			batch.AppendID, batch.CommitPosition, batch.SessionID, batch.ExpectedVersion,
			batch.FirstSequence, batch.LastSequence, len(batch.Events), batch.CommandID,
			make([]byte, 32), auditFormatVersionV1, batch.PreviousDigest[:], batch.BatchDigest[:],
			batch.CommittedAtUnix); err != nil {
			return mapStorageError(err, domain.SessionID(batch.SessionID))
		}
		for i, payload := range batch.Events {
			record, err := domain.UnmarshalRecordedEvent(payload)
			if err != nil {
				return err
			}
			eventCount++
			if _, err := conn.ExecContext(ctx,
				"INSERT INTO events (session_id, sequence, event_id, append_id, order_in_append, command_id, event_type, schema_version, occurred_at, payload, payload_digest) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
				batch.SessionID, record.Sequence, string(record.ID), batch.AppendID, i+1, batch.CommandID,
				record.Event.EventType(), record.SchemaVersion,
				record.OccurredAt.Format(time.RFC3339Nano), payload,
				payloadDigestOf(payload)); err != nil {
				return mapStorageError(err, domain.SessionID(batch.SessionID))
			}
			switch typed := record.Event.(type) {
			case domain.TurnStarted:
				if _, err := conn.ExecContext(ctx,
					"INSERT INTO domain_identities (session_id, identity_kind, identity_id, introducing_event_id) VALUES (?, 'turn', ?, ?)",
					batch.SessionID, string(typed.TurnID), string(record.ID)); err != nil {
					return mapStorageError(err, domain.SessionID(batch.SessionID))
				}
			case domain.AssistantMessageStarted:
				if _, err := conn.ExecContext(ctx,
					"INSERT INTO domain_identities (session_id, identity_kind, identity_id, introducing_event_id) VALUES (?, 'item', ?, ?)",
					batch.SessionID, string(typed.ItemID), string(record.ID)); err != nil {
					return mapStorageError(err, domain.SessionID(batch.SessionID))
				}
			case domain.ToolCallStarted:
				if _, err := conn.ExecContext(ctx,
					"INSERT INTO domain_identities (session_id, identity_kind, identity_id, introducing_event_id) VALUES (?, 'item', ?, ?)",
					batch.SessionID, string(typed.ItemID), string(record.ID)); err != nil {
					return mapStorageError(err, domain.SessionID(batch.SessionID))
				}
			}
			head := heads[batch.SessionID]
			head, err = applyHeadTransition(head, record.Event)
			if err != nil {
				return newStoreError(application.StoreCodeCorrupt, domain.SessionID(batch.SessionID), err)
			}
			heads[batch.SessionID] = head
		}
		head := heads[batch.SessionID]
		head.position = position
		heads[batch.SessionID] = head
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO session_heads (session_id, workspace_root, status, active_turn_id, active_item_id, updated_at_commit_position) VALUES (?, ?, ?, ?, ?, ?) "+
				"ON CONFLICT(session_id) DO UPDATE SET workspace_root = excluded.workspace_root, status = excluded.status, active_turn_id = excluded.active_turn_id, active_item_id = excluded.active_item_id, updated_at_commit_position = excluded.updated_at_commit_position",
			batch.SessionID, head.workspaceRoot, head.status, head.turn, head.item, position); err != nil {
			return mapStorageError(err, domain.SessionID(batch.SessionID))
		}
	}
	if _, err := conn.ExecContext(ctx,
		"UPDATE store_metadata SET head_commit_position = ?, head_audit_digest = ? WHERE id = 1",
		position, replica.batches[len(replica.batches)-1].BatchDigest[:]); err != nil {
		return mapStorageError(err, "")
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return mapStorageError(err, "")
	}
	committed = true

	// Layer 8: rebuilt heads projection agrees with canonical replay.
	if err := store.RebuildAndVerifySessionHeads(ctx); err != nil {
		return err
	}
	var landed int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM events").Scan(&landed); err != nil {
		return mapStorageError(err, "")
	}
	if landed == 0 {
		return newStoreError(application.StoreCodeCorrupt, "", wrapDetail("import landed no events", nil))
	}
	return nil
}

func upsertStream(ctx context.Context, conn *sql.Conn, sessionID string, version, position uint64) error {
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO event_streams (session_id, version, created_at_commit_position, last_append_commit_position) VALUES (?, ?, ?, ?) "+
			"ON CONFLICT(session_id) DO UPDATE SET version = excluded.version, last_append_commit_position = excluded.last_append_commit_position",
		sessionID, version, position, position); err != nil {
		return err
	}
	return nil
}

func payloadDigestOf(payload []byte) []byte {
	digest := auditEventPayloadDigest(payload)
	out := make([]byte, len(digest))
	copy(out, digest[:])
	return out
}
