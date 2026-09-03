package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
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
	"github.com/SongYii/open-code-harness/internal/harness/composition"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/eval"
	"github.com/SongYii/open-code-harness/internal/harness/policy"
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

// sentinelParitySubject sets every Provider/Policy/Limits/Context field a
// non-default, non-zero value so TestBindAssemblyFlagsMatchesBuildConfig
// cannot pass by both routes silently agreeing on a shared zero value.
func sentinelParitySubject() eval.Subject {
	return eval.Subject{
		FormatVersion:      eval.FormatVersion,
		Schema:             eval.SchemaSubject,
		ID:                 "parity-subject",
		RepositoryRevision: "sentinel-revision",
		Provider: eval.SubjectProvider{
			AdapterKind:        "openaicompat",
			NormalizedEndpoint: "https://provider.invalid/v1",
			ModelID:            "sentinel-model",
			ContextWindow:      111111,
			MaxOutput:          22222,
			CredentialEnvVar:   "OCH_TEST_SENTINEL_KEY",
			Lane:               eval.ProviderLaneFixture,
		},
		Context: eval.SubjectContext{
			TriggerPercent:                 91,
			TargetPercent:                  61,
			TailPercent:                    31,
			MaxSummaryChunks:               12,
			MaxOverflowCompactionsPerTurn:  3,
			MaxPrunedToolResultsPerRequest: 40,
			CompactionTimeout:              3*time.Minute + 7*time.Second,
		},
		Policy: eval.SubjectPolicy{
			Mode:                string(policy.ModeAllowWrites),
			ToolCatalogIdentity: "catalog-v2",
			Limits: eval.SubjectLimits{
				MaxSteps:            17,
				MaxToolCallsPerStep: 5,
				MaxAssistantBytes:   90000,
				ApprovalTimeout:     45 * time.Second,
			},
			SandboxPolicy: eval.SandboxPolicySandboxed,
		},
	}
}

// TestBindAssemblyFlagsMatchesBuildConfig proves the CLI and in-process
// executor routes agree field-for-field on every Provider/Policy/Limits/
// Context value a Subject can carry (implementation plan Task 11):
// eval.NormalizedArgv derives och's own flags from a sentinel Subject with
// every field at a non-default value, bindAssemblyFlags parses that exact
// argv, and the resulting composition.Config must equal what
// eval.BuildConfig produces from the same Subject directly. A future
// Subject semantic field with no argv mapping shows up here as a silent
// zero-value mismatch, not a passing test — the table below enumerates
// every field this comparison covers so a missing one is visible in the
// diff, not just in coverage.
func TestBindAssemblyFlagsMatchesBuildConfig(t *testing.T) {
	subject := sentinelParitySubject()

	directories := eval.AttemptRootDirectories{Workspace: "/attempt/workspace", Database: "/attempt/database"}
	inProcess, err := eval.BuildConfig(subject, directories, "runtime-parity-test", nil)
	if err != nil {
		t.Fatalf("BuildConfig() error = %v", err)
	}

	argv, err := eval.NormalizedArgv(subject)
	if err != nil {
		t.Fatalf("NormalizedArgv() error = %v", err)
	}
	// A launcher appends "-acp" plus this Attempt's own path flags; argv
	// itself must carry none of those (design's own "no Attempt-specific
	// path in Executor identity" rule), so this test adds them here,
	// exactly as Task 12's real launcher will.
	fullArgv := append([]string{
		"-acp",
		"-workspace", directories.Workspace,
		"-database", directories.Database,
		"-runtime-id", "runtime-parity-test",
	}, argv...)

	flags := flag.NewFlagSet("och", flag.ContinueOnError)
	parsedConfig := composition.Config{}
	var parsedPolicyMode string
	var uintFlags assemblyUintFlags
	bindAssemblyFlags(flags, &parsedConfig, &parsedPolicyMode, &uintFlags)
	serveACP := flags.Bool("acp", false, "")
	if err := flags.Parse(fullArgv); err != nil {
		t.Fatalf("flags.Parse(%v) error = %v", fullArgv, err)
	}
	if !*serveACP {
		t.Fatal("-acp did not parse as set")
	}
	uintFlags.apply(&parsedConfig)
	parsedConfig.Policy = policy.Mode(parsedPolicyMode)

	for _, field := range []struct {
		name          string
		fromArgv      any
		fromInProcess any
	}{
		{"Provider.BaseURL", parsedConfig.Provider.BaseURL, inProcess.Provider.BaseURL},
		{"Provider.ModelID", parsedConfig.Provider.ModelID, inProcess.Provider.ModelID},
		{"Provider.APIKeyEnv", parsedConfig.Provider.APIKeyEnv, inProcess.Provider.APIKeyEnv},
		{"Provider.ContextWindow", parsedConfig.Provider.ContextWindow, inProcess.Provider.ContextWindow},
		{"Provider.MaxOutput", parsedConfig.Provider.MaxOutput, inProcess.Provider.MaxOutput},
		{"Provider.AllowInsecureLoopback", parsedConfig.Provider.AllowInsecureLoopback, inProcess.Provider.AllowInsecureLoopback},
		{"Policy", parsedConfig.Policy, inProcess.Policy},
		{"Limits.MaxSteps", parsedConfig.Limits.MaxSteps, inProcess.Limits.MaxSteps},
		{"Limits.MaxToolCallsPerStep", parsedConfig.Limits.MaxToolCallsPerStep, inProcess.Limits.MaxToolCallsPerStep},
		{"Limits.MaxAssistantBytes", parsedConfig.Limits.MaxAssistantBytes, inProcess.Limits.MaxAssistantBytes},
		{"Limits.ApprovalTimeout", parsedConfig.Limits.ApprovalTimeout, inProcess.Limits.ApprovalTimeout},
		{"Context.TriggerPercent", parsedConfig.Context.TriggerPercent, inProcess.Context.TriggerPercent},
		{"Context.TargetPercent", parsedConfig.Context.TargetPercent, inProcess.Context.TargetPercent},
		{"Context.TailPercent", parsedConfig.Context.TailPercent, inProcess.Context.TailPercent},
		{"Context.MaxSummaryChunks", parsedConfig.Context.MaxSummaryChunks, inProcess.Context.MaxSummaryChunks},
		{"Context.MaxOverflowCompactionsPerTurn", parsedConfig.Context.MaxOverflowCompactionsPerTurn, inProcess.Context.MaxOverflowCompactionsPerTurn},
		{"Context.MaxPrunedToolResultsPerRequest", parsedConfig.Context.MaxPrunedToolResultsPerRequest, inProcess.Context.MaxPrunedToolResultsPerRequest},
		{"Context.CompactionTimeout", parsedConfig.Context.CompactionTimeout, inProcess.Context.CompactionTimeout},
		{"AllowUnsandboxedExec", parsedConfig.AllowUnsandboxedExec, inProcess.AllowUnsandboxedExec},
	} {
		if field.fromArgv != field.fromInProcess {
			t.Errorf("%s: CLI route = %v, in-process route = %v", field.name, field.fromArgv, field.fromInProcess)
		}
	}
}

// TestNormalizedArgvCarriesNoCredentialValue proves NormalizedArgv never
// emits the credential's own value -- only its environment variable name
// -- even when that value happens to be set in this test process's own
// environment under the same name.
func TestNormalizedArgvCarriesNoCredentialValue(t *testing.T) {
	subject := sentinelParitySubject()
	t.Setenv(subject.Provider.CredentialEnvVar, "super-secret-value-must-not-leak")

	argv, err := eval.NormalizedArgv(subject)
	if err != nil {
		t.Fatalf("NormalizedArgv() error = %v", err)
	}
	for _, arg := range argv {
		if strings.Contains(arg, "super-secret-value-must-not-leak") {
			t.Fatalf("argv leaked the credential value: %v", argv)
		}
	}
}

// TestNormalizedArgvOmitsZeroLimitsButAlwaysEmitsContext confirms the CLI
// route's own zero-means-default convention is preserved for the one
// place Subject legitimately allows a zero field: a Subject that leaves
// every optional Policy.Limits field at zero produces argv naming none of
// their flags, so bindAssemblyFlags leaves them at zero too and Open
// applies its own Application default, exactly like an in-process
// BuildConfig call over the same zero Subject fields would.
// Subject.Context has no such zero state at all — SubjectContext.validate
// requires every field positive — so every Context flag is always
// present, proven here rather than assumed.
func TestNormalizedArgvOmitsZeroLimitsButAlwaysEmitsContext(t *testing.T) {
	subject := sentinelParitySubject()
	subject.Policy.Limits = eval.SubjectLimits{}

	argv, err := eval.NormalizedArgv(subject)
	if err != nil {
		t.Fatalf("NormalizedArgv() error = %v", err)
	}
	for _, flagName := range []string{
		"-max-steps", "-max-tool-calls-per-step", "-max-assistant-bytes", "-approval-timeout",
	} {
		if containsString(argv, flagName) {
			t.Fatalf("argv %v unexpectedly names zero-valued Limits flag %q", argv, flagName)
		}
	}
	for _, flagName := range []string{
		"-context-trigger-percent", "-context-target-percent", "-context-tail-percent",
		"-context-max-summary-chunks", "-context-max-overflow-compactions-per-turn",
		"-context-max-pruned-tool-results-per-request", "-context-compaction-timeout",
	} {
		if !containsString(argv, flagName) {
			t.Fatalf("argv %v is missing always-required Context flag %q", argv, flagName)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
