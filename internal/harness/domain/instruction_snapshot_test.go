package domain

import (
	"bytes"
	"testing"
)

func validInstructionSnapshotRecord() *InstructionSnapshotRecord {
	return &InstructionSnapshotRecord{
		PromptID: "och_coding_agent_v1", PromptDigest: "sha256:8060287d0ddc132ebce66e955b8749804a06d1d1b494f77b23afbe49c2f0fd2d",
		Epoch: 1, ThroughSequence: 10,
		Sources:         []InstructionSource{{Path: "AGENTS.md", Scope: ".", Digest: "sha256:ac44a12762c7417f2ee0a247618913c1448edde6193125c1d4579fd42596a9d5", Content: "Run tests.\n"}},
		RenderedMessage: "rendered snapshot", Digest: "sha256:24a603faa166c8a7ecf88887ec8895c75c80c5091c21cd57943ef287f0aa8106",
	}
}

func TestInstructionSnapshotRejectsCorruptSourceContent(t *testing.T) {
	checkpoint := validContextCheckpointRecord("ckpt-snapshot")
	checkpoint.InstructionSnapshot = validInstructionSnapshotRecord()
	if err := validateContextCheckpointRecord(checkpoint, CodeInvalidEvent); err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}
	checkpoint.InstructionSnapshot.Sources[0].Content = "tampered\n"
	if err := validateContextCheckpointRecord(checkpoint, CodeInvalidEvent); !IsCode(err, CodeInvalidEvent) {
		t.Fatalf("corrupt source content error = %v, want invalid event", err)
	}
}

func TestInstructionSnapshotRejectsUnsortedSources(t *testing.T) {
	checkpoint := validContextCheckpointRecord("ckpt-snapshot")
	checkpoint.InstructionSnapshot = validInstructionSnapshotRecord()
	checkpoint.InstructionSnapshot.Sources = []InstructionSource{
		{Path: "a/AGENTS.md", Scope: "a", Digest: "sha256:ac44a12762c7417f2ee0a247618913c1448edde6193125c1d4579fd42596a9d5", Content: "Run tests.\n"},
		{Path: "AGENTS.md", Scope: ".", Digest: "sha256:ac44a12762c7417f2ee0a247618913c1448edde6193125c1d4579fd42596a9d5", Content: "Run tests.\n"},
	}
	checkpoint.InstructionSnapshot.Digest = "sha256:660e97d48be8569a86922f4dfee2a870fc3fda4c910bc250bb2cb66e30017dea"
	if err := validateContextCheckpointRecord(checkpoint, CodeInvalidEvent); !IsCode(err, CodeInvalidEvent) {
		t.Fatalf("unsorted source error = %v, want invalid event", err)
	}
}

func TestInstructionSnapshotJSONRejectsUnknownNestedFields(t *testing.T) {
	event := ContextCompactionCompleted{ID: "ctx-1", Checkpoint: validContextCheckpointRecord("ckpt-snapshot")}
	event.Checkpoint.InstructionSnapshot = validInstructionSnapshotRecord()
	encoded, err := MarshalRecordedEvent(codecTestRecord(event))
	if err != nil {
		t.Fatal(err)
	}
	tests := [][]byte{
		bytes.Replace(encoded, []byte(`"instructionSnapshot":{"promptID"`), []byte(`"instructionSnapshot":{"extra":true,"promptID"`), 1),
		bytes.Replace(encoded, []byte(`"sources":[{"path"`), []byte(`"sources":[{"extra":true,"path"`), 1),
	}
	for _, input := range tests {
		if _, err := UnmarshalRecordedEvent(input); !IsCode(err, CodeInvalidEvent) {
			t.Fatalf("UnmarshalRecordedEvent() error = %v, want invalid event for %s", err, input)
		}
	}
}

func TestCloneContextCompactionCompletedOwnsInstructionSnapshot(t *testing.T) {
	original := ContextCompactionCompleted{ID: "ctx-1", Checkpoint: validContextCheckpointRecord("ckpt-snapshot")}
	original.Checkpoint.InstructionSnapshot = validInstructionSnapshotRecord()
	clonedEvent, err := CloneEvent(original)
	if err != nil {
		t.Fatal(err)
	}
	cloned := clonedEvent.(ContextCompactionCompleted)
	cloned.Checkpoint.InstructionSnapshot.Sources[0].Content = "mutated"
	if original.Checkpoint.InstructionSnapshot.Sources[0].Content != "Run tests.\n" {
		t.Fatal("CloneEvent shared instruction snapshot storage with the original event")
	}
}

func TestInstructionSnapshotCodecPreservesAnEmptyEffectiveSet(t *testing.T) {
	event := ContextCompactionCompleted{ID: "ctx-1", Checkpoint: validContextCheckpointRecord("ckpt-empty-snapshot")}
	event.Checkpoint.InstructionSnapshot = &InstructionSnapshotRecord{
		PromptID: "och_coding_agent_v1", PromptDigest: "sha256:8060287d0ddc132ebce66e955b8749804a06d1d1b494f77b23afbe49c2f0fd2d",
		Epoch: 2, ThroughSequence: event.Checkpoint.ThroughSequence, Sources: []InstructionSource{},
		RenderedMessage: "rendered empty snapshot", Digest: "sha256:4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945",
	}
	encoded, err := MarshalRecordedEvent(codecTestRecord(event))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnmarshalRecordedEvent(encoded); err != nil {
		t.Fatalf("empty effective-set snapshot did not survive codec round trip: %v", err)
	}
}
