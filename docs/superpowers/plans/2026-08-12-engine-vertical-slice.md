# Industrial Engine Vertical Slice Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first industrial-grade executable Go Turn path: deterministic model streaming enters the existing event-sourced domain through one application authority, produces a durable assistant message, and terminates with bounded, replayable, concurrency-safe semantics.

**Architecture:** `internal/harness/application` owns commands, replay, optimistic concurrency, and durable append; `internal/harness/engine` owns provider-neutral bounded stream consumption; `internal/harness/domain` remains the pure source of lifecycle truth. `MemoryEventStore`, `ScriptedModel`, deterministic clocks/IDs, and recording sinks implement the same formal ports future production adapters will use, so offline tests exercise the real path rather than a demo branch.

**Tech Stack:** Go 1.26, Go standard library only, `testing`, JSON Lines fixtures, GitHub Actions race detector.

## Global Constraints

- Module path remains `github.com/SongYii/open-code-harness`; minimum Go language version remains `1.26`.
- Add no third-party dependency in this milestone.
- This is a pre-v0 industrial-quality slice, not a tutorial, prototype, or claim of general availability.
- Dependency direction is `adapters/testkit → application → engine + domain`; `domain` imports neither `application` nor `engine`, and `engine` imports no concrete adapter, provider SDK, ACP, TUI, or testkit package.
- The application service is the only command authority. No adapter may manufacture durable lifecycle events after invoking a model independently.
- `RunTurn` is synchronous. The core creates no unbounded channel and no detached goroutine.
- `DefaultMaxAssistantBytes` is exactly `1 << 20` (1 MiB); configuration must be positive and the limit is checked before accepting a delta.
- `DefaultTerminalCommitTimeout` is exactly `5 * time.Second`; cancellation cleanup uses `context.WithoutCancel` plus this timeout and never runs in a detached goroutine.
- Model text must be valid UTF-8 and accepted bytes are never trimmed, normalized, or rewritten.
- Production code does not automatically log model input/output, raw provider payloads, or secrets.
- Runtime deltas are transient. Only the final successful assistant text, stable failure/interruption data, and Turn/Item lifecycle facts are durable.
- An assistant Item belongs to one Turn; this milestone permits at most one running Item and one running Turn per Session.
- Item and Turn terminal events commit in one atomic EventStore append batch.
- EventStore compare-and-swap is authoritative. A conflict is returned to the caller and is never retried automatically.
- Every append batch is all-or-nothing; cancellation before commit and injected pre-commit failure write nothing.
- `ScriptedModel` and `MemoryEventStore` implement formal production ports. Production code contains no scripted/test-mode branch.
- Concurrency tests use barriers or channels, never timing sleeps.
- Tests assert stable typed codes/categories, not incidental error prose.
- Every task finishes with formatting, focused tests, the full test suite, and one small commit.

## Milestone Boundary

This plan implements the approved `2026-08-12-engine-vertical-slice-design.md`: assistant-message Item lifecycle, formal EventStore/Model/RuntimeSink ports, deterministic adapters, application service, bounded model-only Turn execution, failure/cancellation semantics, replay, and verification evidence. It does not add a real provider, retry policy, production persistence, crash reconciliation, tools, Tool Runtime, Policy, approvals, ACP, TUI, MCP, Context Engine, OpenTelemetry, or subagents.

## File Map

| Path | Responsibility |
| --- | --- |
| `internal/harness/domain/ids.go` | Add strong `ItemID` parsing |
| `internal/harness/domain/state.go` | Assistant Item state and deep immutable clone |
| `internal/harness/domain/events.go` | Stable assistant-message event catalog |
| `internal/harness/domain/commands.go` | Start Item and atomic Item/Turn terminal commands |
| `internal/harness/domain/decide.go` | Item/Turn command invariants and event decisions |
| `internal/harness/domain/apply.go` | Apply Item lifecycle and prevent Turn termination with a running Item |
| `internal/harness/domain/codec.go` | Strict schema-v1 JSON for new events |
| `internal/harness/domain/record.go` | Defensive event/record copying used by stores |
| `internal/harness/domain/*_test.go` | Item lifecycle, immutability, codec, and replay tests |
| `internal/harness/domain/testdata/assistant_lifecycle.jsonl` | Canonical assistant lifecycle replay fixture |
| `internal/harness/application/doc.go` | Application authority contract |
| `internal/harness/application/ports.go` | EventStore, Clock, IDGenerator, and append request |
| `internal/harness/application/errors.go` | Conflict and stable application error taxonomy |
| `internal/harness/application/append.go` | Decide/append/apply helper with no retry |
| `internal/harness/application/service.go` | Service configuration and dependency validation |
| `internal/harness/application/session.go` | CreateSession, LoadSession, and CloseSession use cases |
| `internal/harness/application/turn.go` | RunTurn orchestration and terminalization |
| `internal/harness/application/eventstoretest/suite.go` | Reusable EventStore contract suite |
| `internal/harness/application/enginescenariotest/suite.go` | Reusable executable-path scenario suite |
| `internal/harness/engine/doc.go` | Provider-neutral Engine boundary contract |
| `internal/harness/engine/model.go` | Model, ModelStream, request, and stream event types |
| `internal/harness/engine/runtime.go` | RuntimeSink, correlation envelope, and monotonic emitter |
| `internal/harness/engine/errors.go` | Stable runner error codes |
| `internal/harness/engine/runner.go` | Synchronous bounded stream consumer |
| `internal/harness/engine/modeltest/suite.go` | Reusable Model contract suite |
| `internal/harness/adapters/memory/event_store.go` | Mutex-protected CAS event store and fault injection |
| `internal/harness/testkit/clock.go` | Fixed deterministic clock |
| `internal/harness/testkit/ids.go` | Concurrent-safe deterministic IDs |
| `internal/harness/testkit/scripted_model.go` | Formal deterministic Model adapter |
| `internal/harness/testkit/recording_sink.go` | Formal deterministic RuntimeSink adapter |
| `internal/harness/application/scenario_test.go` | Real-boundary success/failure/replay scenarios |
| `internal/harness/architecture/dependencies_test.go` | Package import boundary gate |
| `internal/harness/application/testdata/run_turn_success.jsonl` | Exact successful execution trace fixture |
| `docs/architecture/domain-events.md` | Updated domain catalog and invariants |
| `docs/architecture/engine-vertical-slice.md` | Implemented Engine/application contract and evidence commands |
| `docs/README.md` | Authority and milestone status after implementation |

---

### Task 1: Add the Assistant Item Recorded Lifecycle

**Files:**
- Modify: `internal/harness/domain/ids.go`
- Modify: `internal/harness/domain/ids_test.go`
- Modify: `internal/harness/domain/state.go`
- Modify: `internal/harness/domain/events.go`
- Modify: `internal/harness/domain/record.go`
- Modify: `internal/harness/domain/apply.go`
- Modify: `internal/harness/domain/apply_test.go`
- Modify: `internal/harness/domain/codec.go`
- Modify: `internal/harness/domain/codec_test.go`

**Interfaces:**
- Produces: `ItemID`, `ParseItemID(string) (ItemID, error)`
- Produces: `ItemStatus`, `ItemKind`, `Item`, and nested Item collections on `Turn`
- Produces: `AssistantMessageStarted`, `AssistantMessageCompleted`, `AssistantMessageFailed`, `AssistantMessageInterrupted`
- Produces: `CloneEvent(Event) (Event, error)` and `CloneRecordedEvents([]RecordedEvent) ([]RecordedEvent, error)`

- [ ] **Step 1: Write failing Item ID, apply, immutability, and codec tests**

Add table tests for blank, padded, and invalid UTF-8 Item IDs. Add this representative transition test and equivalent failed/interrupted cases:

```go
func TestApplyAssistantMessageLifecycle(t *testing.T) {
	state := runningTurnForTest(t)
	started := recordedForTest(state, AssistantMessageStarted{
		TurnID: "turn-1", ItemID: "item-1",
	})
	state, err := Apply(state, started)
	if err != nil { t.Fatalf("start item: %v", err) }

	completed := recordedForTest(state, AssistantMessageCompleted{
		TurnID: "turn-1", ItemID: "item-1", Text: "你好, exact bytes\n",
	})
	state, err = Apply(state, completed)
	if err != nil { t.Fatalf("complete item: %v", err) }

	item := state.Turns["turn-1"].Items["item-1"]
	if item.Status != ItemStatusCompleted || item.Text != "你好, exact bytes\n" {
		t.Fatalf("item = %#v", item)
	}
}
```

Also prove that mutating `Turn.ItemOrder`, `Turn.Items`, a cloned `Session`, or records returned by `CloneRecordedEvents` cannot mutate the source. Add strict JSON round trips and unknown/missing/duplicate-field rejection for all four new event types.

- [ ] **Step 2: Run focused tests and verify they fail for missing Item types**

Run: `go test ./internal/harness/domain -run 'Test(ParseItemID|ApplyAssistant|CloneRecorded|AssistantMessage.*JSON)' -count=1`

Expected: FAIL because the new ID, state, event, clone, and codec contracts do not exist.

- [ ] **Step 3: Add exact Item state and event types**

```go
type ItemID string

type ItemKind string
const ItemKindAssistantMessage ItemKind = "assistant_message"

type ItemStatus string
const (
	ItemStatusRunning     ItemStatus = "running"
	ItemStatusCompleted   ItemStatus = "completed"
	ItemStatusFailed      ItemStatus = "failed"
	ItemStatusInterrupted ItemStatus = "interrupted"
)

type Item struct {
	ID           ItemID
	TurnID       TurnID
	Kind         ItemKind
	Status       ItemStatus
	Text         string
	StartedAt    time.Time
	EndedAt      time.Time
	FailureCode  string
	FailureText  string
	InterruptWhy string
}
```

Extend `Turn` with `ActiveItemID ItemID`, `ItemOrder []ItemID`, and `Items map[ItemID]Item`. Add `Turn.Clone()` and make `Session.Clone()` deep-copy every Turn and its Item containers.

Use these stable event names and value payloads:

```go
const (
	EventAssistantMessageStarted     = "assistant.message.started"
	EventAssistantMessageCompleted   = "assistant.message.completed"
	EventAssistantMessageFailed      = "assistant.message.failed"
	EventAssistantMessageInterrupted = "assistant.message.interrupted"
)

type AssistantMessageStarted struct { TurnID TurnID `json:"turnID"`; ItemID ItemID `json:"itemID"` }
type AssistantMessageCompleted struct { TurnID TurnID `json:"turnID"`; ItemID ItemID `json:"itemID"`; Text string `json:"text"` }
type AssistantMessageFailed struct { TurnID TurnID `json:"turnID"`; ItemID ItemID `json:"itemID"`; Code string `json:"code"`; Message string `json:"message"` }
type AssistantMessageInterrupted struct { TurnID TurnID `json:"turnID"`; ItemID ItemID `json:"itemID"`; Reason string `json:"reason"` }
```

- [ ] **Step 4: Apply and clone the new recorded facts**

`AssistantMessageStarted` requires the matching active running Turn, a new valid Item ID, and no active Item. It creates a running assistant Item and appends its ID to `ItemOrder`. Each terminal Item event requires the matching active Item, writes exactly one terminal status, clears `Turn.ActiveItemID`, preserves completed text byte-for-byte, and stores only stable failure/interruption strings.

Implement `CloneEvent` with an exhaustive type switch over every current value event. `CloneRecordedEvents` allocates a new slice and clones each event; unknown event implementations return `CodeInvalidEvent` rather than sharing an opaque mutable value.

- [ ] **Step 5: Extend strict schema-v1 JSON handling**

Add all four event types to `marshalEvent` and `unmarshalEvent`, using exactly these required keys:

```text
assistant.message.started:     turnID, itemID
assistant.message.completed:   turnID, itemID, text
assistant.message.failed:      turnID, itemID, code, message
assistant.message.interrupted: turnID, itemID, reason
```

Reject invalid IDs, invalid UTF-8, blank failure data, blank interruption reason, unknown keys, duplicate keys, missing keys, and wrong JSON types. Empty completed text remains valid; Task 6 fixes the Engine behavior for empty successful output.

- [ ] **Step 6: Format, verify, and commit**

Run: `gofmt -w internal/harness/domain`

Run: `go test ./internal/harness/domain -run 'Test(ParseItemID|ApplyAssistant|CloneRecorded|AssistantMessage.*JSON)' -count=1`

Run: `go test ./... -count=1`

Expected: all PASS.

```bash
git add internal/harness/domain
git commit -m "feat(domain): add assistant item lifecycle"
```

---

### Task 2: Add Atomic Item/Turn Domain Commands and Replay Evidence

**Files:**
- Modify: `internal/harness/domain/errors.go`
- Modify: `internal/harness/domain/commands.go`
- Modify: `internal/harness/domain/decide.go`
- Modify: `internal/harness/domain/decide_test.go`
- Modify: `internal/harness/domain/apply.go`
- Modify: `internal/harness/domain/replay_test.go`
- Create: `internal/harness/domain/testdata/assistant_lifecycle.jsonl`
- Modify: `docs/architecture/domain-events.md`

**Interfaces:**
- Consumes: Item types and events from Task 1
- Produces: `StartAssistantMessage`, `CompleteAssistantTurn`, `FailAssistantTurn`, `InterruptAssistantTurn`
- Produces stable codes: `item_already_running`, `item_not_running`, `item_mismatch`, `item_already_exists`
- Guarantees that successful/failed/interrupted Item and Turn facts are returned as one ordered decision batch

- [ ] **Step 1: Write failing command and invariant tests**

```go
func TestDecideCompleteAssistantTurnReturnsAtomicBatch(t *testing.T) {
	state := runningAssistantItemForTest(t)
	events, err := Decide(state, CompleteAssistantTurn{
		SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Text: "done",
	})
	if err != nil { t.Fatalf("Decide() error = %v", err) }
	want := []UncommittedEvent{
		{Event: AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "done"}},
		{Event: TurnCompleted{TurnID: "turn-1"}},
	}
	if !reflect.DeepEqual(events, want) { t.Fatalf("events = %#v", events) }
}
```

Add matching failure and interruption batch tests. Add rejection tests for wrong Item, duplicate Item, second running Item, starting an Item outside the active Turn, terminalizing a Turn while an Item runs, and attempting a second terminal transition.

- [ ] **Step 2: Run focused tests and verify the command types are missing**

Run: `go test ./internal/harness/domain -run 'TestDecide.*Assistant|TestTurnTerminalRejectsRunningItem|TestReplayAssistant' -count=1`

Expected: FAIL because the commands, codes, and fixture do not exist.

- [ ] **Step 3: Define the composite use-case commands**

```go
type StartAssistantMessage struct { SessionID SessionID; TurnID TurnID; ItemID ItemID }
type CompleteAssistantTurn struct { SessionID SessionID; TurnID TurnID; ItemID ItemID; Text string }
type FailAssistantTurn struct { SessionID SessionID; TurnID TurnID; ItemID ItemID; Code string; Message string }
type InterruptAssistantTurn struct { SessionID SessionID; TurnID TurnID; ItemID ItemID; Reason string }
```

Give each command a stable command name and `TargetSessionID`. The three terminal commands return Item terminal first and Turn terminal second. They do not mutate state and they do not manufacture timestamps or IDs.

- [ ] **Step 4: Enforce running-Item invariants in decide and apply**

Add `requireRunningItem` and the event-side equivalent. Existing `CompleteTurn`, `FailTurn`, and `InterruptTurn` must return `item_already_running` when an Item is active. Existing Turn terminal event application must also reject a running Item, so replay cannot bypass the command invariant.

- [ ] **Step 5: Add a canonical assistant replay fixture and update the contract document**

Create an eight-record fixture with this exact type order:

```text
session.created
turn.started
assistant.message.started
assistant.message.completed
turn.completed
turn.started
turn.interrupted
session.closed
```

Use one command ID for the Item-completed/Turn-completed pair and contiguous sequence values `1..8`. Assert replay produces a closed Session, a completed assistant Item with exact Unicode text, and an interrupted second Turn. Update `docs/architecture/domain-events.md` with the new commands, events, error codes, state machine, atomic batch invariant, and both canonical fixtures.

- [ ] **Step 6: Format, verify, and commit**

Run: `gofmt -w internal/harness/domain`

Run: `go test ./internal/harness/domain -run 'TestDecide.*Assistant|TestTurnTerminalRejectsRunningItem|TestReplayAssistant' -count=1`

Run: `go test ./... -count=1`

Expected: all PASS.

```bash
git add internal/harness/domain docs/architecture/domain-events.md
git commit -m "feat(domain): decide atomic assistant turns"
```

---

### Task 3: Define Application Ports, Typed Errors, and Deterministic Sources

**Files:**
- Create: `internal/harness/application/doc.go`
- Create: `internal/harness/application/ports.go`
- Create: `internal/harness/application/errors.go`
- Create: `internal/harness/application/ports_test.go`
- Create: `internal/harness/testkit/clock.go`
- Create: `internal/harness/testkit/ids.go`
- Create: `internal/harness/testkit/ids_test.go`

**Interfaces:**
- Produces: `EventStore`, `AppendRequest`, `Clock`, `IDGenerator`
- Produces: `VersionConflictError`, `IsVersionConflict(error) bool`
- Produces: `ErrorCategory`, `Error`, and `IsCategory(error, ErrorCategory) bool`
- Produces: `testkit.FixedClock` and concurrent-safe `testkit.SequenceIDs`

- [ ] **Step 1: Write failing deterministic source and error tests**

```go
func TestSequenceIDsAreTypedAndConcurrentSafe(t *testing.T) {
	ids := NewSequenceIDs()
	if got, _ := ids.NewSessionID(); got != "session-1" { t.Fatalf("session ID = %q", got) }
	if got, _ := ids.NewTurnID(); got != "turn-1" { t.Fatalf("turn ID = %q", got) }
	if got, _ := ids.NewItemID(); got != "item-1" { t.Fatalf("item ID = %q", got) }
	if got, _ := ids.NewCommandID(); got != "command-1" { t.Fatalf("command ID = %q", got) }
	if got, _ := ids.NewEventID(); got != "event-1" { t.Fatalf("event ID = %q", got) }
}
```

Run 32 goroutines against one `SequenceIDs`, collect each typed ID, and assert uniqueness without sleeps. Add tests that `errors.As` and `errors.Is` chains preserve `VersionConflictError` and application `Error` categories.

- [ ] **Step 2: Run focused tests and verify the packages/types are missing**

Run: `go test ./internal/harness/application ./internal/harness/testkit -count=1`

Expected: FAIL because the packages and interfaces do not exist.

- [ ] **Step 3: Define the application-owned ports exactly**

```go
type EventStore interface {
	Load(context.Context, domain.SessionID) ([]domain.RecordedEvent, error)
	Append(context.Context, AppendRequest) ([]domain.RecordedEvent, error)
}

type AppendRequest struct {
	SessionID       domain.SessionID
	ExpectedVersion uint64
	CommandID       domain.CommandID
	Events          []domain.Event
}

type Clock interface { Now() time.Time }

type IDGenerator interface {
	NewSessionID() (domain.SessionID, error)
	NewTurnID() (domain.TurnID, error)
	NewItemID() (domain.ItemID, error)
	NewCommandID() (domain.CommandID, error)
	NewEventID() (domain.EventID, error)
}
```

Document on `Append` that `ExpectedVersion` is CAS, one request is atomic, the store assigns contiguous sequences and metadata, cancellation before commit writes nothing, and implementations return defensive copies.

- [ ] **Step 4: Implement stable conflict and service error types**

```go
type ErrorCategory string
const (
	CategoryValidation  ErrorCategory = "validation"
	CategoryConflict    ErrorCategory = "conflict"
	CategoryModel       ErrorCategory = "model"
	CategoryCanceled    ErrorCategory = "canceled"
	CategoryOutputLimit ErrorCategory = "output_limit"
	CategoryDelivery    ErrorCategory = "delivery"
	CategoryPersistence ErrorCategory = "persistence"
	CategoryInternal    ErrorCategory = "internal"
)

type Error struct {
	Category          ErrorCategory
	Code              string
	TerminalCommitted bool
	Cause             error
}
```

Implement `Error()`, `Unwrap()`, and `IsCategory`. `Error()` renders only stable category/code and terminal-commit state; it must not concatenate `Cause.Error()` or raw provider text. `Unwrap()` preserves the cause for deliberate programmatic inspection. Define `VersionConflictError` with `SessionID`, `ExpectedVersion`, and `ActualVersion`; implement `Error()` and `IsVersionConflict` via `errors.As`.

- [ ] **Step 5: Implement deterministic formal sources**

`FixedClock.Now()` returns the configured timestamp normalized to UTC. `SequenceIDs` owns one mutex and five independent counters; every method increments its counter and returns the exact prefixes shown in Step 1. It must implement `application.IDGenerator` without importing any adapter package or reading global time/randomness.

- [ ] **Step 6: Format, verify, and commit**

Run: `gofmt -w internal/harness/application internal/harness/testkit`

Run: `go test -race ./internal/harness/application ./internal/harness/testkit -count=1`

Run: `go test ./... -count=1`

Expected: all PASS.

```bash
git add internal/harness/application internal/harness/testkit
git commit -m "feat(application): define runtime ports"
```

---

### Task 4: Implement the Atomic In-Memory EventStore and Contract Suite

**Files:**
- Create: `internal/harness/application/eventstoretest/suite.go`
- Create: `internal/harness/adapters/memory/event_store.go`
- Create: `internal/harness/adapters/memory/event_store_test.go`

**Interfaces:**
- Consumes: `application.EventStore`, `AppendRequest`, `Clock`, `IDGenerator`
- Produces: `memory.NewEventStore(application.Clock, application.IDGenerator) (*EventStore, error)`
- Produces test controls: `FailNextLoad(domain.SessionID, error)` and `FailNextAppend(domain.SessionID, error)`
- Produces reusable `eventstoretest.Run(t, factory)` contract coverage

- [ ] **Step 1: Write the reusable contract suite before the store**

Define this harness:

```go
type Harness struct {
	Store          application.EventStore
	FailNextAppend func(domain.SessionID, error)
}

type Factory func(*testing.T) Harness

func Run(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("contiguous metadata and load", func(t *testing.T) { testContiguousMetadataAndLoad(t, factory(t)) })
	t.Run("compare and swap conflict", func(t *testing.T) { testCompareAndSwapConflict(t, factory(t)) })
	t.Run("atomic injected failure", func(t *testing.T) { testAtomicInjectedFailure(t, factory(t)) })
	t.Run("canceled append", func(t *testing.T) { testCanceledAppend(t, factory(t)) })
	t.Run("defensive copies", func(t *testing.T) { testDefensiveCopies(t, factory(t)) })
}
```

Implement the five named helpers in the same file. Each helper appends concrete domain events: `testContiguousMetadataAndLoad` asserts sequences `1,2`, UTC clock values, distinct non-empty event IDs, and the request command ID; `testCompareAndSwapConflict` submits two version-zero creates and asserts one success plus one `VersionConflictError`; `testAtomicInjectedFailure` injects failure before a two-event append and asserts unchanged length/version; `testCanceledAppend` uses an already-canceled context and asserts zero records; `testDefensiveCopies` mutates source, returned, and loaded values and asserts a fresh load is unchanged.

- [ ] **Step 2: Add the memory adapter test invocation and verify failure**

```go
func TestEventStoreContract(t *testing.T) {
	eventstoretest.Run(t, func(t *testing.T) eventstoretest.Harness {
		store, err := NewEventStore(
		testkit.FixedClock{Time: time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)},
		testkit.NewSequenceIDs(),
		)
		if err != nil { t.Fatal(err) }
		return eventstoretest.Harness{Store: store, FailNextAppend: store.FailNextAppend}
	})
}
```

Run: `go test ./internal/harness/adapters/memory -count=1`

Expected: FAIL because `NewEventStore` and its behavior do not exist.

- [ ] **Step 3: Implement load, CAS, metadata, and atomic commit**

The store owns one mutex and maps keyed by Session ID for records and one-shot faults. `Load` checks `ctx.Err()` before and after acquiring the lock and returns `domain.CloneRecordedEvents`.

`Append` performs this order while holding the lock:

```text
validate context, SessionID, CommandID, non-empty Events
compare len(current records) with ExpectedVersion
consume an injected pre-commit fault, if present
clone and validate every Event
generate every EventID and UTC timestamp into a local batch
apply current + local batch through domain.Replay
check context once more
append the entire local batch to store state in one assignment
return a defensive clone
```

If ID generation, clock validation, cloning, replay, context, or fault injection fails before the assignment, store state is unchanged. Never retry CAS internally.

- [ ] **Step 4: Add deterministic same-Session and independent-Session race tests**

Use a start barrier to launch two appends with `ExpectedVersion: 0` for the same Session; assert exactly one succeeds and one is a version conflict. Launch 32 independent Session appends against one store and assert every stream has version `1`. Run under `-race`; use no sleep.

- [ ] **Step 5: Format, verify, and commit**

Run: `gofmt -w internal/harness/application/eventstoretest internal/harness/adapters/memory`

Run: `go test -race ./internal/harness/adapters/memory -count=1`

Run: `go test -race ./... -count=1`

Expected: all PASS.

```bash
git add internal/harness/application/eventstoretest internal/harness/adapters/memory
git commit -m "feat(memory): add atomic event store"
```

---

### Task 5: Define the Engine Stream Contract and Formal Test Adapters

**Files:**
- Create: `internal/harness/engine/doc.go`
- Create: `internal/harness/engine/model.go`
- Create: `internal/harness/engine/runtime.go`
- Create: `internal/harness/engine/errors.go`
- Create: `internal/harness/engine/modeltest/suite.go`
- Create: `internal/harness/testkit/scripted_model.go`
- Create: `internal/harness/testkit/scripted_model_test.go`
- Create: `internal/harness/testkit/recording_sink.go`
- Create: `internal/harness/testkit/recording_sink_test.go`

**Interfaces:**
- Produces: `engine.Model`, `ModelStream`, `ModelRequest`, `StreamEvent`, `StreamEventType`
- Produces: `RuntimeSink`, `RuntimeEvent`, `RuntimeEventType`, `Correlation`, `Emitter`
- Produces: `engine.ErrorCode`, `engine.Error`, `engine.IsCode`
- Produces: `testkit.ScriptedModel` and `testkit.RecordingSink` as exact port implementations
- Produces: reusable `modeltest.Run(t, factory)`

- [ ] **Step 1: Write the Model contract suite and adapter tests**

The suite factory returns a model scripted for the supplied request and contract steps:

```go
type Factory func(expected engine.ModelRequest, steps []ContractStep, startupError error) engine.Model

type ContractStep struct {
	Event         engine.StreamEvent
	Err           error
	WaitForCancel bool
}
```

Test exact request delivery, ordered Unicode deltas, explicit completion, startup failure, mid-stream failure, blocking until context cancellation, and concurrent call recording. Test `RecordingSink` for ordered defensive copies and deterministic failure at an exact ordinal.

- [ ] **Step 2: Run focused tests and verify the Engine contracts are missing**

Run: `go test ./internal/harness/engine/... ./internal/harness/testkit -run 'Test(ScriptedModel|RecordingSink|ModelContract)' -count=1`

Expected: FAIL because the Engine and adapter types do not exist.

- [ ] **Step 3: Define the provider-neutral Model stream**

```go
type ModelRequest struct {
	SessionID domain.SessionID
	TurnID    domain.TurnID
	ItemID    domain.ItemID
	Input     string
}

type StreamEventType string
const (
	StreamEventTextDelta StreamEventType = "text_delta"
	StreamEventCompleted StreamEventType = "completed"
)

type StreamEvent struct { Type StreamEventType; Text string }

type Model interface {
	Stream(context.Context, ModelRequest) (ModelStream, error)
}

type ModelStream interface {
	Next(context.Context) (StreamEvent, error)
	Close() error
}
```

`Stream` returning an error is startup failure. After a stream is returned, it must emit zero or more text deltas followed by exactly one completed event. `io.EOF` before completed is invalid. Text on completed is invalid. No provider-native object enters these types.

- [ ] **Step 4: Define correlated runtime delivery**

```go
type RuntimeEventType string
const (
	RuntimeModelStreamStarted     RuntimeEventType = "model.stream.started"
	RuntimeModelTextDelta         RuntimeEventType = "model.text.delta"
	RuntimeModelStreamCompleted   RuntimeEventType = "model.stream.completed"
	RuntimeModelStreamFailed      RuntimeEventType = "model.stream.failed"
	RuntimeModelStreamInterrupted RuntimeEventType = "model.stream.interrupted"
	RuntimeAppendCompleted        RuntimeEventType = "append.completed"
	RuntimeDiagnostic             RuntimeEventType = "diagnostic"
)

type Correlation struct {
	SessionID domain.SessionID
	TurnID    domain.TurnID
	ItemID    domain.ItemID
	CommandID domain.CommandID
}

type RuntimeEvent struct {
	Correlation
	Ordinal uint64
	Type    RuntimeEventType
	Text    string
	Code    string
}

type RuntimeSink interface { Emit(context.Context, RuntimeEvent) error }
```

`NewEmitter(sink, correlation)` rejects a nil sink or invalid IDs. `Emitter.Emit` assigns ordinals `1..N` synchronously and invokes the sink inline. It never creates a channel or goroutine.

- [ ] **Step 5: Add stable Engine error codes and deterministic adapters**

Use exact codes `invalid_request`, `model_startup`, `model_stream`, `canceled`, `output_limit`, `delivery`, and `invalid_stream`. `engine.Error` contains `Code` and `Cause`, implements `Error`/`Unwrap`, and is checked through `engine.IsCode`. Its `Error()` string contains the stable code but never interpolates `Cause.Error()`; callers must deliberately unwrap a raw cause.

`ScriptedModel` stores defensive copies, checks the complete `ModelRequest`, supports startup error and this exact adapter step, and records calls under a mutex:

```go
type ScriptedStep struct {
	Event         engine.StreamEvent
	Err           error
	WaitForCancel bool
	Entered       chan<- struct{}
	Release       <-chan struct{}
}
```

`WaitForCancel` blocks only on `ctx.Done()`. Before executing a step, the adapter sends once to `Entered` when non-nil, then waits on `Release` or `ctx.Done()` when `Release` is non-nil. `RecordingSink` records under a mutex and may fail exactly when `event.Ordinal == FailOrdinal`; it never changes behavior based on production environment variables.

- [ ] **Step 6: Format, run the shared contract, and commit**

Run: `gofmt -w internal/harness/engine internal/harness/testkit`

Run: `go test -race ./internal/harness/engine/... ./internal/harness/testkit -count=1`

Run: `go test ./... -count=1`

Expected: all PASS.

```bash
git add internal/harness/engine internal/harness/testkit
git commit -m "feat(engine): define streaming contracts"
```

---

### Task 6: Implement the Synchronous Bounded TurnRunner

**Files:**
- Create: `internal/harness/engine/runner.go`
- Create: `internal/harness/engine/runner_test.go`

**Interfaces:**
- Consumes: `Model`, `ModelStream`, `Emitter`, and typed Engine errors from Task 5
- Produces: `NewTurnRunner(Model) (*TurnRunner, error)`
- Produces: `RunRequest`, `RunResult`, and `(*TurnRunner).Run(context.Context, RunRequest, *Emitter) (RunResult, error)`

- [ ] **Step 1: Write the success and exact-boundary tests**

```go
func TestTurnRunnerPreservesBoundedUTF8Output(t *testing.T) {
	model := scriptedModel(t, []string{"你", "好\n"})
	sink := &testkit.RecordingSink{}
	emitter, _ := NewEmitter(sink, validCorrelation())
	runner, _ := NewTurnRunner(model)

	got, err := runner.Run(context.Background(), RunRequest{
		ModelRequest: ModelRequest{SessionID: "session-1", TurnID: "turn-1", ItemID: "item-1", Input: "inspect"},
		MaxAssistantBytes: len([]byte("你好\n")),
	}, emitter)
	if err != nil { t.Fatalf("Run() error = %v", err) }
	if got.Text != "你好\n" { t.Fatalf("Text = %q", got.Text) }
}
```

Assert runtime order is `model.stream.started`, delta 1, delta 2. The runner deliberately does not emit a terminal runtime event; application code does that only after durable terminal append.

- [ ] **Step 2: Write the complete failure matrix before implementation**

Add table/subtests for: nil dependencies; invalid request IDs/input; zero/negative limit; startup error; error before first delta; error after deltas; `io.EOF` before completed; unknown stream event; text attached to completed; invalid UTF-8 delta; output exactly at limit; one byte over limit; empty successful output; caller cancellation before Stream; cancellation in `Next`; sink failure on started; sink failure on a delta; and stream `Close` failure. Empty successful output is valid and returns `Text == ""`. Assert exact Engine codes and that an over-limit delta is neither delivered nor accumulated.

- [ ] **Step 3: Run the focused tests and verify `TurnRunner` is absent**

Run: `go test ./internal/harness/engine -run 'TestTurnRunner' -count=1`

Expected: FAIL because `TurnRunner`, `RunRequest`, and `RunResult` do not exist.

- [ ] **Step 4: Implement the pull loop without hidden concurrency**

```go
type RunRequest struct {
	ModelRequest
	MaxAssistantBytes int
}

type RunResult struct { Text string }
```

Execution order is: validate request and context; call `Model.Stream`; emit `model.stream.started`; pull events synchronously; validate UTF-8 and size before emitting/accumulating each delta; require explicit completed; close the stream; return exact accumulated text. Map a canceled context to `canceled`, sink failure to `delivery`, startup/Next/Close errors to their model codes, and protocol violations to `invalid_stream`. Preserve causes with `errors.Join` when cleanup also fails.

Use the overflow-safe bound check:

```go
if len(delta) > request.MaxAssistantBytes-builder.Len() {
	return RunResult{}, engineError(CodeOutputLimit, ErrAssistantOutputLimit)
}
```

- [ ] **Step 5: Prove there is no goroutine leak or timing dependency**

The cancellation test uses a ScriptedModel `WaitForCancel` step and an explicit channel announcing that `Next` was entered. Cancel the context and wait on the result channel; do not use `time.Sleep`. Compare `runtime.NumGoroutine` only if a barrier can prove all test goroutines exited; correctness must not depend on an approximate goroutine count.

- [ ] **Step 6: Format, verify, and commit**

Run: `gofmt -w internal/harness/engine`

Run: `go test -race ./internal/harness/engine -run 'TestTurnRunner' -count=1`

Run: `go test -race ./... -count=1`

Expected: all PASS.

```bash
git add internal/harness/engine/runner.go internal/harness/engine/runner_test.go
git commit -m "feat(engine): run bounded model streams"
```

---

### Task 7: Build the Application Service and Session Use Cases

**Files:**
- Create: `internal/harness/application/service.go`
- Create: `internal/harness/application/append.go`
- Create: `internal/harness/application/session.go`
- Create: `internal/harness/application/session_test.go`

**Interfaces:**
- Consumes: `EventStore`, `IDGenerator`, domain `Decide`/`Apply`/`Replay`, and `engine.TurnRunner`
- Produces: `Config`, `DefaultConfig()`, `NewService(...)`
- Produces: `CreateSession`, `LoadSession`, and `CloseSession`
- Produces one internal decide/append/apply path reused by RunTurn

- [ ] **Step 1: Write failing session use-case tests through real ports**

Use `MemoryEventStore`, `FixedClock`, `SequenceIDs`, and a valid scripted runner. Test create → load → close, duplicate creation conflict, close missing Session, close with a running Turn, store load failure, append failure, generated-ID failure, and canceled context before append. Assert domain failures become `CategoryValidation`, store failures become `CategoryPersistence`, conflicts become `CategoryConflict`, and every error has `TerminalCommitted == false`.

```go
func TestCreateLoadCloseSession(t *testing.T) {
	service, store := newServiceForTest(t)
	created, err := service.CreateSession(context.Background(), CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil { t.Fatalf("CreateSession() error = %v", err) }
	loaded, err := service.LoadSession(context.Background(), created.SessionID)
	if err != nil { t.Fatalf("LoadSession() error = %v", err) }
	if loaded.Status != domain.SessionStatusActive { t.Fatalf("state = %#v", loaded) }
	if _, err := service.CloseSession(context.Background(), CloseSessionRequest{SessionID: created.SessionID}); err != nil {
		t.Fatalf("CloseSession() error = %v", err)
	}
	records, _ := store.Load(context.Background(), created.SessionID)
	if got := eventTypes(records); !reflect.DeepEqual(got, []string{"session.created", "session.closed"}) {
		t.Fatalf("types = %v", got)
	}
}
```

- [ ] **Step 2: Run focused tests and verify Service APIs are absent**

Run: `go test ./internal/harness/application -run 'Test(Create|Load|Close)Session' -count=1`

Expected: FAIL because the Service and request/result types do not exist.

- [ ] **Step 3: Fix exact service configuration and constructor validation**

```go
const (
	DefaultMaxAssistantBytes      = 1 << 20
	DefaultTerminalCommitTimeout = 5 * time.Second
)

type Config struct {
	MaxAssistantBytes      int
	TerminalCommitTimeout time.Duration
}

func DefaultConfig() Config {
	return Config{MaxAssistantBytes: DefaultMaxAssistantBytes, TerminalCommitTimeout: DefaultTerminalCommitTimeout}
}

func NewService(store EventStore, ids IDGenerator, runner *engine.TurnRunner, config Config) (*Service, error)
```

Reject nil dependencies, non-positive output limit, and non-positive terminal timeout as `CategoryValidation`. Store immutable configuration on `Service`.

- [ ] **Step 4: Implement one decide/append/apply helper**

The unexported helper accepts context, current state, command, and command ID. It calls `domain.Decide`, extracts concrete events, calls one `EventStore.Append` with `ExpectedVersion: state.Version`, and applies the returned records in order to a cloned state. It validates that the store returned exactly the requested count with contiguous metadata. It never reloads or retries a conflict.

Map errors at the boundary:

```text
domain error            → validation / domain_rejected
VersionConflictError    → conflict / version_conflict
context.Canceled        → canceled / canceled
other load/append error → persistence / load_failed or append_failed
bad returned records    → internal / store_contract_violation
```

- [ ] **Step 5: Implement CreateSession, LoadSession, and CloseSession**

```go
type CreateSessionRequest struct { WorkspaceRoot string }
type CreateSessionResult struct { SessionID domain.SessionID; Records []domain.RecordedEvent }
type CloseSessionRequest struct { SessionID domain.SessionID }
type CloseSessionResult struct { Session domain.Session; Records []domain.RecordedEvent }
```

`CreateSession` generates Session and command IDs, decides against pristine state, and appends at version `0`. `LoadSession` loads then replays; an empty stream is `CategoryValidation/session_not_found`. `CloseSession` loads/replays, generates one command ID, decides, and appends. All returned slices and state are defensive copies.

- [ ] **Step 6: Format, verify, and commit**

Run: `gofmt -w internal/harness/application`

Run: `go test -race ./internal/harness/application -run 'Test(Create|Load|Close)Session' -count=1`

Run: `go test -race ./... -count=1`

Expected: all PASS.

```bash
git add internal/harness/application
git commit -m "feat(application): add session service"
```

---

### Task 8: Orchestrate the Successful Durable RunTurn Path

**Files:**
- Create: `internal/harness/application/turn.go`
- Create: `internal/harness/application/turn_success_test.go`

**Interfaces:**
- Consumes: Service, TurnRunner, RuntimeSink, and atomic assistant domain commands
- Produces: `RunTurnRequest`, `RunTurnResult`, and `(*Service).RunTurn`
- Guarantees one command identity and one ordered runtime ordinal space for the complete call

- [ ] **Step 1: Write a failing end-to-end success test**

```go
func TestRunTurnPersistsExactAssistantMessage(t *testing.T) {
	service, store, model := newRunTurnHarness(t, []testkit.ScriptedStep{
		{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: "你"}},
		{Event: engine.StreamEvent{Type: engine.StreamEventTextDelta, Text: "好\n"}},
		{Event: engine.StreamEvent{Type: engine.StreamEventCompleted}},
	})
	session := mustCreateSession(t, service)
	sink := &testkit.RecordingSink{}

	result, err := service.RunTurn(context.Background(), RunTurnRequest{
		SessionID: session, Input: "inspect repository", Sink: sink,
	})
	if err != nil { t.Fatalf("RunTurn() error = %v", err) }
	if result.Status != domain.TurnStatusCompleted || result.Text != "你好\n" || !result.TerminalCommitted {
		t.Fatalf("result = %#v", result)
	}
	assertExactModelRequest(t, model.Calls()[0], session, result.TurnID, result.ItemID, "inspect repository")
	assertReplayMatchesResult(t, store, result)
}
```

Assert durable type order after `session.created` is `turn.started`, `assistant.message.started`, `assistant.message.completed`, `turn.completed`. Assert the last two share a command ID and are consecutive records returned by one append. Assert runtime order is `model.stream.started`, deltas, `append.completed`, `model.stream.completed`, with ordinals `1..N` and identical correlation IDs.

- [ ] **Step 2: Add sequential Turn and defensive-result tests**

Run two successful Turns in the same Session and assert distinct Turn/Item/command IDs, exact Turn order, two completed Items, and final replay version. Mutate `RunTurnResult.Records` and prove a subsequent store load is unchanged.

- [ ] **Step 3: Run focused tests and verify `RunTurn` is absent**

Run: `go test ./internal/harness/application -run 'TestRunTurn.*(Persists|Sequential|Defensive)' -count=1`

Expected: FAIL because the RunTurn API is not implemented.

- [ ] **Step 4: Implement the exact request and result contract**

```go
type RunTurnRequest struct {
	SessionID domain.SessionID
	Input     string
	Sink      engine.RuntimeSink
}

type RunTurnResult struct {
	SessionID         domain.SessionID
	TurnID            domain.TurnID
	ItemID            domain.ItemID
	Status            domain.TurnStatus
	Text              string
	TerminalCommitted bool
	DeliveryWarning   error
	Records           []domain.RecordedEvent // every record committed by this RunTurn call
}
```

Reject invalid Session ID, blank/invalid UTF-8 input, and nil sink before generating IDs or loading state.

- [ ] **Step 5: Implement the successful orchestration in one authority**

Use this exact order:

```text
Load → Replay
generate TurnID, ItemID, CommandID
Decide/Append turn.started at loaded version
Decide/Append assistant.message.started at next version
create one engine.Emitter with all correlation IDs
TurnRunner.Run using Service.MaxAssistantBytes
Decide CompleteAssistantTurn
Append [assistant.message.completed, turn.completed] atomically
Emit append.completed
Emit model.stream.completed
return completed result with defensive terminal records
```

Every append in one `RunTurn` uses the same command ID. If the initial append conflicts, return immediately and prove the model call count remains zero. Do not emit `model.stream.completed` until the terminal batch is durable.

- [ ] **Step 6: Handle post-commit delivery failure without rewriting success**

If `append.completed` or `model.stream.completed` delivery fails after the terminal append, return the completed result plus `CategoryDelivery/runtime_delivery_failed` with `TerminalCommitted == true`; also set `result.DeliveryWarning` to the sink cause. Do not append failure/interruption events after durable completion.

- [ ] **Step 7: Format, verify, and commit**

Run: `gofmt -w internal/harness/application`

Run: `go test -race ./internal/harness/application -run 'TestRunTurn' -count=1`

Run: `go test -race ./... -count=1`

Expected: all PASS.

```bash
git add internal/harness/application/turn.go internal/harness/application/turn_success_test.go
git commit -m "feat(application): persist successful turns"
```

---

### Task 9: Complete Failure, Cancellation, Delivery, and Persistence Semantics

**Files:**
- Modify: `internal/harness/application/turn.go`
- Create: `internal/harness/application/turn_failure_test.go`
- Modify: `internal/harness/testkit/scripted_model.go`
- Modify: `internal/harness/testkit/scripted_model_test.go`

**Interfaces:**
- Consumes: typed Engine and application errors plus atomic fail/interruption commands
- Produces deterministic terminalization for every post-start outcome
- Consumes the `testkit.ScriptedStep` entered/release barriers defined in Task 5

- [ ] **Step 1: Verify deterministic ScriptedModel barriers in orchestration tests**

Use the existing Task 5 type without renaming its fields:

```go
type ScriptedStep struct {
	Event         engine.StreamEvent
	Err           error
	WaitForCancel bool
	Entered       chan<- struct{}
	Release       <-chan struct{}
}
```

Add an application-level test that observes `Entered`, cancels or closes `Release`, and proves the expected branch. Tests close `Release`; they never sleep.

- [ ] **Step 2: Write the model/output failure table before orchestration changes**

Cover startup failure, failure before delta, failure after deltas, invalid stream, invalid UTF-8, output limit, empty successful output, and stream close failure. Required durable terminal pairs are:

```text
model/startup/stream/invalid/output failure:
  assistant.message.failed
  turn.failed

caller cancellation or runtime delivery failure before terminal commit:
  assistant.message.interrupted
  turn.interrupted
```

Assert partial deltas are absent from durable state; stable codes are present; raw provider error objects are not stored; each pair shares one command ID; and returned application errors report `TerminalCommitted == true` only after the pair commits.

- [ ] **Step 3: Write cancellation boundary tests with barriers**

Test: cancellation before any append writes nothing; cancellation immediately after `turn.started` but before Item start appends only `turn.interrupted`; cancellation during streaming appends the interrupted Item/Turn pair; cancellation released concurrently with model completion selects exactly one terminal pair; cancellation after completed terminal commit cannot replace success.

Use wrapper stores/streams that announce entry and wait on channels. Do not add hooks to production Service for tests.

- [ ] **Step 4: Write delivery and persistence failure tests**

Test sink failure on `model.stream.started` and on a delta; initial append failure prevents any model call; Item-start append failure leaves a visible running Turn and does not call the model; terminal batch injected failure leaves no partial assistant/Turn terminal fact and never reports success; terminal append cancellation falls back to an interrupted cleanup commit; load failure stays persistence; and a cleanup append failure reports persistence with `TerminalCommitted == false` while preserving the original cause with `errors.Join`.

- [ ] **Step 5: Run focused tests and verify the missing terminal mappings**

Run: `go test ./internal/harness/application -run 'TestRunTurn.*(Failure|Cancel|Delivery|Persistence|Limit|Invalid)' -count=1`

Expected: FAIL because RunTurn currently implements only successful and post-commit delivery paths.

- [ ] **Step 6: Implement bounded synchronous terminal cleanup**

For cancellation after a durable start, create cleanup context exactly as follows and `defer cancel()` in the same call stack:

```go
cleanupBase := context.WithoutCancel(ctx)
cleanupCtx, cancel := context.WithTimeout(cleanupBase, s.config.TerminalCommitTimeout)
```

If the Item was not durable, decide `InterruptTurn` with reason `caller_canceled`; if it was durable, decide `InterruptAssistantTurn` with reason `caller_canceled`. For delivery failure before terminal commit, decide `InterruptAssistantTurn` with reason `runtime_delivery_failed`.

Map Engine failure codes to durable values without storing provider prose:

```text
model_startup → code model_startup, message "model failed before streaming"
model_stream  → code model_stream,  message "model stream failed"
output_limit  → code output_limit,  message "assistant output exceeded limit"
invalid_stream → code invalid_stream, message "model stream violated contract"
```

On successful failure/interruption append, emit `append.completed` followed by `model.stream.failed` or `model.stream.interrupted`. A later sink failure becomes `RunTurnResult.DeliveryWarning`; it does not replace an earlier model/canceled primary category. If no primary execution error exists, return CategoryDelivery.

- [ ] **Step 7: Make terminal-commit reporting unambiguous**

All returned `application.Error` values set `TerminalCommitted` from observed durable state, not from intended outcome. A terminal append error always returns false. A completed/failed/interrupted batch followed by sink error returns true. Return `RunTurnResult.Status == running` when persistence leaves a running boundary; never manufacture a terminal result.

- [ ] **Step 8: Format, verify, and commit**

Run: `gofmt -w internal/harness/application internal/harness/testkit`

Run: `go test -race ./internal/harness/application ./internal/harness/testkit -count=1`

Run: `go test -race ./... -count=1`

Expected: all PASS.

```bash
git add internal/harness/application internal/harness/testkit
git commit -m "feat(application): make turn failures durable"
```

---

### Task 10: Add Reusable Scenarios, Concurrency Gates, Fixtures, and Implemented Documentation

**Files:**
- Create: `internal/harness/application/enginescenariotest/suite.go`
- Create: `internal/harness/application/scenario_test.go`
- Create: `internal/harness/application/concurrency_test.go`
- Create: `internal/harness/application/testdata/run_turn_success.jsonl`
- Create: `internal/harness/architecture/dependencies_test.go`
- Create: `docs/architecture/engine-vertical-slice.md`
- Modify: `docs/README.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: the complete real application/engine/domain ports and deterministic adapters
- Produces: reusable `enginescenariotest.Run(t, factory)`
- Produces exact success trace and automated dependency-boundary evidence
- Changes milestone status only after all gates pass

- [ ] **Step 1: Define and run the reusable scenario suite**

```go
type Scenario struct {
	Name          string
	Input         string
	Steps         []testkit.ScriptedStep
	StartupError  error
	MaxBytes      int
	CancelDuringStream bool
	SinkFailOrdinal uint64
	WantStatus    domain.TurnStatus
	WantCategory  application.ErrorCategory
	WantText      string
}

type Harness struct {
	Service       *application.Service
	Store         application.EventStore
	Sink          engine.RuntimeSink
	RuntimeEvents func() []engine.RuntimeEvent
}

type Factory func(*testing.T, Scenario) Harness
func Run(t *testing.T, factory Factory)
```

The shared table includes success, startup failure, mid-stream failure, cancellation, output exactly at limit, output one byte over, and sink delivery failure. For cancellation, the suite installs `Entered` on the blocking scripted step, starts `RunTurn`, waits for entry, and cancels explicitly. `SinkFailOrdinal` configures the formal sink without an environment branch. Every case creates a Session, calls `Service.RunTurn` with `Harness.Sink`, loads records, replays them, and asserts status/error/runtime correlation. The memory/scripted composition must pass this suite; future store/model compositions reuse the same entry point.

- [ ] **Step 2: Add authoritative concurrency tests**

For one Session, wrap `Load` with a two-party barrier so both calls observe the same version. Start two `RunTurn` calls and assert exactly one initial append wins, the loser is `CategoryConflict`, and total model call count is one. For different Sessions, run 32 complete Turns concurrently against one MemoryEventStore and shared SequenceIDs; assert all replay to completed and the race detector reports no issue.

- [ ] **Step 3: Add the exact successful durable trace fixture**

Generate `run_turn_success.jsonl` through `domain.MarshalRecordedEvent`, not handwritten alternate JSON. Fix clock and IDs so the fixture has deterministic metadata. A test decodes it, compares every record to the live scenario after normalization of only explicitly injected clock/IDs, replays both, and asserts exact final assistant text.

- [ ] **Step 4: Add a standard-library dependency boundary test**

Use `go/parser`, `go/token`, and `filepath.WalkDir` to inspect non-test production `.go` imports. Assert:

```text
domain imports no internal/harness/application, engine, adapters, testkit, ACP, MCP, TUI, or provider package
engine imports no internal/harness/application, adapters, testkit, ACP, MCP, TUI, or provider package
application imports no internal/harness/adapters or testkit package
no production file contains a branch or type assertion for ScriptedModel
application, engine, and memory production files import no os, os/exec, net, or net/http package
```

The test fails with file and forbidden import/token context. Do not shell out or depend on repository-global environment variables.

- [ ] **Step 5: Publish the implemented internal contract and evidence commands**

Create `docs/architecture/engine-vertical-slice.md` containing: exact public-internal interfaces, package dependency direction, lifecycle diagrams, CAS/atomic semantics, output and timeout constants, error table, runtime-versus-durable table, cancellation boundaries, formal adapters, known running-boundary persistence limitation, and explicit deferred capabilities.

Update `README.md` and `docs/README.md` only after the implementation passes all gates: mark the Engine vertical slice implemented, link the contract and completed plan, retain pre-v0/not-GA wording, and keep provider/tools/ACP/TUI/persistence/recovery listed as not implemented.

- [ ] **Step 6: Run the complete industrial verification matrix**

Run: `gofmt -w .`

Run: `test -z "$(gofmt -l .)"`

Run: `go vet ./...`

Run: `go test ./... -count=1`

Run: `go test -race ./... -count=1`

Run: `git diff --check`

Run the local Markdown-link check:

```bash
ruby -e 'bad=[]; Dir.glob("**/*.md").sort.each{|f| File.read(f).scan(/\[[^\]]+\]\(([^)]+)\)/).flatten.each{|raw| h=raw.split(/[[:space:]]+/,2).first.delete_prefix("<").delete_suffix(">"); next if h.empty? || h.start_with?("http://","https://","mailto:","#","/"); p=h.split("#",2).first; bad << "#{f}: #{h}" unless p.empty? || File.exist?(File.expand_path(p,File.dirname(f)))}}; abort(bad.join("\n")) unless bad.empty?'
```

Expected: every command exits `0`; scenario output reports success/failure/cancellation/conflict/boundary cases; no package race, forbidden import, partial batch, missing terminal pair, or broken link remains.

- [ ] **Step 7: Request independent review and resolve every material finding**

The reviewer receives the approved spec, this plan, and the full diff. They check spec coverage, command authority, port direction, atomicity, cancellation race semantics, defensive copies, error categories, bounds, deterministic tests, and scope exclusions. Resolve all critical and important findings, rerun Step 6, and record the final commands/results in the implementation handoff.

- [ ] **Step 8: Commit the scenario and documentation evidence**

```bash
git add README.md docs internal/harness/application internal/harness/architecture
git commit -m "test: verify industrial engine vertical slice"
```

After committing, run `git status --short` and `go test -race ./... -count=1` once more. Expected: clean worktree and PASS.

## Plan Completion Gate

The implementation branch is ready to integrate only when all ten task commits exist, every checkbox is supported by command output, both formal contract suites pass, the scenario and race matrices pass, the success fixture replays exactly, documentation still states pre-v0/not-GA, and independent review has no open critical or important finding. Passing the happy path alone is not completion.
