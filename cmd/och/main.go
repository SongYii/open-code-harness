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
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

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

	flags := flag.NewFlagSet("och", flag.ContinueOnError)
	config := composition.Config{}
	var policyMode string

	flags.StringVar(&config.WorkspaceRoot, "workspace", "", "absolute path to the workspace root (required)")
	flags.StringVar(&config.DatabasePath, "database", "", "absolute path to the SQLite database (required)")
	flags.StringVar(&config.RuntimeID, "runtime-id", "", "writer identity for the fencing lease (required)")
	flags.StringVar(&config.AuditDirectory, "audit-dir", "", "directory for the JSONL audit replica; empty disables the exporter")
	flags.StringVar(&config.Provider.BaseURL, "provider-url", "", "OpenAI-compatible base URL (required)")
	flags.StringVar(&config.Provider.ModelID, "model", "", "provider model identifier (required)")
	flags.StringVar(&config.Provider.APIKeyEnv, "api-key-env", "OCH_API_KEY", "environment variable holding the provider API key")
	contextWindow := flags.Uint("context-window", 0, "provider context window in tokens (required)")
	maxOutput := flags.Uint("max-output", 0, "provider maximum output in tokens (required)")
	flags.StringVar(&policyMode, "policy", string(policy.ModeDefault), "policy mode: default, read_only, allow_writes, deny_all")
	flags.DurationVar(&config.ShutdownTimeout, "shutdown-timeout", composition.DefaultShutdownTimeout, "how long shutdown may wait for the host's loops")
	serveACP := flags.Bool("acp", false, "serve ACP v1 JSON-RPC on stdin/stdout (stderr for diagnostics)")

	if err := flags.Parse(arguments); err != nil {
		return err
	}
	config.Provider.ContextWindow = uint32(*contextWindow)
	config.Provider.MaxOutput = uint32(*maxOutput)
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
