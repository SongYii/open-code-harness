package sqlite

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// BenchmarkLoadLatestContextCheckpoint measures design §22.4's own
// required property: a normal checkpoint lookup stays bounded, not a
// full-stream scan. context_checkpoint_heads (migration 5) is a single
// indexed row per session plus one canonical-event join by primary key, so
// this should stay flat as session history grows -- unlike the memory
// adapter's own disclosed simplicity-over-performance choice (a fresh full
// rescan every read, see LoadLatestContextCheckpoint's own doc comment in
// adapters/memory/context_checkpoint.go), which is intentionally not
// benchmarked here as an equivalent comparison: the two adapters made a
// deliberately different trade, not an accidental one.
func BenchmarkLoadLatestContextCheckpoint(b *testing.B) {
	for _, turnCount := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("turns=%d", turnCount), func(b *testing.B) {
			dir, err := os.MkdirTemp("", "context-checkpoint-bench-*")
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = os.RemoveAll(dir) })
			store, err := Open(context.Background(), Config{Path: dir + "/bench.db", RuntimeID: "bench-runtime", LeaseDuration: 10 * time.Minute})
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = store.Close() })

			sessionID := domain.SessionID("bench-session")
			mustBenchAppend(b, store, benchAppendRequest("append-bench-open", sessionID, 0, "command-bench-open",
				domain.SessionCreated{WorkspaceRoot: "/w"}))
			version := uint64(1)
			for i := 0; i < turnCount; i++ {
				turnID := domain.TurnID(fmt.Sprintf("turn-bench-%d", i))
				receipt := mustBenchAppend(b, store, benchAppendRequest(
					domain.AppendID(fmt.Sprintf("append-bench-%d", i)), sessionID, version, domain.CommandID(fmt.Sprintf("command-bench-%d", i)),
					domain.TurnStarted{TurnID: turnID, Input: "hi"},
					domain.AssistantMessageCompleted{TurnID: turnID, ItemID: domain.ItemID(fmt.Sprintf("item-bench-%d", i)), Text: "hello"},
					domain.TurnCompleted{TurnID: turnID},
				))
				version = receipt.LastSequence
			}

			records := benchReadEvents(b, store, sessionID)
			through := records[len(records)-1].Sequence
			digest, count, err := contextengine.ComputeSourceDigest(records)
			if err != nil {
				b.Fatal(err)
			}
			completed := domain.ContextCompactionCompleted{
				ID: "compaction-bench",
				Checkpoint: domain.ContextCheckpointRecord{
					ID: "checkpoint-bench", Kind: domain.ContextCheckpointKindRollingSummary, SourceSchema: "och_source_v1",
					CoveredEventCount: count, ThroughSequence: through,
					SourceDigestHex: fmt.Sprintf("%x", digest), Summary: "benchmark summary",
				},
			}
			mustBenchAppend(b, store, benchAppendRequest("append-bench-checkpoint", sessionID, version, "command-bench-checkpoint",
				domain.ContextCompactionStarted{
					ID: "compaction-bench", Trigger: domain.ContextTriggerManual, Strategy: domain.ContextStrategySummary,
					MeterID: "och_wire_estimate_v1", SourceSchema: "och_source_v1",
				},
				completed,
			))

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := store.LoadLatestContextCheckpoint(context.Background(), sessionID); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchAppendRequest(appendID domain.AppendID, sessionID domain.SessionID, expected uint64, commandID domain.CommandID, events ...domain.Event) application.AppendRequest {
	request := application.AppendRequest{
		AppendID: appendID, SessionID: sessionID, ExpectedVersion: expected, CommandID: commandID,
		Authority: application.WriterAuthority{RuntimeID: "bench-runtime", FencingToken: 1},
		Events:    make([]application.ProposedEvent, len(events)),
	}
	for i, event := range events {
		request.Events[i] = application.ProposedEvent{
			ID: domain.EventID(fmt.Sprintf("event-%s-%d", appendID, i)), SchemaVersion: 1, OccurredAt: testTime, Event: event,
		}
	}
	return request
}

func mustBenchAppend(b *testing.B, store *Store, request application.AppendRequest) application.CommitReceipt {
	b.Helper()
	receipt, err := store.Append(context.Background(), request)
	if err != nil {
		b.Fatalf("Append(%s) error = %v", request.AppendID, err)
	}
	return receipt
}

func benchReadEvents(b *testing.B, store *Store, sessionID domain.SessionID) []domain.RecordedEvent {
	b.Helper()
	rows, err := store.db.QueryContext(context.Background(),
		"SELECT payload FROM events WHERE session_id = ? ORDER BY sequence", string(sessionID))
	if err != nil {
		b.Fatal(err)
	}
	defer rows.Close()
	var records []domain.RecordedEvent
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			b.Fatal(err)
		}
		record, err := domain.UnmarshalRecordedEvent(payload)
		if err != nil {
			b.Fatal(err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		b.Fatal(err)
	}
	return records
}
