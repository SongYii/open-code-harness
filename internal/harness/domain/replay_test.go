package domain

import (
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
	first, err := Replay(records)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	second, err := Replay(records)
	if err != nil {
		t.Fatalf("Replay() second error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replays differ")
	}
	if first.Status != SessionStatusClosed || first.Version != 6 || len(first.TurnOrder) != 2 {
		t.Fatalf("Replay() = %#v", first)
	}
	if first.Turns[TurnID("turn-1")].Status != TurnStatusCompleted || first.Turns[TurnID("turn-2")].Status != TurnStatusInterrupted {
		t.Fatalf("Replay() turns = %#v", first.Turns)
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

	for _, input := range []string{string(exactlyOneMiB), string(exactlyOneMiB) + "\n"} {
		records, err := DecodeJSONL(strings.NewReader(input))
		if err != nil {
			t.Fatalf("DecodeJSONL() exact %d-byte record error = %v", maxJSONLRecordSize, err)
		}
		if len(records) != 1 {
			t.Fatalf("DecodeJSONL() exact %d-byte record count = %d, want 1", maxJSONLRecordSize, len(records))
		}
	}

	_, err := DecodeJSONL(strings.NewReader(string(jsonlRecordOfSize(t, maxJSONLRecordSize+1))))
	if !IsCode(err, CodeInvalidEvent) {
		t.Fatalf("DecodeJSONL() over-limit record error = %v, want code %q", err, CodeInvalidEvent)
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
			state, err := Replay(test.records)
			if !IsCode(err, test.code) {
				t.Fatalf("Replay() error = %v, want code %q", err, test.code)
			}
			if !reflect.DeepEqual(state, Session{}) {
				t.Fatalf("Replay() state = %#v, want zero Session on error", state)
			}
		})
	}
}

func TestReplayDoesNotMutateImmutableRecordsDuringParallelUse(t *testing.T) {
	records := replayFixtureRecords(t)
	before := append([]RecordedEvent(nil), records...)

	const workers = 32
	type result struct {
		state Session
		err   error
	}
	results := make(chan result, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			state, err := Replay(records)
			results <- result{state: state, err: err}
		}()
	}
	group.Wait()
	close(results)

	var first Session
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
