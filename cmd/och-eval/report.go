package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

const reportSchema = "och.eval.cli.report"

type evaluationReport struct {
	FormatVersion int                  `json:"formatVersion"`
	Schema        string               `json:"schema"`
	SetID         eval.EvalSetID       `json:"setId"`
	Attempts      []reportAttemptEntry `json:"attempts"`
}

type reportAttemptEntry struct {
	AttemptID        eval.AttemptID     `json:"attemptId"`
	RecoveryState    string             `json:"recoveryState"`
	Status           string             `json:"status,omitempty"`
	CollectionStatus string             `json:"collectionStatus,omitempty"`
	Scores           []reportScoreEntry `json:"scores,omitempty"`
	Error            string             `json:"error,omitempty"`
}

type reportScoreEntry struct {
	ScorerID string `json:"scorerId"`
	Verdict  string `json:"verdict"`
}

// reportCommand aggregates every Attempt already published under an
// EvalSet's artifact root (this EvalSet's own declared artifactRoot
// field, or -artifacts to override it) into one report document. It
// never runs anything — every fact it prints comes from
// eval.ClassifyAttemptDirectory and eval.AssembleEvaluationResult reading
// already-committed documents.
//
// Every published Score is currently treated as gating (this command's
// own scoped simplification, documented rather than silently assumed —
// design's finer required-vs-optional-criterion distinction is not
// modeled by Score itself yet): any Fail verdict anywhere in scope moves
// the exit code to exitGateFailure.
func reportCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("och-eval report", flag.ContinueOnError)
	flags.SetOutput(stderr)
	setPath := flags.String("set", "", "path to the EvalSet document")
	artifactRootFlag := flags.String("artifacts", "", "override the EvalSet document's own artifactRoot field")
	outputPath := flags.String("output", "", "write the report document here instead of stdout")
	if err := flags.Parse(args); err != nil {
		return exitValidation
	}
	if *setPath == "" {
		fmt.Fprintln(stderr, "och-eval report: -set is required")
		return exitValidation
	}

	setData, err := os.ReadFile(*setPath)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval report:", err)
		return exitValidation
	}
	set, err := eval.DecodeEvalSet(setData)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval report:", err)
		return exitValidation
	}

	artifactRoot := set.ArtifactRoot
	if *artifactRootFlag != "" {
		artifactRoot = *artifactRootFlag
	}
	absRoot, err := filepath.Abs(artifactRoot)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval report:", err)
		return exitValidation
	}

	entries, err := os.ReadDir(absRoot)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval report:", err)
		return exitInternal
	}

	report := evaluationReport{FormatVersion: 1, Schema: reportSchema, SetID: set.ID}
	exitCode := exitOK
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		attemptRoot := filepath.Join(absRoot, entry.Name())
		state, classifyErr := eval.ClassifyAttemptDirectory(attemptRoot)
		if classifyErr != nil {
			exitCode = maxExitCode(exitCode, exitInternal)
			report.Attempts = append(report.Attempts, reportAttemptEntry{
				AttemptID: eval.AttemptID(entry.Name()), RecoveryState: string(state), Error: classifyErr.Error(),
			})
			continue
		}

		reportEntry := reportAttemptEntry{AttemptID: eval.AttemptID(entry.Name()), RecoveryState: string(state)}
		if state == eval.RecoveryTerminal {
			directories := eval.AttemptRootDirectoriesFor(attemptRoot)
			result, assembleErr := eval.AssembleEvaluationResult(directories)
			if assembleErr != nil {
				reportEntry.Error = assembleErr.Error()
				exitCode = maxExitCode(exitCode, exitInternal)
			} else {
				reportEntry.Status = string(result.Outcome.Status)
				reportEntry.CollectionStatus = string(result.Outcome.CollectionStatus)
				switch result.Outcome.Status {
				case eval.OutcomeInfraFailed:
					exitCode = maxExitCode(exitCode, exitInfraFailure)
				case eval.OutcomeIndeterminate:
					exitCode = maxExitCode(exitCode, exitIndeterminate)
				}
				for _, score := range result.Scores {
					reportEntry.Scores = append(reportEntry.Scores, reportScoreEntry{ScorerID: score.ScorerID, Verdict: string(score.Verdict)})
					switch score.Verdict {
					case eval.ScoreFail:
						exitCode = maxExitCode(exitCode, exitGateFailure)
					case eval.ScoreIndeterminate:
						exitCode = maxExitCode(exitCode, exitIndeterminate)
					}
				}
			}
		}
		report.Attempts = append(report.Attempts, reportEntry)
	}

	data, err := jsonEncode(report)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval report:", err)
		return exitInternal
	}
	if *outputPath != "" {
		if err := os.WriteFile(*outputPath, data, 0o600); err != nil {
			fmt.Fprintln(stderr, "och-eval report:", err)
			return exitInternal
		}
		return exitCode
	}
	if _, err := stdout.Write(data); err != nil {
		fmt.Fprintln(stderr, "och-eval report:", err)
		return exitInternal
	}
	return exitCode
}
