package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func exportStore(t *testing.T) (*Store, string) {
	t.Helper()
	store := openStore(t, tempStoreConfig(t))
	directory := filepath.Join(t.TempDir(), "audit")
	return store, directory
}

var exportSeedCounter int

func seedAppends(t *testing.T, store *Store, count int) {
	t.Helper()
	version := exportedHead(t, store)
	for i := 0; i < count; i++ {
		exportSeedCounter++
		request := appendRequest(
			domain.AppendID(appendName(exportSeedCounter)), "session-export", version,
			domain.CommandID(commandName(exportSeedCounter)),
			domain.TurnStarted{TurnID: domain.TurnID(turnName(exportSeedCounter)), Input: "x"})
		receipt := mustAppend(t, store, request)
		version = receipt.LastSequence
	}
}

func exportedHead(t *testing.T, store *Store) uint64 {
	t.Helper()
	var version uint64
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT version FROM event_streams WHERE session_id = 'session-export'").Scan(&version); err != nil {
		if err == sql.ErrNoRows {
			return 0
		}
		t.Fatalf("read export head: %v", err)
	}
	return version
}

func appendName(i int) string {
	return "append-export-" + string(rune('a'+i%20)) + string(rune('a'+i/20))
}
func commandName(i int) string {
	return "command-export-" + string(rune('a'+i%20)) + string(rune('a'+i/20))
}
func turnName(i int) string { return "turn-export-" + string(rune('a'+i%20)) + string(rune('a'+i/20)) }

func listFiles(t *testing.T, directory, sub string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(directory, sub))
	if err != nil {
		t.Fatalf("read %s: %v", sub, err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestExportOnceSealsSegmentsAndUpdatesCheckpoint(t *testing.T) {
	store, directory := exportStore(t)
	seedAppends(t, store, 3)

	result, err := store.ExportOnce(context.Background(), ExportConfig{Directory: directory})
	if err != nil {
		t.Fatalf("ExportOnce: %v", err)
	}
	if result.SegmentsSealed != 1 || result.VerifiedHead != 3 || result.PositionsExported != 3 {
		t.Fatalf("result = %+v", result)
	}
	segments := listFiles(t, directory, "segments")
	if len(segments) != 1 {
		t.Fatalf("sealed segments = %v, want 1", segments)
	}
	raw, err := os.ReadFile(filepath.Join(directory, "segments", segments[0]))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(raw), "\n"); lines != 3 {
		t.Fatalf("segment lines = %d, want 3", lines)
	}
	manifests := listFiles(t, directory, "manifests")
	if len(manifests) != 1 {
		t.Fatalf("manifest generations = %v, want 1", manifests)
	}
	if hint, err := os.Stat(filepath.Join(directory, "manifest.json")); err != nil || hint.IsDir() {
		t.Fatalf("latest hint missing: %v", err)
	}
	var checkpoint uint64
	if err := store.db.QueryRowContext(context.Background(),
		"SELECT last_exported_commit_position FROM export_checkpoints WHERE id = 1").Scan(&checkpoint); err != nil {
		t.Fatalf("read checkpoint: %v", err)
	}
	if checkpoint != 3 {
		t.Fatalf("checkpoint = %d, want 3", checkpoint)
	}
	if got := tableCount(t, store, "export_outbox"); got != 0 {
		t.Fatalf("outbox rows after prune = %d, want 0", got)
	}
}

func TestExportIsIdempotentByRangeAndDigest(t *testing.T) {
	store, directory := exportStore(t)
	seedAppends(t, store, 2)
	if _, err := store.ExportOnce(context.Background(), ExportConfig{Directory: directory}); err != nil {
		t.Fatalf("first export: %v", err)
	}
	result, err := store.ExportOnce(context.Background(), ExportConfig{Directory: directory})
	if err != nil {
		t.Fatalf("second export: %v", err)
	}
	if result.SegmentsSealed != 0 {
		t.Fatalf("second export sealed %d segments, want 0 (already complete)", result.SegmentsSealed)
	}
	if len(listFiles(t, directory, "segments")) != 1 || len(listFiles(t, directory, "manifests")) != 1 {
		t.Fatal("idempotent export duplicated replica files")
	}
}

func TestExporterIncrementalExport(t *testing.T) {
	store, directory := exportStore(t)
	seedAppends(t, store, 1)
	if _, err := store.ExportOnce(context.Background(), ExportConfig{Directory: directory}); err != nil {
		t.Fatalf("first export: %v", err)
	}
	seedAppends(t, store, 1)
	result, err := store.ExportOnce(context.Background(), ExportConfig{Directory: directory})
	if err != nil {
		t.Fatalf("second export: %v", err)
	}
	if result.SegmentsSealed != 1 || result.VerifiedHead != 2 {
		t.Fatalf("incremental result = %+v", result)
	}
	if got := len(listFiles(t, directory, "segments")); got != 2 {
		t.Fatalf("segments = %d, want 2", got)
	}
	if got := len(listFiles(t, directory, "manifests")); got != 2 {
		t.Fatalf("manifest generations = %d, want 2 (immutable)", got)
	}
}

func TestExporterRestartDiscardsStaging(t *testing.T) {
	store, directory := exportStore(t)
	seedAppends(t, store, 1)
	staging := filepath.Join(directory, "staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "leftover.partial"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExportOnce(context.Background(), ExportConfig{Directory: directory}); err != nil {
		t.Fatalf("export with staging leftover: %v", err)
	}
	if leftovers := listFiles(t, directory, "staging"); len(leftovers) != 0 {
		t.Fatalf("staging leftovers = %v, want discarded", leftovers)
	}
}

func TestExporterRestartRecoversCheckpointBehindManifest(t *testing.T) {
	store, directory := exportStore(t)
	seedAppends(t, store, 2)
	if _, err := store.ExportOnce(context.Background(), ExportConfig{Directory: directory}); err != nil {
		t.Fatal(err)
	}
	// Crash between manifest publication and checkpoint update: the
	// checkpoint is behind. The inventory recomputes it from the manifest.
	store.writeMu.Lock()
	_, err := store.writer.ExecContext(context.Background(),
		"UPDATE export_checkpoints SET last_exported_commit_position = 0 WHERE id = 1")
	store.writeMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	result, retryErr := store.ExportOnce(context.Background(), ExportConfig{Directory: directory})
	if retryErr != nil {
		t.Fatalf("restart export: %v", retryErr)
	}
	if result.SegmentsSealed != 0 || result.VerifiedHead != 2 {
		t.Fatalf("restart result = %+v, want converged no-op", result)
	}
}

func TestExporterRegeneratesMissingReplicaFromCanonical(t *testing.T) {
	store, directory := exportStore(t)
	seedAppends(t, store, 2)
	if _, err := store.ExportOnce(context.Background(), ExportConfig{Directory: directory}); err != nil {
		t.Fatal(err)
	}
	first := listFiles(t, directory, "segments")[0]

	// The replica is lost entirely; outbox rows are already pruned. The
	// exporter must regenerate identical bytes from canonical SQLite data.
	if err := os.RemoveAll(directory); err != nil {
		t.Fatal(err)
	}
	result, err := store.ExportOnce(context.Background(), ExportConfig{Directory: directory})
	if err != nil {
		t.Fatalf("regeneration export: %v", err)
	}
	if result.VerifiedHead != 2 {
		t.Fatalf("regenerated head = %d, want 2", result.VerifiedHead)
	}
	regenerated := listFiles(t, directory, "segments")[0]
	if regenerated != first {
		t.Fatalf("regenerated segment %q differs from original %q; canonical re-encode is not deterministic", regenerated, first)
	}
}

func TestExporterQuarantinesTamperedSegment(t *testing.T) {
	store, directory := exportStore(t)
	seedAppends(t, store, 1)
	if _, err := store.ExportOnce(context.Background(), ExportConfig{Directory: directory}); err != nil {
		t.Fatal(err)
	}
	segment := listFiles(t, directory, "segments")[0]
	path := filepath.Join(directory, "segments", segment)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte{}, raw...)
	tampered[0] ^= 0xff
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExportOnce(context.Background(), ExportConfig{Directory: directory}); err == nil {
		t.Fatal("export over tampered segment = nil, want quarantine")
	}
}

func TestExporterQuarantinesConflictingGenerations(t *testing.T) {
	store, directory := exportStore(t)
	seedAppends(t, store, 1)
	if _, err := store.ExportOnce(context.Background(), ExportConfig{Directory: directory}); err != nil {
		t.Fatal(err)
	}
	manifests := listFiles(t, directory, "manifests")
	original := filepath.Join(directory, "manifests", manifests[0])
	raw, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	// Same head position, different digest: a conflicting generation.
	conflicting := strings.Replace(string(raw), `"headAuditDigest":"`, `"headAuditDigest":"0`, 1)
	digest := sha256.Sum256([]byte(conflicting))
	name := manifests[0][:13] + hex.EncodeToString(digest[:6]) + ".json"
	if err := os.WriteFile(filepath.Join(directory, "manifests", name), []byte(conflicting), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExportOnce(context.Background(), ExportConfig{Directory: directory}); err == nil {
		t.Fatal("conflicting generations accepted; want quarantine")
	}
}

func TestExporterLeaseRefusesForeignLiveLease(t *testing.T) {
	store, directory := exportStore(t)
	seedAppends(t, store, 1)
	if _, err := store.writer.ExecContext(context.Background(),
		"INSERT INTO export_leases (id, exporter_id, fencing_token, lease_expires_at_unix, last_heartbeat_at_unix) VALUES (1, 'runtime-other-exporter', 5, unixepoch('subsec') + 3600, unixepoch('subsec'))"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExportOnce(context.Background(), ExportConfig{Directory: directory}); err == nil {
		t.Fatal("export with foreign live lease = nil, want refusal")
	}
}

var _ = sql.ErrNoRows
