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
	Parity        []reportParityEntry  `json:"parity,omitempty"`

	// Variance is present only when a policy was supplied. Without one the
	// report is exactly the document it has always been, which matters
	// because every checked-in set runs each Cell once and claims no
	// variance signal.
	VariancePolicy *reportVariancePolicy `json:"variancePolicy,omitempty"`
	Variance       []reportVarianceCell  `json:"variance,omitempty"`
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

// reportParityEntry is one design §22 baseline/candidate comparison this
// report performed: an in_process Attempt and an acp_subprocess Attempt
// whose evidence both loaded successfully and whose ParityPairKey
// (Scenario digest, repetition index) matched. An empty Mismatches means
// parity held.
type reportParityEntry struct {
	BaselineAttemptID  eval.AttemptID        `json:"baselineAttemptId"`
	CandidateAttemptID eval.AttemptID        `json:"candidateAttemptId"`
	Mismatches         []eval.ParityMismatch `json:"mismatches,omitempty"`
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
	variancePolicyPath := flags.String("variance-policy", "", "path to an och.eval.variance-policy document; without one no variance block is produced")
	varianceScorer := flags.String("variance-scorer", "", "which scorer's Scores the variance block measures")
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

	policy, policyDigest, wantVariance, varianceErr := loadVariancePolicy(
		varianceInputs{policyPath: *variancePolicyPath, scorerID: *varianceScorer}, stderr)
	if varianceErr != nil {
		fmt.Fprintln(stderr, "och-eval report:", varianceErr)
		return exitValidation
	}
	if wantVariance && *varianceScorer == "" {
		fmt.Fprintln(stderr, "och-eval report: -variance-scorer is required with -variance-policy;"+
			" an Attempt can carry Scores from several scorers and pooling them would measure the difference between two questions")
		return exitValidation
	}

	report := evaluationReport{FormatVersion: 1, Schema: reportSchema, SetID: set.ID}
	exitCode := exitOK
	var parityCandidates []parityCandidate
	var variancePairs []eval.AttemptScore
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
				// Parity is best-effort augmentation, never a hard
				// requirement of aggregating this Attempt into the
				// report: an Attempt whose evidence can't support a
				// ParityArm (e.g. audit wasn't a required role for its
				// own Scenario) simply never enters a candidate pair,
				// exactly like design's own "not every Scenario claims
				// a parity pairing" shape.
				if result.Outcome.CollectionStatus == eval.CollectionComplete {
					if attempt, attemptErr := eval.ReadAttempt(attemptRoot); attemptErr == nil {
						if wantVariance {
							for _, score := range result.Scores {
								variancePairs = append(variancePairs,
									eval.AttemptScore{Attempt: attempt, Score: score})
							}
						}
						if arm, armErr := eval.LoadParityArm(directories); armErr == nil {
							parityCandidates = append(parityCandidates, parityCandidate{
								key: eval.ParityPairKeyForAttempt(attempt), arm: arm,
							})
						}
					}
				}
			}
		}
		report.Attempts = append(report.Attempts, reportEntry)
	}

	for _, parityEntry := range pairParityCandidates(parityCandidates) {
		report.Parity = append(report.Parity, parityEntry)
		if len(parityEntry.Mismatches) > 0 {
			exitCode = maxExitCode(exitCode, exitGateFailure)
		}
	}

	if wantVariance {
		block, blockErr := buildVarianceBlock(variancePairs, *varianceScorer, policy)
		if blockErr != nil {
			fmt.Fprintln(stderr, "och-eval report: variance:", blockErr)
			return exitInternal
		}
		report.VariancePolicy = &reportVariancePolicy{
			ID: policy.ID, Version: policy.Version,
			Digest: string(policyDigest), Calibration: string(policy.Calibration),
		}
		report.Variance = block
		// Deliberately no exitCode change. A variance signal is advisory
		// until every GA blocker it depends on has its own accepted
		// evidence, and ordinary PR CI gates on deterministic verifiers
		// only. Letting a variance signal turn a report red here would
		// make a measurement nobody has calibrated into a gate.
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

// parityCandidate is one terminal, fully-collected Attempt's own parity
// pairing key and loaded arm, gathered while walking the artifact root so
// pairing itself can happen in one pass afterward.
type parityCandidate struct {
	key eval.ParityPairKey
	arm eval.ParityArm
}

// pairParityCandidates groups candidates by ParityPairKey and, within
// each group that actually contains both an in_process and an
// acp_subprocess arm, compares every in_process arm against every
// acp_subprocess arm (design §22: baseline/candidate pairing). A group
// containing only one Executor Kind is not a parity claim at all — most
// Scenarios in an EvalSet are never meant to run through both executors
// — so it produces no entry, matching (not missing) pair: design's own
// "missing pairs are explicit" requirement applies to a Scenario an
// EvalSet actually declared as paired but whose counterpart Attempt
// never reached complete, collected evidence at all; detecting that
// specific case would require this command to also load the EvalSet's
// own Scenario documents for their pairingTags, which this first
// implementation does not yet do — a documented, deliberate scope
// choice, not an oversight.
func pairParityCandidates(candidates []parityCandidate) []reportParityEntry {
	groups := make(map[eval.ParityPairKey][]parityCandidate)
	for _, candidate := range candidates {
		groups[candidate.key] = append(groups[candidate.key], candidate)
	}

	var entries []reportParityEntry
	for _, group := range groups {
		var baselines, candidatesInGroup []eval.ParityArm
		for _, member := range group {
			switch member.arm.ExecutorKind {
			case eval.ExecutorInProcess:
				baselines = append(baselines, member.arm)
			case eval.ExecutorACPSubprocess:
				candidatesInGroup = append(candidatesInGroup, member.arm)
			}
		}
		for _, baseline := range baselines {
			for _, candidate := range candidatesInGroup {
				entries = append(entries, reportParityEntry{
					BaselineAttemptID: baseline.AttemptID, CandidateAttemptID: candidate.AttemptID,
					Mismatches: eval.ComparePairedArms(baseline, candidate),
				})
			}
		}
	}
	return entries
}
