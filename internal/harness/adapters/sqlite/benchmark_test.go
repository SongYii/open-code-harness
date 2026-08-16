package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func benchmarkStore(b *testing.B) *Store {
	b.Helper()
	config := Config{Path: filepath.Join(b.TempDir(), "bench.db"), RuntimeID: "runtime-bench"}
	store, err := Open(context.Background(), config)
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	b.Cleanup(func() { _ = store.Close() })
	return store
}

func benchAppend(b *testing.B, eventsPerAppend int) {
	store := benchmarkStore(b)
	ctx := context.Background()
	authority := store.Authority()
	var version uint64
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		request := application.AppendRequest{
			AppendID:        domain.AppendID(fmt.Sprintf("append-bench-%d", i)),
			SessionID:       "session-bench",
			ExpectedVersion: version,
			CommandID:       domain.CommandID(fmt.Sprintf("command-bench-%d", i)),
			Authority:       authority,
			Events:          make([]application.ProposedEvent, eventsPerAppend),
		}
		for j := range request.Events {
			request.Events[j] = application.ProposedEvent{
				ID:            domain.EventID(fmt.Sprintf("event-bench-%d-%d", i, j)),
				SchemaVersion: 1,
				OccurredAt:    testTime,
				Event:         domain.TurnStarted{TurnID: domain.TurnID(fmt.Sprintf("turn-bench-%d-%d", i, j)), Input: "benchmark"},
			}
		}
		receipt, err := store.Append(ctx, request)
		if err != nil {
			b.Fatalf("append %d: %v", i, err)
		}
		version = receipt.LastSequence
	}
}

func BenchmarkAppend1Event(b *testing.B)  { benchAppend(b, 1) }
func BenchmarkAppend8Events(b *testing.B) { benchAppend(b, 8) }

func BenchmarkReadStreamPaged(b *testing.B) {
	store := benchmarkStore(b)
	ctx := context.Background()
	authority := store.Authority()
	var version uint64
	for i := 0; i < 200; i++ {
		request := application.AppendRequest{
			AppendID:        domain.AppendID(fmt.Sprintf("append-seed-%d", i)),
			SessionID:       "session-read-bench",
			ExpectedVersion: version,
			CommandID:       domain.CommandID(fmt.Sprintf("command-seed-%d", i)),
			Authority:       authority,
			Events: []application.ProposedEvent{
				{ID: domain.EventID(fmt.Sprintf("event-seed-%d-0", i)), SchemaVersion: 1, OccurredAt: testTime, Event: domain.TurnStarted{TurnID: domain.TurnID(fmt.Sprintf("turn-seed-%d", i)), Input: "seed"}},
				{ID: domain.EventID(fmt.Sprintf("event-seed-%d-1", i)), SchemaVersion: 1, OccurredAt: testTime, Event: domain.TurnCompleted{TurnID: domain.TurnID(fmt.Sprintf("turn-seed-%d", i))}},
			},
		}
		receipt, err := store.Append(ctx, request)
		if err != nil {
			b.Fatalf("seed append %d: %v", i, err)
		}
		version = receipt.LastSequence
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var after uint64
		pages := 0
		for {
			page, err := store.ReadStream(ctx, application.ReadStreamRequest{SessionID: "session-read-bench", Limit: 256, AfterSequence: after})
			if err != nil {
				b.Fatalf("read: %v", err)
			}
			pages++
			after = page.NextAfterSequence
			if page.End {
				break
			}
		}
		if pages == 0 {
			b.Fatal("no pages read")
		}
	}
}

func BenchmarkBackup(b *testing.B) {
	store := benchmarkStore(b)
	ctx := context.Background()
	authority := store.Authority()
	var version uint64
	for i := 0; i < 100; i++ {
		request := application.AppendRequest{
			AppendID:        domain.AppendID(fmt.Sprintf("append-bk-%d", i)),
			SessionID:       "session-backup-bench",
			ExpectedVersion: version,
			CommandID:       domain.CommandID(fmt.Sprintf("command-bk-%d", i)),
			Authority:       authority,
			Events: []application.ProposedEvent{
				{ID: domain.EventID(fmt.Sprintf("event-bk-%d-0", i)), SchemaVersion: 1, OccurredAt: testTime, Event: domain.TurnStarted{TurnID: domain.TurnID(fmt.Sprintf("turn-bk-%d", i)), Input: "backup"}},
				{ID: domain.EventID(fmt.Sprintf("event-bk-%d-1", i)), SchemaVersion: 1, OccurredAt: testTime, Event: domain.TurnCompleted{TurnID: domain.TurnID(fmt.Sprintf("turn-bk-%d", i))}},
			},
		}
		receipt, err := store.Append(ctx, request)
		if err != nil {
			b.Fatalf("seed append %d: %v", i, err)
		}
		version = receipt.LastSequence
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		destination := filepath.Join(b.TempDir(), fmt.Sprintf("backup-bench-%d.db", i))
		if err := store.Backup(ctx, destination); err != nil {
			b.Fatalf("backup %d: %v", i, err)
		}
	}
}
