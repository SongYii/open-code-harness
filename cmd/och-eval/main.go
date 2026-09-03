// Command och-eval runs Milestone 10's evaluation subsystem: Stage A's
// deterministic in-process foundation only (design's own accepted first
// slice) — an ACP-subprocess Cell is refused before any Attempt is
// created, not silently skipped, until Stage B registers that executor.
//
// It prints exactly one versioned JSON document on stdout per invocation
// and human-readable diagnostics on stderr, and exits with one of five
// stable classes (see exit.go): success, validation error, deterministic
// gate failure, infrastructure failure, or indeterminate completion — plus
// an internal-error class for anything this command failed to classify.
// Every other decision belongs to internal/harness/eval, which is a
// library so evaluation behavior can be asserted by tests rather than by
// launching a process.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(runCLI(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func runCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "och-eval: missing subcommand: run, regrade, report, or judge")
		return exitValidation
	}
	switch args[0] {
	case "run":
		return runCommand(ctx, args[1:], stdout, stderr)
	case "regrade":
		return regradeCommand(args[1:], stdout, stderr)
	case "report":
		return reportCommand(args[1:], stdout, stderr)
	case "judge":
		return judgeCommand(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "och-eval: unknown subcommand %q: want run, regrade, report, or judge\n", args[0])
		return exitValidation
	}
}
