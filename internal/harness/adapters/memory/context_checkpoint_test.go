package memory

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

var checkpointTestTime = time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

func memoryRecords(t *testing.T, store *EventStore, sessionID domain.SessionID) []domain.RecordedEvent {
	t.Helper()
	store.mu.Lock()
	records := append([]domain.RecordedEvent(nil), store.state.streams[sessionID]...)
	store.mu.Unlock()
	return records
}

func memoryValidCheckpoint(t *testing.T, id string, records []domain.RecordedEvent, through uint64) domain.ContextCompactionCompleted {
	t.Helper()
	covered := make([]domain.RecordedEvent, 0, len(records))
	for _, record := range records {
		if record.Sequence > through {
			break
		}
		covered = append(covered, record)
	}
	digest, count, err := contextengine.ComputeSourceDigest(covered)
	if err != nil {
		t.Fatalf("ComputeSourceDigest: %v", err)
	}
	return domain.ContextCompactionCompleted{
		ID: domain.ContextCompactionID(id + "-compaction"),
		Checkpoint: domain.ContextCheckpointRecord{
			ID:                id,
			Kind:              domain.ContextCheckpointKindRollingSummary,
			SourceSchema:      "och_source_v1",
			CoveredEventCount: count,
			ThroughSequence:   through,
			SourceDigestHex:   hex.EncodeToString(digest[:]),
			Summary:           "summary text",
		},
	}
}

func memoryInstructionSnapshot(through uint64) *domain.InstructionSnapshotRecord {
	return &domain.InstructionSnapshotRecord{
		PromptID: "och_coding_agent_v1", PromptDigest: "sha256:8060287d0ddc132ebce66e955b8749804a06d1d1b494f77b23afbe49c2f0fd2d",
		Epoch: 1, ThroughSequence: through,
		Sources:         []domain.InstructionSource{{Path: "AGENTS.md", Scope: ".", Digest: "sha256:ac44a12762c7417f2ee0a247618913c1448edde6193125c1d4579fd42596a9d5", Content: "Run tests.\n"}},
		RenderedMessage: "rendered snapshot", Digest: "sha256:24a603faa166c8a7ecf88887ec8895c75c80c5091c21cd57943ef287f0aa8106",
	}
}

// TestLoadLatestContextCheckpointFindsVerifiedCompletion covers the happy
// path: a genuinely correct ContextCompactionCompleted event is found and
// its independently-recomputed digest agrees.
func TestLoadLatestContextCheckpointFindsVerifiedCompletion(t *testing.T) {
	authority := application.WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1}
	store := mustV2Store(t, authority)
	ids := testkit.NewSequenceIDs()
	sessionID := domain.SessionID("session-checkpoint-ok")

	appendMemoryEvent(t, store, authority, ids, sessionID, 0, checkpointTestTime, domain.SessionCreated{WorkspaceRoot: "/w"})
	appendMemoryEvent(t, store, authority, ids, sessionID, 1, checkpointTestTime, domain.TurnStarted{TurnID: "turn-1", Input: "hi"})
	appendMemoryEvent(t, store, authority, ids, sessionID, 2, checkpointTestTime, domain.TurnCompleted{TurnID: "turn-1"})

	records := memoryRecords(t, store, sessionID)
	through := records[len(records)-1].Sequence
	completed := memoryValidCheckpoint(t, "checkpoint-1", records, through)
	completed.Checkpoint.InstructionSnapshot = memoryInstructionSnapshot(through)

	appendMemoryEvent(t, store, authority, ids, sessionID, 3, checkpointTestTime, domain.ContextCompactionStarted{
		ID: completed.ID, Trigger: domain.ContextTriggerManual, Strategy: domain.ContextStrategySummary,
		MeterID: "och_wire_estimate_v1", SourceSchema: "och_source_v1",
	})
	appendMemoryEvent(t, store, authority, ids, sessionID, 4, checkpointTestTime, completed)

	lookup, err := store.LoadLatestContextCheckpoint(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("LoadLatestContextCheckpoint: %v", err)
	}
	if lookup.Status != application.ContextCheckpointLookupFound {
		t.Fatalf("lookup status = %v, want Found", lookup.Status)
	}
	if lookup.Checkpoint.ID != "checkpoint-1" || lookup.Checkpoint.ThroughSequence != through {
		t.Fatalf("lookup checkpoint = %+v", lookup.Checkpoint)
	}
	if lookup.Checkpoint.InstructionSnapshot == nil || lookup.Checkpoint.InstructionSnapshot.Sources[0].Content != "Run tests.\n" {
		t.Fatalf("lookup instruction snapshot = %#v", lookup.Checkpoint.InstructionSnapshot)
	}
	lookup.Checkpoint.InstructionSnapshot.Sources[0].Content = "caller mutation"
	again, err := store.LoadLatestContextCheckpoint(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Checkpoint.InstructionSnapshot.Sources[0].Content != "Run tests.\n" {
		t.Fatal("Memory checkpoint lookup returned snapshot storage shared with its event stream")
	}
}

// TestLoadLatestContextCheckpointReturnsNoneWithoutAnyCompletion covers the
// absent case.
func TestLoadLatestContextCheckpointReturnsNoneWithoutAnyCompletion(t *testing.T) {
	authority := application.WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1}
	store := mustV2Store(t, authority)
	ids := testkit.NewSequenceIDs()
	sessionID := domain.SessionID("session-checkpoint-none")
	appendMemoryEvent(t, store, authority, ids, sessionID, 0, checkpointTestTime, domain.SessionCreated{WorkspaceRoot: "/w"})

	lookup, err := store.LoadLatestContextCheckpoint(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("LoadLatestContextCheckpoint: %v", err)
	}
	if lookup.Status != application.ContextCheckpointLookupNone {
		t.Fatalf("lookup status = %v, want None", lookup.Status)
	}
}

// TestLoadLatestContextCheckpointRejectsMismatchedDigest is the core
// corruption-boundary test: a completion whose claimed SourceDigestHex does
// not match the canonical events it says it covers must be rejected by the
// read-time independent re-verification, not trusted.
func TestLoadLatestContextCheckpointRejectsMismatchedDigest(t *testing.T) {
	authority := application.WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1}
	store := mustV2Store(t, authority)
	ids := testkit.NewSequenceIDs()
	sessionID := domain.SessionID("session-checkpoint-bad")

	appendMemoryEvent(t, store, authority, ids, sessionID, 0, checkpointTestTime, domain.SessionCreated{WorkspaceRoot: "/w"})
	appendMemoryEvent(t, store, authority, ids, sessionID, 1, checkpointTestTime, domain.TurnStarted{TurnID: "turn-1", Input: "hi"})
	appendMemoryEvent(t, store, authority, ids, sessionID, 2, checkpointTestTime, domain.TurnCompleted{TurnID: "turn-1"})

	records := memoryRecords(t, store, sessionID)
	through := records[len(records)-1].Sequence
	completed := memoryValidCheckpoint(t, "checkpoint-bad", records, through)
	forged := hex.EncodeToString([]byte("0123456789012345678901234567890123456789012345678901234567890123456789"[:32]))
	completed.Checkpoint.SourceDigestHex = forged

	appendMemoryEvent(t, store, authority, ids, sessionID, 3, checkpointTestTime, domain.ContextCompactionStarted{
		ID: completed.ID, Trigger: domain.ContextTriggerManual, Strategy: domain.ContextStrategySummary,
		MeterID: "och_wire_estimate_v1", SourceSchema: "och_source_v1",
	})
	appendMemoryEvent(t, store, authority, ids, sessionID, 4, checkpointTestTime, completed)

	_, err := store.LoadLatestContextCheckpoint(context.Background(), sessionID)
	if !application.IsStoreCode(err, application.StoreCodeCorrupt) {
		t.Fatalf("LoadLatestContextCheckpoint error = %v, want store_corrupt", err)
	}
}
