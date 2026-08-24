package transcript

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestGoldenFixturesRoundTrip(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"testdata/snapshot.jsonl",
		"testdata/complete.jsonl",
		"testdata/facts.jsonl",
	} {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) == 0 {
				t.Fatal("fixture is empty")
			}
			for index, line := range lines {
				decoded, err := UnmarshalLine([]byte(line))
				if err != nil {
					t.Fatalf("line %d UnmarshalLine() error = %v", index+1, err)
				}
				encoded, err := marshalDecoded(decoded)
				if err != nil {
					t.Fatalf("line %d marshal error = %v", index+1, err)
				}
				if string(encoded) != line {
					t.Fatalf("line %d re-marshal = %s\nwant exact %s", index+1, encoded, line)
				}
				if decoded.Snapshot != nil {
					if strings.Contains(line, `"eventId"`) || strings.Contains(line, `"commandId"`) || strings.Contains(line, `"sequence"`) {
						t.Fatalf("snapshot line contains fact keys: %s", line)
					}
				}
				if decoded.Complete != nil {
					if strings.Contains(line, `"eventId"`) || strings.Contains(line, `"commandId"`) || strings.Contains(line, `"sequence"`) {
						t.Fatalf("complete line contains fact keys: %s", line)
					}
				}
			}
		})
	}
}

func TestSnapshotAndCompleteGoldensMatchSpec(t *testing.T) {
	t.Parallel()

	snapshotPayload, err := json.Marshal(snapshotPayload{
		HeadSequence: 12, Open: true, Running: false, Stability: StabilityExperimental,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := MarshalSnapshot(SnapshotLine{
		FormatVersion: FormatVersion,
		Schema:        Schema,
		SessionID:     "session-1",
		OccurredAt:    "2026-08-23T12:00:00.000000000Z",
		Type:          TypeSnapshot,
		Payload:       snapshotPayload,
	})
	if err != nil {
		t.Fatalf("MarshalSnapshot() error = %v", err)
	}
	wantSnapshot := strings.TrimSpace(readFixture(t, "testdata/snapshot.jsonl"))
	if string(encoded) != wantSnapshot {
		t.Fatalf("snapshot = %s\nwant = %s", encoded, wantSnapshot)
	}

	completePayload, err := json.Marshal(completePayload{
		HeadSequence: 12, FactLines: 9, Open: true, Running: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = MarshalComplete(CompleteLine{
		FormatVersion: FormatVersion,
		Schema:        Schema,
		SessionID:     "session-1",
		OccurredAt:    "2026-08-23T12:00:00.000000000Z",
		Type:          TypeComplete,
		Payload:       completePayload,
	})
	if err != nil {
		t.Fatalf("MarshalComplete() error = %v", err)
	}
	wantComplete := strings.TrimSpace(readFixture(t, "testdata/complete.jsonl"))
	if string(encoded) != wantComplete {
		t.Fatalf("complete = %s\nwant = %s", encoded, wantComplete)
	}
}

func TestProjectRecordFrozenPayloads(t *testing.T) {
	t.Parallel()

	occurred := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	offer := domain.ToolCallOffer{ID: "call-1", Name: "read_file", Arguments: `{"path":"README.md"}`}
	tests := []struct {
		name  string
		seq   uint64
		event domain.Event
		steps map[domain.TurnID]uint32
	}{
		{name: "session created", seq: 1, event: domain.SessionCreated{WorkspaceRoot: "/workspace"}},
		{name: "session closed", seq: 2, event: domain.SessionClosed{}},
		{name: "turn started", seq: 3, event: domain.TurnStarted{TurnID: "turn-1", Input: "inspect"}},
		{name: "turn completed", seq: 4, event: domain.TurnCompleted{TurnID: "turn-1"}},
		{name: "turn failed", seq: 5, event: domain.TurnFailed{TurnID: "turn-1", Code: "provider_error", Message: "failed"}},
		{name: "turn interrupted", seq: 6, event: domain.TurnInterrupted{TurnID: "turn-1", Reason: "canceled"}},
		{name: "assistant started", seq: 7, event: domain.AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"}, steps: map[domain.TurnID]uint32{}},
		{name: "assistant completed", seq: 8, event: domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "hello", ToolCalls: []domain.ToolCallOffer{offer}}, steps: map[domain.TurnID]uint32{"turn-1": 1}},
		{name: "assistant failed", seq: 9, event: domain.AssistantMessageFailed{TurnID: "turn-1", ItemID: "item-1", Code: "provider_error", Message: "failed"}, steps: map[domain.TurnID]uint32{"turn-1": 1}},
		{name: "assistant interrupted", seq: 10, event: domain.AssistantMessageInterrupted{TurnID: "turn-1", ItemID: "item-1", Code: "canceled", Message: "stopped"}, steps: map[domain.TurnID]uint32{"turn-1": 1}},
		{name: "model usage", seq: 11, event: domain.ModelUsageRecorded{TurnID: "turn-1", ItemID: "item-1", InputTokens: 3, OutputTokens: 5, CachedInputTokens: 1, LatencyMs: 12, FinishReason: "stop", ProviderRequestID: "req-1"}},
		{name: "tool started", seq: 12, event: domain.ToolCallStarted{TurnID: "turn-1", ItemID: "item-1", CallID: "call-1", Name: "read_file", Arguments: `{"path":"README.md"}`, StepIndex: 1}},
		{name: "tool completed", seq: 13, event: domain.ToolCallCompleted{TurnID: "turn-1", ItemID: "item-1", CallID: "call-1", Content: "file contents", Truncated: false}, steps: map[domain.TurnID]uint32{"turn-1": 1}},
		{name: "tool failed", seq: 14, event: domain.ToolCallFailed{TurnID: "turn-1", ItemID: "item-1", CallID: "call-1", Code: "unknown_tool", Message: "missing"}, steps: map[domain.TurnID]uint32{"turn-1": 1}},
		{name: "tool interrupted", seq: 15, event: domain.ToolCallInterrupted{TurnID: "turn-1", ItemID: "item-1", CallID: "call-1", Code: "canceled", Message: "stopped"}, steps: map[domain.TurnID]uint32{"turn-1": 1}},
		{name: "approval requested", seq: 16, event: domain.ApprovalRequested{TurnID: "turn-1", ItemID: "item-1", ApprovalID: "approval-1", CallID: "call-1", Name: "run_command", Reason: "exec"}},
		{name: "approval resolved", seq: 17, event: domain.ApprovalResolved{TurnID: "turn-1", ItemID: "item-1", ApprovalID: "approval-1", Decision: "granted"}},
	}

	wantLines := strings.Split(strings.TrimSpace(readFixture(t, "testdata/facts.jsonl")), "\n")
	if len(wantLines) != len(tests) {
		t.Fatalf("facts fixture lines = %d, tests = %d", len(wantLines), len(tests))
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			steps := test.steps
			if steps == nil {
				steps = map[domain.TurnID]uint32{}
			}
			line, ok, err := ProjectRecord(fixtureRecord(test.seq, occurred, test.event), steps)
			if err != nil {
				t.Fatalf("ProjectRecord() error = %v", err)
			}
			if !ok {
				t.Fatal("ProjectRecord() ok = false")
			}
			encoded, err := MarshalLine(line)
			if err != nil {
				t.Fatalf("MarshalLine() error = %v", err)
			}
			if string(encoded) != wantLines[index] {
				t.Fatalf("encoded = %s\nwant = %s", encoded, wantLines[index])
			}
		})
	}
}

func TestProjectRecordOmitsRequestAndPolicy(t *testing.T) {
	t.Parallel()

	occurred := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	tests := []domain.Event{
		domain.ModelRequestRecorded{TurnID: "turn-1", ItemID: "item-1"},
		domain.PolicyDecisionRecorded{TurnID: "turn-1", ItemID: "item-1", CallID: "call-1", Name: "read_file", Effect: "deny", RuleID: "r1", Reason: "blocked"},
	}
	for _, event := range tests {
		t.Run(event.EventType(), func(t *testing.T) {
			line, ok, err := ProjectRecord(fixtureRecord(4, occurred, event), map[domain.TurnID]uint32{})
			if err != nil {
				t.Fatalf("ProjectRecord() error = %v", err)
			}
			if ok {
				t.Fatalf("ProjectRecord() ok = true, line = %+v", line)
			}
			if line.Type != "" {
				t.Fatalf("omitted line = %+v", line)
			}
		})
	}
}

func TestProjectRecordRejectsUnknownDomainType(t *testing.T) {
	t.Parallel()

	record := fixtureRecord(1, time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC), unknownEvent{})
	_, ok, err := ProjectRecord(record, map[domain.TurnID]uint32{})
	if ok || !IsCode(err, CodeUnsupportedEventType) {
		t.Fatalf("ProjectRecord() ok = %t, error = %v, want code %q", ok, err, CodeUnsupportedEventType)
	}
}

func TestProjectRecordStepRefAlignment(t *testing.T) {
	t.Parallel()

	occurred := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	steps := map[domain.TurnID]uint32{}
	project := func(seq uint64, event domain.Event) Line {
		t.Helper()
		line, ok, err := ProjectRecord(fixtureRecord(seq, occurred, event), steps)
		if err != nil || !ok {
			t.Fatalf("ProjectRecord(%d) ok = %t, error = %v", seq, ok, err)
		}
		return line
	}

	started1 := project(1, domain.AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"})
	assertPayload(t, started1, `"stepIndex":1`, `"stepRef":"turn-1/1"`)
	if steps[domain.TurnID("turn-1")] != 1 {
		t.Fatalf("steps[turn-1] = %d, want 1", steps[domain.TurnID("turn-1")])
	}

	toolStarted1 := project(2, domain.ToolCallStarted{
		TurnID: "turn-1", ItemID: "item-1", CallID: "call-1",
		Name: "read_file", Arguments: `{"path":"a.txt"}`, StepIndex: 1,
	})
	assertPayload(t, toolStarted1, `"stepIndex":1`, `"stepRef":"turn-1/1"`)

	toolCompleted1 := project(3, domain.ToolCallCompleted{
		TurnID: "turn-1", ItemID: "item-1", CallID: "call-1", Content: "a", Truncated: false,
	})
	assertPayload(t, toolCompleted1, `"stepIndex":1`, `"stepRef":"turn-1/1"`)

	started2 := project(4, domain.AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-2"})
	assertPayload(t, started2, `"stepIndex":2`, `"stepRef":"turn-1/2"`)

	toolStarted2 := project(5, domain.ToolCallStarted{
		TurnID: "turn-1", ItemID: "item-2", CallID: "call-2",
		Name: "read_file", Arguments: `{"path":"b.txt"}`, StepIndex: 2,
	})
	assertPayload(t, toolStarted2, `"stepIndex":2`, `"stepRef":"turn-1/2"`)

	toolCompleted2 := project(6, domain.ToolCallCompleted{
		TurnID: "turn-1", ItemID: "item-2", CallID: "call-2", Content: "b", Truncated: false,
	})
	assertPayload(t, toolCompleted2, `"stepIndex":2`, `"stepRef":"turn-1/2"`)

	mismatched := project(7, domain.ToolCallStarted{
		TurnID: "turn-1", ItemID: "item-2", CallID: "call-3",
		Name: "read_file", Arguments: `{"path":"c.txt"}`, StepIndex: 99,
	})
	assertPayload(t, mismatched, `"stepIndex":99`, `"stepRef":"turn-1/99"`)
	if steps[domain.TurnID("turn-1")] != 2 {
		t.Fatalf("mismatched start rewrote steps[turn-1] = %d", steps[domain.TurnID("turn-1")])
	}
}

func TestProjectRecordOmitsUsageWhenAbsent(t *testing.T) {
	t.Parallel()

	occurred := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	line, ok, err := ProjectRecord(fixtureRecord(1, occurred, domain.TurnCompleted{TurnID: "turn-1"}), map[domain.TurnID]uint32{})
	if err != nil || !ok {
		t.Fatalf("ProjectRecord() ok = %t, error = %v", ok, err)
	}
	if line.Type == domain.EventModelUsageRecorded {
		t.Fatal("usage line emitted without model.usage.recorded")
	}
}

func TestProjectRecordUsagePresent(t *testing.T) {
	t.Parallel()

	occurred := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	line, ok, err := ProjectRecord(fixtureRecord(1, occurred, domain.ModelUsageRecorded{
		TurnID: "turn-1", ItemID: "item-1", InputTokens: 3, OutputTokens: 5,
	}), map[domain.TurnID]uint32{})
	if err != nil || !ok {
		t.Fatalf("ProjectRecord() ok = %t, error = %v", ok, err)
	}
	if line.Type != domain.EventModelUsageRecorded {
		t.Fatalf("type = %q", line.Type)
	}
	if bytes.Contains(line.Payload, []byte(`"origin"`)) {
		t.Fatalf("payload introduced origin: %s", line.Payload)
	}
}

func TestUnmarshalLineThreeArm(t *testing.T) {
	t.Parallel()

	snapshot := strings.TrimSpace(readFixture(t, "testdata/snapshot.jsonl"))
	decoded, err := UnmarshalLine([]byte(snapshot))
	if err != nil {
		t.Fatalf("snapshot UnmarshalLine() error = %v", err)
	}
	if decoded.Snapshot == nil || decoded.Line != nil || decoded.Complete != nil {
		t.Fatalf("snapshot arm = %+v", decoded)
	}

	complete := strings.TrimSpace(readFixture(t, "testdata/complete.jsonl"))
	decoded, err = UnmarshalLine([]byte(complete))
	if err != nil {
		t.Fatalf("complete UnmarshalLine() error = %v", err)
	}
	if decoded.Complete == nil || decoded.Line != nil || decoded.Snapshot != nil {
		t.Fatalf("complete arm = %+v", decoded)
	}

	fact := strings.Split(strings.TrimSpace(readFixture(t, "testdata/facts.jsonl")), "\n")[0]
	decoded, err = UnmarshalLine([]byte(fact))
	if err != nil {
		t.Fatalf("fact UnmarshalLine() error = %v", err)
	}
	if decoded.Line == nil || decoded.Snapshot != nil || decoded.Complete != nil {
		t.Fatalf("fact arm = %+v", decoded)
	}
}

func TestUnmarshalLineRejectsWrongKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		code  string
	}{
		{
			name:  "snapshot with eventId",
			input: `{"formatVersion":1,"schema":"och.session.transcript","sessionId":"session-1","eventId":"event-1","occurredAt":"2026-08-23T12:00:00.000000000Z","type":"transcript.snapshot","payload":{"headSequence":12,"open":true,"running":false,"stability":"experimental"}}`,
			code:  CodeInvalidLine,
		},
		{
			name:  "complete with sequence",
			input: `{"formatVersion":1,"schema":"och.session.transcript","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-23T12:00:00.000000000Z","type":"transcript.complete","payload":{"headSequence":12,"factLines":9,"open":true,"running":false}}`,
			code:  CodeInvalidLine,
		},
		{
			name:  "fact missing sequence",
			input: `{"formatVersion":1,"schema":"och.session.transcript","sessionId":"session-1","eventId":"event-1","commandId":"command-1","occurredAt":"2026-08-23T12:00:00.000000000Z","type":"session.closed","payload":{}}`,
			code:  CodeInvalidLine,
		},
		{
			name:  "unknown format version",
			input: `{"formatVersion":2,"schema":"och.session.transcript","sessionId":"session-1","occurredAt":"2026-08-23T12:00:00.000000000Z","type":"transcript.snapshot","payload":{"headSequence":12,"open":true,"running":false,"stability":"experimental"}}`,
			code:  CodeUnsupportedFormatVersion,
		},
		{
			name:  "trailing JSON",
			input: strings.TrimSpace(readFixture(t, "testdata/snapshot.jsonl")) + ` {}`,
			code:  CodeInvalidLine,
		},
		{
			name:  "unknown fact type",
			input: `{"formatVersion":1,"schema":"och.session.transcript","sessionId":"session-1","eventId":"event-1","commandId":"command-1","sequence":1,"occurredAt":"2026-08-23T12:00:00.000000000Z","type":"future.fact","payload":{}}`,
			code:  CodeUnsupportedEventType,
		},
		{
			name:  "snapshot extra payload key",
			input: `{"formatVersion":1,"schema":"och.session.transcript","sessionId":"session-1","occurredAt":"2026-08-23T12:00:00.000000000Z","type":"transcript.snapshot","payload":{"headSequence":12,"open":true,"running":false,"stability":"experimental","extra":true}}`,
			code:  CodeInvalidLine,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := UnmarshalLine([]byte(test.input))
			if !IsCode(err, test.code) {
				t.Fatalf("UnmarshalLine() error = %v, want code %q", err, test.code)
			}
		})
	}
}

func TestDecodeSkipsUnknownFactTypesOnly(t *testing.T) {
	t.Parallel()

	unknownFact := `{"formatVersion":1,"schema":"och.session.transcript","sessionId":"session-1","eventId":"event-1","commandId":"command-1","sequence":1,"occurredAt":"2026-08-23T12:00:00.000000000Z","type":"future.fact","payload":{}}`
	decoded, skipped, err := DecodeSkipsUnknown([]byte(unknownFact))
	if err != nil || !skipped {
		t.Fatalf("unknown fact skipped = %t, error = %v", skipped, err)
	}
	if decoded.Line != nil || decoded.Snapshot != nil || decoded.Complete != nil {
		t.Fatalf("skipped decode = %+v", decoded)
	}

	_, err = UnmarshalLine([]byte(unknownFact))
	if !IsCode(err, CodeUnsupportedEventType) {
		t.Fatalf("strict decoder error = %v, want %q", err, CodeUnsupportedEventType)
	}

	brokenSnapshot := `{"formatVersion":1,"schema":"och.session.transcript","sessionId":"session-1","occurredAt":"2026-08-23T12:00:00.000000000Z","type":"transcript.snapshot","payload":{"headSequence":12}}`
	_, skipped, err = DecodeSkipsUnknown([]byte(brokenSnapshot))
	if skipped || !IsCode(err, CodeInvalidLine) {
		t.Fatalf("broken snapshot skipped = %t, error = %v", skipped, err)
	}

	brokenComplete := `{"formatVersion":1,"schema":"och.session.transcript","sessionId":"session-1","occurredAt":"2026-08-23T12:00:00.000000000Z","type":"transcript.complete","payload":{}}`
	_, skipped, err = DecodeSkipsUnknown([]byte(brokenComplete))
	if skipped || !IsCode(err, CodeInvalidLine) {
		t.Fatalf("broken complete skipped = %t, error = %v", skipped, err)
	}

	known := strings.Split(strings.TrimSpace(readFixture(t, "testdata/facts.jsonl")), "\n")[0]
	decoded, skipped, err = DecodeSkipsUnknown([]byte(known))
	if err != nil || skipped || decoded.Line == nil {
		t.Fatalf("known fact skipped = %t, error = %v, decoded = %+v", skipped, err, decoded)
	}

	futureVersion := `{"formatVersion":2,"schema":"och.session.transcript","sessionId":"session-1","eventId":"event-1","commandId":"command-1","sequence":1,"occurredAt":"2026-08-23T12:00:00.000000000Z","type":"future.fact","payload":{}}`
	_, skipped, err = DecodeSkipsUnknown([]byte(futureVersion))
	if skipped || !IsCode(err, CodeUnsupportedFormatVersion) {
		t.Fatalf("future version skipped = %t, error = %v", skipped, err)
	}

	incomplete := `{"formatVersion":1,"type":"future.fact"}`
	_, skipped, err = DecodeSkipsUnknown([]byte(incomplete))
	if skipped || !IsCode(err, CodeInvalidLine) {
		t.Fatalf("incomplete envelope skipped = %t, error = %v", skipped, err)
	}

	trailing := unknownFact + `{"extra":true}`
	_, skipped, err = DecodeSkipsUnknown([]byte(trailing))
	if skipped || !IsCode(err, CodeInvalidLine) {
		t.Fatalf("trailing JSON skipped = %t, error = %v", skipped, err)
	}

	missingPayload := `{"formatVersion":1,"schema":"och.session.transcript","sessionId":"session-1","eventId":"event-1","commandId":"command-1","sequence":1,"occurredAt":"2026-08-23T12:00:00.000000000Z","type":"future.fact","payload":null}`
	_, skipped, err = DecodeSkipsUnknown([]byte(missingPayload))
	if skipped || !IsCode(err, CodeInvalidLine) {
		t.Fatalf("null payload skipped = %t, error = %v", skipped, err)
	}
}

func TestMarshalLineLimit(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(struct {
		Text string `json:"text"`
	}{Text: strings.Repeat("a", maxLineBytes)})
	if err != nil {
		t.Fatal(err)
	}
	line := Line{
		FormatVersion: FormatVersion,
		Schema:        Schema,
		SessionID:     "session-1",
		EventID:       "event-1",
		CommandID:     "command-1",
		Sequence:      1,
		OccurredAt:    "2026-08-23T12:00:00.000000000Z",
		Type:          domain.EventAssistantMessageCompleted,
		Payload:       payload,
	}
	encoded, err := MarshalLine(line)
	if encoded != nil || !IsCode(err, CodeLineLimit) {
		t.Fatalf("MarshalLine() encoded len = %d, error = %v, want code %q", len(encoded), err, CodeLineLimit)
	}

	snapshot := SnapshotLine{
		FormatVersion: FormatVersion,
		Schema:        Schema,
		SessionID:     "session-1",
		OccurredAt:    "2026-08-23T12:00:00.000000000Z",
		Type:          TypeSnapshot,
		Payload:       payload,
	}
	encoded, err = MarshalSnapshot(snapshot)
	if encoded != nil || !IsCode(err, CodeLineLimit) {
		t.Fatalf("MarshalSnapshot() encoded len = %d, error = %v, want code %q", len(encoded), err, CodeLineLimit)
	}
}

func TestProjectRecordFormatsUTCNanoseconds(t *testing.T) {
	t.Parallel()

	occurred := time.Date(2026, 8, 23, 20, 0, 0, 1, time.FixedZone("offset", 8*60*60))
	line, ok, err := ProjectRecord(fixtureRecord(1, occurred, domain.SessionClosed{}), map[domain.TurnID]uint32{})
	if err != nil || !ok {
		t.Fatalf("ProjectRecord() ok = %t, error = %v", ok, err)
	}
	if line.OccurredAt != "2026-08-23T12:00:00.000000001Z" {
		t.Fatalf("occurredAt = %q", line.OccurredAt)
	}
}

func TestAssistantCompletedOmitsEmptyToolCalls(t *testing.T) {
	t.Parallel()

	occurred := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	line, ok, err := ProjectRecord(fixtureRecord(1, occurred, domain.AssistantMessageCompleted{
		TurnID: "turn-1", ItemID: "item-1", Text: "hello",
	}), map[domain.TurnID]uint32{"turn-1": 1})
	if err != nil || !ok {
		t.Fatalf("ProjectRecord() ok = %t, error = %v", ok, err)
	}
	if bytes.Contains(line.Payload, []byte(`"toolCalls"`)) {
		t.Fatalf("empty toolCalls encoded: %s", line.Payload)
	}
}

func marshalDecoded(decoded Decoded) ([]byte, error) {
	switch {
	case decoded.Snapshot != nil:
		return MarshalSnapshot(*decoded.Snapshot)
	case decoded.Complete != nil:
		return MarshalComplete(*decoded.Complete)
	case decoded.Line != nil:
		return MarshalLine(*decoded.Line)
	default:
		return nil, &Error{Code: CodeInvalidLine, Message: "empty decoded line"}
	}
}

func fixtureRecord(sequence uint64, occurred time.Time, event domain.Event) domain.RecordedEvent {
	return domain.RecordedEvent{
		SchemaVersion: 1,
		ID:            domain.EventID("event-" + strconv.FormatUint(sequence, 10)),
		CommandID:     domain.CommandID("command-" + strconv.FormatUint(sequence, 10)),
		SessionID:     "session-1",
		Sequence:      sequence,
		OccurredAt:    occurred,
		Event:         event,
	}
}

func readFixture(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return string(data)
}

func assertPayload(t *testing.T, line Line, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !bytes.Contains(line.Payload, []byte(fragment)) {
			t.Fatalf("payload %s does not contain %s", line.Payload, fragment)
		}
	}
}

type unknownEvent struct{}

func (unknownEvent) EventType() string { return "future.invented" }
