package contextengine

import (
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestContextCheckpointCloneOwnsInstructionSnapshotSources(t *testing.T) {
	checkpoint := baseCheckpoint()
	checkpoint.InstructionSnapshot = &InstructionSnapshot{
		PromptID: "och_coding_agent_v1", Epoch: 1, ThroughSequence: 9,
		Sources: []domain.InstructionSource{{Path: "AGENTS.md", Scope: ".", Content: "Run tests.\n"}},
	}
	clone := checkpoint.Clone()
	clone.InstructionSnapshot.Sources[0].Content = "mutated"
	if checkpoint.InstructionSnapshot.Sources[0].Content != "Run tests.\n" {
		t.Fatal("mutating clone instruction sources changed the original checkpoint")
	}
}
