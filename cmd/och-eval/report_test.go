package main

import (
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

func TestPairParityCandidatesPairsMatchingKeyDifferentKinds(t *testing.T) {
	key := eval.ParityPairKey{ScenarioDigest: "sha256:aaa", RepetitionIndex: 0}
	candidates := []parityCandidate{
		{key: key, arm: eval.ParityArm{AttemptID: "baseline-1", ExecutorKind: eval.ExecutorInProcess}},
		{key: key, arm: eval.ParityArm{AttemptID: "candidate-1", ExecutorKind: eval.ExecutorACPSubprocess}},
	}

	entries := pairParityCandidates(candidates)
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].BaselineAttemptID != "baseline-1" || entries[0].CandidateAttemptID != "candidate-1" {
		t.Fatalf("entries[0] = %+v, want baseline-1/candidate-1", entries[0])
	}
}

func TestPairParityCandidatesIgnoresSingleKindGroups(t *testing.T) {
	key := eval.ParityPairKey{ScenarioDigest: "sha256:bbb", RepetitionIndex: 0}
	candidates := []parityCandidate{
		{key: key, arm: eval.ParityArm{AttemptID: "only-in-process-1", ExecutorKind: eval.ExecutorInProcess}},
		{key: key, arm: eval.ParityArm{AttemptID: "only-in-process-2", ExecutorKind: eval.ExecutorInProcess}},
	}

	if entries := pairParityCandidates(candidates); len(entries) != 0 {
		t.Fatalf("entries = %+v, want none: a group with only one Executor Kind is not a parity claim", entries)
	}
}

func TestPairParityCandidatesNeverCrossesDifferentPairKeys(t *testing.T) {
	candidates := []parityCandidate{
		{
			key: eval.ParityPairKey{ScenarioDigest: "sha256:one", RepetitionIndex: 0},
			arm: eval.ParityArm{AttemptID: "baseline-one", ExecutorKind: eval.ExecutorInProcess},
		},
		{
			key: eval.ParityPairKey{ScenarioDigest: "sha256:two", RepetitionIndex: 0},
			arm: eval.ParityArm{AttemptID: "candidate-two", ExecutorKind: eval.ExecutorACPSubprocess},
		},
	}

	if entries := pairParityCandidates(candidates); len(entries) != 0 {
		t.Fatalf("entries = %+v, want none: different ParityPairKeys must never pair", entries)
	}
}
