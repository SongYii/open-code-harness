package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

const runReportSchema = "och.eval.cli.run-report"

type runReport struct {
	FormatVersion int                  `json:"formatVersion"`
	Schema        string               `json:"schema"`
	SetID         eval.EvalSetID       `json:"setId"`
	ArtifactRoot  string               `json:"artifactRoot"`
	Attempts      []attemptReportEntry `json:"attempts"`
}

type attemptReportEntry struct {
	ScenarioID       eval.ScenarioID `json:"scenarioId"`
	SubjectID        eval.SubjectID  `json:"subjectId"`
	ExecutorID       eval.ExecutorID `json:"executorId"`
	RepetitionIndex  int             `json:"repetitionIndex"`
	AttemptID        eval.AttemptID  `json:"attemptId,omitempty"`
	Status           string          `json:"status,omitempty"`
	CollectionStatus string          `json:"collectionStatus,omitempty"`
	Error            string          `json:"error,omitempty"`
}

func runCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("och-eval run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	setPath := flags.String("set", "", "path to the EvalSet document")
	artifactRootFlag := flags.String("artifacts", "", "absolute path Attempts are published under")
	live := flags.Bool("live", false, "confirm this EvalSet's lane is live (design's dual-consent gate)")
	ochBinaryFlag := flags.String("och-binary", "", "path to an already-built och binary; required only when this EvalSet references an acp_subprocess Executor")
	if err := flags.Parse(args); err != nil {
		return exitValidation
	}
	if *setPath == "" || *artifactRootFlag == "" {
		fmt.Fprintln(stderr, "och-eval run: -set and -artifacts are both required")
		return exitValidation
	}

	tree, err := loadDocumentTree(*setPath)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval run:", err)
		return exitValidation
	}
	if err := checkLaneConsent(tree.Set, *live); err != nil {
		fmt.Fprintln(stderr, "och-eval run:", err)
		return exitValidation
	}

	var acpLaunch eval.ACPLaunchConfig
	if *ochBinaryFlag != "" {
		binary, err := eval.ResolveACPBinary(*ochBinaryFlag)
		if err != nil {
			fmt.Fprintln(stderr, "och-eval run:", err)
			return exitValidation
		}
		acpLaunch.Binary = binary
	}
	if err := checkACPExecutorsHaveABinary(tree.Executors, acpLaunch); err != nil {
		fmt.Fprintln(stderr, "och-eval run:", err)
		return exitValidation
	}

	artifactRoot, err := filepath.Abs(*artifactRootFlag)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval run:", err)
		return exitValidation
	}
	for _, scenarioRef := range tree.Set.Scenarios {
		if err := eval.RefuseArtifactRootWithinFixture(artifactRoot, tree.FixtureSources[scenarioRef.ID]); err != nil {
			fmt.Fprintln(stderr, "och-eval run:", err)
			return exitValidation
		}
	}
	if err := os.MkdirAll(artifactRoot, 0o700); err != nil {
		fmt.Fprintln(stderr, "och-eval run:", err)
		return exitInternal
	}

	providerEndpointOverrides, cleanup, err := resolveFixtureSubjects(tree.Subjects)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		fmt.Fprintln(stderr, "och-eval run:", err)
		return exitValidation
	}

	results, err := eval.RunEvalSet(ctx, eval.RunnerInputs{
		Set:                       tree.Set,
		Scenarios:                 tree.Scenarios,
		Subjects:                  tree.Subjects,
		Executors:                 tree.Executors,
		FixtureSources:            tree.FixtureSources,
		ProviderEndpointOverrides: providerEndpointOverrides,
		ArtifactRootOverride:      artifactRoot,
		ACPLaunch:                 acpLaunch,
	})
	if err != nil {
		fmt.Fprintln(stderr, "och-eval run:", err)
		return exitValidation
	}

	report := runReport{FormatVersion: 1, Schema: runReportSchema, SetID: tree.Set.ID, ArtifactRoot: artifactRoot}
	exitCode := exitOK
	for _, result := range results {
		entry := attemptReportEntry{
			ScenarioID: result.Cell.ScenarioID, SubjectID: result.Cell.SubjectID, ExecutorID: result.Cell.ExecutorID,
			RepetitionIndex: result.RepetitionIndex, AttemptID: result.AttemptID,
		}
		if result.Err != nil {
			entry.Error = result.Err.Error()
			exitCode = maxExitCode(exitCode, exitInfraFailure)
		} else {
			entry.Status = string(result.Outcome.Status)
			entry.CollectionStatus = string(result.Outcome.CollectionStatus)
			switch result.Outcome.Status {
			case eval.OutcomeInfraFailed:
				exitCode = maxExitCode(exitCode, exitInfraFailure)
			case eval.OutcomeIndeterminate:
				exitCode = maxExitCode(exitCode, exitIndeterminate)
			}
		}
		report.Attempts = append(report.Attempts, entry)
	}

	data, err := jsonEncode(report)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval run:", err)
		return exitInternal
	}
	if _, err := stdout.Write(data); err != nil {
		fmt.Fprintln(stderr, "och-eval run:", err)
		return exitInternal
	}
	return exitCode
}

func maxExitCode(a, b int) int {
	if b > a {
		return b
	}
	return a
}

// checkLaneConsent implements design's live dual-consent gate by
// delegating to eval.RequireLiveConsent, this repository's own single
// source of truth for it (Task 17): -live must be passed if and only if
// the EvalSet's own declared lane is live, and a live lane additionally
// requires OCH_EVAL_LIVE_CONFIRM=I_UNDERSTAND in the environment before
// any credential is ever read. This function's own job is only to read
// that one environment variable — RequireLiveConsent itself never reads
// the environment, so it never has the opportunity to read a credential
// either.
func checkLaneConsent(set eval.EvalSet, live bool) error {
	return eval.RequireLiveConsent(set.Lane, live, os.Getenv("OCH_EVAL_LIVE_CONFIRM"))
}

// checkACPExecutorsHaveABinary refuses an EvalSet that references an
// acp_subprocess Executor without -och-binary having resolved one, before
// any Attempt is created — RunEvalSet's own whole-set validation would
// catch the same thing, but failing here first gives a clearer message
// naming the missing flag rather than a generic ACPLaunch error.
func checkACPExecutorsHaveABinary(executors map[eval.ExecutorID]eval.Executor, acpLaunch eval.ACPLaunchConfig) error {
	if acpLaunch.Binary.Path != "" {
		return nil
	}
	for _, executor := range executors {
		if executor.Kind == eval.ExecutorACPSubprocess {
			return fmt.Errorf("executor %q is acp_subprocess but -och-binary was not given", executor.ID)
		}
	}
	return nil
}
