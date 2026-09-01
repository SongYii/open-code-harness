package contextengine

import (
	"fmt"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// recordSchemaVersion matches domain's own unexported schemaVersion (1):
// MarshalRecordedEvent's validation rejects any other value, so every
// fixture RecordedEvent this file builds must carry it.
const recordSchemaVersion = 1

func record(sequence uint64, event domain.Event) domain.RecordedEvent {
	return domain.RecordedEvent{
		SchemaVersion: recordSchemaVersion,
		ID:            domain.EventID(fmt.Sprintf("evt_%d", sequence)),
		CommandID:     domain.CommandID(fmt.Sprintf("cmd_%d", sequence)),
		SessionID:     domain.SessionID("sess_fixture"),
		Sequence:      sequence,
		OccurredAt:    time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(sequence) * time.Second),
		Event:         event,
	}
}

func TestIsSourceEvent(t *testing.T) {
	tests := []struct {
		name  string
		want  bool
		event domain.Event
	}{
		{name: "TurnStarted is source", want: true, event: domain.TurnStarted{TurnID: "t1", Input: "hi"}},
		{name: "AssistantMessageCompleted is source", want: true, event: domain.AssistantMessageCompleted{TurnID: "t1"}},
		{name: "ToolCallStarted is source", want: true, event: domain.ToolCallStarted{TurnID: "t1", CallID: "c1"}},
		{name: "ToolCallCompleted is source", want: true, event: domain.ToolCallCompleted{TurnID: "t1", CallID: "c1"}},
		{name: "ToolCallFailed is source", want: true, event: domain.ToolCallFailed{TurnID: "t1", CallID: "c1"}},
		{name: "ToolCallInterrupted is source", want: true, event: domain.ToolCallInterrupted{TurnID: "t1", CallID: "c1"}},
		{name: "SessionCreated is not source", want: false, event: domain.SessionCreated{}},
		{name: "ModelRequestRecorded is not source", want: false, event: domain.ModelRequestRecorded{}},
		{name: "ModelUsageRecorded is not source", want: false, event: domain.ModelUsageRecorded{}},
		{name: "PolicyDecisionRecorded is not source", want: false, event: domain.PolicyDecisionRecorded{}},
		{name: "ApprovalRequested is not source", want: false, event: domain.ApprovalRequested{}},
		{name: "TurnCompleted is not source", want: false, event: domain.TurnCompleted{TurnID: "t1"}},
		{name: "TurnFailed is not source", want: false, event: domain.TurnFailed{TurnID: "t1"}},
		{name: "TurnInterrupted is not source", want: false, event: domain.TurnInterrupted{TurnID: "t1"}},
		{name: "AssistantMessageStarted is not source", want: false, event: domain.AssistantMessageStarted{TurnID: "t1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsSourceEvent(test.event); got != test.want {
				t.Fatalf("IsSourceEvent(%T) = %t, want %t", test.event, got, test.want)
			}
		})
	}
}

func sampleSourceRecords() []domain.RecordedEvent {
	return []domain.RecordedEvent{
		record(1, domain.TurnStarted{TurnID: "t1", Input: "hello"}),
		record(2, domain.SessionCreated{}), // operational, must be excluded
		record(3, domain.AssistantMessageCompleted{TurnID: "t1", ItemID: "item1", Text: "hi there"}),
		record(4, domain.ModelUsageRecorded{}), // operational, must be excluded
	}
}

func TestComputeSourceDigestDeterministic(t *testing.T) {
	records := sampleSourceRecords()
	digestA, countA, err := ComputeSourceDigest(records)
	if err != nil {
		t.Fatal(err)
	}
	digestB, countB, err := ComputeSourceDigest(records)
	if err != nil {
		t.Fatal(err)
	}
	if digestA != digestB || countA != countB {
		t.Fatalf("ComputeSourceDigest is not deterministic: (%x,%d) vs (%x,%d)", digestA, countA, digestB, countB)
	}
	if countA != 2 {
		t.Fatalf("coveredCount = %d, want 2 (only TurnStarted and AssistantMessageCompleted are source events)", countA)
	}
}

func TestRollingSuccessorMatchesColdRebuild(t *testing.T) {
	records := sampleSourceRecords()
	more := []domain.RecordedEvent{
		record(5, domain.ToolCallStarted{TurnID: "t1", ItemID: "item2", CallID: "c1", Name: "read_file", StepIndex: 1}),
		record(6, domain.ToolCallCompleted{TurnID: "t1", ItemID: "item2", CallID: "c1", Content: "file contents"}),
	}
	full := append(append([]domain.RecordedEvent{}, records...), more...)

	coldDigest, coldCount, err := ComputeSourceDigest(full)
	if err != nil {
		t.Fatal(err)
	}

	firstDigest, _, err := ComputeSourceDigest(records)
	if err != nil {
		t.Fatal(err)
	}
	rollingDigest, rollingCoveredInStep2, err := ExtendSourceDigestOverRecords(firstDigest, more)
	if err != nil {
		t.Fatal(err)
	}

	if rollingDigest != coldDigest {
		t.Fatalf("rolling successor digest %x != cold rebuild digest %x", rollingDigest, coldDigest)
	}
	_, firstCount, _ := ComputeSourceDigest(records)
	if firstCount+rollingCoveredInStep2 != coldCount {
		t.Fatalf("covered counts do not add up: %d + %d != %d", firstCount, rollingCoveredInStep2, coldCount)
	}
}

// TestSourceFilterDigestMutation is the mutation-check counterpart for the
// "source filter/digest" mutation-kill target (design §22.4): stopping the
// exclusion of an operational event changes the digest and the covered
// count, so this test would fail if IsSourceEvent's filter were bypassed.
func TestSourceFilterDigestMutation(t *testing.T) {
	withOperational := sampleSourceRecords()
	withoutOperational := []domain.RecordedEvent{withOperational[0], withOperational[2]}

	digestWith, countWith, err := ComputeSourceDigest(withOperational)
	if err != nil {
		t.Fatal(err)
	}
	digestWithout, countWithout, err := ComputeSourceDigest(withoutOperational)
	if err != nil {
		t.Fatal(err)
	}
	if digestWith != digestWithout || countWith != countWithout {
		t.Fatalf("operational events changed the digest/count: (%x,%d) vs (%x,%d) -- IsSourceEvent's filter is not being applied", digestWith, countWith, digestWithout, countWithout)
	}
}

func TestExtendSourceDigestLengthFraming(t *testing.T) {
	// Two adjacent records whose encodings could concatenate ambiguously
	// without a length prefix must still produce distinguishable digests
	// from a differently-split sequence carrying the same total bytes.
	a := record(1, domain.TurnStarted{TurnID: "t1", Input: "ab"})
	b := record(2, domain.TurnStarted{TurnID: "t1", Input: "cd"})
	digest1, _, err := ComputeSourceDigest([]domain.RecordedEvent{a, b})
	if err != nil {
		t.Fatal(err)
	}
	c := record(1, domain.TurnStarted{TurnID: "t1", Input: "abcd"})
	digest2, _, err := ComputeSourceDigest([]domain.RecordedEvent{c})
	if err != nil {
		t.Fatal(err)
	}
	if digest1 == digest2 {
		t.Fatal("two different event sequences produced the same digest")
	}
}
