package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestCompactSessionMissingSession(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := compactSession(context.Background(), []string{
		"-workspace", t.TempDir(),
		"-database", filepath.Join(t.TempDir(), "harness.db"),
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("compact-session without -session = nil, want error")
	}
	if !strings.Contains(err.Error(), "session is required") {
		t.Fatalf("error = %v, want it to name the missing -session flag", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.Bytes())
	}
}

func TestCompactSessionDoesNotUseExportFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := compactSession(context.Background(), []string{"-session", "s", "-output", "x"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("compact-session -output = nil, want undefined-flag error")
	}
	if !strings.Contains(err.Error(), "flag provided but not defined: -output") {
		t.Fatalf("error = %v, want a dedicated FlagSet that rejects -output", err)
	}
}

func TestCompactSessionRejectsACPFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := compactSession(context.Background(), []string{"-acp", "-session", "s"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("compact-session -acp = nil, want undefined-flag error")
	}
	if !strings.Contains(err.Error(), "flag provided but not defined: -acp") {
		t.Fatalf("error = %v, want a dedicated FlagSet that rejects -acp", err)
	}
}

func TestCompactSessionNothingToCompactIsNotAnError(t *testing.T) {
	workspace := t.TempDir()
	database := filepath.Join(t.TempDir(), "harness.db")
	sessionID := seedCompactCLISession(t, database, nil)

	t.Setenv("OCH_COMPACT_CLI_TEST_KEY", "test-key")
	server := failingProvider(t)
	var stdout, stderr bytes.Buffer
	err := compactSession(context.Background(), compactCLIArgs(workspace, database, server.URL, string(sessionID)), &stdout, &stderr)
	if err != nil {
		t.Fatalf("compact-session with nothing to compact error = %v", err)
	}

	output := decodeCompactOutput(t, stdout.Bytes())
	if output.Ran {
		t.Fatalf("output = %+v, want Ran=false", output)
	}
	if output.SessionID != string(sessionID) || output.Strategy != domain.ContextStrategySummary {
		t.Fatalf("output = %+v, want sessionId=%s strategy=summary", output, sessionID)
	}
	wantStderr := fmt.Sprintf("och: nothing to compact for session %s\n", sessionID)
	if stderr.String() != wantStderr {
		t.Fatalf("stderr = %q, want %q", stderr.String(), wantStderr)
	}
}

func TestCompactSessionSummaryStrategySucceeds(t *testing.T) {
	workspace := t.TempDir()
	database := filepath.Join(t.TempDir(), "harness.db")
	events := syntheticCompactCLITurns(15)
	sessionID := seedCompactCLISession(t, database, events)

	t.Setenv("OCH_COMPACT_CLI_TEST_KEY", "test-key")
	server := singleShotSummaryProvider(t, "## Objective\nShip the feature.\n## User Constraints\nNone.\n## Established Facts\nThe session discussed a roadmap.\n## Work Completed\nNone.\n## Files and Commands\nNone.\n## Open Work\nNone.\n## Risks and Unknowns\nNone.\n## Continuation\nProceed.")
	var stdout, stderr bytes.Buffer
	err := compactSession(context.Background(), compactCLIArgs(workspace, database, server.URL, string(sessionID)), &stdout, &stderr)
	if err != nil {
		t.Fatalf("compact-session error = %v", err)
	}
	if server.requests.Load() != 1 {
		t.Fatalf("provider requests = %d, want exactly 1 (the summarizer call)", server.requests.Load())
	}

	output := decodeCompactOutput(t, stdout.Bytes())
	if !output.Ran || output.CheckpointID == "" || output.CheckpointKind != string(domain.ContextCheckpointKindRollingSummary) {
		t.Fatalf("output = %+v, want Ran=true with a rolling-summary checkpoint", output)
	}
	if output.CoveredEventCount == 0 || output.ThroughSequence == 0 {
		t.Fatalf("output = %+v, want nonzero coverage", output)
	}
	if !strings.Contains(stderr.String(), "och: compacted session "+string(sessionID)) {
		t.Fatalf("stderr = %q, want a compacted-session summary line", stderr.String())
	}

	// The durable stream is the authority: confirm the checkpoint actually
	// landed, not merely that the CLI printed something plausible.
	store, err := sqlite.Open(context.Background(), sqlite.Config{Path: database, RuntimeID: "compact-cli-verify"})
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() { _ = store.Close() }()
	lookup, err := store.LoadLatestContextCheckpoint(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("LoadLatestContextCheckpoint: %v", err)
	}
	if lookup.Status != application.ContextCheckpointLookupFound || lookup.Checkpoint.ID != output.CheckpointID {
		t.Fatalf("lookup = %+v, want Found matching the CLI's own reported checkpoint ID %q", lookup, output.CheckpointID)
	}
}

func TestCompactSessionResetStrategyNeverCallsTheProvider(t *testing.T) {
	workspace := t.TempDir()
	database := filepath.Join(t.TempDir(), "harness.db")
	events := syntheticCompactCLITurns(15)
	sessionID := seedCompactCLISession(t, database, events)

	t.Setenv("OCH_COMPACT_CLI_TEST_KEY", "test-key")
	server := failingProvider(t)
	args := append(compactCLIArgs(workspace, database, server.URL, string(sessionID)), "-strategy", "reset")
	var stdout, stderr bytes.Buffer
	err := compactSession(context.Background(), args, &stdout, &stderr)
	if err != nil {
		t.Fatalf("compact-session -strategy reset error = %v", err)
	}

	output := decodeCompactOutput(t, stdout.Bytes())
	if !output.Ran || output.CheckpointKind != string(domain.ContextCheckpointKindSourceTailReset) || output.Strategy != domain.ContextStrategyReset {
		t.Fatalf("output = %+v, want Ran=true with a source-tail-reset checkpoint", output)
	}
}

func compactCLIArgs(workspace, database, providerURL, sessionID string) []string {
	return []string{
		"-workspace", workspace,
		"-database", database,
		"-runtime-id", "compact-cli-test",
		"-provider-url", providerURL,
		"-provider-allow-insecure-loopback",
		"-model", "test-model",
		"-api-key-env", "OCH_COMPACT_CLI_TEST_KEY",
		"-context-window", "8192",
		"-max-output", "1024",
		"-allow-unsandboxed-exec",
		"-session", sessionID,
	}
}

// syntheticCompactCLITurns builds turnCount Turns' worth of canonical
// source events with enough real content per Turn that their combined
// estimated size comfortably exceeds design §8's default protectedTail
// (25% of hardInput) at the small context-window compactCLIArgs uses --
// otherwise SelectCutPoint's own tail-protection walk retains the whole,
// tiny fixture history and there is nothing left to cover.
func syntheticCompactCLITurns(turnCount int) []domain.Event {
	input := strings.Repeat("please review the project notes and describe what changed recently. ", 6)
	reply := strings.Repeat("the notes describe several changes across the workspace in detail. ", 8)
	var events []domain.Event
	for i := 0; i < turnCount; i++ {
		turnID := domain.TurnID(fmt.Sprintf("turn-%d", i))
		itemID := domain.ItemID(fmt.Sprintf("item-%d", i))
		events = append(events,
			domain.TurnStarted{TurnID: turnID, Input: input},
			domain.AssistantMessageStarted{TurnID: turnID, ItemID: itemID},
			domain.AssistantMessageCompleted{TurnID: turnID, ItemID: itemID, Text: reply},
			domain.TurnCompleted{TurnID: turnID},
		)
	}
	return events
}

func decodeCompactOutput(t *testing.T, data []byte) compactSessionOutput {
	t.Helper()
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		t.Fatal("stdout is empty, want one JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	var output compactSessionOutput
	if err := decoder.Decode(&output); err != nil {
		t.Fatalf("unmarshal stdout %s: %v", trimmed, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		t.Fatalf("stdout %s decoded more than one JSON value", trimmed)
	}
	return output
}

// seedCompactCLISession opens its own store directly (mirroring
// newCLIDatabase/seedCLISession's own established pattern in
// main_test.go), appends a SessionCreated plus whatever events are given,
// and closes the store again -- compact-session itself must be able to
// reopen and lock the database cleanly afterward.
func seedCompactCLISession(t *testing.T, databasePath string, events []domain.Event) domain.SessionID {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(context.Background(), sqlite.Config{Path: databasePath, RuntimeID: "compact-cli-seed"})
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	sessionID := domain.SessionID("session-compact-cli")
	all := append([]domain.Event{domain.SessionCreated{WorkspaceRoot: "/workspace"}}, events...)
	proposed := make([]application.ProposedEvent, len(all))
	now := time.Now().UTC()
	for i, event := range all {
		proposed[i] = application.ProposedEvent{
			ID: domain.EventID(fmt.Sprintf("event-compact-cli-%d", i)), SchemaVersion: 1, OccurredAt: now, Event: event,
		}
	}
	if _, err := store.Append(context.Background(), application.AppendRequest{
		AppendID: "append-compact-cli-seed", SessionID: sessionID, ExpectedVersion: 0,
		CommandID: "command-compact-cli-seed", Authority: store.Authority(), Events: proposed,
	}); err != nil {
		t.Fatalf("seed Append() error = %v", err)
	}
	// compact-session opens its own fresh writer lease afterward: release
	// this seeding lease explicitly rather than leaving it to expire, so
	// the real test does not race the lease's own default duration.
	if err := store.ReleaseLease(context.Background()); err != nil {
		t.Fatalf("seed release lease: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("seed close: %v", err)
	}
	return sessionID
}

type compactCLIProvider struct {
	*httptest.Server
	requests atomic.Int64
}

// singleShotSummaryProvider returns a fixture OpenAI-compatible SSE server
// that always answers with the given text as one streamed content delta --
// enough for the summarizer's own single Collect call.
func singleShotSummaryProvider(t *testing.T, text string) *compactCLIProvider {
	t.Helper()
	provider := &compactCLIProvider{}
	provider.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provider.requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		encoded, err := json.Marshal(text)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%s},\"finish_reason\":null}]}\n\n", encoded)
		if flusher != nil {
			flusher.Flush()
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	t.Cleanup(provider.Server.Close)
	return provider
}

// failingProvider fails any request it receives -- used to prove a
// strategy or no-op path never dispatches a summarizer call at all.
func failingProvider(t *testing.T) *compactCLIProvider {
	t.Helper()
	provider := &compactCLIProvider{}
	provider.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provider.requests.Add(1)
		http.Error(w, "compact-session test: no provider call was expected", http.StatusInternalServerError)
	}))
	t.Cleanup(provider.Server.Close)
	return provider
}
