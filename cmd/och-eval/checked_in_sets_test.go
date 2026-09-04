package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

// TestCheckedInSetsResolveEveryFrozenDigest recomputes every referenced
// document's canonical digest for every checked-in EvalSet.
//
// A digest pinned in one file that names another drifts silently the moment
// someone edits the file it names, and the failure would otherwise surface
// as a confusing mid-run refusal. Recomputing here means a stale digest
// fails the build instead.
func TestCheckedInSetsResolveEveryFrozenDigest(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "eval", "sets"))
	if err != nil {
		t.Fatalf("read eval/sets: %v", err)
	}
	checked := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			tree, err := loadDocumentTree(filepath.Join(root, "eval", "sets", entry.Name()))
			if err != nil {
				t.Fatalf("loadDocumentTree: %v", err)
			}
			for _, ref := range tree.Set.Scenarios {
				digest, err := eval.ScenarioDigest(tree.Scenarios[ref.ID])
				if err != nil {
					t.Fatalf("ScenarioDigest(%s): %v", ref.ID, err)
				}
				if digest != ref.Digest {
					t.Fatalf("scenario %q is pinned at %q but digests to %q", ref.ID, ref.Digest, digest)
				}
			}
			for _, ref := range tree.Set.Subjects {
				digest, err := eval.SubjectDigest(tree.Subjects[ref.ID])
				if err != nil {
					t.Fatalf("SubjectDigest(%s): %v", ref.ID, err)
				}
				if digest != ref.Digest {
					t.Fatalf("subject %q is pinned at %q but digests to %q", ref.ID, ref.Digest, digest)
				}
			}
			for _, ref := range tree.Set.Executors {
				digest, err := eval.ExecutorDigest(tree.Executors[ref.ID])
				if err != nil {
					t.Fatalf("ExecutorDigest(%s): %v", ref.ID, err)
				}
				if digest != ref.Digest {
					t.Fatalf("executor %q is pinned at %q but digests to %q", ref.ID, ref.Digest, digest)
				}
			}
			// A Scenario's own fixture tree is frozen the same way.
			for _, ref := range tree.Set.Scenarios {
				scenario := tree.Scenarios[ref.ID]
				fixtureDigest, err := eval.DigestFixtureTree(tree.FixtureSources[ref.ID])
				if err != nil {
					t.Fatalf("DigestFixtureTree(%s): %v", ref.ID, err)
				}
				if string(fixtureDigest) != scenario.FixtureDigest {
					t.Fatalf("scenario %q pins fixtureDigest %q but its tree digests to %q",
						ref.ID, scenario.FixtureDigest, fixtureDigest)
				}
			}
			checked++
		})
	}
	if checked == 0 {
		t.Fatal("no checked-in EvalSet was verified")
	}
}
