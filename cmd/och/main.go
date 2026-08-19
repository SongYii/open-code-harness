// Command och runs one Open Code Harness assembly until it is signalled to
// stop.
//
// It contains flag parsing and signal handling and nothing else. Every
// decision belongs to internal/harness/composition, which is a library so
// that assembly can be asserted by tests rather than by launching a process.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/composition"
	"github.com/SongYii/open-code-harness/internal/harness/policy"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "och:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
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
