package memory

import (
	"context"
	"sync"

	"github.com/SongYii/open-code-harness/internal/harness/telemetry"
)

type TraceRecord struct {
	ID     uint64
	Parent uint64
	Start  telemetry.Start
	End    telemetry.End
}

type Telemetry struct {
	mu      sync.Mutex
	nextID  uint64
	records []TraceRecord
}

type memoryTraceContextKey struct{}

type memorySpan struct {
	once   sync.Once
	owner  *Telemetry
	record TraceRecord
}

func (adapter *Telemetry) Start(ctx context.Context, start telemetry.Start) (context.Context, telemetry.Span) {
	adapter.mu.Lock()
	adapter.nextID++
	id := adapter.nextID
	adapter.mu.Unlock()
	parent, _ := ctx.Value(memoryTraceContextKey{}).(uint64)
	span := &memorySpan{owner: adapter, record: TraceRecord{ID: id, Parent: parent, Start: cloneTelemetryStart(start)}}
	return context.WithValue(ctx, memoryTraceContextKey{}, id), span
}

func (span *memorySpan) End(end telemetry.End) {
	span.once.Do(func() {
		span.record.End = cloneTelemetryEnd(end)
		span.owner.mu.Lock()
		span.owner.records = append(span.owner.records, span.record)
		span.owner.mu.Unlock()
	})
}

func (adapter *Telemetry) Records() []TraceRecord {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	records := make([]TraceRecord, len(adapter.records))
	for index, record := range adapter.records {
		records[index] = record
		records[index].Start = cloneTelemetryStart(record.Start)
		records[index].End = cloneTelemetryEnd(record.End)
	}
	return records
}

func cloneTelemetryStart(start telemetry.Start) telemetry.Start {
	start.Attributes = append([]telemetry.Attribute(nil), start.Attributes...)
	return start
}

func cloneTelemetryEnd(end telemetry.End) telemetry.End {
	end.Attributes = append([]telemetry.Attribute(nil), end.Attributes...)
	return end
}
