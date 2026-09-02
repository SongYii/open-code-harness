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
	if err := checkOnlyInProcessExecutors(tree.Executors); err != nil {
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

// checkLaneConsent implements design's live dual-consent gate: -live must
// be passed if and only if the EvalSet's own declared lane is live, and a
// live lane additionally requires an explicit environment confirmation
// before any credential is ever read.
func checkLaneConsent(set eval.EvalSet, live bool) error {
	switch set.Lane {
	case eval.LaneFixture:
		if live {
			return fmt.Errorf("-live was passed but this EvalSet's lane is %q", set.Lane)
		}
	case eval.LaneLive:
		if !live {
			return fmt.Errorf("this EvalSet's lane is %q: pass -live to confirm", set.Lane)
		}
		if os.Getenv("OCH_EVAL_LIVE_CONFIRM") != "I_UNDERSTAND" {
			return fmt.Errorf("live lane requires OCH_EVAL_LIVE_CONFIRM=I_UNDERSTAND before any credential is read")
		}
	default:
		return fmt.Errorf("unknown lane %q", set.Lane)
	}
	return nil
}

// checkOnlyInProcessExecutors implements Stage A's own restriction
// (implementation plan Task 10): an acp_subprocess Cell is refused before
// any Attempt is created, not silently skipped, until Stage B registers
// that executor.
func checkOnlyInProcessExecutors(executors map[eval.ExecutorID]eval.Executor) error {
	for _, executor := range executors {
		if executor.Kind != eval.ExecutorInProcess {
			return fmt.Errorf("executor %q: Stage A's CLI supports only %q, got %q", executor.ID, eval.ExecutorInProcess, executor.Kind)
		}
	}
	return nil
}
