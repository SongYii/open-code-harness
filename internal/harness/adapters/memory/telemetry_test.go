package memory

import (
	"context"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/telemetry"
)

func TestTelemetryRecordsParentageAndCopies(t *testing.T) {
	adapter := &Telemetry{}
	rootCtx, root := telemetry.SafeStart(adapter, context.Background(), telemetry.Start{Kind: telemetry.KindTurn})
	_, child := telemetry.SafeStart(adapter, rootCtx, telemetry.Start{Kind: telemetry.KindModelRequest})
	child.End(telemetry.End{Outcome: telemetry.OutcomeOK})
	root.End(telemetry.End{Outcome: telemetry.OutcomeOK})

	records := adapter.Records()
	if len(records) != 2 || records[0].Parent == 0 || records[1].Parent != 0 {
		t.Fatalf("records = %#v, want child then root with parentage", records)
	}
	records[0].Start.Kind = telemetry.KindToolExecute
	if adapter.Records()[0].Start.Kind != telemetry.KindModelRequest {
		t.Fatal("Records returned aliased data")
	}
}
