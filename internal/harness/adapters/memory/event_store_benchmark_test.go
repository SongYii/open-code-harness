package memory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func BenchmarkEventStore(b *testing.B) {
	store, err := NewEventStore(application.WriterAuthority{RuntimeID: "benchmark", FencingToken: 1})
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < b.N; i++ {
		_, err := store.Append(context.Background(), application.AppendRequest{AppendID: domain.AppendID(fmt.Sprintf("append-benchmark-%d", i)), SessionID: domain.SessionID(fmt.Sprintf("session-benchmark-%d", i)), CommandID: domain.CommandID(fmt.Sprintf("command-benchmark-%d", i)), Authority: application.WriterAuthority{RuntimeID: "benchmark", FencingToken: 1}, Events: []application.ProposedEvent{{ID: domain.EventID(fmt.Sprintf("event-benchmark-%d", i)), SchemaVersion: 1, OccurredAt: v2TimeBenchmark, Event: domain.SessionCreated{WorkspaceRoot: "/benchmark"}}}})
		if err != nil {
			b.Fatal(err)
		}
	}
}

var v2TimeBenchmark = v2BenchmarkTime()

func v2BenchmarkTime() time.Time { return time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC) }
