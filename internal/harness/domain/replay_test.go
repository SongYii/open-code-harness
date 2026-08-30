package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReplayFixtureIsDeterministic(t *testing.T) {
	data, err := os.Open("testdata/session_lifecycle.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	records, err := DecodeJSONL(data)
	if err != nil {
		t.Fatalf("DecodeJSONL() error = %v", err)
	}
	first, err := HistoricalReplay(records)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	second, err := HistoricalReplay(records)
	if err != nil {
		t.Fatalf("Replay() second error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replays differ")
	}
	if first.Status != SessionStatusDeleted || first.Version != 7 || len(first.TurnOrder) != 2 {
		t.Fatalf("Replay() = %#v", first)
	}
	if first.Turns[TurnID("turn-1")].Status != TurnStatusCompleted || first.Turns[TurnID("turn-2")].Status != TurnStatusInterrupted {
		t.Fatalf("Replay() turns = %#v", first.Turns)
	}
}

func TestReplayAssistantLifecycleFixture(t *testing.T) {
	t.Parallel()

	data, err := os.Open("testdata/assistant_lifecycle.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	records, err := DecodeJSONL(data)
	if err != nil {
		t.Fatalf("DecodeJSONL() error = %v", err)
	}
	wantTypes := []string{
		EventSessionCreated,
		EventTurnStarted,
		EventAssistantMessageStarted,
		EventAssistantMessageCompleted,
		EventTurnCompleted,
		EventTurnStarted,
		EventTurnInterrupted,
		EventSessionClosed,
	}
	if len(records) != len(wantTypes) {
		t.Fatalf("record count = %d, want %d", len(records), len(wantTypes))
	}
	for index, wantType := range wantTypes {
		if got := records[index].Event.EventType(); got != wantType {
			t.Fatalf("record %d type = %q, want %q", index+1, got, wantType)
		}
		if records[index].Sequence != uint64(index+1) {
			t.Fatalf("record %d sequence = %d, want %d", index+1, records[index].Sequence, index+1)
		}
	}
	if records[3].CommandID != "command-complete-assistant-turn" || records[4].CommandID != records[3].CommandID {
		t.Fatalf("terminal command IDs = %q, %q, want one literal command ID", records[3].CommandID, records[4].CommandID)
	}
	if !records[3].OccurredAt.Equal(records[4].OccurredAt) {
		t.Fatalf("terminal occurrence times = %s, %s, want equal", records[3].OccurredAt, records[4].OccurredAt)
	}

	state, err := HistoricalReplay(records)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if state.Status != SessionStatusClosed || state.Version != 8 {
		t.Fatalf("Replay() session status/version = %q/%d, want %q/8", state.Status, state.Version, SessionStatusClosed)
	}
	firstTurn := state.Turns["turn-1"]
	item := firstTurn.Items["item-1"]
	payload, ok := item.Payload.(AssistantMessagePayload)
	if firstTurn.Status != TurnStatusCompleted || item.Status != ItemStatusCompleted || !ok || payload.Text != "你好，工业级 harness 🌏" {
		t.Fatalf("Replay() first turn/item = %#v/%#v, payload = %#v", firstTurn, item, item.Payload)
	}
	if state.Turns["turn-2"].Status != TurnStatusInterrupted || state.Turns["turn-2"].InterruptWhy != "caller_canceled" {
		t.Fatalf("Replay() second turn = %#v", state.Turns["turn-2"])
	}
}

func TestDecodeJSONLRejectsEmptyAndBlankRecords(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantLine int
	}{
		{name: "empty stream", input: ""},
		{name: "blank first line", input: "\n", wantLine: 1},
		{name: "blank second line", input: replayJSONL(t, []RecordedEvent{replayRecord(1, SessionCreated{WorkspaceRoot: "/workspace"})}) + "\n\n", wantLine: 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeJSONL(strings.NewReader(test.input))
			if !IsCode(err, CodeInvalidEvent) {
				t.Fatalf("DecodeJSONL() error = %v, want code %q", err, CodeInvalidEvent)
			}
			if test.wantLine != 0 && !strings.Contains(err.Error(), fmt.Sprintf("line %d", test.wantLine)) {
				t.Fatalf("DecodeJSONL() error = %v, want line %d context", err, test.wantLine)
			}
		})
	}
}

func TestDecodeJSONLAcceptsExactlyOneMiBRecordAndRejectsLargerRecord(t *testing.T) {
	exactlyOneMiB := jsonlRecordOfSize(t, maxJSONLRecordSize)

	for _, test := range []struct {
		name   string
		suffix string
	}{
		{name: "EOF"},
		{name: "LF", suffix: "\n"},
		{name: "CRLF", suffix: "\r\n"},
	} {
		t.Run("exact limit with "+test.name, func(t *testing.T) {
			records, err := DecodeJSONL(strings.NewReader(string(exactlyOneMiB) + test.suffix))
			if err != nil {
				t.Fatalf("DecodeJSONL() exact %d-byte record error = %v", maxJSONLRecordSize, err)
			}
			if len(records) != 1 {
				t.Fatalf("DecodeJSONL() exact %d-byte record count = %d, want 1", maxJSONLRecordSize, len(records))
			}
		})
	}

	overLimit := jsonlRecordOfSize(t, maxJSONLRecordSize+1)
	for _, test := range []struct {
		name   string
		suffix string
	}{
		{name: "EOF"},
		{name: "LF", suffix: "\n"},
		{name: "CRLF", suffix: "\r\n"},
	} {
		t.Run("over limit with "+test.name, func(t *testing.T) {
			records, err := DecodeJSONL(strings.NewReader(string(overLimit) + test.suffix))
			if !IsCode(err, CodeInvalidEvent) {
				t.Fatalf("DecodeJSONL() over-limit record error = %v, want code %q", err, CodeInvalidEvent)
			}
			if records != nil {
				t.Fatalf("DecodeJSONL() over-limit records = %#v, want nil", records)
			}
		})
	}
}

func TestDecodeJSONLReportsLaterOversizedRecordLine(t *testing.T) {
	t.Parallel()

	overLimit := jsonlRecordOfSize(t, maxJSONLRecordSize+1)
	prefixes := []struct {
		name     string
		records  []RecordedEvent
		wantLine int
	}{
		{
			name: "line 2",
			records: []RecordedEvent{
				replayRecord(1, SessionCreated{WorkspaceRoot: "/workspace"}),
			},
			wantLine: 2,
		},
		{
			name: "line 4",
			records: []RecordedEvent{
				replayRecord(1, SessionCreated{WorkspaceRoot: "/workspace"}),
				replayRecord(2, TurnStarted{TurnID: "turn-1", Input: "inspect"}),
				replayRecord(3, TurnCompleted{TurnID: "turn-1"}),
			},
			wantLine: 4,
		},
	}
	endings := []struct {
		name             string
		suffix           string
		wantScannerCause bool
	}{
		{name: "EOF"},
		{name: "LF", suffix: "\n"},
		{name: "CRLF", suffix: "\r\n", wantScannerCause: true},
	}

	for _, prefix := range prefixes {
		for _, ending := range endings {
			t.Run(prefix.name+" with "+ending.name, func(t *testing.T) {
				input := replayJSONL(t, prefix.records) + "\n" + string(overLimit) + ending.suffix
				records, err := DecodeJSONL(strings.NewReader(input))
				if !IsCode(err, CodeInvalidEvent) {
					t.Fatalf("DecodeJSONL() error = %v, want code %q", err, CodeInvalidEvent)
				}
				if records != nil {
					t.Fatalf("DecodeJSONL() records = %#v, want nil", records)
				}
				wantLine := fmt.Sprintf("JSONL line %d:", prefix.wantLine)
				if !strings.Contains(err.Error(), wantLine) {
					t.Fatalf("DecodeJSONL() error = %v, want %q", err, wantLine)
				}
				if ending.wantScannerCause && !strings.Contains(err.Error(), "token too long") {
					t.Fatalf("DecodeJSONL() error = %v, want scanner token-too-long cause", err)
				}
			})
		}
	}
}

func TestDecodeJSONLPreservesReaderError(t *testing.T) {
	sentinel := errors.New("reader failed")
	_, err := DecodeJSONL(failingReader{err: sentinel})
	if !IsCode(err, CodeInvalidEvent) {
		t.Fatalf("DecodeJSONL() error = %v, want code %q", err, CodeInvalidEvent)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("DecodeJSONL() error = %v, want wrapped reader error", err)
	}
}

func TestDecodeJSONLPreservesReaderErrorWhenPartialTokenFailsToDecode(t *testing.T) {
	sentinel := errors.New("reader failed after data")
	records, err := DecodeJSONL(dataAndErrorReader{data: []byte(`{"schemaVersion":`), err: sentinel})
	if !IsCode(err, CodeInvalidEvent) {
		t.Fatalf("DecodeJSONL() error = %v, want code %q", err, CodeInvalidEvent)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("DecodeJSONL() error = %v, want wrapped reader error", err)
	}
	if !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("DecodeJSONL() error = %v, want line 1 context", err)
	}
	if records != nil {
		t.Fatalf("DecodeJSONL() records = %#v, want nil", records)
	}
}

func TestDecodeJSONLAttributesReaderErrorAfterCompleteRecordToThatLine(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("reader failed with complete record")
	line := replayJSONL(t, []RecordedEvent{
		replayRecord(1, SessionCreated{WorkspaceRoot: "/workspace"}),
	}) + "\n"
	records, err := DecodeJSONL(dataAndErrorReader{data: []byte(line), err: sentinel})
	if !IsCode(err, CodeInvalidEvent) {
		t.Fatalf("DecodeJSONL() error = %v, want code %q", err, CodeInvalidEvent)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("DecodeJSONL() error = %v, want wrapped reader error", err)
	}
	if !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("DecodeJSONL() error = %v, want line 1 context", err)
	}
	if records != nil {
		t.Fatalf("DecodeJSONL() records = %#v, want nil", records)
	}
}

func TestReplayRejectsCorruptStreamsWithoutPartialState(t *testing.T) {
	created := replayRecord(1, SessionCreated{WorkspaceRoot: "/workspace"})
	startedAtThree := replayRecord(3, TurnStarted{TurnID: "turn-1", Input: "inspect"})
	duplicatedSequence := replayRecord(1, TurnStarted{TurnID: "turn-1", Input: "inspect"})
	changedSession := replayRecord(2, TurnStarted{TurnID: "turn-1", Input: "inspect"})
	changedSession.SessionID = "session-other"

	tests := []struct {
		name    string
		records []RecordedEvent
		code    ErrorCode
	}{
		{
			name:    "missing sequence two",
			records: []RecordedEvent{created, startedAtThree},
			code:    CodeSequenceMismatch,
		},
		{
			name:    "duplicated sequence one",
			records: []RecordedEvent{created, duplicatedSequence},
			code:    CodeSequenceMismatch,
		},
		{
			name:    "session ID changes at sequence two",
			records: []RecordedEvent{created, changedSession},
			code:    CodeInvalidEvent,
		},
		{
			name:    "turn starts before session creation",
			records: []RecordedEvent{replayRecord(1, TurnStarted{TurnID: "turn-1", Input: "inspect"})},
			code:    CodeSessionNotFound,
		},
		{
			name: "second session created",
			records: []RecordedEvent{
				created,
				replayRecord(2, SessionCreated{WorkspaceRoot: "/workspace"}),
			},
			code: CodeSessionAlreadyExists,
		},
		{
			name: "event after session close",
			records: []RecordedEvent{
				created,
				replayRecord(2, SessionClosed{}),
				replayRecord(3, TurnStarted{TurnID: "turn-1", Input: "inspect"}),
			},
			code: CodeSessionClosed,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := HistoricalReplay(test.records)
			if !IsCode(err, test.code) {
				t.Fatalf("Replay() error = %v, want code %q", err, test.code)
			}
			if !reflect.DeepEqual(state, HistoricalSession{}) {
				t.Fatalf("Replay() state = %#v, want zero HistoricalSession on error", state)
			}
		})
	}
}

func TestReplayRejectsTypedRecordOutsideCodecContract(t *testing.T) {
	t.Parallel()

	record := replayRecord(1, SessionCreated{WorkspaceRoot: "/workspace"})
	record.Event = SessionCreated{WorkspaceRoot: " \t "}

	state, err := HistoricalReplay([]RecordedEvent{record})
	if !IsCode(err, CodeInvalidEvent) {
		t.Fatalf("Replay() error = %v, want code %q", err, CodeInvalidEvent)
	}
	if !reflect.DeepEqual(state, HistoricalSession{}) {
		t.Fatalf("Replay() state = %#v, want zero HistoricalSession", state)
	}
}

func TestInvalidUTF8CannotDivergeBetweenTypedAndWireReplay(t *testing.T) {
	t.Parallel()

	invalid := "workspace-\xff"
	record := replayRecord(1, SessionCreated{WorkspaceRoot: invalid})
	rewrittenPayload, err := json.Marshal(record.Event)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !bytes.Contains(rewrittenPayload, []byte(`\ufffd`)) {
		t.Fatalf("json.Marshal() = %q, want replacement escape proving invalid UTF-8 rewrite", rewrittenPayload)
	}

	state, replayErr := HistoricalReplay([]RecordedEvent{record})
	encoded, marshalErr := MarshalRecordedEvent(record)
	if !IsCode(replayErr, CodeInvalidEvent) || !IsCode(marshalErr, CodeInvalidEvent) {
		t.Fatalf("typed Replay() state = %#v, error = %v; MarshalRecordedEvent() = %q, error = %v; want invalid_event at both boundaries", state, replayErr, encoded, marshalErr)
	}
}

func TestReplayDoesNotMutateImmutableRecordsDuringParallelUse(t *testing.T) {
	records := replayFixtureRecords(t)
	before := append([]RecordedEvent(nil), records...)

	const workers = 32
	type result struct {
		state HistoricalSession
		err   error
	}
	results := make(chan result, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			state, err := HistoricalReplay(records)
			results <- result{state: state, err: err}
		}()
	}
	group.Wait()
	close(results)

	var first HistoricalSession
	for index := 0; index < workers; index++ {
		result := <-results
		if result.err != nil {
			t.Fatalf("Replay() error = %v", result.err)
		}
		if index == 0 {
			first = result.state
			continue
		}
		if !reflect.DeepEqual(result.state, first) {
			t.Fatalf("Replay() state = %#v, want %#v", result.state, first)
		}
	}
	if !reflect.DeepEqual(records, before) {
		t.Fatalf("Replay() mutated records: got %#v, want %#v", records, before)
	}
}

func replayFixtureRecords(t *testing.T) []RecordedEvent {
	t.Helper()
	data, err := os.Open("testdata/session_lifecycle.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()

	records, err := DecodeJSONL(data)
	if err != nil {
		t.Fatalf("DecodeJSONL() error = %v", err)
	}
	return records
}

func replayJSONL(t *testing.T, records []RecordedEvent) string {
	t.Helper()
	lines := make([]string, len(records))
	for index, record := range records {
		encoded, err := MarshalRecordedEvent(record)
		if err != nil {
			t.Fatalf("MarshalRecordedEvent() error = %v", err)
		}
		lines[index] = string(encoded)
	}
	return strings.Join(lines, "\n")
}

func replayRecord(sequence uint64, event Event) RecordedEvent {
	return RecordedEvent{
		SchemaVersion: 1,
		ID:            EventID(fmt.Sprintf("event-%d", sequence)),
		CommandID:     CommandID(fmt.Sprintf("command-%d", sequence)),
		SessionID:     "session-1",
		Sequence:      sequence,
		OccurredAt:    time.Date(2026, 8, 11, 1, 2, int(sequence), 0, time.UTC),
		Event:         event,
	}
}

func jsonlRecordOfSize(t *testing.T, size int) []byte {
	t.Helper()
	record := replayRecord(1, SessionCreated{WorkspaceRoot: "x"})
	encoded, err := MarshalRecordedEvent(record)
	if err != nil {
		t.Fatalf("MarshalRecordedEvent() base record error = %v", err)
	}
	workspaceRootSize := size - len(encoded) + 1
	if workspaceRootSize < 1 {
		t.Fatalf("record size %d is too small", size)
	}
	record.Event = SessionCreated{WorkspaceRoot: strings.Repeat("x", workspaceRootSize)}
	encoded, err = MarshalRecordedEvent(record)
	if err != nil {
		t.Fatalf("MarshalRecordedEvent() sized record error = %v", err)
	}
	if len(encoded) != size {
		t.Fatalf("encoded record size = %d, want %d", len(encoded), size)
	}
	return encoded
}

type failingReader struct {
	err error
}

func (reader failingReader) Read([]byte) (int, error) {
	return 0, reader.err
}

var _ io.Reader = failingReader{}

type dataAndErrorReader struct {
	data []byte
	err  error
}

func (reader dataAndErrorReader) Read(buffer []byte) (int, error) {
	return copy(buffer, reader.data), reader.err
}

var _ io.Reader = dataAndErrorReader{}
