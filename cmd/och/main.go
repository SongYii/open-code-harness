// Command och runs one Open Code Harness assembly until it is signalled to
// stop, or exports a session transcript.
//
// It contains flag parsing, signal handling, and the atomic -output publish
// for export-session. Every other decision belongs to
// internal/harness/composition, which is a library so that assembly can be
// asserted by tests rather than by launching a process.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/composition"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/policy"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "och:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) > 0 && arguments[0] == "export-session" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return exportSession(ctx, arguments[1:], os.Stdout, os.Stderr)
	}
	if len(arguments) > 0 && arguments[0] == "compact-session" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return compactSession(ctx, arguments[1:], os.Stdout, os.Stderr)
	}

	flags := flag.NewFlagSet("och", flag.ContinueOnError)
	config := composition.Config{}
	var policyMode string
	var contextWindow, maxOutput uint
	bindAssemblyFlags(flags, &config, &policyMode, &contextWindow, &maxOutput)
	serveACP := flags.Bool("acp", false, "serve ACP v1 JSON-RPC on stdin/stdout (stderr for diagnostics)")

	if err := flags.Parse(arguments); err != nil {
		return err
	}
	config.Provider.ContextWindow = uint32(contextWindow)
	config.Provider.MaxOutput = uint32(maxOutput)
	config.Policy = policy.Mode(policyMode)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	assembly, err := composition.Open(ctx, config)
	if err != nil {
		return err
	}
	if *serveACP {
		fmt.Fprintf(os.Stderr, "och: acp v1 on stdin/stdout; workspace=%s\n", config.WorkspaceRoot)
		serveErr := assembly.ServeACP(ctx, os.Stdin, os.Stdout)
		stop()
		closeErr := assembly.Close()
		return errors.Join(serveErr, closeErr)
	}
	fmt.Fprintf(os.Stdout, "och: ready; workspace=%s database=%s runtime=%s\n",
		config.WorkspaceRoot, config.DatabasePath, config.RuntimeID)

	<-ctx.Done()
	// The signal context is already cancelled, so shutdown gets its own bound
	// from Config rather than inheriting a dead deadline.
	stop()
	fmt.Fprintln(os.Stdout, "och: shutting down")
	start := time.Now()
	if err := assembly.Close(); err != nil {
		return fmt.Errorf("shutdown after %s: %w", time.Since(start).Round(time.Millisecond), err)
	}
	return nil
}

// bindAssemblyFlags registers every flag composition.Config needs to open a
// full assembly, shared between the serve path (run) and compact-session:
// both open the same normal composition root and so need the same
// workspace/database/provider/policy surface. export-session is
// deliberately not built on this — it never opens a full assembly, only a
// read-only sqlite reader (composition.ExportSession), so it keeps its own
// small, independent flag set.
func bindAssemblyFlags(flags *flag.FlagSet, config *composition.Config, policyMode *string, contextWindow, maxOutput *uint) {
	flags.StringVar(&config.WorkspaceRoot, "workspace", "", "absolute path to the workspace root (required)")
	flags.StringVar(&config.DatabasePath, "database", "", "absolute path to the SQLite database (required)")
	flags.StringVar(&config.RuntimeID, "runtime-id", "", "writer identity for the fencing lease (required)")
	flags.StringVar(&config.AuditDirectory, "audit-dir", "", "directory for the JSONL audit replica; empty disables the exporter")
	flags.StringVar(&config.Provider.BaseURL, "provider-url", "", "OpenAI-compatible base URL (required)")
	flags.BoolVar(&config.Provider.AllowInsecureLoopback, "provider-allow-insecure-loopback", false, "permit a plain-HTTP provider-url when it resolves to loopback; for a local fixture server only, never a real endpoint")
	flags.StringVar(&config.Provider.ModelID, "model", "", "provider model identifier (required)")
	flags.StringVar(&config.Provider.APIKeyEnv, "api-key-env", "OCH_API_KEY", "environment variable holding the provider API key")
	*contextWindow = 0
	*maxOutput = 0
	flags.UintVar(contextWindow, "context-window", 0, "provider context window in tokens (required)")
	flags.UintVar(maxOutput, "max-output", 0, "provider maximum output in tokens (required)")
	flags.StringVar(policyMode, "policy", string(policy.ModeDefault), "policy mode: default, read_only, allow_writes, deny_all")
	flags.DurationVar(&config.ShutdownTimeout, "shutdown-timeout", composition.DefaultShutdownTimeout, "how long shutdown may wait for the host's loops")
	flags.BoolVar(&config.AllowUnsandboxedExec, "allow-unsandboxed-exec", false, "proceed even if no OS-level exec sandbox (bwrap, sandbox-exec) is available on this host; logs which guarantee is absent")
}

// compactSessionOutput is compact-session's one stable JSON object on
// stdout (design CE-14). Fields left at their zero value when Ran is false
// are omitted rather than printed as misleading zeros.
type compactSessionOutput struct {
	Ran                    bool   `json:"ran"`
	SessionID              string `json:"sessionId"`
	Strategy               string `json:"strategy"`
	CheckpointID           string `json:"checkpointId,omitempty"`
	CheckpointKind         string `json:"checkpointKind,omitempty"`
	CoveredEventCount      uint64 `json:"coveredEventCount,omitempty"`
	CoveredTurnCount       uint64 `json:"coveredTurnCount,omitempty"`
	ThroughSequence        uint64 `json:"throughSequence,omitempty"`
	TokensBefore           uint64 `json:"tokensBefore,omitempty"`
	CheckpointTokens       uint64 `json:"checkpointTokens,omitempty"`
	EstimatedRequestTokens uint64 `json:"estimatedRequestTokens,omitempty"`
}

// compactSession implements design CE-14's `och compact-session`: it opens
// the normal composition root exactly like the serve path does (acquiring
// the Runtime lease; failing rather than operating beside another live
// writer, matching export-session's own sqlite.OpenReader-vs-lease
// precedent but on the write side), runs one manual compaction, and prints
// one stable JSON object to stdout with a human-readable summary on
// stderr — export-session's own stdout/stderr discipline.
func compactSession(ctx context.Context, arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("och compact-session", flag.ContinueOnError)
	flags.SetOutput(stderr)
	config := composition.Config{}
	var policyMode string
	var contextWindow, maxOutput uint
	bindAssemblyFlags(flags, &config, &policyMode, &contextWindow, &maxOutput)
	session := flags.String("session", "", "session id to compact (required)")
	strategy := flags.String("strategy", "", "compaction strategy: summary (default) or reset")
	focus := flags.String("focus", "", "optional operator focus string for a summary strategy, bounded to 4 KiB UTF-8")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *session == "" {
		return fmt.Errorf("session is required")
	}
	config.Provider.ContextWindow = uint32(contextWindow)
	config.Provider.MaxOutput = uint32(maxOutput)
	config.Policy = policy.Mode(policyMode)

	assembly, err := composition.Open(ctx, config)
	if err != nil {
		return err
	}
	result, compactErr := assembly.Service().CompactSession(ctx, application.CompactSessionRequest{
		SessionID: domain.SessionID(*session), Strategy: *strategy, Focus: *focus,
	})
	closeErr := assembly.Close()
	if compactErr != nil {
		return errors.Join(compactErr, closeErr)
	}
	if closeErr != nil {
		return closeErr
	}

	effectiveStrategy := *strategy
	if effectiveStrategy == "" {
		effectiveStrategy = domain.ContextStrategySummary
	}
	encoded, err := json.Marshal(compactSessionOutput{
		Ran: result.Ran, SessionID: *session, Strategy: effectiveStrategy,
		CheckpointID: result.CheckpointID, CheckpointKind: result.CheckpointKind,
		CoveredEventCount: result.CoveredEventCount, CoveredTurnCount: result.CoveredTurnCount,
		ThroughSequence: result.ThroughSequence, TokensBefore: result.TokensBefore,
		CheckpointTokens: result.CheckpointTokens, EstimatedRequestTokens: result.EstimatedRequestTokens,
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, string(encoded))
	if result.Ran {
		fmt.Fprintf(stderr, "och: compacted session %s checkpoint=%s kind=%s covered_events=%d covered_turns=%d\n",
			*session, result.CheckpointID, result.CheckpointKind, result.CoveredEventCount, result.CoveredTurnCount)
	} else {
		fmt.Fprintf(stderr, "och: nothing to compact for session %s\n", *session)
	}
	return nil
}

func exportSession(ctx context.Context, arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("och export-session", flag.ContinueOnError)
	flags.SetOutput(stderr)
	database := flags.String("database", "", "absolute path to the SQLite database (required)")
	session := flags.String("session", "", "session id to export (required)")
	output := flags.String("output", "", "write JSONL to PATH instead of stdout")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *database == "" {
		return fmt.Errorf("database is required")
	}
	if *session == "" {
		return fmt.Errorf("session is required")
	}

	sessionID := domain.SessionID(*session)
	if *output == "" {
		result, err := composition.ExportSession(ctx, *database, sessionID, stdout)
		if err != nil {
			return err
		}
		fmt.Fprintf(stderr, "och: exported session %s facts=%d head=%d open=%t running=%t\n",
			*session, result.FactLines, result.HeadSequence, result.Open, result.Running)
		return nil
	}
	return exportSessionToPath(ctx, *database, sessionID, *session, *output, stderr)
}

func exportSessionToPath(ctx context.Context, databasePath string, sessionID domain.SessionID, session, outputPath string, stderr io.Writer) error {
	dir := filepath.Dir(outputPath)
	temp, err := os.CreateTemp(dir, ".och-export-session-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	published := false
	defer func() {
		if temp != nil {
			_ = temp.Close()
		}
		if !published {
			_ = os.Remove(tempName)
		}
	}()

	result, err := composition.ExportSession(ctx, databasePath, sessionID, temp)
	if err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		temp = nil
		return err
	}
	temp = nil
	if err := os.Rename(tempName, outputPath); err != nil {
		return err
	}
	published = true
	fmt.Fprintf(stderr, "och: exported session %s facts=%d head=%d open=%t running=%t\n",
		session, result.FactLines, result.HeadSequence, result.Open, result.Running)
	return nil
}
