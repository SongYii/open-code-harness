# Industrial Engine Vertical Slice Design

- Status: Proposed for user review
- Date: 2026-08-12
- Repository: `open-code-harness`
- Authority: Normative after approval
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

The design compares implementation evidence from official repositories and
primary technical documents. A useful idea is adopted only when it supports the
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

### 3.6 DeepSeek-Reasonix

Reasonix deliberately optimizes for DeepSeek: stable prompt prefixes for cache
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

- compare-and-swap on `ExpectedVersion`;
- contiguous sequences assigned by the store;
- metadata generated from injected ID and clock sources;
- one append request is all-or-nothing;
- returned and loaded records are defensive copies;
- cancellation before commit writes nothing;
- no automatic retry of a conflicting command; and
- deterministic fault injection can fail before commit without partial state.

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
turn.failed                         diagnostic
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
- failed/interrupted Items store stable reason data, not provider-native error
  objects; and
- maps/slices retain the existing domain immutability rules.

`ModelAttempt`, usage accounting, tool Items, reasoning Items, and images remain
future domain extensions. Deferring them is a scope choice, not permission to
store provider-specific data in the assistant-message Item.

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

### 6.1 Successful Turn

```text
caller
  → application.RunTurn
  → EventStore.Load
  → domain.Replay
  → domain.Decide(StartTurn)
  → EventStore.Append(expectedVersion)          [turn.started]
  → EventStore.Append(expectedVersion + 1)      [assistant.message.started]
  → Model.Stream
  → RuntimeSink(model.stream.started)
  → RuntimeSink(model.text.delta)*
  → EventStore.Append atomically                [assistant.message.completed,
                                                 turn.completed]
  → RuntimeSink(model.stream.completed)
  → RunTurnResult
```

The terminal Item and Turn events are appended in one atomic batch. A caller
must never observe `turn.completed` without the final message fact.

### 6.2 Model startup failure

If the model fails before yielding a stream, the Engine atomically appends:

```text
assistant.message.failed
turn.failed
```

The error is normalized into a stable Engine category. Raw provider payloads do
not enter domain state.

### 6.3 Mid-stream failure

Previously emitted deltas remain runtime observations only. The Engine appends
the failed Item and Turn terminal events atomically. No partial assistant text
is represented as a completed message.

### 6.4 Cancellation

Cancellation is checked before each irreversible boundary and passed to the
model stream and sink. Once the Turn and Item have started, a cancellation
attempts an atomic interrupted Item/Turn append. A successful interrupt commit
produces an interrupted result even if the provider reports an ordinary abort
error afterward.

Cancellation before the initial Turn append writes nothing. Cancellation after
a terminal commit cannot replace the terminal state.

### 6.5 Append failure

If the initial Turn append fails, the model is never called. If a terminal batch
append fails, the Engine returns a persistence failure and must not report model
success as Turn success. The event stream may remain at a running boundary;
production reconciliation is owned by the future persistence/recovery milestone.
The failure is explicit and testable rather than silently repaired.

### 6.6 Runtime sink failure

The sink is part of the required execution path in this milestone. If it fails
before terminal commit, the model stream is canceled and the Engine attempts to
append interrupted Item/Turn facts with a stable runtime-delivery reason. If it
fails after terminal commit, durable success remains authoritative and the
returned result carries a delivery warning/error distinct from execution state.

The implementation plan must make this distinction explicit and prevent a sink
failure from rewriting an already committed terminal fact.

## 7. Concurrency and transaction semantics

- EventStore CAS is the authoritative same-Session concurrency control.
- Two callers may load the same version, but only one initial Turn append can
  commit; the losing caller does not invoke the model.
- Different Sessions may execute concurrently.
- No automatic command retry follows a CAS conflict, because repeating a user
  command may duplicate model cost or external work in later milestones.
- An append batch either commits every event in order or commits none.
- The MemoryEventStore must pass `go test -race` under same-Session conflicts
  and independent-Session parallelism.
- Tests use barriers/channels, never timing sleeps, to establish concurrency.

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

## 9. Deterministic adapters and contract suites

### 9.1 ScriptedModel

The model test adapter is reusable by Engine and future adapter contract tests.
It performs exact request assertions, records calls, supports deterministic
blocking/cancellation, and returns defensive copies of scripted data.

### 9.2 MemoryEventStore

The store supports deterministic metadata, CAS conflicts, atomic batches,
load/append fault injection, defensive-copy assertions, and concurrent access.

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
- empty successful output, with an explicit chosen contract;
- output exactly at the byte limit and one byte over it;
- invalid model stream event order.

### Cancellation and delivery

- cancellation before any append;
- cancellation after Turn start and before model start;
- cancellation during streaming;
- cancellation racing with model completion;
- sink failure before and after terminal commit;
- every started Item and Turn reaches at most one terminal state.

### Storage and concurrency

- load failure;
- initial append failure prevents model invocation;
- terminal batch failure never reports Turn success;
- fault injection proves no partial batch append;
- same-Session concurrent RunTurn has one winner and one conflict;
- 32 independent Sessions complete without races;
- loaded and returned records cannot mutate store state;
- `go test -race ./... -count=1` passes.

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

These exclusions keep dependency order correct. They do not lower the quality
requirements of the Engine, event-store contract, Item lifecycle, or
deterministic adapters delivered here.

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
