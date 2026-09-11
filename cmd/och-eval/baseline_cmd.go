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
	if err := eval.VerifyVariancePolicyBinding(policy, set.VariancePolicyDigest); err != nil {
		fmt.Fprintln(stderr, "och-eval baseline: variance policy:", err)
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

	pairs, err := collectAttemptScores(absRoot, set)
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
func collectAttemptScores(root string, set eval.EvalSet) ([]eval.AttemptScore, error) {
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
		if len(scores) == 0 {
			continue
		}
		directories := eval.AttemptRootDirectoriesFor(attemptRoot)
		if bindingErr := verifyFrozenEvalSet(directories, set); bindingErr != nil {
			return nil, bindingErr
		}
		for _, score := range scores {
			pairs = append(pairs, eval.AttemptScore{Attempt: attempt, Score: score})
		}
	}
	return pairs, nil
}

// verifyFrozenEvalSet proves that an Attempt being used for a variance
// measurement came from the exact EvalSet supplied to the command. Matching
// only evalSetId is insufficient: repetitions, limits, or policy bindings can
// change while a human-readable ID stays the same.
func verifyFrozenEvalSet(directories eval.AttemptRootDirectories, expected eval.EvalSet) error {
	attempt, err := eval.ReadAttempt(directories.Root)
	if err != nil {
		return fmt.Errorf("attempt %s: read attempt: %w", filepath.Base(directories.Root), err)
	}
	reader, err := eval.NewArtifactReader(directories)
	if err != nil {
		return fmt.Errorf("attempt %s: open evidence: %w", filepath.Base(directories.Root), err)
	}
	entries := reader.Entries("eval_set")
	if len(entries) != 1 || entries[0].State != eval.EntryCollected {
		return fmt.Errorf("attempt %s: frozen eval_set evidence must contain exactly one collected entry", filepath.Base(directories.Root))
	}
	data, err := reader.ReadEntry(entries[0].Path)
	if err != nil {
		return fmt.Errorf("attempt %s: read frozen eval set: %w", filepath.Base(directories.Root), err)
	}
	frozen, err := eval.DecodeEvalSet(data)
	if err != nil {
		return fmt.Errorf("attempt %s: decode frozen eval set: %w", filepath.Base(directories.Root), err)
	}
	frozenDigest, err := eval.EvalSetDigest(frozen)
	if err != nil {
		return err
	}
	expectedDigest, err := eval.EvalSetDigest(expected)
	if err != nil {
		return err
	}
	if frozenDigest != expectedDigest {
		return fmt.Errorf("attempt %s: frozen eval set digest %q does not match requested set digest %q", filepath.Base(directories.Root), frozenDigest, expectedDigest)
	}
	if err := verifyAttemptInEvalSet(attempt, frozen); err != nil {
		return fmt.Errorf("attempt %s: %w", filepath.Base(directories.Root), err)
	}
	return nil
}

func verifyAttemptInEvalSet(attempt eval.Attempt, set eval.EvalSet) error {
	if attempt.EvalSetID != set.ID {
		return fmt.Errorf("attempt evalSetId %q does not match frozen set %q", attempt.EvalSetID, set.ID)
	}
	if attempt.RepetitionIndex >= set.RepetitionCount {
		return fmt.Errorf("repetition index %d is outside frozen set repetitionCount %d", attempt.RepetitionIndex, set.RepetitionCount)
	}
	hasScenario := false
	for _, ref := range set.Scenarios {
		hasScenario = hasScenario || ref.ID == attempt.ScenarioID && ref.Digest == attempt.ScenarioDigest
	}
	hasSubject := false
	for _, ref := range set.Subjects {
		hasSubject = hasSubject || ref.ID == attempt.SubjectID && ref.Digest == attempt.SubjectDigest
	}
	hasExecutor := false
	for _, ref := range set.Executors {
		hasExecutor = hasExecutor || ref.ID == attempt.ExecutorID && ref.Digest == attempt.ExecutorDigest
	}
	if !hasScenario || !hasSubject || !hasExecutor {
		return fmt.Errorf("cell identity is not present in frozen set %q", set.ID)
	}
	return nil
}
