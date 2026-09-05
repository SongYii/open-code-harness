package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

// baselineCommand regenerates a pinned baseline document from an artifact
// root.
//
// It is a separate command on purpose. A baseline is what later runs are
// compared against, so a lane that rewrote its own baseline whenever it
// drifted would measure nothing; regeneration is an explicit act whose output
// is committed and reviewed like any other document.
func baselineCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("och-eval baseline", flag.ContinueOnError)
	flags.SetOutput(stderr)
	setPath := flags.String("set", "", "path to the EvalSet document")
	artifactRootFlag := flags.String("artifacts", "", "override the EvalSet document's own artifactRoot field")
	policyPath := flags.String("variance-policy", "", "path to the och.eval.variance-policy document")
	scorerID := flags.String("variance-scorer", "", "which scorer's Scores the baseline records")
	id := flags.String("id", "", "identity for the baseline document")
	outputPath := flags.String("output", "", "write the baseline here instead of stdout")
	recordedAt := flags.String("recorded-at", "", "RFC3339 timestamp; defaults to now")
	if err := flags.Parse(args); err != nil {
		return exitValidation
	}
	for name, value := range map[string]string{
		"-set": *setPath, "-variance-policy": *policyPath, "-variance-scorer": *scorerID, "-id": *id,
	} {
		if value == "" {
			fmt.Fprintf(stderr, "och-eval baseline: %s is required\n", name)
			return exitValidation
		}
	}

	setData, err := os.ReadFile(*setPath)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval baseline:", err)
		return exitValidation
	}
	set, err := eval.DecodeEvalSet(setData)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval baseline:", err)
		return exitValidation
	}
	policyData, err := os.ReadFile(*policyPath)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval baseline:", err)
		return exitValidation
	}
	policy, err := eval.DecodeVariancePolicy(policyData)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval baseline:", err)
		return exitValidation
	}

	when := time.Now().UTC()
	if *recordedAt != "" {
		parsed, parseErr := time.Parse(time.RFC3339, *recordedAt)
		if parseErr != nil {
			fmt.Fprintln(stderr, "och-eval baseline: -recorded-at:", parseErr)
			return exitValidation
		}
		when = parsed.UTC()
	}

	artifactRoot := set.ArtifactRoot
	if *artifactRootFlag != "" {
		artifactRoot = *artifactRootFlag
	}
	absRoot, err := filepath.Abs(artifactRoot)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval baseline:", err)
		return exitValidation
	}

	pairs, err := collectAttemptScores(absRoot)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval baseline:", err)
		return exitInternal
	}
	cells, err := eval.GroupByCellForScorer(pairs, *scorerID)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval baseline:", err)
		return exitInternal
	}
	if len(cells) == 0 {
		fmt.Fprintf(stderr, "och-eval baseline: no Attempt under %s carries a %q Score\n", absRoot, *scorerID)
		return exitValidation
	}

	baseline, err := eval.BuildBaseline(*id, when, cells, policy)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval baseline:", err)
		return exitValidation
	}
	data, err := jsonEncode(baseline)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval baseline:", err)
		return exitInternal
	}
	if *outputPath != "" {
		if err := os.WriteFile(*outputPath, data, 0o600); err != nil {
			fmt.Fprintln(stderr, "och-eval baseline:", err)
			return exitInternal
		}
		return exitOK
	}
	if _, err := stdout.Write(data); err != nil {
		fmt.Fprintln(stderr, "och-eval baseline:", err)
		return exitInternal
	}
	return exitOK
}

// collectAttemptScores reads every complete Attempt under root together with
// its Scores.
//
// An Attempt whose evidence is incomplete is skipped rather than failing the
// whole regeneration: a baseline is built from what actually finished, and an
// in-progress or crashed Attempt has nothing to contribute.
func collectAttemptScores(root string) ([]eval.AttemptScore, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var pairs []eval.AttemptScore
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		attemptRoot := filepath.Join(root, entry.Name())
		attempt, attemptErr := eval.ReadAttempt(attemptRoot)
		if attemptErr != nil {
			continue
		}
		scores, scoreErr := eval.ReadScores(attemptRoot)
		if scoreErr != nil {
			continue
		}
		for _, score := range scores {
			pairs = append(pairs, eval.AttemptScore{Attempt: attempt, Score: score})
		}
	}
	return pairs, nil
}
