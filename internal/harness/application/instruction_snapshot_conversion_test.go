package application

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/agentinstructions"
	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestInstructionSnapshotCheckpointRecordRoundTrip(t *testing.T) {
	_, state, err := agentinstructions.Reconcile(agentinstructions.State{}, []agentinstructions.Observation{{Path: "AGENTS.md", Scope: ".", Present: true, Content: []byte("Run tests.\n")}})
	if err != nil {
		t.Fatal(err)
	}
	want := agentinstructions.RenderSnapshot(state)
	checkpoint := contextengine.ContextCheckpoint{
		ID: "ckpt-1", SessionID: "session-1", Kind: contextengine.CheckpointKindRollingSummary,
		SourceSchema: contextengine.SourceSchemaVersion, SummaryFormat: contextengine.SummaryFormatVersion,
		Coverage: contextengine.Coverage{CoveredEventCount: 1, ThroughSequence: 7}, Summary: "summary",
		InstructionSnapshot: &contextengine.InstructionSnapshot{
			PromptID: agentinstructions.PromptID, PromptDigest: agentinstructions.PromptDigest,
			Epoch: 1, ThroughSequence: 7,
			Sources:         []domain.InstructionSource{{Path: "AGENTS.md", Scope: ".", Digest: "sha256:ac44a12762c7417f2ee0a247618913c1448edde6193125c1d4579fd42596a9d5", Content: "Run tests.\n"}},
			RenderedMessage: want.RenderedMessage, Digest: want.Digest,
		},
	}
	restored, err := checkpointFromRecord(checkpoint.SessionID, recordFromCheckpoint(checkpoint))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored.InstructionSnapshot, checkpoint.InstructionSnapshot) {
		t.Fatalf("restored snapshot = %#v, want %#v", restored.InstructionSnapshot, checkpoint.InstructionSnapshot)
	}
}

func TestInstructionSnapshotCheckpointConversionRejectsSemanticDrift(t *testing.T) {
	_, state, err := agentinstructions.Reconcile(agentinstructions.State{}, []agentinstructions.Observation{{Path: "AGENTS.md", Scope: ".", Present: true, Content: []byte("Run tests.\n")}})
	if err != nil {
		t.Fatal(err)
	}
	want := agentinstructions.RenderSnapshot(state)
	base := domain.ContextCheckpointRecord{
		ID: "ckpt-1", Kind: string(contextengine.CheckpointKindRollingSummary), SourceSchema: contextengine.SourceSchemaVersion,
		SummaryFormat: contextengine.SummaryFormatVersion, Summary: "summary", CoveredEventCount: 1, ThroughSequence: 7,
		SourceDigestHex: "0000000000000000000000000000000000000000000000000000000000000000",
		InstructionSnapshot: &domain.InstructionSnapshotRecord{
			PromptID: agentinstructions.PromptID, PromptDigest: agentinstructions.PromptDigest, Epoch: want.Epoch, ThroughSequence: 7,
			Sources:         []domain.InstructionSource{{Path: "AGENTS.md", Scope: ".", Digest: "sha256:ac44a12762c7417f2ee0a247618913c1448edde6193125c1d4579fd42596a9d5", Content: "Run tests.\n"}},
			RenderedMessage: want.RenderedMessage, Digest: want.Digest,
		},
	}
	for _, mutate := range []func(*domain.InstructionSnapshotRecord){
		func(snapshot *domain.InstructionSnapshotRecord) { snapshot.PromptID = "other" },
		func(snapshot *domain.InstructionSnapshotRecord) { snapshot.RenderedMessage += "tampered" },
		func(snapshot *domain.InstructionSnapshotRecord) {
			snapshot.Digest = "sha256:" + strings.Repeat("0", 64)
		},
	} {
		record := base
		snapshot := *base.InstructionSnapshot
		record.InstructionSnapshot = &snapshot
		mutate(record.InstructionSnapshot)
		if _, err := checkpointFromRecord("session-1", record); err == nil {
			t.Fatal("checkpointFromRecord() accepted semantically inconsistent instruction snapshot")
		}
	}
}
