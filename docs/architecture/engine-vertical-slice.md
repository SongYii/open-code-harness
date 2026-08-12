# Implemented Engine Vertical Slice

- Status: Implemented internal contract
- Maturity: pre-v0; not a general availability release
- Scope: one synchronous, provider-neutral, model-only Turn
- Normative design: [Industrial Engine vertical slice design](../superpowers/specs/2026-08-12-engine-vertical-slice-design.md)
- Implemented plan: [Industrial Engine vertical slice implementation plan](../superpowers/plans/2026-08-12-engine-vertical-slice.md)
- Completion evidence: [Engine vertical slice evidence ledger](engine-vertical-slice-evidence.md)
- Chinese reading copy: [已实现 Engine 纵切](engine-vertical-slice.zh-CN.md)

This document records behavior enforced by the current code and tests. It is an
internal Go contract, not a stable public protocol. Pre-v0 changes still require
the design, implementation, tests, and this document to move together.

## Delivered capability

The implemented path can create and load a Session, atomically admit one
assistant Turn, consume one bounded model stream, emit synchronous runtime
progress, atomically persist a terminal assistant Item and Turn, and rebuild the
same state by replay. Success, model failure, cancellation, sink failure, output
bounds, persistence faults, and optimistic-concurrency conflicts use the same
application and Engine path.

This is a Minimal Executable Turn Runner. It is not yet a tool-using Agent Loop.

## Package authority and dependency direction

```text
headless caller / future protocol adapters
                    |
                    v
internal/harness/application  -----> internal/harness/engine
  command + durability authority       bounded stream authority
                    |
                    v
            internal/harness/domain
             lifecycle fact authority

internal/harness/adapters/memory ----implements----> application.EventStore
internal/harness/testkit          ----implements----> application/engine ports
```

- `domain` imports no Application, Engine, adapter, testkit, provider, ACP, MCP,
  or TUI package.
- `engine` imports Domain only; it imports no Application, concrete adapter,
  testkit, provider, ACP, MCP, or TUI package.
- `application` imports Engine and Domain, but no concrete adapter or testkit.
- Adapter and testkit packages depend inward on consumer-owned interfaces.
- Application is the only authority allowed to manufacture durable lifecycle
  commands around model execution.

The standard-library AST gate in
[`dependencies_test.go`](../../internal/harness/architecture/dependencies_test.go)
enforces these directions, the absence of `ScriptedModel` production branches,
and the host/network import boundary.

## Exported internal interfaces

The Application persistence boundary is:

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

The Application service surface is:

```go
type Config struct {
    MaxAssistantBytes     int
    TerminalCommitTimeout time.Duration
}

func DefaultConfig() Config
func NewService(EventStore, IDGenerator, *engine.TurnRunner, Config) (*Service, error)

func (*Service) CreateSession(context.Context, CreateSessionRequest) (CreateSessionResult, error)
func (*Service) LoadSession(context.Context, domain.SessionID) (domain.Session, error)
func (*Service) CloseSession(context.Context, CloseSessionRequest) (CloseSessionResult, error)
func (*Service) RunTurn(context.Context, RunTurnRequest) (RunTurnResult, error)
```

The current request/result values are:

```go
type CreateSessionRequest struct { WorkspaceRoot string }
type CreateSessionResult struct {
    SessionID domain.SessionID
    Records   []domain.RecordedEvent
}

type CloseSessionRequest struct { SessionID domain.SessionID }
type CloseSessionResult struct {
    Session domain.Session
    Records []domain.RecordedEvent
}

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
    Records           []domain.RecordedEvent
}
```

Result records are defensive copies of every batch this invocation knows it
committed.

The Engine model boundary is:

```go
type Model interface {
    Stream(context.Context, ModelRequest) (ModelStream, error)
}

type ModelStream interface {
    Next(context.Context) (StreamEvent, error)
    Close() error
}

type RuntimeSink interface {
    Emit(context.Context, RuntimeEvent) error
}

type ModelRequest struct {
    SessionID domain.SessionID
    TurnID    domain.TurnID
    ItemID    domain.ItemID
    Input     string
}

type StreamEvent struct {
    Type StreamEventType
    Text string
}

type Correlation struct {
    SessionID domain.SessionID
    TurnID    domain.TurnID
    ItemID    domain.ItemID
    CommandID domain.CommandID
}

type RuntimePayload struct {
    Type RuntimeEventType
    Text string
    Code string
}

type RuntimeEvent struct {
    Correlation
    Ordinal uint64
    Type    RuntimeEventType
    Text    string
    Code    string
}

type RunRequest struct {
    ModelRequest
    MaxAssistantBytes int
}

type RunResult struct { Text string }

func NewTurnRunner(Model) (*TurnRunner, error)
func (*TurnRunner) Run(context.Context, RunRequest, *Emitter) (RunResult, error)
func NewEmitter(RuntimeSink, Correlation) (*Emitter, error)
func (*Emitter) Emit(context.Context, RuntimePayload) error
```

`ModelRequest` contains Session, Turn, and Item IDs plus exact input. The stream
grammar is `text_delta* -> completed`. `RuntimePayload` contains only Type, Text,
and Code; the run-scoped Emitter exclusively stamps Session/Turn/Item/Command
correlation and one-based attempt ordinals.

The only stream event types are `text_delta` and `completed`. The implemented
runtime types are `model.stream.started`, `model.text.delta`,
`model.stream.completed`, `model.stream.failed`, `model.stream.interrupted`, and
`append.completed`.

## Lifecycle and execution

Assistant Item lifecycle:

```text
absent --assistant.message.started--> running
running --assistant.message.completed--> completed
        --assistant.message.failed-----> failed
        --assistant.message.interrupted-> interrupted
```

Turn lifecycle for this slice:

```text
absent --turn.started--> running
running --turn.completed--> completed
        --turn.failed-----> failed
        --turn.interrupted-> interrupted
```

Admission and terminal transitions are paired:

```text
atomic admission:       turn.started
                        assistant.message.started

atomic success:         assistant.message.completed
                        turn.completed

atomic failure:         assistant.message.failed
                        turn.failed

atomic interruption:    assistant.message.interrupted
                        turn.interrupted
```

The complete successful call order is:

```text
validate request/dependencies
  -> Load complete Session stream
  -> Replay and check domain eligibility
  -> generate and validate Turn/Item/Command IDs
  -> construct one Emitter
  -> atomic admission CAS
  -> Model.Stream and synchronous Next loop
  -> atomic completed Item/Turn CAS
  -> runtime append.completed
  -> runtime model.stream.completed
```

No model call occurs before accepted atomic admission. No terminal runtime
signal occurs before the matching terminal durable batch is accepted and
applied.

## CAS and atomic append semantics

- A Session version is exactly the length of its contiguous authoritative
  recorded-event stream.
- `ExpectedVersion` must equal that length. A successful batch of `N` events
  advances the version by `N` and returns exactly those new records.
- One append is ordered and all-or-nothing. No partial admission or partial
  terminal pair is valid.
- Every record in one batch has a distinct Event ID, contiguous sequence, one
  Command ID, and one shared non-zero UTC occurrence time.
- Returned and loaded records are defensive copies and are accepted only after
  exact metadata, event, ordering, Apply, and final-version verification.
- Same-Session concurrency linearizes at admission CAS. The loser receives a
  conflict and never calls the model. Application and Store perform no hidden
  reload or retry.
- A non-nil `Append` error means the batch did not commit. Once committed, the
  current port requires the records to be returned despite a later caller
  cancellation.
- One RunTurn Command ID is correlation lineage across two append batches and
  runtime events. It is not an idempotency or Store-deduplication key.

A future remote store with ambiguous post-commit acknowledgement cannot weaken
this port. It needs exact retry identity or an explicit unknown-commit result.

## Bounds, stream ownership, and cleanup

```go
const DefaultMaxAssistantBytes = 1 << 20
const DefaultTerminalCommitTimeout = 5 * time.Second
```

- Both configured values must be positive.
- Each delta must be non-empty valid UTF-8. Exact accepted bytes are never
  trimmed, normalized, replaced, or re-chunked.
- The byte bound is checked before sink delivery and before accumulation. Exact
  limit is valid; one byte over is `output_limit`.
- Engine consumes streams synchronously and creates no goroutine or channel.
- Every non-nil acquired stream, including `(stream, error)`, is closed exactly
  once. A nil stream is never closed.
- Failure cancels the derived stream context before `Close`; explicit completion
  closes before derived cancellation. Close errors retain the primary stable
  code and remain inspectable in the error tree.
- RuntimeSink delivery is inline and backpressured. A production shared sink
  must be safe for calls from separate Emitters.

## Durable facts and runtime signals

| Durable, replayable facts | Transient runtime signals |
| --- | --- |
| `turn.started` | `model.stream.started` |
| `assistant.message.started` | `model.text.delta` |
| `assistant.message.completed` | `append.completed` |
| `assistant.message.failed` | `model.stream.completed` |
| `assistant.message.interrupted` | `model.stream.failed` |
| `turn.completed`, `turn.failed`, `turn.interrupted` | `model.stream.interrupted` |

Text deltas are not persisted. A completed assistant Item stores exact final
text. Failed or interrupted Items store stable terminal code/message data and
never persist partial model output as a successful answer.

Runtime ordinals are one-based sink-attempt order within one RunTurn. A failed
sink attempt consumes an ordinal. Invalid payloads and cancellation observed
before the attempt do not. Runtime ordinals are neither durable sequence values
nor a global clock.

## Cancellation and terminal authority

| Phase | Durable boundary | Context and outcome |
| --- | --- | --- |
| Preflight | none | Caller context; cancellation writes nothing and calls no model |
| Admission | started Turn + Item | Caller context; one atomic CAS is the model-call boundary |
| Execution | runtime started/deltas | Caller context reaches Model and RuntimeSink |
| Terminalization: success | completed Item + Turn | Caller context; accepted commit wins over later cancellation |
| Terminalization: failure/interruption | failed/interrupted Item + Turn | `context.WithoutCancel` immediately bounded by configured timeout, in the same call stack |

The bounded cleanup context is used only to persist a post-admission failure or
interruption. It is never used for model work, ordinary success, runtime
delivery, retry, or detached background work. A post-terminal runtime delivery
failure sets `DeliveryWarning`; it cannot rewrite durable completion.

## Error contract

Application errors expose stable Category, Code, and `TerminalCommitted` while
retaining raw causes only for deliberate unwrapping.

| Category | Representative codes/outcome | Retry owner |
| --- | --- | --- |
| `validation` | `invalid_request`, `session_not_found`, `domain_rejected` | Caller changes request |
| `conflict` | `version_conflict`; no terminal commit | Caller reloads and decides explicitly |
| `model` | `model_startup`, `model_stream`, `invalid_stream` | Future policy; no retry here |
| `canceled` | `canceled`; interrupted pair after admission | Caller decides whether to create a new Turn |
| `output_limit` | `output_limit`; failed pair | Caller/configuration policy |
| `delivery` | `runtime_delivery_failed`; interrupted before terminal or warning after terminal | Adapter/caller |
| `persistence` | `load_failed`, `append_failed`; durable boundary may remain running | Storage/recovery policy |
| `internal` | Store/ID/Engine contract violation | Operator/developer |

Engine stable codes are `invalid_request`, `model_startup`, `model_stream`,
`canceled`, `output_limit`, `delivery`, and `invalid_stream`. Engine and
Application code/category matchers traverse wrapped and joined error trees and
are safe around typed-nil errors. Stable error text never interpolates provider
or sink prose.

## Formal adapters and executable evidence

- `MemoryEventStore` is the mutex-protected CAS/atomic EventStore contract
  implementation with deterministic one-shot pre-commit faults.
- `ScriptedModel` implements the exact Model port, request assertions,
  independent streams, deterministic barriers, startup/stream/close failures,
  and concurrent call recording.
- `FixedClock` and `SequenceIDs` supply deterministic, concurrency-safe metadata.
- `RecordingSink` records Attempts before one-shot ordinal failure and Delivered
  only after success. Its snapshots are defensive.
- Reusable suites are `eventstoretest.Run`, `modeltest.Run`, and
  `enginescenariotest.Run`.
- `enginescenariotest` owns its adapter-neutral `Step`, `ModelBehavior`, exact
  Application-error, durable-terminal, and runtime-event expectations. Its
  Factory translates those behaviors to a concrete model fixture. A returned
  Harness exposes `RuntimeAttempts` (every sink call, including a rejected
  call) separately from `RuntimeDelivered` (only accepted calls); the suite
  checks exact ordinal, correlation, Type, Text, Code, outer Application
  Category/Code/TerminalCommitted, and replayed terminal Code/Message.
- The generated [successful JSONL trace](../../internal/harness/application/testdata/run_turn_success.jsonl)
  is produced through the real service and `domain.MarshalRecordedEvent`, then
  decoded, compared record-for-record with a live run, and replayed.
- Barrier tests prove one winner for concurrent same-Session admission and 32
  completed independent Sessions through one Store, ID source, runner, and
  shared sink.

Run the complete local evidence matrix with:

```bash
gofmt -w .
test -z "$(gofmt -l .)"
go vet ./...
go test ./... -count=1
go test -race ./... -count=1
git diff --check
```

The repository also runs a local Markdown-link check documented by the
completed plan.

## Known running-boundary limitation

Admission is durably committed before external model work. Process death after
admission, or a terminal persistence/conflict/Store-contract failure, can
therefore leave a valid replayable Session with a running Turn and Item. The
current result reports that known boundary with `Status == running`,
`TerminalCommitted == false`, and the accepted admission records. It emits no
false success signal and performs no blind model or append retry.

Startup reconciliation, continuation, and recovery are not implemented. A
production reconciliation design and adapter are blocking capabilities before
general availability.

## Explicitly deferred capabilities

The following are not implemented by this milestone:

- real provider contract or provider adapter;
- authentication, rate limits, retry, cache repair, usage, cost, or fallback;
- tools, Tool Runtime, Policy, approvals, or workspace sandboxing;
- ACP/JSON-RPC, TUI, IDE integration, public SDK, or protocol compatibility;
- production file/SQLite/remote persistence, ambiguous-commit protocol,
  snapshots, checkpoints, migration, backup, reconciliation, or recovery;
- Context Engine, prompt construction, compaction, memory, Skills, MCP,
  subagents, or multi-agent graphs;
- persisted runtime log, catch-up, OpenTelemetry, and complete evaluation
  infrastructure.

These exclusions preserve dependency order. They do not weaken the verified
contracts delivered in this pre-v0 slice, and they prevent this milestone from
being presented as a GA harness.
