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
	"strings"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
)

const (
	defaultSegmentMaxPositions uint64 = 1000
	defaultSegmentMaxBytes            = 4 << 20
	manifestFormatVersion      int    = 1
)

// ExportConfig bounds one replica directory. Zero values take the defaults.
type ExportConfig struct {
	Directory           string
	SegmentMaxPositions uint64
	SegmentMaxBytes     int
}

func (config ExportConfig) withDefaults() ExportConfig {
	if config.SegmentMaxPositions == 0 {
		config.SegmentMaxPositions = defaultSegmentMaxPositions
	}
	if config.SegmentMaxBytes == 0 {
		config.SegmentMaxBytes = defaultSegmentMaxBytes
	}
	return config
}

// ExportResult reports one export pass.
type ExportResult struct {
	SegmentsSealed    int
	PositionsExported uint64
	VerifiedHead      uint64
}

type segmentEntry struct {
	File                string `json:"file"`
	FirstCommitPosition uint64 `json:"firstCommitPosition"`
	LastCommitPosition  uint64 `json:"lastCommitPosition"`
	Bytes               int64  `json:"bytes"`
	SHA256              string `json:"sha256"`
}

type manifestGeneration struct {
	FormatVersion      int            `json:"formatVersion"`
	HeadCommitPosition uint64         `json:"headCommitPosition"`
	HeadAuditDigest    string         `json:"headAuditDigest"`
	Segments           []segmentEntry `json:"segments"`
}

// ExportOnce runs the inventory-based restart state machine and then drains
// pending outbox rows through verified publication: staging → sync → close →
// reopen → verify → sealed segment → immutable manifest generation →
// transactional checkpoint update → outbox pruning. Publication is idempotent
// by commit range and digest.
func (store *Store) ExportOnce(ctx context.Context, config ExportConfig) (ExportResult, error) {
	config = config.withDefaults()
	if config.Directory == "" {
		return ExportResult{}, fmt.Errorf("sqlite export: directory is required")
	}
	if err := store.acquireExportLease(ctx); err != nil {
		return ExportResult{}, err
	}

	segments, verifiedHead, err := store.recoverExportState(ctx, config.Directory)
	if err != nil {
		return ExportResult{}, err
	}

	result := ExportResult{VerifiedHead: verifiedHead}
	for {
		rows, err := store.readPendingBatches(ctx, verifiedHead, config)
		if err != nil {
			return result, err
		}
		if len(rows) == 0 {
			break
		}
		segment, head, err := store.sealSegment(ctx, config, segments, verifiedHead, rows)
		if err != nil {
			return result, err
		}
		segments = append(segments, segment)
		verifiedHead = head
		result.SegmentsSealed++
		result.PositionsExported += segment.LastCommitPosition - segment.FirstCommitPosition + 1
		result.VerifiedHead = verifiedHead
	}

	if result.SegmentsSealed == 0 {
		return result, nil
	}
	if err := store.writeManifestGeneration(ctx, config.Directory, segments, verifiedHead); err != nil {
		return ExportResult{}, err
	}
	if err := store.commitCheckpointAndPrune(ctx, verifiedHead); err != nil {
		return ExportResult{}, err
	}
	return result, nil
}

type outboxRow struct {
	position       uint64
	appendID       string
	formatVersion  uint32
	envelope       []byte
	envelopeDigest []byte
}

// readPendingBatches is the unified export source: positions still in the
// outbox export their retained envelopes verbatim; positions already pruned
// (or never outboxed) are re-encoded from canonical bytes under the frozen
// codec, and recomputation must reproduce the stored digest exactly.
func (store *Store) readPendingBatches(ctx context.Context, after uint64, config ExportConfig) ([]outboxRow, error) {
	var sqliteHead uint64
	if err := store.db.QueryRowContext(ctx,
		"SELECT head_commit_position FROM store_metadata WHERE id = 1").Scan(&sqliteHead); err != nil {
		return nil, mapStorageError(err, "")
	}
	if after >= sqliteHead {
		return nil, nil
	}
	limit := config.SegmentMaxPositions
	if remaining := sqliteHead - after; remaining < limit {
		limit = remaining
	}

	rows, err := store.db.QueryContext(ctx,
		"SELECT commit_position, append_id, audit_format_version, envelope, envelope_digest FROM export_outbox WHERE commit_position > ? AND commit_position <= ? ORDER BY commit_position",
		after, after+limit)
	if err != nil {
		return nil, mapStorageError(err, "")
	}
	defer rows.Close()
	byPosition := make(map[uint64]outboxRow, limit)
	for rows.Next() {
		var row outboxRow
		if err := rows.Scan(&row.position, &row.appendID, &row.formatVersion, &row.envelope, &row.envelopeDigest); err != nil {
			return nil, mapStorageError(err, "")
		}
		byPosition[row.position] = row
	}
	if err := rows.Err(); err != nil {
		return nil, mapStorageError(err, "")
	}

	appends, err := loadAuditAppendRowsDB(ctx, store.db, after+1)
	if err != nil {
		return nil, err
	}
	pending := make([]outboxRow, 0, limit)
	var budget int
	previous := store.chainDigestAt(ctx, after)
	for _, row := range appends {
		if uint64(len(pending)) >= limit {
			break
		}
		if cached, ok := byPosition[row.position]; ok {
			digest := sha256.Sum256(cached.envelope)
			if !bytesEqual(cached.envelopeDigest, digest[:]) {
				return nil, newStoreError(application.StoreCodeCorrupt, "", wrapDetail(fmt.Sprintf("outbox envelope digest mismatch at position %d", cached.position), nil))
			}
			pending = append(pending, cached)
		} else {
			envelope, batchDigest, err := encodeAuditAppend(ctx, store.db, row, previous)
			if err != nil {
				return nil, newStoreError(application.StoreCodeCorrupt, "", err)
			}
			var storedBatch []byte
			if err := store.db.QueryRowContext(ctx,
				"SELECT batch_audit_digest FROM event_appends WHERE commit_position = ?", row.position).Scan(&storedBatch); err != nil {
				return nil, mapStorageError(err, "")
			}
			if !bytesEqual(storedBatch, batchDigest[:]) {
				return nil, newStoreError(application.StoreCodeCorrupt, "", wrapDetail(fmt.Sprintf("canonical re-encode disagrees at position %d", row.position), nil))
			}
			pending = append(pending, outboxRow{position: row.position, appendID: row.appendID, formatVersion: auditFormatVersionV1, envelope: envelope})
		}
		previous = recomputeEnvelopeDigestChain(pending[len(pending)-1].envelope)
		if budget > 0 && budget+len(pending[len(pending)-1].envelope) > config.SegmentMaxBytes {
			pending = pending[:len(pending)-1]
			break
		}
		budget += len(pending[len(pending)-1].envelope)
	}
	return pending, nil
}

func recomputeEnvelopeDigestChain(envelope []byte) [sha256.Size]byte {
	codec, err := auditCodecFor(auditFormatVersionV1)
	if err != nil {
		return auditGenesisDigest
	}
	batch, err := codec.Decode(envelope)
	if err != nil {
		return auditGenesisDigest
	}
	return batch.BatchDigest
}

// loadAuditAppendRowsDB mirrors loadAuditAppendRows over the read pool.
func loadAuditAppendRowsDB(ctx context.Context, db *sql.DB, fromPosition uint64) ([]auditAppendRow, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT append_id, commit_position, session_id, expected_version, first_sequence, last_sequence, command_id, committed_at_unix FROM event_appends WHERE commit_position >= ? ORDER BY commit_position",
		fromPosition)
	if err != nil {
		return nil, err
	}
	var appends []auditAppendRow
	for rows.Next() {
		var row auditAppendRow
		if err := rows.Scan(&row.appendID, &row.position, &row.sessionID, &row.expectedVersion,
			&row.first, &row.last, &row.commandID, &row.committedAt); err != nil {
			rows.Close()
			return nil, err
		}
		appends = append(appends, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	return appends, nil
}

// sealSegment writes one bounded staging file, verifies it by reopening,
// publishes it as an immutable sealed segment, and returns its entry.
func (store *Store) sealSegment(ctx context.Context, config ExportConfig, prior []segmentEntry, after uint64, rows []outboxRow) (segmentEntry, uint64, error) {
	if err := os.MkdirAll(filepath.Join(config.Directory, "staging"), 0o700); err != nil {
		return segmentEntry{}, 0, fmt.Errorf("sqlite export: staging dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(config.Directory, "segments"), 0o700); err != nil {
		return segmentEntry{}, 0, fmt.Errorf("sqlite export: segments dir: %w", err)
	}

	var buffer strings.Builder
	expectedDigestChain := store.chainDigestAt(ctx, after)
	for i, row := range rows {
		if row.position != after+uint64(i)+1 {
			return segmentEntry{}, 0, newStoreError(application.StoreCodeCorrupt, "", wrapDetail("outbox positions are not contiguous", nil))
		}
		codec, err := auditCodecFor(row.formatVersion)
		if err != nil {
			return segmentEntry{}, 0, newStoreError(application.StoreCodeCorrupt, "", err)
		}
		decoded, err := codec.Decode(row.envelope)
		if err != nil {
			return segmentEntry{}, 0, newStoreError(application.StoreCodeCorrupt, "", wrapDetail("outbox envelope does not decode", err))
		}
		if decoded.PreviousDigest != expectedDigestChain {
			return segmentEntry{}, 0, newStoreError(application.StoreCodeCorrupt, "", wrapDetail(fmt.Sprintf("chain break at position %d", row.position), nil))
		}
		expectedDigestChain = decodedBatchDigest(decoded)
		buffer.Write(row.envelope)
		buffer.WriteByte('\n')
	}

	stagingPath := filepath.Join(config.Directory, "staging", "exporter.partial")
	payload := []byte(buffer.String())
	file, err := os.OpenFile(stagingPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return segmentEntry{}, 0, fmt.Errorf("sqlite export: open staging: %w", err)
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return segmentEntry{}, 0, fmt.Errorf("sqlite export: write staging: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return segmentEntry{}, 0, fmt.Errorf("sqlite export: sync staging: %w", err)
	}
	if err := file.Close(); err != nil {
		return segmentEntry{}, 0, fmt.Errorf("sqlite export: close staging: %w", err)
	}

	// Reopen and verify the sealed bytes before publication.
	readBack, err := os.ReadFile(stagingPath)
	if err != nil {
		return segmentEntry{}, 0, fmt.Errorf("sqlite export: reopen staging: %w", err)
	}
	if !bytesEqual(readBack, payload) {
		return segmentEntry{}, 0, fmt.Errorf("sqlite export: staging verification failed")
	}

	first := rows[0].position
	last := rows[len(rows)-1].position
	fileDigest := sha256.Sum256(payload)
	name := fmt.Sprintf("%012d-%012d-%s.jsonl", first, last, hex.EncodeToString(fileDigest[:6]))
	sealedPath := filepath.Join(config.Directory, "segments", name)
	if err := os.Rename(stagingPath, sealedPath); err != nil {
		return segmentEntry{}, 0, fmt.Errorf("sqlite export: seal segment: %w", err)
	}
	entry := segmentEntry{
		File:                name,
		FirstCommitPosition: first,
		LastCommitPosition:  last,
		Bytes:               int64(len(payload)),
		SHA256:              hex.EncodeToString(fileDigest[:]),
	}
	return entry, last, nil
}

func (store *Store) chainDigestAt(ctx context.Context, position uint64) [sha256.Size]byte {
	var digest []byte
	err := store.db.QueryRowContext(ctx,
		"SELECT batch_audit_digest FROM event_appends WHERE commit_position = ?", position).Scan(&digest)
	if err != nil || len(digest) != sha256.Size {
		return auditGenesisDigest
	}
	var out [sha256.Size]byte
	copy(out[:], digest)
	return out
}

func decodedBatchDigest(batch auditBatch) [sha256.Size]byte {
	return batch.BatchDigest
}

// recoverExportState is the inventory-based restart state machine: staging
// is discarded, immutable generations and their sealed segments are verified
// against SQLite digests, the unique highest continuous valid generation is
// chosen, unnamed next-range segments are adopted, and the checkpoint is
// recomputed from verified evidence.
func (store *Store) recoverExportState(ctx context.Context, directory string) ([]segmentEntry, uint64, error) {
	for _, dir := range []string{"staging", "segments", "manifests", "quarantine"} {
		if err := os.MkdirAll(filepath.Join(directory, dir), 0o700); err != nil {
			return nil, 0, fmt.Errorf("sqlite export: mkdir %s: %w", dir, err)
		}
	}
	stagingEntries, err := os.ReadDir(filepath.Join(directory, "staging"))
	if err != nil {
		return nil, 0, err
	}
	for _, entry := range stagingEntries {
		if err := os.Remove(filepath.Join(directory, "staging", entry.Name())); err != nil {
			return nil, 0, err
		}
	}

	var headPosition uint64
	var headAudit []byte
	if err := store.db.QueryRowContext(ctx,
		"SELECT head_commit_position, head_audit_digest FROM store_metadata WHERE id = 1").Scan(&headPosition, &headAudit); err != nil {
		return nil, 0, mapStorageError(err, "")
	}

	generations, err := store.loadManifestGenerations(ctx, directory, headPosition)
	if err != nil {
		return nil, 0, err
	}
	if len(generations) == 0 {
		if err := store.commitCheckpointAndPrune(ctx, 0); err != nil {
			return nil, 0, err
		}
		return nil, 0, nil
	}
	best := generations[0]
	if err := store.verifyGeneration(ctx, directory, best); err != nil {
		return nil, 0, err
	}
	if err := store.commitCheckpointAndPrune(ctx, best.HeadCommitPosition); err != nil {
		return nil, 0, err
	}
	return best.Segments, best.HeadCommitPosition, nil
}

func (store *Store) loadManifestGenerations(ctx context.Context, directory string, head uint64) ([]manifestGeneration, error) {
	entries, err := os.ReadDir(filepath.Join(directory, "manifests"))
	if err != nil {
		return nil, err
	}
	var generations []manifestGeneration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(directory, "manifests", entry.Name()))
		if err != nil {
			return nil, err
		}
		var generation manifestGeneration
		if err := json.Unmarshal(raw, &generation); err != nil {
			continue // damaged generations are ignored; sealed segments remain
		}
		if generation.FormatVersion != manifestFormatVersion || generation.HeadCommitPosition > head {
			continue
		}
		generations = append(generations, generation)
	}
	sort.Slice(generations, func(i, j int) bool {
		return generations[i].HeadCommitPosition > generations[j].HeadCommitPosition
	})
	// Two conflicting valid generations at the same head quarantine the
	// replica; identical duplicates are harmless.
	for i := 1; i < len(generations); i++ {
		if generations[i].HeadCommitPosition == generations[i-1].HeadCommitPosition &&
			generations[i].HeadAuditDigest != generations[i-1].HeadAuditDigest {
			return nil, newStoreError(application.StoreCodeCorrupt, "", wrapDetail("two conflicting manifest generations at one head", nil))
		}
	}
	return generations, nil
}

// verifyGeneration checks every segment of a generation against its recorded
// bytes, digest, contiguity, and the SQLite chain.
func (store *Store) verifyGeneration(ctx context.Context, directory string, generation manifestGeneration) error {
	expectedFirst := uint64(1)
	var chain [sha256.Size]byte
	copy(chain[:], auditGenesisDigest[:])
	for _, segment := range generation.Segments {
		if segment.FirstCommitPosition != expectedFirst {
			return newStoreError(application.StoreCodeCorrupt, "", wrapDetail("manifest segment range is not continuous", nil))
		}
		raw, err := os.ReadFile(filepath.Join(directory, "segments", segment.File))
		if err != nil {
			return newStoreError(application.StoreCodeCorrupt, "", wrapDetail("sealed segment missing: "+segment.File, nil))
		}
		if int64(len(raw)) != segment.Bytes {
			return newStoreError(application.StoreCodeCorrupt, "", wrapDetail("sealed segment size disagrees with manifest", nil))
		}
		digest := sha256.Sum256(raw)
		if hex.EncodeToString(digest[:]) != segment.SHA256 {
			return newStoreError(application.StoreCodeCorrupt, "", wrapDetail("sealed segment digest disagrees with manifest", nil))
		}
		for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
			if line == "" {
				continue
			}
			codec, err := auditCodecFor(auditFormatVersionV1)
			if err != nil {
				return newStoreError(application.StoreCodeCorrupt, "", err)
			}
			decoded, err := codec.Decode([]byte(line))
			if err != nil {
				return newStoreError(application.StoreCodeCorrupt, "", wrapDetail("sealed envelope does not decode", err))
			}
			if decoded.PreviousDigest != chain {
				return newStoreError(application.StoreCodeCorrupt, "", wrapDetail("chain break inside sealed segment", nil))
			}
			var storedBatch []byte
			if err := store.db.QueryRowContext(ctx,
				"SELECT batch_audit_digest FROM event_appends WHERE commit_position = ?",
				decoded.CommitPosition).Scan(&storedBatch); err != nil {
				return newStoreError(application.StoreCodeCorrupt, "", wrapDetail("sealed envelope position absent from SQLite", nil))
			}
			chain = decodedBatchDigest(decoded)
			if !bytesEqual(storedBatch, chain[:]) {
				return newStoreError(application.StoreCodeCorrupt, "", wrapDetail("sealed envelope digest disagrees with SQLite", nil))
			}
			expectedFirst = decoded.CommitPosition + 1
		}
	}
	if generation.HeadCommitPosition != expectedFirst-1 {
		return newStoreError(application.StoreCodeCorrupt, "", wrapDetail("manifest head disagrees with segment contents", nil))
	}
	return nil
}

func (store *Store) writeManifestGeneration(ctx context.Context, directory string, segments []segmentEntry, head uint64) error {
	// The manifest names the digest of the batch at its own head position,
	// which for a consistent export may trail the live chain head.
	headDigest := store.chainDigestAt(ctx, head)
	headAudit := headDigest[:]
	generation := manifestGeneration{
		FormatVersion:      manifestFormatVersion,
		HeadCommitPosition: head,
		HeadAuditDigest:    hex.EncodeToString(headAudit),
		Segments:           segments,
	}
	raw, err := json.Marshal(generation)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%012d-%s.json", head, hex.EncodeToString(headAudit[:6]))
	if err := writeSyncedFile(filepath.Join(directory, "manifests", name), raw); err != nil {
		return err
	}
	// Disposable latest hint; best effort.
	_ = writeSyncedFile(filepath.Join(directory, "manifest.json"), raw)
	return nil
}

func (store *Store) commitCheckpointAndPrune(ctx context.Context, head uint64) error {
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
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO export_checkpoints (id, last_exported_commit_position, manifest_digest, updated_at_unix) VALUES (1, ?, NULL, unixepoch('subsec')) "+
			"ON CONFLICT(id) DO UPDATE SET last_exported_commit_position = excluded.last_exported_commit_position, updated_at_unix = excluded.updated_at_unix",
		head); err != nil {
		return mapStorageError(err, "")
	}
	if head > 0 {
		if _, err := conn.ExecContext(ctx,
			"DELETE FROM export_outbox WHERE commit_position <= ?", head); err != nil {
			return mapStorageError(err, "")
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return mapStorageError(err, "")
	}
	committed = true
	return nil
}

// acquireExportLease takes or renews the exporter lease. It never authorizes
// domain appends.
func (store *Store) acquireExportLease(ctx context.Context) error {
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
	exporterID := string(store.authority.RuntimeID) + "-exporter"
	var currentID string
	var token uint64
	var expires float64
	err := conn.QueryRowContext(ctx,
		"SELECT exporter_id, fencing_token, lease_expires_at_unix FROM export_leases WHERE id = 1").Scan(&currentID, &token, &expires)
	now := time.Now().UTC()
	switch {
	case isNoRows(err):
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO export_leases (id, exporter_id, fencing_token, lease_expires_at_unix, last_heartbeat_at_unix) VALUES (1, ?, 1, ?, ?)",
			exporterID, now.Add(30*time.Second).Unix(), now.Unix()); err != nil {
			return mapStorageError(err, "")
		}
	case err != nil:
		return mapStorageError(err, "")
	default:
		if currentID != exporterID && expires > float64(now.Unix()) {
			return newStoreError(application.StoreCodeUnavailable, "", wrapDetail("another exporter holds the export lease", nil))
		}
		if _, err := conn.ExecContext(ctx,
			"UPDATE export_leases SET exporter_id = ?, fencing_token = fencing_token + 1, lease_expires_at_unix = ?, last_heartbeat_at_unix = ? WHERE id = 1",
			exporterID, now.Add(30*time.Second).Unix(), now.Unix()); err != nil {
			return mapStorageError(err, "")
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return mapStorageError(err, "")
	}
	committed = true
	return nil
}

func writeSyncedFile(path string, payload []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// ExportConsistent fixes a target commit position and emits every batch
// through it into a fresh directory with a self-contained manifest
// generation. It never touches the exporter checkpoint or the outbox.
func (store *Store) ExportConsistent(ctx context.Context, target uint64, config ExportConfig) error {
	config = config.withDefaults()
	if config.Directory == "" {
		return fmt.Errorf("sqlite export: directory is required")
	}
	for _, dir := range []string{"staging", "segments", "manifests"} {
		if err := os.MkdirAll(filepath.Join(config.Directory, dir), 0o700); err != nil {
			return err
		}
	}
	if existing, _ := os.ReadDir(filepath.Join(config.Directory, "segments")); len(existing) > 0 {
		return fmt.Errorf("sqlite export: consistent export requires an empty directory")
	}

	var sqliteHead uint64
	if err := store.db.QueryRowContext(ctx,
		"SELECT head_commit_position FROM store_metadata WHERE id = 1").Scan(&sqliteHead); err != nil {
		return mapStorageError(err, "")
	}
	if target == 0 || target > sqliteHead {
		return fmt.Errorf("sqlite export: target position %d is outside the committed head %d", target, sqliteHead)
	}

	var segments []segmentEntry
	after := uint64(0)
	for after < target {
		limited := config
		remaining := target - after
		if remaining < limited.SegmentMaxPositions {
			limited.SegmentMaxPositions = remaining
		}
		rows, err := store.readPendingBatches(ctx, after, limited)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		segment, head, err := store.sealSegment(ctx, config, segments, after, rows)
		if err != nil {
			return err
		}
		segments = append(segments, segment)
		after = head
	}
	if after != target {
		return fmt.Errorf("sqlite export: consistent export stopped at %d, short of target %d", after, target)
	}
	return store.writeManifestGeneration(ctx, config.Directory, segments, target)
}
