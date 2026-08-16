package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func replicaRoundTrip(t *testing.T, batches int) (*Store, string) {
	t.Helper()
	store, directory := exportStore(t)
	seedAppends(t, store, batches)
	if _, err := store.ExportOnce(context.Background(), ExportConfig{Directory: directory}); err != nil {
		t.Fatalf("export: %v", err)
	}
	return store, directory
}

func TestExportConsistentProducesSelfContainedReplica(t *testing.T) {
	store, _ := exportStore(t)
	seedAppends(t, store, 3)
	consistent := filepath.Join(t.TempDir(), "consistent")
	if err := store.ExportConsistent(context.Background(), 2, ExportConfig{Directory: consistent}); err != nil {
		t.Fatalf("ExportConsistent: %v", err)
	}
	if got := len(listFiles(t, consistent, "segments")); got != 1 {
		t.Fatalf("consistent segments = %d, want 1", got)
	}
	manifests := listFiles(t, consistent, "manifests")
	if len(manifests) != 1 || !strings.HasPrefix(manifests[0], "000000000002-") {
		t.Fatalf("consistent manifests = %v, want one generation at head 2", manifests)
	}
	imported, err := ImportAuditReplica(context.Background(), consistent,
		Config{Path: filepath.Join(t.TempDir(), "imported.db"), RuntimeID: "runtime-import"})
	if err != nil {
		t.Fatalf("import consistent replica: %v", err)
	}
	defer imported.Close()
	page, err := imported.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: "session-export", Limit: 256})
	if err != nil {
		t.Fatalf("read imported stream: %v", err)
	}
	// Both committed batches landed: three events plus a completed turn.
	if page.HeadVersion != 5 || len(page.Records) != 5 {
		t.Fatalf("imported page = %d records at head %d, want 5 at 5", len(page.Records), page.HeadVersion)
	}
}

func TestImportVerifiesAndLandsWorkingStore(t *testing.T) {
	_, directory := replicaRoundTrip(t, 2)
	imported, err := ImportAuditReplica(context.Background(), directory,
		Config{Path: filepath.Join(t.TempDir(), "imported.db"), RuntimeID: "runtime-import"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	defer imported.Close()

	page, err := imported.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: "session-export", Limit: 256})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if page.HeadVersion != 5 || len(page.Records) != 5 {
		t.Fatalf("imported page = %d records at head %d, want 5 at 5", len(page.Records), page.HeadVersion)
	}
	var head uint64
	if err := imported.db.QueryRowContext(context.Background(),
		"SELECT head_commit_position FROM store_metadata WHERE id = 1").Scan(&head); err != nil {
		t.Fatal(err)
	}
	if head != 2 {
		t.Fatalf("imported head position = %d, want 2", head)
	}
	if err := imported.RebuildAndVerifySessionHeads(context.Background()); err != nil {
		t.Fatalf("heads projection disagrees: %v", err)
	}
}

func TestImportRefusesNonEmptyDatabase(t *testing.T) {
	store, directory := replicaRoundTrip(t, 1)
	destination := filepath.Join(t.TempDir(), "dest.db")
	seeded, err := Open(context.Background(), Config{Path: destination, RuntimeID: "runtime-seed"})
	if err != nil {
		t.Fatal(err)
	}
	seedRequest := appendRequest("append-dest", "session-dest", 0, "command-dest", domain.SessionCreated{WorkspaceRoot: "/w"})
	seedRequest.Authority = seeded.Authority()
	mustAppend(t, seeded, seedRequest)
	if err := seeded.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportAuditReplica(context.Background(), directory, Config{Path: destination, RuntimeID: "runtime-seed"}); err == nil {
		t.Fatal("import into non-empty database = nil, want refusal")
	}
	_ = store
}

func TestImportRefusesTamperedSegment(t *testing.T) {
	_, directory := replicaRoundTrip(t, 1)
	segment := listFiles(t, directory, "segments")[0]
	path := filepath.Join(directory, "segments", segment)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] ^= 0xff
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportAuditReplica(context.Background(), directory, Config{Path: filepath.Join(t.TempDir(), "x.db"), RuntimeID: "r"}); err == nil {
		t.Fatal("tampered segment imported; want refusal")
	}
}

func TestImportRefusesMissingSegment(t *testing.T) {
	_, directory := replicaRoundTrip(t, 1)
	segment := listFiles(t, directory, "segments")[0]
	if err := os.Remove(filepath.Join(directory, "segments", segment)); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportAuditReplica(context.Background(), directory, Config{Path: filepath.Join(t.TempDir(), "x.db"), RuntimeID: "r"}); err == nil {
		t.Fatal("missing segment imported; want refusal")
	}
}

func TestImportRefusesTornFinalLine(t *testing.T) {
	_, directory := replicaRoundTrip(t, 1)
	segment := listFiles(t, directory, "segments")[0]
	path := filepath.Join(directory, "segments", segment)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	torn := raw[:len(raw)-5] // cut mid-final-line without the newline
	if err := os.WriteFile(path, torn, 0o600); err != nil {
		t.Fatal(err)
	}
	// The manifest still records the original size; either the size check or
	// the torn-line check must refuse.
	if _, err := ImportAuditReplica(context.Background(), directory, Config{Path: filepath.Join(t.TempDir(), "x.db"), RuntimeID: "r"}); err == nil {
		t.Fatal("torn final line imported; want refusal")
	}
}

func TestImportRefusesBrokenChainManifest(t *testing.T) {
	_, directory := replicaRoundTrip(t, 1)
	manifest := listFiles(t, directory, "manifests")[0]
	path := filepath.Join(directory, "manifests", manifest)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	corrupted := strings.Replace(string(raw), `"headAuditDigest":"`, `"headAuditDigest":"ff`, 1)
	if err := os.WriteFile(path, []byte(corrupted), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportAuditReplica(context.Background(), directory, Config{Path: filepath.Join(t.TempDir(), "x.db"), RuntimeID: "r"}); err == nil {
		t.Fatal("manifest with wrong head digest imported; want refusal")
	}
}

func TestImportRefusesCraftedSequenceGap(t *testing.T) {
	// Build a one-envelope replica whose manifest and digests are fully
	// valid but whose event sequence skips a number: only the deep
	// verification layers can catch it.
	directory := filepath.Join(t.TempDir(), "crafted")
	for _, sub := range []string{"segments", "manifests"} {
		if err := os.MkdirAll(filepath.Join(directory, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	batch := auditBatch{
		FormatVersion: 1, CommitPosition: 1, AppendID: "append-crafted",
		CommandID: "command-crafted", SessionID: "session-crafted",
		ExpectedVersion: 0, FirstSequence: 2, LastSequence: 2, CommittedAtUnix: 1786900000,
	}
	copy(batch.PreviousDigest[:], auditGenesisDigest[:])
	record := domain.RecordedEvent{
		SchemaVersion: 1, ID: "event-crafted-0", CommandID: "command-crafted",
		SessionID: "session-crafted", Sequence: 2,
		OccurredAt: testTime, Event: domain.SessionCreated{WorkspaceRoot: "/w"},
	}
	payload, err := domain.MarshalRecordedEvent(record)
	if err != nil {
		t.Fatal(err)
	}
	batch.Events = [][]byte{payload}
	codec, _ := auditCodecFor(1)
	envelope, _, err := codec.Encode(batch)
	if err != nil {
		t.Fatal(err)
	}
	segmentPayload := append(envelope, '\n')
	fileDigest := sha256.Sum256(segmentPayload)
	segmentName := fmt.Sprintf("%012d-%012d-%s.jsonl", 1, 1, hex.EncodeToString(fileDigest[:6]))
	if err := os.WriteFile(filepath.Join(directory, "segments", segmentName), segmentPayload, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := manifestGeneration{
		FormatVersion:      1,
		HeadCommitPosition: 1,
		HeadAuditDigest:    hex.EncodeToString(batch.BatchDigest[:]),
		Segments: []segmentEntry{{
			File: segmentName, FirstCommitPosition: 1, LastCommitPosition: 1,
			Bytes: int64(len(segmentPayload)), SHA256: hex.EncodeToString(fileDigest[:]),
		}},
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestName := fmt.Sprintf("%012d-%s.json", 1, hex.EncodeToString(batch.BatchDigest[:6]))
	if err := os.WriteFile(filepath.Join(directory, "manifests", manifestName), manifestRaw, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ImportAuditReplica(context.Background(), directory, Config{Path: filepath.Join(t.TempDir(), "x.db"), RuntimeID: "r"}); err == nil {
		t.Fatal("crafted sequence-gap replica imported; want refusal by the deep verification layers")
	}
}
