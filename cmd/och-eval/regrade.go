package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

// regradeCommand's own flag set carries no Subject execution
// configuration whatsoever — no provider endpoint, no credential, no
// sandbox policy, nothing capable of constructing an Executor, Provider,
// or Service. It only ever reads already-committed, manifest-constrained
// frozen evidence through eval.RegradeAttempt.
func regradeCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("och-eval regrade", flag.ContinueOnError)
	flags.SetOutput(stderr)
	attemptPath := flags.String("attempt", "", "path to the Attempt's publication root")
	scorerID := flags.String("scorer", "", "compiled scorer id")
	if err := flags.Parse(args); err != nil {
		return exitValidation
	}
	if *attemptPath == "" || *scorerID == "" {
		fmt.Fprintln(stderr, "och-eval regrade: -attempt and -scorer are both required")
		return exitValidation
	}
	scorer, ok := scorerCatalog[*scorerID]
	if !ok {
		fmt.Fprintf(stderr, "och-eval regrade: unknown scorer id %q\n", *scorerID)
		return exitValidation
	}

	directories := eval.AttemptRootDirectoriesFor(*attemptPath)
	score, err := eval.RegradeAttempt(directories, scorer)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval regrade:", err)
		return exitValidation
	}

	data, err := jsonEncode(score)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval regrade:", err)
		return exitInternal
	}
	if _, err := stdout.Write(data); err != nil {
		fmt.Fprintln(stderr, "och-eval regrade:", err)
		return exitInternal
	}

	switch score.Verdict {
	case eval.ScoreFail:
		return exitGateFailure
	case eval.ScoreIndeterminate:
		return exitIndeterminate
	default:
		return exitOK
	}
}
