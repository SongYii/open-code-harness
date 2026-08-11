# Harness Domain Events and State Machine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the deterministic Go domain foundation in which validated commands produce typed events and recorded events reconstruct session/turn state with explicit terminal semantics.

**Architecture:** The package `internal/harness/domain` is a pure domain module with no ACP, MCP, model SDK, filesystem, clock, randomness, logging, or storage dependency. Decision functions return uncommitted typed events; application functions consume metadata-bearing recorded events; replay is a fold over an ordered event stream. Event JSON exists for fixtures and future persistence, but this milestone does not implement a production event store or executable Engine.

**Tech Stack:** Go 1.26, Go standard library, `testing`, JSON Lines fixtures, GitHub Actions.

## Global Constraints

- Module path is `github.com/SongYii/open-code-harness`.
- Minimum Go language version is `1.26`, the current stable major release at planning time.
- Use the Go standard library only in this milestone; `go.mod` has no third-party requirements.
- All domain code lives under `internal/harness/domain`; it is not a public client API.
- Domain decisions are pure: no `time.Now`, UUID/ULID generation, filesystem access, environment reads, network calls, or global mutable state.
- ACP v1 remains the future public client protocol, but this milestone imports no ACP types.
- MCP, Provider adapters, TUI, tools, approvals, items, persistence backends, and OpenTelemetry are outside this milestone.
- Session states are exactly `active` and `closed`.
- Turn states are exactly `running`, `completed`, `failed`, and `interrupted`.
- Terminal turn events are mutually exclusive; a terminal turn can never transition again.
- A session can have at most one running turn and cannot close while a turn is running.
- Recorded event sequence numbers start at `1` and must be contiguous per session.
- Recorded timestamps are supplied by the caller, normalized to UTC, and encoded as RFC3339 with nanosecond precision.
- Tests assert stable error codes rather than matching incidental error prose.
- Every task ends with `gofmt`, focused tests, full tests, and a small commit.

## Milestone Boundary

This plan implements only architecture design section 13, item 1: Harness domain, event model, and session/turn state machine. It also supplies an in-memory deterministic replay function so the domain can be verified. It deliberately leaves production append-only persistence and the executable Engine vertical slice to the next spec and plan.

## File Map

| Path | Responsibility |
|---|---|
| `go.mod` | Module identity and Go version floor |
| `internal/harness/domain/doc.go` | Package contract and dependency boundary |
| `internal/harness/domain/errors.go` | Stable domain error codes and typed errors |
| `internal/harness/domain/ids.go` | Strong session, turn, command, and event identifiers |
| `internal/harness/domain/state.go` | Session/turn state and immutable clone helpers |
| `internal/harness/domain/events.go` | Typed domain events and stable event names |
| `internal/harness/domain/record.go` | Event metadata and recorded/uncommitted envelopes |
| `internal/harness/domain/commands.go` | Typed domain commands and stable command names |
| `internal/harness/domain/decide.go` | Command validation and event decisions |
| `internal/harness/domain/apply.go` | Recorded event validation and state transitions |
| `internal/harness/domain/codec.go` | Versioned JSON encoding/decoding for recorded events |
| `internal/harness/domain/replay.go` | Deterministic ordered replay |
| `internal/harness/domain/*_test.go` | Focused behavioral tests |
| `internal/harness/domain/test_helpers_test.go` | Deterministic state/record builders shared by tests |
| `internal/harness/domain/testdata/session_lifecycle.jsonl` | Canonical cross-version replay fixture |
| `docs/architecture/domain-events.md` | State machines, invariants, and event catalog |
| `.github/workflows/ci.yml` | Formatting, vet, race, and test gates |

---

### Task 1: Bootstrap the Go Module, Strong IDs, and Stable Errors

**Files:**
- Create: `go.mod`
- Create: `internal/harness/domain/doc.go`
- Create: `internal/harness/domain/errors.go`
- Create: `internal/harness/domain/ids.go`
- Test: `internal/harness/domain/ids_test.go`

**Interfaces:**
- Produces: `SessionID`, `TurnID`, `CommandID`, `EventID`
- Produces: `ParseSessionID(string) (SessionID, error)`, `ParseTurnID(string) (TurnID, error)`, `ParseCommandID(string) (CommandID, error)`, `ParseEventID(string) (EventID, error)`
- Produces: `ErrorCode`, `DomainError`, `IsCode(error, ErrorCode) bool`

- [ ] **Step 1: Write the failing identifier and error tests**

```go
package domain

import "testing"

func TestParseSessionID(t *testing.T) {
	t.Parallel()

	got, err := ParseSessionID("session-1")
	if err != nil {
		t.Fatalf("ParseSessionID() error = %v", err)
	}
	if got != SessionID("session-1") {
		t.Fatalf("ParseSessionID() = %q", got)
	}
}

func TestParseSessionIDRejectsBlankOrPaddedValues(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"", "   ", " session-1", "session-1 "} {
		_, err := ParseSessionID(input)
		if !IsCode(err, CodeInvalidID) {
			t.Fatalf("ParseSessionID(%q) error = %v, want code %q", input, err, CodeInvalidID)
		}
	}
}
```

- [ ] **Step 2: Run the focused test and verify the expected failure**

Run: `go test ./internal/harness/domain -run 'TestParseSessionID' -count=1`

Expected: FAIL because `ParseSessionID`, `SessionID`, `IsCode`, and `CodeInvalidID` do not exist.

- [ ] **Step 3: Add the module and package contract**

```go
// go.mod
module github.com/SongYii/open-code-harness

go 1.26
```

```go
// internal/harness/domain/doc.go
// Package domain contains the deterministic state and transition rules for
// Open Code Harness sessions and turns. It must remain independent of
// transports, storage, model providers, tools, clocks, and ID generators.
package domain
```

- [ ] **Step 4: Implement stable errors and strong ID parsers**

```go
// errors.go
package domain

import "errors"

type ErrorCode string

const (
	CodeInvalidID            ErrorCode = "invalid_id"
	CodeInvalidCommand       ErrorCode = "invalid_command"
	CodeInvalidEvent         ErrorCode = "invalid_event"
	CodeSessionAlreadyExists ErrorCode = "session_already_exists"
	CodeSessionNotFound      ErrorCode = "session_not_found"
	CodeSessionClosed        ErrorCode = "session_closed"
	CodeTurnAlreadyRunning   ErrorCode = "turn_already_running"
	CodeTurnNotRunning       ErrorCode = "turn_not_running"
	CodeTurnMismatch         ErrorCode = "turn_mismatch"
	CodeTurnAlreadyExists    ErrorCode = "turn_already_exists"
	CodeSequenceMismatch     ErrorCode = "sequence_mismatch"
)

type DomainError struct {
	Code    ErrorCode
	Message string
}

func (e *DomainError) Error() string { return string(e.Code) + ": " + e.Message }

func IsCode(err error, code ErrorCode) bool {
	var target *DomainError
	return errors.As(err, &target) && target.Code == code
}
```

Implement each ID as its own string type. All four parsers use one unexported helper that rejects empty strings and values changed by `strings.TrimSpace`; do not normalize accepted IDs.

```go
type SessionID string
type TurnID string
type CommandID string
type EventID string

func ParseSessionID(value string) (SessionID, error) {
	if err := validateID(value); err != nil {
		return "", err
	}
	return SessionID(value), nil
}
```

- [ ] **Step 5: Format and run focused and full tests**

Run: `gofmt -w internal/harness/domain`

Run: `go test ./internal/harness/domain -run 'TestParseSessionID' -count=1`

Expected: PASS.

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the domain primitives**

```bash
git add go.mod internal/harness/domain/doc.go internal/harness/domain/errors.go internal/harness/domain/ids.go internal/harness/domain/ids_test.go
git commit -m "feat(domain): add identifiers and stable errors"
```

---

### Task 2: Define Recorded Events and Apply Session Creation

**Files:**
- Create: `internal/harness/domain/state.go`
- Create: `internal/harness/domain/events.go`
- Create: `internal/harness/domain/record.go`
- Create: `internal/harness/domain/apply.go`
- Test: `internal/harness/domain/apply_test.go`

**Interfaces:**
- Consumes: ID and error types from Task 1
- Produces: `Session`, `Turn`, `SessionStatus`, `TurnStatus`
- Produces: `Event`, `SessionCreated`, `TurnStarted`, `TurnCompleted`, `TurnFailed`, `TurnInterrupted`, `SessionClosed`
- Produces: `UncommittedEvent`, `RecordedEvent`, `Apply(Session, RecordedEvent) (Session, error)`

- [ ] **Step 1: Write the failing session creation tests**

```go
func TestApplySessionCreated(t *testing.T) {
	t.Parallel()

	record := RecordedEvent{
		SchemaVersion: 1,
		ID:            EventID("event-1"),
		CommandID:     CommandID("command-1"),
		SessionID:     SessionID("session-1"),
		Sequence:      1,
		OccurredAt:    time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC),
		Event:         SessionCreated{WorkspaceRoot: "/workspace"},
	}

	got, err := Apply(Session{}, record)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got.ID != SessionID("session-1") || got.Status != SessionStatusActive || got.Version != 1 {
		t.Fatalf("Apply() state = %#v", got)
	}
	if got.WorkspaceRoot != "/workspace" || len(got.Turns) != 0 {
		t.Fatalf("Apply() state = %#v", got)
	}
}

func TestApplyRejectsNonInitialSequence(t *testing.T) {
	t.Parallel()

	_, err := Apply(Session{}, RecordedEvent{
		SchemaVersion: 1,
		ID: EventID("event-2"), CommandID: CommandID("command-2"),
		SessionID: SessionID("session-1"), Sequence: 2,
		OccurredAt: time.Now(), Event: SessionCreated{WorkspaceRoot: "/workspace"},
	})
	if !IsCode(err, CodeSequenceMismatch) {
		t.Fatalf("Apply() error = %v, want code %q", err, CodeSequenceMismatch)
	}
}
```

The test may use `time.Now` to create input; production domain code may not call it.

- [ ] **Step 2: Run the focused test and verify the expected failure**

Run: `go test ./internal/harness/domain -run 'TestApply' -count=1`

Expected: FAIL because state, event, record, and `Apply` types do not exist.

- [ ] **Step 3: Define state and event types**

```go
type SessionStatus string
const (
	SessionStatusActive SessionStatus = "active"
	SessionStatusClosed SessionStatus = "closed"
)

type TurnStatus string
const (
	TurnStatusRunning     TurnStatus = "running"
	TurnStatusCompleted   TurnStatus = "completed"
	TurnStatusFailed      TurnStatus = "failed"
	TurnStatusInterrupted TurnStatus = "interrupted"
)

type Turn struct {
	ID           TurnID
	Status       TurnStatus
	Input        string
	StartedAt    time.Time
	EndedAt      time.Time
	FailureCode  string
	FailureText  string
	InterruptWhy string
}

type Session struct {
	ID             SessionID
	Status         SessionStatus
	Version        uint64
	WorkspaceRoot  string
	ActiveTurnID   TurnID
	TurnOrder      []TurnID
	Turns          map[TurnID]Turn
}

func (s Session) Exists() bool { return s.ID != "" }
```

Define the event interface and stable names:

```go
type Event interface { EventType() string }

const (
	EventSessionCreated  = "session.created"
	EventTurnStarted     = "turn.started"
	EventTurnCompleted   = "turn.completed"
	EventTurnFailed      = "turn.failed"
	EventTurnInterrupted = "turn.interrupted"
	EventSessionClosed   = "session.closed"
)

type SessionCreated struct { WorkspaceRoot string `json:"workspaceRoot"` }
func (SessionCreated) EventType() string { return EventSessionCreated }
```

Give every remaining event a concrete payload and `EventType` method. Terminal events carry `TurnID`; `TurnFailed` additionally carries `Code` and `Message`; `TurnInterrupted` carries `Reason`; `TurnStarted` carries `TurnID` and `Input`; `SessionClosed` has no fields.

- [ ] **Step 4: Define event envelopes and minimal apply logic**

```go
type UncommittedEvent struct { Event Event }

type RecordedEvent struct {
	SchemaVersion int
	ID            EventID
	CommandID     CommandID
	SessionID     SessionID
	Sequence      uint64
	OccurredAt    time.Time
	Event         Event
}
```

`Apply` first validates schema version `1`, non-empty IDs, a non-zero timestamp, UTC-normalizes the supplied timestamp, and requires `Sequence == state.Version+1`. For `SessionCreated`, require an empty state and non-empty workspace root, initialize maps/slices, set `Status=SessionStatusActive`, and set `Version=Sequence`. Unknown events return `CodeInvalidEvent`.

- [ ] **Step 5: Format and run tests**

Run: `gofmt -w internal/harness/domain`

Run: `go test ./internal/harness/domain -run 'TestApply' -count=1`

Expected: PASS.

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 6: Commit recorded event application**

```bash
git add internal/harness/domain/state.go internal/harness/domain/events.go internal/harness/domain/record.go internal/harness/domain/apply.go internal/harness/domain/apply_test.go
git commit -m "feat(domain): apply recorded session events"
```

---

### Task 3: Decide Session Creation and Turn Start Commands

**Files:**
- Create: `internal/harness/domain/commands.go`
- Create: `internal/harness/domain/decide.go`
- Create: `internal/harness/domain/test_helpers_test.go`
- Modify: `internal/harness/domain/apply.go`
- Test: `internal/harness/domain/decide_test.go`
- Test: `internal/harness/domain/apply_test.go`

**Interfaces:**
- Consumes: `Session`, `Event`, and stable errors
- Produces: `Command`, `CreateSession`, `StartTurn`, `CompleteTurn`, `FailTurn`, `InterruptTurn`, `CloseSession`
- Produces: `Decide(Session, Command) ([]UncommittedEvent, error)`

- [ ] **Step 1: Write failing decision tests for create and start**

```go
func TestDecideCreateSession(t *testing.T) {
	t.Parallel()

	events, err := Decide(Session{}, CreateSession{
		SessionID: SessionID("session-1"), WorkspaceRoot: "/workspace",
	})
	if err != nil { t.Fatalf("Decide() error = %v", err) }
	want := []UncommittedEvent{{Event: SessionCreated{WorkspaceRoot: "/workspace"}}}
	if !reflect.DeepEqual(events, want) { t.Fatalf("Decide() = %#v, want %#v", events, want) }
}

func TestDecideStartTurnRejectsBlankInput(t *testing.T) {
	t.Parallel()

	state := activeSessionForTest(t)
	_, err := Decide(state, StartTurn{
		SessionID: state.ID, TurnID: TurnID("turn-1"), Input: "  ",
	})
	if !IsCode(err, CodeInvalidCommand) {
		t.Fatalf("Decide() error = %v, want code %q", err, CodeInvalidCommand)
	}
}
```

Add table cases for session ID mismatch, duplicate create, closed session, duplicate turn ID, and a second active turn.

- [ ] **Step 2: Run the decision tests and verify the expected failure**

Run: `go test ./internal/harness/domain -run 'TestDecide(CreateSession|StartTurn)' -count=1`

Expected: FAIL because command and decision types do not exist.

- [ ] **Step 3: Define commands and stable names**

```go
type Command interface {
	CommandType() string
	TargetSessionID() SessionID
}

const (
	CommandCreateSession = "session.create"
	CommandStartTurn     = "turn.start"
	CommandCompleteTurn  = "turn.complete"
	CommandFailTurn      = "turn.fail"
	CommandInterruptTurn = "turn.interrupt"
	CommandCloseSession  = "session.close"
)

type CreateSession struct { SessionID SessionID; WorkspaceRoot string }
type StartTurn struct { SessionID SessionID; TurnID TurnID; Input string }
type CompleteTurn struct { SessionID SessionID; TurnID TurnID }
type FailTurn struct { SessionID SessionID; TurnID TurnID; Code string; Message string }
type InterruptTurn struct { SessionID SessionID; TurnID TurnID; Reason string }
type CloseSession struct { SessionID SessionID }
```

Implement both interface methods on every command without changing user input text.

- [ ] **Step 4: Implement create/start decisions and turn application**

`Decide` dispatches on concrete command type. `CreateSession` requires an empty state, valid IDs, and non-blank workspace root. `StartTurn` requires an existing active session, matching session ID, valid unused turn ID, non-blank input, and no active turn.

`Apply` handles `TurnStarted` by cloning `Turns` and `TurnOrder`, inserting a running turn with `StartedAt=record.OccurredAt`, setting `ActiveTurnID`, appending the ID to `TurnOrder`, and advancing `Version`. Never mutate the input state's map or slice.

- [ ] **Step 5: Add deterministic test helpers**

```go
// test_helpers_test.go
package domain

import (
	"fmt"
	"testing"
	"time"
)

func recordedForTest(state Session, event Event) RecordedEvent {
	sequence := state.Version + 1
	return RecordedEvent{
		SchemaVersion: 1,
		ID:            EventID(fmt.Sprintf("event-%d", sequence)),
		CommandID:     CommandID(fmt.Sprintf("command-%d", sequence)),
		SessionID:     SessionID("session-1"),
		Sequence:      sequence,
		OccurredAt:    time.Date(2026, 8, 11, 0, 0, int(sequence), 0, time.UTC),
		Event:         event,
	}
}

func activeSessionForTest(t *testing.T) Session {
	t.Helper()
	state, err := Apply(Session{}, recordedForTest(Session{}, SessionCreated{WorkspaceRoot: "/workspace"}))
	if err != nil { t.Fatalf("create test session: %v", err) }
	return state
}

func runningTurnForTest(t *testing.T) Session {
	t.Helper()
	state := activeSessionForTest(t)
	state, err := Apply(state, recordedForTest(state, TurnStarted{TurnID: TurnID("turn-1"), Input: "inspect repository"}))
	if err != nil { t.Fatalf("start test turn: %v", err) }
	return state
}
```

- [ ] **Step 6: Add an immutability regression test**

```go
func TestApplyTurnStartedDoesNotMutateInputState(t *testing.T) {
	state := activeSessionForTest(t)
	before := state.Clone()

	_, err := Apply(state, recordedForTest(state, TurnStarted{
		TurnID: TurnID("turn-1"), Input: "inspect repository",
	}))
	if err != nil { t.Fatalf("Apply() error = %v", err) }
	if !reflect.DeepEqual(state, before) {
		t.Fatalf("Apply() mutated input: got %#v want %#v", state, before)
	}
}
```

Expose `Session.Clone() Session` for tests and later read models; it must copy both the map and `TurnOrder` slice.

- [ ] **Step 7: Format and run focused and full tests**

Run: `gofmt -w internal/harness/domain`

Run: `go test ./internal/harness/domain -run 'TestDecide(CreateSession|StartTurn)|TestApplyTurnStarted' -count=1`

Expected: PASS.

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 8: Commit create/start decisions**

```bash
git add internal/harness/domain/commands.go internal/harness/domain/decide.go internal/harness/domain/decide_test.go internal/harness/domain/apply.go internal/harness/domain/apply_test.go internal/harness/domain/state.go internal/harness/domain/test_helpers_test.go
git commit -m "feat(domain): decide session and turn start"
```

---

### Task 4: Enforce Mutually Exclusive Turn Terminal States

**Files:**
- Modify: `internal/harness/domain/decide.go`
- Modify: `internal/harness/domain/apply.go`
- Test: `internal/harness/domain/decide_test.go`
- Test: `internal/harness/domain/apply_test.go`

**Interfaces:**
- Consumes: terminal commands/events defined in Tasks 2-3
- Produces: complete, fail, and interrupt transitions with stable invariants

- [ ] **Step 1: Write failing terminal transition tests**

```go
func TestTerminalTurnTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cmd  Command
		want Event
	}{
		{"complete", CompleteTurn{SessionID: SessionID("session-1"), TurnID: TurnID("turn-1")}, TurnCompleted{TurnID: TurnID("turn-1")}},
		{"fail", FailTurn{SessionID: SessionID("session-1"), TurnID: TurnID("turn-1"), Code: "provider_rate_limit", Message: "retry budget exhausted"}, TurnFailed{TurnID: TurnID("turn-1"), Code: "provider_rate_limit", Message: "retry budget exhausted"}},
		{"interrupt", InterruptTurn{SessionID: SessionID("session-1"), TurnID: TurnID("turn-1"), Reason: "user_cancelled"}, TurnInterrupted{TurnID: TurnID("turn-1"), Reason: "user_cancelled"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := runningTurnForTest(t)
			events, err := Decide(state, tt.cmd)
			if err != nil { t.Fatalf("Decide() error = %v", err) }
			if len(events) != 1 || !reflect.DeepEqual(events[0].Event, tt.want) {
				t.Fatalf("Decide() = %#v, want %#v", events, tt.want)
			}
		})
	}
}
```

Add negative cases: wrong turn ID returns `CodeTurnMismatch`; no running turn returns `CodeTurnNotRunning`; blank failure code/message and blank interrupt reason return `CodeInvalidCommand`.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./internal/harness/domain -run 'TestTerminalTurnTransitions|TestDecideTerminal' -count=1`

Expected: FAIL because terminal decisions/application are incomplete.

- [ ] **Step 3: Implement terminal decisions**

Use one helper:

```go
func requireRunningTurn(state Session, sessionID SessionID, turnID TurnID) (Turn, error)
```

It validates session existence/status, session match, active turn presence, and exact active turn identity. Each terminal command returns exactly one corresponding event.

- [ ] **Step 4: Implement terminal event application**

For each terminal event, clone state, replace the active turn value in the cloned map, set `EndedAt=record.OccurredAt`, populate only the relevant failure/interruption fields, clear `ActiveTurnID`, and advance `Version`. Applying any terminal event without the matching running turn returns a stable domain error and does not mutate the input.

- [ ] **Step 5: Add the mutual-exclusion replay test**

Apply `TurnCompleted`, then attempt `TurnFailed` and `TurnInterrupted` for the same turn at the next sequence. Both must return `CodeTurnNotRunning`, and the completed state's version and turn status must remain unchanged.

- [ ] **Step 6: Format and run tests**

Run: `gofmt -w internal/harness/domain`

Run: `go test ./internal/harness/domain -run 'TestTerminal|TestDecideTerminal|TestApplyTerminal' -count=1`

Expected: PASS.

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 7: Commit terminal states**

```bash
git add internal/harness/domain/decide.go internal/harness/domain/apply.go internal/harness/domain/decide_test.go internal/harness/domain/apply_test.go
git commit -m "feat(domain): enforce turn terminal states"
```

---

### Task 5: Close Sessions Only at a Safe Boundary

**Files:**
- Modify: `internal/harness/domain/decide.go`
- Modify: `internal/harness/domain/apply.go`
- Test: `internal/harness/domain/decide_test.go`
- Test: `internal/harness/domain/apply_test.go`

**Interfaces:**
- Consumes: `CloseSession`, `SessionClosed`
- Produces: close semantics that reject active turns and all later commands

- [ ] **Step 1: Write failing close-session tests**

Cover these exact cases:

```text
active session without running turn + CloseSession -> SessionClosed
active session with running turn + CloseSession -> turn_already_running
closed session + StartTurn -> session_closed
closed session + CloseSession -> session_closed
```

For the successful case, apply the event and assert `Status=closed`, empty `ActiveTurnID`, and incremented version.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./internal/harness/domain -run 'Test.*CloseSession' -count=1`

Expected: FAIL because close behavior is incomplete.

- [ ] **Step 3: Implement close decision and application**

`Decide(CloseSession)` checks an existing matching active session and rejects a non-empty `ActiveTurnID` with `CodeTurnAlreadyRunning`. It returns one `SessionClosed` event. `Apply(SessionClosed)` performs the same invariant checks defensively, sets the status, and advances the version.

- [ ] **Step 4: Add a table test proving every non-create command rejects a closed session**

The table includes `StartTurn`, `CompleteTurn`, `FailTurn`, `InterruptTurn`, and `CloseSession`; each result must satisfy `IsCode(err, CodeSessionClosed)`.

- [ ] **Step 5: Format and run tests**

Run: `gofmt -w internal/harness/domain`

Run: `go test ./internal/harness/domain -run 'Test.*CloseSession|TestClosedSession' -count=1`

Expected: PASS.

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 6: Commit close semantics**

```bash
git add internal/harness/domain/decide.go internal/harness/domain/apply.go internal/harness/domain/decide_test.go internal/harness/domain/apply_test.go
git commit -m "feat(domain): close sessions at safe boundaries"
```

---

### Task 6: Add a Versioned Recorded-Event JSON Codec

**Files:**
- Create: `internal/harness/domain/codec.go`
- Create: `internal/harness/domain/codec_test.go`
- Create: `internal/harness/domain/testdata/session_lifecycle.jsonl`

**Interfaces:**
- Consumes: `RecordedEvent` and all event payloads
- Produces: `MarshalRecordedEvent(RecordedEvent) ([]byte, error)`
- Produces: `UnmarshalRecordedEvent([]byte) (RecordedEvent, error)`

- [ ] **Step 1: Write the failing canonical JSON test**

```go
func TestRecordedEventJSONRoundTrip(t *testing.T) {
	t.Parallel()

	record := RecordedEvent{
		SchemaVersion: 1,
		ID: EventID("event-1"), CommandID: CommandID("command-1"),
		SessionID: SessionID("session-1"), Sequence: 1,
		OccurredAt: time.Date(2026, 8, 11, 1, 2, 3, 456000000, time.FixedZone("offset", 8*60*60)),
		Event: SessionCreated{WorkspaceRoot: "/workspace"},
	}

	encoded, err := MarshalRecordedEvent(record)
	if err != nil { t.Fatalf("MarshalRecordedEvent() error = %v", err) }
	want := `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.created","data":{"workspaceRoot":"/workspace"}}`
	if string(encoded) != want { t.Fatalf("encoded = %s\nwant = %s", encoded, want) }

	decoded, err := UnmarshalRecordedEvent(encoded)
	if err != nil { t.Fatalf("UnmarshalRecordedEvent() error = %v", err) }
	record.OccurredAt = record.OccurredAt.UTC()
	if !reflect.DeepEqual(decoded, record) {
		t.Fatalf("decoded = %#v", decoded)
	}
}
```

- [ ] **Step 2: Run the codec test and verify failure**

Run: `go test ./internal/harness/domain -run 'TestRecordedEventJSON' -count=1`

Expected: FAIL because codec functions do not exist.

- [ ] **Step 3: Implement explicit wire encoding**

Use an unexported wire envelope so field names/order are deliberate:

```go
type recordedEventWire struct {
	SchemaVersion int             `json:"schemaVersion"`
	ID            EventID         `json:"id"`
	CommandID     CommandID       `json:"commandId"`
	SessionID     SessionID       `json:"sessionId"`
	Sequence      uint64          `json:"sequence"`
	OccurredAt    string          `json:"occurredAt"`
	Type          string          `json:"type"`
	Data          json.RawMessage `json:"data"`
}
```

Marshal event payloads with a closed type switch. Unmarshal with a closed switch on the stable event name and `json.Decoder.DisallowUnknownFields()` for both envelope and payload. Reject unknown schema versions, event names, missing IDs, sequence `0`, invalid timestamps, and trailing JSON with `CodeInvalidEvent`. Normalize timestamps to `UTC()` and format with `time.RFC3339Nano`.

- [ ] **Step 4: Add invalid-wire table tests**

Include unknown top-level field, unknown payload field, schema version `2`, unknown event type, zero sequence, padded ID, invalid timestamp, and a second JSON value after the envelope. Every case must return `CodeInvalidEvent`.

- [ ] **Step 5: Add the canonical lifecycle JSONL fixture**

Create six one-line records with contiguous sequence numbers and fixed UTC timestamps:

```text
session.created
turn.started
turn.completed
turn.started
turn.interrupted
session.closed
```

Use session `session-fixture`, turns `turn-1` and `turn-2`, commands/events numbered consistently, workspace `/workspace`, and user interruption reason `user_cancelled`.

- [ ] **Step 6: Format and run tests**

Run: `gofmt -w internal/harness/domain`

Run: `go test ./internal/harness/domain -run 'TestRecordedEventJSON|TestUnmarshalRecordedEvent' -count=1`

Expected: PASS.

Run: `go test ./... -count=1`

Expected: PASS.

- [ ] **Step 7: Commit the event codec and fixture**

```bash
git add internal/harness/domain/codec.go internal/harness/domain/codec_test.go internal/harness/domain/testdata/session_lifecycle.jsonl
git commit -m "feat(domain): add versioned event codec"
```

---

### Task 7: Prove Deterministic Replay and Reject Corrupt Streams

**Files:**
- Create: `internal/harness/domain/replay.go`
- Create: `internal/harness/domain/replay_test.go`
- Modify: `internal/harness/domain/testdata/session_lifecycle.jsonl` only if a fixture defect is found

**Interfaces:**
- Consumes: ordered `[]RecordedEvent`, `UnmarshalRecordedEvent`, `Apply`
- Produces: `Replay([]RecordedEvent) (Session, error)`
- Produces: `DecodeJSONL(io.Reader) ([]RecordedEvent, error)`

- [ ] **Step 1: Write the failing fixture replay test**

```go
func TestReplayFixtureIsDeterministic(t *testing.T) {
	data, err := os.Open("testdata/session_lifecycle.jsonl")
	if err != nil { t.Fatal(err) }
	defer data.Close()

	records, err := DecodeJSONL(data)
	if err != nil { t.Fatalf("DecodeJSONL() error = %v", err) }
	first, err := Replay(records)
	if err != nil { t.Fatalf("Replay() error = %v", err) }
	second, err := Replay(records)
	if err != nil { t.Fatalf("Replay() second error = %v", err) }
	if !reflect.DeepEqual(first, second) { t.Fatalf("replays differ") }
	if first.Status != SessionStatusClosed || first.Version != 6 || len(first.TurnOrder) != 2 {
		t.Fatalf("Replay() = %#v", first)
	}
	if first.Turns[TurnID("turn-1")].Status != TurnStatusCompleted || first.Turns[TurnID("turn-2")].Status != TurnStatusInterrupted {
		t.Fatalf("Replay() turns = %#v", first.Turns)
	}
}
```

- [ ] **Step 2: Run the replay test and verify failure**

Run: `go test ./internal/harness/domain -run 'TestReplayFixture' -count=1`

Expected: FAIL because replay functions do not exist.

- [ ] **Step 3: Implement JSONL decoding and replay**

`DecodeJSONL` uses `bufio.Scanner`, raises its buffer limit to 1 MiB, skips no blank lines, reports the 1-based line number on errors, and returns `CodeInvalidEvent` for an empty stream. `Replay` starts with `Session{}` and folds records through `Apply`; it returns the first error with sequence context.

Do not sort records. Out-of-order input must fail through the sequence invariant rather than being silently repaired.

- [ ] **Step 4: Add corrupt stream tests**

Cover an empty stream, blank JSONL line, missing sequence `2`, duplicated sequence `1`, a session ID change at sequence `2`, an event before `session.created`, a second `session.created`, and an event after `session.closed`. Assert stable codes and confirm no partial state is returned on error.

- [ ] **Step 5: Add a race-safe parallel replay test**

Launch 32 goroutines replaying the same immutable record slice. Collect returned states through a channel and assert all equal the first state. The implementation must not mutate records or shared payloads.

- [ ] **Step 6: Format and run focused, full, and race tests**

Run: `gofmt -w internal/harness/domain`

Run: `go test ./internal/harness/domain -run 'TestReplay|TestDecodeJSONL' -count=1`

Expected: PASS.

Run: `go test ./... -count=1`

Expected: PASS.

Run: `go test -race ./... -count=1`

Expected: PASS with no race report.

- [ ] **Step 7: Commit deterministic replay**

```bash
git add internal/harness/domain/replay.go internal/harness/domain/replay_test.go internal/harness/domain/testdata/session_lifecycle.jsonl
git commit -m "feat(domain): add deterministic event replay"
```

---

### Task 8: Document the Domain Contract and Enforce It in CI

**Files:**
- Create: `docs/architecture/domain-events.md`
- Create: `.github/workflows/ci.yml`
- Modify: `README.md`

**Interfaces:**
- Consumes: all implemented event names, commands, state rules, and verification commands
- Produces: contributor-facing contract and automated merge gates

- [ ] **Step 1: Write the domain contract document**

Document these exact state machines:

```text
nonexistent --session.created--> active --session.closed--> closed

absent --turn.started--> running --turn.completed----> completed
                              |--turn.failed---------> failed
                              `--turn.interrupted----> interrupted
```

List the six event names, six command names, stable error codes, one-active-turn invariant, contiguous-sequence invariant, timestamp rule, immutability rule, and explicit milestone exclusions. State that internal events are not ACP messages and are not a public compatibility promise before v1.0.

- [ ] **Step 2: Add the CI workflow**

```yaml
name: ci

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

jobs:
  go:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: true
      - name: Check formatting
        run: test -z "$(gofmt -l .)"
      - name: Vet
        run: go vet ./...
      - name: Test with race detector
        run: go test -race ./... -count=1
```

- [ ] **Step 3: Add README development commands**

Append a `Development` section linking `docs/architecture/domain-events.md` and listing:

```bash
gofmt -w .
go vet ./...
go test -race ./... -count=1
```

- [ ] **Step 4: Run the full local quality gate**

Run: `test -z "$(gofmt -l .)"`

Expected: exit 0 with no output.

Run: `go vet ./...`

Expected: exit 0 with no output.

Run: `go test -race ./... -count=1`

Expected: PASS.

- [ ] **Step 5: Verify repository scope**

Run: `git status --short`

Expected: only `README.md`, `.github/workflows/ci.yml`, and `docs/architecture/domain-events.md` are uncommitted in this task.

Run: `rg -n 'ACP|MCP|provider|tui' internal/harness/domain`

Expected: no imports or implementation dependencies; mentions are allowed only in the package boundary comment if retained.

- [ ] **Step 6: Commit documentation and CI**

```bash
git add README.md docs/architecture/domain-events.md .github/workflows/ci.yml
git commit -m "ci: enforce domain quality gates"
```

## Final Verification

After Task 8, run all commands from a clean checkout:

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test ./... -count=1
go test -race ./... -count=1
git status --short
```

Expected results:

- formatting check exits `0` with no paths;
- `go vet` exits `0`;
- normal and race test suites pass;
- `git status --short` prints nothing;
- replaying `testdata/session_lifecycle.jsonl` twice yields equal closed session states at version `6`;
- no implementation file imports ACP, MCP, a model SDK, a TUI package, a persistence backend, or a third-party module.

## Completion Evidence

The milestone is complete only when its handoff includes:

- the final commit hash;
- exact output summaries for formatting, vet, normal tests, and race tests;
- the lifecycle fixture path;
- the stable command/event/error catalog path;
- confirmation that `go.mod` contains no third-party requirements;
- any deliberate deviation from this plan recorded in an ADR or updated design document.
