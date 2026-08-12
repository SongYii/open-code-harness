# Industrial Engine Vertical Slice Design

- Status: Accepted by user
- Date: 2026-08-12
- Repository: `open-code-harness`
- Authority: Normative
- Chinese reading copy: `2026-08-12-engine-vertical-slice-design.zh-CN.md`
- Depends on: `2026-08-11-open-code-harness-architecture-design.md`
- Builds on: `docs/architecture/domain-events.md`

## 1. Positioning

Open Code Harness is an industrial-grade open-source code-agent harness. This
milestone is intentionally narrow, but it is not a demo, tutorial, prototype,
or disposable integration. "Minimal" limits the number of capabilities delivered
at once; it does not relax consistency, cancellation, concurrency, bounded-resource,
error, testing, or operability requirements.

The repository is still pre-v0. Industrial-grade is the engineering target and
delivery discipline, not a claim that the current repository is already a
general availability production release.

This milestone delivers the first executable application/Engine slice. It runs
one model-backed Turn through the existing domain state machine, records durable
facts atomically, exposes bounded streaming progress, terminates every started
object explicitly, and proves the result by deterministic replay. A scripted
model and in-memory event store are formal adapters used for deterministic
verification; they are not alternate demo-only code paths.

## 2. Goal and success definition

The milestone is complete when a headless caller can:

1. create or load a Session;
2. start exactly one Turn using optimistic concurrency control;
3. run one provider-neutral streaming model attempt;
4. observe ordered runtime progress with backpressure;
5. durably record the assistant message terminal fact;
6. complete, fail, or interrupt the Turn exactly once;
7. replay the stored event stream into the same final state; and
8. reproduce success, failure, cancellation, conflict, and output-boundary
   scenarios without a network connection, API key, TUI, ACP client, tool, or
   wall-clock dependency.

This is a **Minimal Executable Turn Runner**, not yet a complete tool-using
Agent Loop. Naming must remain honest until model → tool → result → model
iteration exists.

## 3. Evidence review

The design treats official repositories, official documentation, and
language/runtime documentation as primary evidence. A community project may be
included only as explicitly labeled, non-authoritative context and cannot by
itself establish a contract. A useful idea is adopted only when it supports the
Open Code Harness charter; reference projects are not dependency or API sources.

### 3.1 Pi agent core

Pi keeps its core loop small and injectable. Its loop receives a streaming
function, tool set, context transformation hooks, cancellation signal, event
sink, and steering/follow-up queues. It can execute tool calls sequentially or
in parallel while publishing a detailed in-memory event stream.

Adopt:

- a small Engine core with injected model behavior;
- explicit cancellation propagation;
- streaming lifecycle events;
- deterministic test injection rather than provider-specific conditionals.

Do not copy:

- treating the in-memory message list as the durable domain authority;
- mixing future Tool/Policy concerns into the first model-only slice;
- relying on an event emitter alone for consistency or recovery.

Evidence: <https://github.com/badlogic/pi-mono/blob/main/packages/agent/src/agent-loop.ts>

### 3.2 MiniMax

The public `MiniMax-AI/minimax-code` repository is an issue-report repository
for a desktop product and does not expose the product implementation. No claim
about MiniMax Code internals is made from that repository.

The official open-source Mini-Agent combines a complete model/tool loop,
persistent notes, context summarization, Skills, MCP, and request/tool logging.
It demonstrates a useful runnable reference and a direct path from model stream
to tool execution, but its concern set and MiniMax-oriented API path are broader
than this milestone.

Adopt:

- end-to-end runnable verification;
- complete request, response, and execution evidence;
- deterministic testing alongside the user-facing path.

Do not copy:

- coupling the Engine milestone to one provider-compatible API;
- adding memory, Skills, MCP, context compaction, and tools before their
  contracts and evaluation boundaries exist.

Evidence:

- <https://github.com/MiniMax-AI/minimax-code>
- <https://github.com/MiniMax-AI/Mini-Agent>

### 3.3 Kimi Code

Kimi Code separates CLI/TUI, server, client SDK, provider abstraction,
execution environment, transcript, storage, and agent engines in a TypeScript
monorepo. Its current architecture includes a DI × Scope engine with App,
Workspace, Session, and Agent lifecycles, plus a server and sequenced transcript
operations.

Adopt:

- UI and server surfaces must consume an Engine contract rather than own the
  loop;
- transcript/runtime signals need explicit sequence and lifecycle semantics;
- execution-environment and provider abstractions remain outside the domain.

Do not copy now:

- a general DI container or four-level lifecycle scope framework;
- dual engine generations, server endpoints, SDK facades, and transcript
  replication before the first application boundary is proven;
- package decomposition without a current consumer.

Evidence: <https://github.com/MoonshotAI/kimi-code/blob/main/AGENTS.md>

### 3.4 OpenAI Codex

Codex app-server exposes Thread → Turn → Item primitives and explicit
`started → delta* → completed` notifications. It uses generated protocol
schemas, bounded queues, overload errors, cancellation, approvals, and separate
transport surfaces.

Adopt:

- every streamed object has one explicit terminal state;
- final Item facts and transient deltas have different responsibilities;
- bounded flow control is a correctness requirement;
- cancellation completes lifecycle state rather than merely stopping output.

Do not copy now:

- JSON-RPC methods, transport servers, approvals, pagination, or public schema;
- app-server protocol objects as internal domain objects.

Evidence: <https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md>

### 3.5 Maka

Maka defines one Runtime Host as the execution authority. Model messages, tool
calls, tool results, and termination facts enter a Runtime Event Log; Session,
context, UI, and recovery are projections. Its evaluation subjects also execute
through the same Runtime instead of a benchmark-only shortcut.

Adopt:

- one execution authority for Session/Turn/model lifecycle;
- durable facts are not rewritten by context pruning or UI projections;
- deterministic evaluation uses the real Engine boundary;
- append and replay are core correctness paths.

Do not copy now:

- SQLite operational storage, Runtime Host process, Graph control plane,
  desktop composition, or production recovery implementation;
- the full evaluation platform before the Engine contract exists.

Evidence: <https://github.com/maka-agent/maka-agent/blob/main/ARCHITECTURE.md>

### 3.6 DeepSeek-Reasonix (community, non-authoritative context)

DeepSeek-Reasonix is a community project, not an official DeepSeek repository,
and is non-authoritative context here. It deliberately optimizes for DeepSeek:
stable prompt prefixes for cache
hits, model-specific tool-call repair, flash/pro cost routing, result pruning,
and parallel-safety annotations.

Adopt later:

- provider capability profiles;
- trace-driven optimization and A/B evaluation;
- explicit cost, retry, cache, and parallel-safety evidence.

Do not copy into the neutral Engine:

- provider-name branches;
- DeepSeek repair heuristics;
- cache layout, escalation, or compaction policies without a provider/context
  contract and benchmark evidence.

Evidence: <https://github.com/esengine/DeepSeek-Reasonix/blob/v1/docs/ARCHITECTURE.md>

### 3.7 Result of the review

The selected combination is:

- Maka's single execution authority and fact/projection separation;
- Pi's small injected loop and cancellation model;
- Codex's explicit lifecycle and bounded-flow discipline.

Kimi's service and lifecycle decomposition is a later-stage reference.
Reasonix's model-specific optimizations belong in future provider and context
profiles. Mini-Agent remains a runnable integration reference, not the core
architecture template.

## 4. Architectural decisions

### 4.1 Application service is the only command authority

Headless tests, future CLI, ACP, TUI, and evaluation callers invoke the same
application service. No adapter may call a model and then manufacture domain
events independently.

The initial application surface contains:

```text
CreateSession
RunTurn
CloseSession
```

`RunTurn` delegates model streaming to `TurnRunner`, but the application layer
owns loading, replay, command identity, optimistic concurrency, durable append,
and final result mapping.

### 4.2 Interfaces live with their consumers

The code does not create a generic `ports` package. Go interfaces live in the
package that consumes them:

- application package: `EventStore`, `Clock`, `IDGenerator`;
- engine package: `Model`, `ModelStream`, `RuntimeSink`.

Adapters implement these interfaces without the consumer importing adapter
types. This avoids an abstract-interface catalog detached from real use cases.

### 4.3 ScriptedModel is a formal adapter

`ScriptedModel` implements the exact `engine.Model` interface used by future
providers. Production code contains no `if scripted`, test mode, environment
flag, or alternate execution path.

A script can:

- assert the complete expected request;
- emit ordered text deltas;
- finish normally;
- fail before streaming;
- fail after one or more deltas;
- block until cancellation; and
- record calls safely for concurrent assertions.

### 4.4 MemoryEventStore is a contract implementation

The in-memory store is mutex-protected and implements the same append contract
required of future file or database stores:

```go
type EventStore interface {
    Load(ctx context.Context, sessionID domain.SessionID) ([]domain.RecordedEvent, error)
    Append(ctx context.Context, request AppendRequest) ([]domain.RecordedEvent, error)
}

type AppendRequest struct {
    SessionID      domain.SessionID
    ExpectedVersion uint64
    CommandID      domain.CommandID
    Events         []domain.Event
}
```

The final implementation plan may refine Go names, but not these semantics:

- compare-and-swap on `ExpectedVersion`, defined as the exact length of the
  authoritative contiguous recorded-event stream for that Session;
- contiguous sequences assigned by the store;
- metadata generated from injected ID and clock sources;
- one `Clock.Now()` value normalized to UTC and shared by every record in one
  append request;
- one append request is all-or-nothing;
- returned and loaded records are defensive copies;
- cancellation before commit writes nothing;
- no automatic retry of a conflicting command; and
- deterministic fault injection can fail before commit without partial state.

For this milestone, the port additionally guarantees that a non-nil `Append`
error means the requested batch did not commit. Once a batch commits, the
adapter returns the committed records even if caller cancellation races after
the commit point. `Application` accepts a returned batch only when its count,
overflow-safe contiguous sequences, Session/Command IDs, record validity,
event IDs, one shared non-zero UTC occurrence time, exact event types,
payloads and order all match the request. Ordered Apply must succeed and the
final Version must equal `ExpectedVersion + N`. Any metadata/event mismatch,
Apply failure, or final-version mismatch is
`internal/store_contract_violation`; an apply cause remains unwrap-able. This
check fails closed but cannot undo a Store that committed and then lied about
its result.

An in-process `MemoryEventStore` can implement this no-ambiguous-error rule. A
future remote Store that can commit while losing its acknowledgement cannot
honestly implement the current port: it must add stable exact-retry batch
identity or an explicit unknown-commit outcome before it is admitted as a
production adapter.

`Load` returns the complete authoritative stream in this milestone. An absent
stream is an empty result, and the Application use case decides whether that
means `session_not_found`. `Append` success returns exactly the newly committed
batch. A successful batch of `N` events advances the stream from
`ExpectedVersion` to `ExpectedVersion + N`.

Snapshots, indexes, transcript models, and UI state are future discardable
projections. They must never determine CAS acceptance, recorded sequence, or
authoritative version. Event IDs are opaque uniqueness tokens: a late
pre-commit failure may consume IDs that never become records. This does not
violate atomicity; committed record sequences remain gapless and Store state is
unchanged. The port does not promise transactional or gap-free ID/clock sources.

### 4.5 Durable facts and runtime signals are separate

Durable domain events reconstruct authoritative state. Runtime signals support
live consumers and may be coalesced or discarded according to an adapter's
documented policy.

```text
Durable facts                       Runtime signals
────────────────────────────────    ─────────────────────────────
turn.started                        model.stream.started
assistant.message.started           model.text.delta
assistant.message.completed         model.stream.completed
assistant.message.failed            model.stream.failed
assistant.message.interrupted       model.stream.interrupted
turn.completed                      append.completed
turn.failed
turn.interrupted
```

Text deltas are not persisted in this milestone. The terminal assistant message
contains the final exact text. Context pruning, future compaction, and UI
rendering must not rewrite this fact.

### 4.6 Minimal Item lifecycle extends the domain

The existing domain is extended with the smallest durable assistant-message
Item needed for a truthful executable Turn:

```text
absent --assistant.message.started--> running
running --assistant.message.completed--> completed
        --assistant.message.failed-----> failed
        --assistant.message.interrupted-> interrupted
```

Rules:

- an Item belongs to exactly one Turn;
- only the active running Turn may start an Item;
- at most one assistant-message Item runs in this milestone;
- an Item has exactly one terminal event;
- a Turn cannot become terminal while its Item remains running;
- successful final text is durable and preserved byte-for-byte;
- Item identity and lifecycle are generic, but payload is a closed,
  kind-specific domain type rather than a flattened bag of fields;
- failed/interrupted Items store a required stable machine code and an optional
  safe display message, never a provider-native error object;
- running and completed Items have no terminal metadata; failed/interrupted
  Items have terminal metadata and do not persist partial assistant text;
- `ActiveItemID`, Item order, Item map keys, ownership, payload kind, timestamps,
  and status must be mutually consistent before and after every transition; and
- maps, slices, payloads, and terminal metadata retain the existing domain
  immutability rules.

The domain represents payloads as a closed `ItemPayload` sum. The only payload
in this milestone is `AssistantMessagePayload`; future kinds add their own
payload type instead of adding sparse fields to every Item. `caller_canceled`
and `runtime_delivery_failed` are the initial stable interruption codes.

Application admission uses one composite domain command,
`StartAssistantTurn{SessionID, TurnID, ItemID, Input}`. It validates the complete
known pre-effect transition and returns exactly `TurnStarted` followed by
`AssistantMessageStarted` as one decision batch. The lower-level commands may
remain domain building blocks for compatibility, but Application never exposes
a split Turn-start/Item-start durable branch.

`ModelAttempt`, usage accounting, tool Items, reasoning Items, and images remain
future domain extensions. Deferring them is a scope choice, not permission to
store provider-specific data in the assistant-message Item.

The recorded-event `schemaVersion: 1` versions the envelope and strict payload
encoding; it is not a frozen event-type catalog. New internal pre-v0 event
types may be recognized under schema version 1 only when existing event bytes
and replay meaning remain compatible. The existing Session fixture must still
decode, replay equivalently, and re-marshal each record byte-for-byte.

### 4.7 The core is synchronous and naturally backpressured

`RunTurn` is synchronous. It consumes a pull-style `ModelStream` and calls a
`RuntimeSink` inline. The core creates no unbounded channel and no detached
goroutine.

The sink contract receives `context.Context` and must return promptly when the
context is canceled. A future ACP/server adapter may place a bounded queue
outside the Engine, with an explicit overload policy. Transport backpressure is
not hidden inside the domain.

### 4.8 Output is bounded

The runner requires an explicit `MaxAssistantBytes` configuration. It rejects
or fails the Turn when accumulated output would exceed the bound. The default,
configuration ownership, and stable error code are fixed in the implementation
plan and tests; no path may accumulate unlimited model output.

The byte limit is evaluated before appending a delta to the accumulator. Valid
UTF-8 is required at the model boundary, and no accepted text is normalized.

### 4.9 Runtime event ownership and validation

Callers emit a payload, not an envelope. A run-scoped `Emitter` exclusively
owns correlation and ordering: it stamps the complete correlation tuple and a
strictly increasing ordinal before each sink attempt. A failed attempt consumes
its ordinal; ordinals never roll back or get reused. An Emitter is single-run,
non-copyable, and not safe for concurrent use. Separate Emitters may call a
thread-safe sink concurrently.

Runtime payload validation is centralized before ordinal allocation. Started,
completed, and `append.completed` carry neither text nor code. A text delta is
non-empty valid UTF-8 and carries no code. Failed and interrupted payloads carry
a required stable code and no text. A stable runtime code is 1–64 ASCII bytes,
begins with `a`–`z`, and thereafter contains only `a`–`z`, `0`–`9`, or `_`.
Unknown event types are caller contract errors. `diagnostic` is deferred until
a consumer and redaction contract exist.

Emitter validates the payload, checks `ctx.Err()`, allocates the ordinal, then
attempts the sink in that exact order. Validation failure or pre-attempt
cancellation consumes no ordinal; cancellation returns `canceled`. A sink
attempt always consumes its ordinal. If the context is canceled when the sink
returns an error, `canceled` is primary; otherwise the result is `delivery`.

### 4.10 Model stream ownership and cleanup

The Engine becomes cleanup owner of every non-nil stream returned by
`Model.Stream`, including the `(stream, error)` case, and calls `Close` exactly
once on every exit. `(nil, nil)` is an invalid stream; `(nil, error)` is startup
failure; `(stream, error)` is startup failure plus owned cleanup. If `Next`
returns both an event and an error, the event is ignored. Context cancellation
wins over a concurrent provider error; premature `io.EOF` is invalid; other
`Next` failures are model-stream failures. Explicit completion is terminal and
the runner performs no extra `Next` call.

On non-success, the Engine cancels its derived stream context before closing;
on success it observes completion, closes, then cancels. Close is synchronous
and must promptly join provider-owned background work. A close-only failure is
`model_stream`. When cleanup also fails after a primary error, the primary
stable code is preserved and the causes are joined inside one Engine error.

For each delta the exact order is: reject empty text, validate UTF-8, enforce
the byte limit, emit to the sink, then append to the result builder. An invalid,
undelivered, or over-limit delta is never accumulated. Exact-limit output is
valid, and the Engine does not trim, normalize, or re-chunk text. Provider
adapters must not split a UTF-8 code point across stream events.

## 5. Component layout

The expected dependency shape is:

```text
headless caller / future adapters
              │
              ▼
internal/harness/application
  Service · use cases · EventStore · Clock · IDGenerator
              │
       ┌──────┴──────────┐
       ▼                 ▼
internal/harness/engine  internal/harness/domain
  TurnRunner · Model       commands · events · replay
  ModelStream · RuntimeSink
       ▲
       │ implements
internal/harness/adapters/memory
  MemoryEventStore

internal/harness/testkit
  ScriptedModel · FixedClock · SequenceIDs · RecordingSink
```

Package names may be refined for Go conventions during planning, but dependency
direction may not invert. Domain imports neither application nor engine. Engine
does not import memory adapters, ACP, TUI, provider SDKs, persistence libraries,
or testkit.

## 6. Turn execution flow

`RunTurn` has four irreversible phases:

| Phase | Durable boundary | Context | Required outcome |
| --- | --- | --- | --- |
| Preflight | none | caller context | failure returns no records and invokes no model |
| Admission | atomic started Turn + Item | caller context | success establishes the only legal model-call boundary |
| Execution | runtime started/deltas only | caller context | Engine owns stream cancellation and `Close` |
| Terminalization | atomic terminal Item + Turn | caller context for success; bounded detached context for failure/interruption | exactly one terminal batch, or an explicit running persistence/conflict result |

The exact pre-admission order is: validate request and typed-nil dependencies;
Load the complete stream; Replay and validate authoritative state; call the
pure domain `CheckStartAssistantTurnEligibility`; generate Turn, Item, and
Command IDs; validate every generated ID; construct the one run-scoped Emitter;
Decide `StartAssistantTurn`; append its atomic admission batch; then invoke
`TurnRunner`. The eligibility predicate has a finite scope: Session existence,
active status, full structural validity, and absence of a running Turn or Item.
It does not validate the not-yet-generated IDs or request input. Domain
`Decide(StartAssistantTurn)` calls the same predicate before its command-field
checks; Application never duplicates those invariants. Missing, closed,
corrupt, or already-running Sessions therefore consume no run IDs. Invalid IDs
returned with nil source errors and Emitter construction failures cannot strand
a running Turn.

### 6.1 Successful Turn

```text
caller
  → application.RunTurn
  → EventStore.Load
  → domain.Replay
  → domain.CheckStartAssistantTurnEligibility
  → validate generated Turn/Item/Command IDs
  → construct one engine.Emitter
  → domain.Decide(StartAssistantTurn)
  → EventStore.Append(expectedVersion)          [turn.started,
                                                 assistant.message.started]
  → Model.Stream
  → RuntimeSink(model.stream.started)
  → RuntimeSink(model.text.delta)*
  → EventStore.Append atomically                [assistant.message.completed,
                                                 turn.completed]
  → RuntimeSink(append.completed)
  → RuntimeSink(model.stream.completed)
  → RunTurnResult
```

Admission is one atomic batch: `StartAssistantTurn` decides, in order,
`turn.started` and `assistant.message.started`; both records share one command
ID and occurrence timestamp and advance the loaded version by two. If admission
fails or cancellation wins before commit, neither event is visible and the
model is not called.

The terminal Item and Turn events are also appended in one atomic batch. A caller
must never observe `turn.completed` without the final message fact. The Item
terminal fact precedes the Turn terminal fact, and both records share one
command ID and one occurrence timestamp.

### 6.2 Model startup failure

If the model fails before yielding a stream, the Engine returns a stable error
to Application. Application then atomically appends:

```text
assistant.message.failed
turn.failed
```

The error is normalized into a stable Engine category. Raw provider payloads do
not enter domain state.

### 6.3 Mid-stream failure

Previously emitted deltas remain runtime observations only. Application maps
the Engine result and atomically appends the failed Item and Turn terminal
events. No partial assistant text is represented as a completed message.

### 6.4 Cancellation

Cancellation is checked at every boundary and the original caller context is
passed to the model and RuntimeSink. Once admission commits, an Engine failure,
caller cancellation, or pre-terminal delivery failure terminalizes with a
bounded cleanup context created in the same stack frame:

```go
cleanupBase := context.WithoutCancel(ctx)
cleanupCtx, cancel := context.WithTimeout(cleanupBase, s.config.TerminalCommitTimeout)
defer cancel()
```

This detached context is used only for the failure/interruption append. It is
never used for model work, retries, ordinary success, or RuntimeSink delivery.
Cancellation before admission writes nothing. A successful terminal commit
wins over cancellation observed afterward.

### 6.5 Append failure

If admission fails, the model is never called. Model success is checked against
caller cancellation before the completed batch and uses the caller context.
If that append returns records, durable completion wins. If it fails while the
caller context is canceled, the no-ambiguous-error rule authorizes one bounded
attempt to append the interrupted pair. A different persistence failure or any
conflict does not invent a second terminal outcome. Any terminal append failure
returns the known admission records and a running result with
`TerminalCommitted == false`; production reconciliation is a blocking
capability before general availability.

### 6.6 Runtime sink failure

The sink is part of the required execution path in this milestone. If it fails
before terminal commit, Engine cancels and closes the model stream, then returns
the stable error to Application. Application attempts to append interrupted
Item/Turn facts with a stable runtime-delivery reason. If delivery fails or the
caller cancels after terminal commit, durable success remains authoritative and
the returned result carries a delivery warning/error distinct from execution
state.

The implementation plan must make this distinction explicit and prevent a sink
failure from rewriting an already committed terminal fact.

### 6.7 Result and error algebra

`RunTurn` deliberately returns a value together with an error. `Records` is a
defensive, ordered concatenation of every batch this call knows committed: two
admission records after admission and two more after terminalization. `Text` is
exactly the completed output, including a valid empty output; failed and
interrupted results have empty text and never expose partial deltas as final
text.

| Outcome | Result status/text | `TerminalCommitted` | Error category |
| --- | --- | --- | --- |
| completed and delivered | completed / exact text | true | nil |
| completed, terminal delivery failed or suppressed | completed / exact text, warning | true | delivery |
| model startup/stream/close failure, terminal committed | failed / empty | true | model |
| invalid provider stream, terminal committed | failed / empty | true | model (`invalid_stream`) |
| output limit, terminal committed | failed / empty | true | output_limit |
| caller cancellation, terminal committed | interrupted / empty | true | canceled |
| pre-terminal sink failure, interruption committed | interrupted / empty | true | delivery |
| admission/load/terminal persistence or conflict failure | absent or running / empty | false | persistence, conflict, or internal |
| request validation failure | zero result | false | validation |

Before accepted admission, every failure returns the zero `RunTurnResult`:
zero IDs/status, empty text/records, false terminal flag, and nil warning. After
accepted admission, every return carries Session/Turn/Item IDs, running status,
the two accepted admission records, empty text, false terminal flag, and nil
warning until a terminal batch is accepted. Accepted terminalization adds its
two records and sets failed/interrupted or completed plus the table's text and
terminal flag. `DeliveryWarning` is non-nil only for a failed or suppressed
post-terminal runtime delivery and preserves that cause; the returned
application error preserves the primary execution and terminalization causes
according to precedence.

Durable terminal facts authorize runtime terminal signals. Only after accepting
and applying the terminal batch may Application emit `append.completed` and
then exactly one of `model.stream.completed`, `model.stream.failed`, or
`model.stream.interrupted`. No terminal commit means no such signal. A later
delivery failure becomes `DeliveryWarning`; it is the returned category only
when no earlier execution error exists, otherwise the earlier category remains
primary and the delivery cause is joined. Absence of a post-cancel signal never
implies absence of a durable terminal fact.

```text
success:     model.stream.started, delta*, append.completed, model.stream.completed
failure:     model.stream.started?, delta*, append.completed, model.stream.failed
interrupted: model.stream.started?, delta*, append.completed, model.stream.interrupted
```

Error precedence is: a Store contract violation/conflict/persistence error that
prevents terminalization; then the original model/output/canceled/delivery
execution error; then a post-terminal delivery warning. When terminalization
fails, `errors.Join` preserves both execution and append causes while the outer
category describes the running durable boundary.

## 7. Concurrency and transaction semantics

- EventStore CAS is the authoritative same-Session concurrency control.
- Atomic admission CAS is the same-Session `RunTurn` linearization point. Two
  callers may load the same version and complete preflight, but only one admission can
  commit; the losing caller does not invoke the model.
- A call that loads an already-running Turn is rejected by `domain.Decide`
  before append and invokes the model zero times.
- `CloseSession` racing admission is resolved solely by CAS; only one valid
  append wins and neither path retries.
- Different Sessions may execute concurrently through one Service/TurnRunner;
  shared Model and RuntimeSink implementations obey the Tasks 5–6 concurrency
  contracts.
- No automatic command retry follows a CAS conflict, because repeating a user
  command may duplicate model cost or external work in later milestones.
- An append batch either commits every event in order or commits none.
- A conflict, already-canceled request, malformed request, or injected
  pre-commit fault performs no clock read and allocates no Event ID. A candidate
  append reads the clock exactly once; later validation failure may consume
  otherwise-unused opaque Event IDs but never advances the stream version.
- The MemoryEventStore must pass `go test -race` under same-Session conflicts
  and independent-Session parallelism.
- Tests use barriers/channels, never timing sleeps, to establish concurrency.
- A terminal conflict is not retried or rewritten as a model failure: the
  result is running/unknown from this call's local authority,
  `TerminalCommitted == false`, and the caller must reload.

## 8. Error model

Application/Engine errors are structured and preserve causes. At minimum they
distinguish:

| Category | Example | Retry ownership |
| --- | --- | --- |
| validation/domain | closed Session, blank input | caller must change request |
| conflict | expected version lost | caller reloads and decides explicitly |
| model | startup or stream failure | future policy; no automatic retry here |
| canceled | caller context canceled | caller decides whether to start a new Turn |
| output_limit | assistant output exceeded bound | caller/configuration policy |
| delivery | required runtime sink failed | adapter/caller |
| persistence | load or append failed | storage/recovery policy |
| internal | invariant or impossible stream sequence | operator/developer |

Every error states whether a durable terminal event was committed. Error-message
prose is not a compatibility contract; stable category/code and typed fields are.

Caller input and `domain.Decide` rejection are validation errors; an empty load
is `validation/session_not_found`; ID source failure is
`internal/id_generation_failed`; a syntactically invalid ID returned with nil
error is `internal/id_generator_contract_violation`. Replay or Apply failure on
Store-supplied records and any append-return mismatch are
`internal/store_contract_violation`, never caller validation. Ordinary
Load/Append dependency errors are persistence, and every
`VersionConflictError`, including cleanup, is conflict. A dependency error that
wraps `context.Canceled` remains a dependency error unless the supplied context
is actually canceled.

Application `Error`, `IsCategory`, and `VersionConflictError` are nil-safe and
traverse all branches of wrapped/joined errors, including later siblings and
direct or joined typed-nil values.

One generated `CommandID` correlates both RunTurn batches and every runtime
event in the invocation. It is not an idempotency or Store-deduplication key;
because it spans two appends, `(SessionID, CommandID)` cannot identify one
append request. Service never automatically reloads, re-decides, retries an
append, or invokes the model again. A caller must not blindly retry an uncertain
response. Future public idempotency requires a separate caller-stable operation
identity, exact batch identity, and explicit return/resume/new-Turn semantics.

## 9. Deterministic adapters and contract suites

### 9.1 ScriptedModel

The model test adapter is reusable by Engine and future adapter contract tests.
It performs exact request assertions, records calls, supports deterministic
blocking/cancellation, and returns defensive copies of scripted data.

The recording sink stores every attempted event before applying its injected
one-shot ordinal failure. Failed calls appear in `Attempts` but not in
`Delivered`; successful calls appear in both. Both snapshots are defensive.
One recording sink may be shared by separate Emitters concurrently only when
ordinal failure injection is disabled; a nonzero run-local failure ordinal is a
single-Emitter fixture. A test must not drive one Emitter concurrently.

### 9.2 MemoryEventStore

The store supports deterministic metadata, CAS conflicts, atomic batches,
per-Session one-shot load/append fault injection, defensive-copy assertions,
and concurrent access. Contract tests cover an absent stream, append return
shape, load-fault consumption/isolation, and defensive copies. Adapter-specific
tests use counting/failing sources to prove nil-dependency rejection, one clock
read on success, zero source calls for failures rejected before candidate
construction, and unchanged records/version when Event ID or clock generation
fails.

### 9.3 Shared contract suites

Future implementations must pass reusable suites, conceptually:

```text
eventstoretest.Run(factory)
modeltest.Run(factory)
enginescenariotest.Run(harness)
```

The exact Go package structure is set by the implementation plan. The required
principle is that a production adapter cannot claim compatibility using only
its own bespoke tests.

## 10. Verification matrix

The implementation plan must include at least these cases:

### Normal behavior

- create, run, complete, close;
- multiple sequential Turns in one Session;
- multi-delta UTF-8 output preserved exactly;
- final state and assistant Item rebuilt by replay;
- identical scripted inputs produce equivalent traces after normalizing injected
  IDs and timestamps.

### Validation and model failure

- blank or invalid request input;
- model startup failure;
- failure before the first delta;
- failure after one or more deltas;
- invalid UTF-8 model output;
- empty text delta is invalid and is neither emitted nor accumulated;
- empty successful output, with an explicit chosen contract;
- output exactly at the byte limit and one byte over it;
- invalid model stream event order;
- `(nil, nil)`, `(stream, error)`, and `Next(event, error)` combinations;
- exactly one stream close on every non-nil-stream exit, including sink failure;
- close-only failure and primary-plus-close error precedence.

### Cancellation and delivery

- cancellation before any append;
- cancellation before atomic admission and immediately after admission;
- cancellation during streaming;
- cancellation racing with model completion;
- sink failure before and after terminal commit;
- every started Item and Turn reaches at most one terminal state.

### Storage and concurrency

- load failure;
- atomic admission failure prevents model invocation and exposes no partial start;
- terminal batch failure never reports Turn success;
- fault injection proves no partial batch append;
- same-Session concurrent RunTurn has one winner and one conflict;
- 32 independent Sessions complete without races;
- loaded and returned records cannot mutate store state;
- `go test -race ./... -count=1` passes.
- stable Engine-code matching traverses complete joined error trees and remains
  safe for direct or joined typed-nil errors.
- generated-ID source errors and nil-error invalid IDs occur before admission;
- exact append-return rejection covers count, overflow, metadata, event
  type/payload/order, shared UTC time, ordered Apply success, and final Version;
- Application category/conflict matching traverses nested joins, a later
  matching sibling, and direct/joined typed nils;
- barrier races cover Load, admission commit, terminal entry, and terminal
  return; a running boundary remains replayable after terminal persistence,
  conflict, or Store-contract failure, with no hidden retry.

### Repository boundaries

- domain imports no application, engine, adapter, provider, ACP, MCP, or TUI;
- engine imports no concrete adapter or provider SDK;
- no production code branches on ScriptedModel;
- no third-party dependency is added without a documented decision and need;
- normal, focused, race, formatting, vet, and diff checks are CI gates.

## 11. Observability and evaluation evidence

This milestone does not add OpenTelemetry, but its RuntimeEvent envelope must
carry correlation fields needed later:

```text
session_id
turn_id
item_id when applicable
command_id
monotonic event ordinal within RunTurn
runtime event type
```

No secret model input/output is logged automatically. The RecordingSink used in
tests receives explicit test data. Future content telemetry remains opt-in and
redacted by default.

Completion evidence includes:

- an exact scenario trace fixture;
- fault-matrix results;
- contract-suite results;
- normal and race test outputs;
- dependency-boundary checks;
- replay equivalence evidence; and
- a list of deliberately deferred production capabilities.

## 12. Security and resource baseline

- No network, filesystem, shell, MCP, or tool permission is introduced.
- Context cancellation reaches model and sink calls.
- Assistant output has an explicit byte bound.
- The core creates no unbounded queue or detached goroutine.
- Script and store adapters are safe for concurrent test use.
- Error values do not automatically expose raw provider payloads or secrets.
- IDs, timestamps, and all text retain the domain's strict validation rules.

## 13. Explicit exclusions

The following are separate future specifications:

- real model Provider contract and adapter;
- retry, rate-limit, authentication, capability, usage, and cost policy;
- production JSONL, file, SQLite, or remote EventStore;
- crash reconciliation, checkpoint, migration, backup, and recovery;
- tool calls, Tool Runtime, Policy, approvals, and workspace sandboxing;
- ACP, JSON-RPC server, TUI, IDE, and public SDK;
- Context Engine, prompt construction, compaction, and cache policy;
- MCP, Skills, memory, subagents, and multi-agent graphs;
- OpenTelemetry and full scenario-evaluation infrastructure.
- diagnostic runtime events before a consumer and redaction contract exist.

These exclusions keep dependency order correct. They do not lower the quality
requirements of the Engine, event-store contract, Item lifecycle, or
deterministic adapters delivered here.

Process death after admission and terminal persistence/conflict/contract
failures can leave a durable running boundary. This milestone performs no
startup repair, continuation, or blind retry. The boundary must remain
inspectable through returned records and Replay, and production reconciliation
is explicitly a GA-blocking capability rather than a silently repaired state.

## 14. Rejected alternatives

### 14.1 Demo-only direct model call

Rejected because it would bypass formal ports, durable output facts, failure
semantics, and reusable adapter tests, forcing a rewrite at the first provider.

### 14.2 Full tool-using loop now

Rejected because Tool/Policy/approval state machines have not been designed.
Adding them would make failures impossible to attribute to the Turn Runner or
tool subsystem independently.

### 14.3 Runtime Host and public protocol now

Rejected because server, transport, subscription, schema, and persistence
decisions would dominate the first Engine contract. The application boundary is
designed so a Runtime Host can be added without moving execution authority.

### 14.4 Generic DI/container framework

Rejected until multiple real compositions demonstrate a need. Constructors and
consumer-owned interfaces are sufficient for this slice.

### 14.5 Persist every token delta

Rejected because deltas are high-volume delivery signals, not necessary final
facts. The terminal assistant message is durable; future transcript persistence
may define coalescing or journaling separately.

### 14.6 Provider-specific optimization in Engine

Rejected because cache, repair, retry, and cost behavior vary by provider and
model capability. Such behavior requires provider contract tests and benchmark
evidence before adoption.

## 15. Acceptance criteria

The design is ready for implementation planning only after:

- the industrial positioning and pre-v0 maturity statement are accepted;
- the single execution authority and package dependency direction are accepted;
- the minimal Item lifecycle and durable/runtime separation are accepted;
- CAS and atomic append semantics are accepted;
- synchronous bounded streaming and sink-failure semantics are accepted;
- the error taxonomy and no-automatic-retry rule are accepted;
- the verification matrix is considered sufficient; and
- every exclusion has an identified future milestone.

Implementation is complete only when all required contract, scenario, failure,
concurrency, race, replay, boundary, and documentation checks pass with a clean
worktree and an independent review reports no open critical or important defect.
