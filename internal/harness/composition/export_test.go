package composition_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/composition"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/transcript"
)

func TestExportSessionMissingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	var buf bytes.Buffer
	_, err := composition.ExportSession(context.Background(), path, "session-1", &buf)
	if err == nil {
		t.Fatal("ExportSession() error = nil, want missing database")
	}
	if buf.Len() != 0 {
		t.Fatalf("wrote %d bytes after missing database", buf.Len())
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("ExportSession created %s: %v", path, statErr)
	}
}

func TestExportSessionUnknownSession(t *testing.T) {
	path := newExportDatabase(t)
	var buf bytes.Buffer
	_, err := composition.ExportSession(context.Background(), path, "session-missing", &buf)
	if !transcript.IsCode(err, transcript.CodeSessionNotFound) {
		t.Fatalf("ExportSession() error = %v, want code %q", err, transcript.CodeSessionNotFound)
	}
	if buf.Len() != 0 {
		t.Fatalf("wrote %d bytes after session_not_found", buf.Len())
	}
}

func TestExportSessionJSONLStartsWithSnapshotEndsWithComplete(t *testing.T) {
	path, sessionID, store := seedExportSession(t, domain.SessionCreated{WorkspaceRoot: "/workspace"})
	var buf bytes.Buffer
	result, err := composition.ExportSession(context.Background(), path, sessionID, &buf)
	if err != nil {
		t.Fatalf("ExportSession() error = %v", err)
	}
	if result.HeadSequence != 1 || result.FactLines != 1 || !result.Open || result.Running {
		t.Fatalf("result = %+v, want head=1 facts=1 open running=false", result)
	}
	assertSnapshotThenComplete(t, buf.Bytes())
	if store.Authority().RuntimeID != "export-test" {
		t.Fatal("ExportSession mutated the writer identity")
	}
}

func TestExportSessionRejectsNilContext(t *testing.T) {
	path := newExportDatabase(t)
	//nolint:staticcheck // passing a nil context is exactly what this asserts.
	_, err := composition.ExportSession(nil, path, "session-1", bytes.NewBuffer(nil))
	if err == nil {
		t.Fatal("ExportSession(nil) error = nil, want error")
	}
}

func newExportDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "harness.db")
	store, err := sqlite.Open(context.Background(), sqlite.Config{Path: path, RuntimeID: "export-test"})
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return path
}

func seedExportSession(t *testing.T, events ...domain.Event) (string, domain.SessionID, *sqlite.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "harness.db")
	store, err := sqlite.Open(context.Background(), sqlite.Config{Path: path, RuntimeID: "export-test"})
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	sessionID := domain.SessionID("session-1")
	authority := store.Authority()
	now := time.Now().UTC()
	for i, event := range events {
		seq := uint64(i + 1)
		id := strconv.FormatUint(seq, 10)
		_, err := store.Append(context.Background(), application.AppendRequest{
			AppendID:        domain.AppendID("append-" + id),
			SessionID:       sessionID,
			ExpectedVersion: uint64(i),
			CommandID:       domain.CommandID("command-" + id),
			Authority:       authority,
			Events: []application.ProposedEvent{{
				ID:            domain.EventID("event-" + id),
				SchemaVersion: 1,
				OccurredAt:    now,
				Event:         event,
			}},
		})
		if err != nil {
			t.Fatalf("Append(%d) error = %v", seq, err)
		}
	}
	return path, sessionID, store
}

func assertSnapshotThenComplete(t *testing.T, data []byte) {
	t.Helper()
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("export must be newline-terminated JSONL, got %q", data)
	}
	raw := bytes.Split(data[:len(data)-1], []byte{'\n'})
	if len(raw) < 2 {
		t.Fatalf("export lines = %d, want snapshot and complete", len(raw))
	}
	first := jsonType(t, raw[0])
	last := jsonType(t, raw[len(raw)-1])
	if first != transcript.TypeSnapshot {
		t.Fatalf("first line type = %q, want %q", first, transcript.TypeSnapshot)
	}
	if last != transcript.TypeComplete {
		t.Fatalf("last line type = %q, want %q", last, transcript.TypeComplete)
	}
}

func jsonType(t *testing.T, line []byte) string {
	t.Helper()
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		t.Fatalf("unmarshal %s: %v", line, err)
	}
	return envelope.Type
}
