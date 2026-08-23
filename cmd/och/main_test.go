package main

import (
	"bytes"
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestExportSessionMissingDatabase(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := exportSession(context.Background(), []string{
		"-database", filepath.Join(t.TempDir(), "missing.db"),
		"-session", "session-1",
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("export-session missing database = nil, want error")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.Bytes())
	}
}

func TestExportSessionUnknownSession(t *testing.T) {
	path := newCLIDatabase(t)
	var stdout, stderr bytes.Buffer
	err := exportSession(context.Background(), []string{
		"-database", path,
		"-session", "session-missing",
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("export-session unknown session = nil, want error")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.Bytes())
	}
}

func TestExportSessionStdoutStartsWithSnapshotEndsWithComplete(t *testing.T) {
	path, sessionID := seedCLISession(t)
	var stdout, stderr bytes.Buffer
	if err := exportSession(context.Background(), []string{
		"-database", path,
		"-session", string(sessionID),
	}, &stdout, &stderr); err != nil {
		t.Fatalf("export-session error = %v", err)
	}
	assertSnapshotThenComplete(t, stdout.Bytes())
	want := "och: exported session session-1 facts=1 head=1 open=true running=false\n"
	if stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestExportSessionCancelledOutputLeavesDestAbsent(t *testing.T) {
	path, sessionID := seedCLISession(t)
	dest := filepath.Join(t.TempDir(), "session.jsonl")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	err := exportSession(ctx, []string{
		"-database", path,
		"-session", string(sessionID),
		"-output", dest,
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("cancelled export-session = nil, want error")
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("cancelled -output left dest %s: %v", dest, statErr)
	}
	if leftovers := tempExportFiles(t, filepath.Dir(dest)); len(leftovers) != 0 {
		t.Fatalf("cancelled -output left temp files %v", leftovers)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.Bytes())
	}
}

func TestExportSessionFailedOutputLeavesDestUntouched(t *testing.T) {
	path := newCLIDatabase(t)
	dest := filepath.Join(t.TempDir(), "session.jsonl")
	const previous = "previous-complete\n"
	if err := os.WriteFile(dest, []byte(previous), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := exportSession(context.Background(), []string{
		"-database", path,
		"-session", "session-missing",
		"-output", dest,
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("failed export-session = nil, want error")
	}
	got, readErr := os.ReadFile(dest)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != previous {
		t.Fatalf("dest = %q, want previous file untouched", got)
	}
}

func TestServeModeACPFlagRemainsValid(t *testing.T) {
	err := run([]string{"-acp"})
	if err == nil {
		t.Fatal("och -acp without serve config = nil, want validation error")
	}
	if strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("serve path dropped -acp: %v", err)
	}
}

func TestExportSessionDoesNotUseServeFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := exportSession(context.Background(), []string{"-acp", "-database", "x", "-session", "s"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("export-session -acp = nil, want undefined-flag error")
	}
	if !strings.Contains(err.Error(), "flag provided but not defined: -acp") {
		t.Fatalf("error = %v, want dedicated FlagSet that rejects -acp", err)
	}
}

func TestExportSessionDoesNotOpenAssembly(t *testing.T) {
	err := run([]string{"export-session", "-database", filepath.Join(t.TempDir(), "missing.db"), "-session", "session-1"})
	if err == nil {
		t.Fatal("export-session missing database = nil, want error")
	}
	if strings.Contains(err.Error(), "WorkspaceRoot") || strings.Contains(err.Error(), "API key") {
		t.Fatalf("export-session called composition.Open: %v", err)
	}
}

func TestExportSessionOutputPublishesCompleteFile(t *testing.T) {
	path, sessionID := seedCLISession(t)
	dest := filepath.Join(t.TempDir(), "session.jsonl")
	var stdout, stderr bytes.Buffer
	if err := exportSession(context.Background(), []string{
		"-database", path,
		"-session", string(sessionID),
		"-output", dest,
	}, &stdout, &stderr); err != nil {
		t.Fatalf("export-session -output error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty when -output is set", stdout.Bytes())
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	assertSnapshotThenComplete(t, data)
	if leftovers := tempExportFiles(t, filepath.Dir(dest)); len(leftovers) != 0 {
		t.Fatalf("-output left temp files %v", leftovers)
	}
}

func TestProductionFilesDoNotImportTranscriptOrSQLite(t *testing.T) {
	fileSet := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, parseErr := parser.ParseFile(fileSet, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		for _, spec := range parsed.Imports {
			importPath, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil {
				return unquoteErr
			}
			if importPath == "github.com/SongYii/open-code-harness/internal/harness/transcript" ||
				strings.HasPrefix(importPath, "github.com/SongYii/open-code-harness/internal/harness/transcript/") ||
				importPath == "github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite" ||
				strings.HasPrefix(importPath, "github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite/") {
				t.Errorf("%s imports %s", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func newCLIDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "harness.db")
	store, err := sqlite.Open(context.Background(), sqlite.Config{Path: path, RuntimeID: "och-export-test"})
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return path
}

func seedCLISession(t *testing.T) (string, domain.SessionID) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "harness.db")
	store, err := sqlite.Open(context.Background(), sqlite.Config{Path: path, RuntimeID: "och-export-test"})
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	sessionID := domain.SessionID("session-1")
	_, err = store.Append(context.Background(), application.AppendRequest{
		AppendID:        "append-1",
		SessionID:       sessionID,
		ExpectedVersion: 0,
		CommandID:       "command-1",
		Authority:       store.Authority(),
		Events: []application.ProposedEvent{{
			ID:            "event-1",
			SchemaVersion: 1,
			OccurredAt:    time.Now().UTC(),
			Event:         domain.SessionCreated{WorkspaceRoot: "/workspace"},
		}},
	})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	return path, sessionID
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
	if got := jsonType(t, raw[0]); got != "transcript.snapshot" {
		t.Fatalf("first line type = %q, want transcript.snapshot", got)
	}
	if got := jsonType(t, raw[len(raw)-1]); got != "transcript.complete" {
		t.Fatalf("last line type = %q, want transcript.complete", got)
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

func tempExportFiles(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".och-export-session-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}
