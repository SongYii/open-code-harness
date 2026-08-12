# Tasks 3–4 Architecture Gate: Application and EventStore Boundary

- Date: 2026-08-12
- Scope: Engine plan Tasks 3–4; load-bearing implications for Tasks 7–10
- Verdict at review: **READY_WITH_AMENDMENTS**
- Implementation state at review: not started

## Evidence

| Project | Primary evidence | Relevant behavior | Decision for Open Code Harness |
| --- | --- | --- | --- |
| OpenAI Codex | [`codex-rs/app-server/README.md`](https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md) | A Turn contains persisted typed Items while clients receive `item/*` and `turn/*` notifications; realtime events are explicitly separate from persisted Thread Items and clients may opt out of selected notifications. | Adopt durable facts versus delivery signals. Do not infer CAS or atomic append semantics that the public app-server contract does not state. |
| Maka | [`ARCHITECTURE.md`](https://github.com/maka-agent/maka-agent/blob/main/ARCHITECTURE.md) | Runtime Host is the single execution authority; Runtime Event Log is canonical for messages, tool calls, results, and termination facts; context, UI, recovery, and compaction are projections. | Adopt one Application command authority, canonical replayable facts, and non-authoritative projections. Maka's public architecture does not define expected-version CAS. |
| Kimi Code | [`AGENTS.md`](https://github.com/MoonshotAI/kimi-code/blob/main/AGENTS.md), [session storage](https://moonshotai.github.io/kimi-code/en/guides/sessions.html), [wire replay](https://moonshotai.github.io/kimi-cli/en/customization/wire-mode.html#replay) | Transcript contracts are isolated from engine imports and own per-scope op-batch sequencing; `wire.jsonl` is replayed in recorded order for recovery. | Adopt consumer-facing contract ownership, monotonic scoped ordering, and read-only replay. Defer subscriptions, catch-up cursors, and transcript replication. The evidence does not establish atomic EventStore batches or CAS. |
| Pi | [`session-manager.ts`](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/core/session-manager.ts) | Sessions are append-only JSONL trees; history is preserved while `buildSessionContext()` derives the model projection and compaction path. | Adopt append-only history and derived context. Do not copy synchronous file mutation, rewrite behavior, or its lack of an explicit optimistic-concurrency contract into an industrial store port. |
| MiniMax Mini-Agent | [`MiniMax-AI/Mini-Agent`](https://github.com/MiniMax-AI/Mini-Agent) | The project explicitly presents a minimal demo with a complete loop, persistent notes, context management, logging, and integration tests. | Use only as an end-to-end learning reference. Its public material does not support CAS, atomic batch, replay-authority, or defensive-copy decisions. |
| DeepSeek-Reasonix | [`docs/ARCHITECTURE.md`](https://github.com/esengine/DeepSeek-Reasonix/blob/v1/docs/ARCHITECTURE.md) | Provider-specific architecture separates immutable prefix, append-only log, and volatile scratch; log entries retain append order. | Adopt ordered append-only history and transient scratch separation. Reject provider-cache optimization in Application/EventStore. No CAS evidence is public. This is an `esengine` community project, not an official DeepSeek repository. |
| KurrentDB / EventStoreDB | [batch append and atomicity](https://docs.kurrent.io/server/v22.10/http-api/introduction#batch-append-operation), [expected revision and concurrency](https://docs.kurrent.io/clients/rust/legacy/v4.0/appending-events#handling-concurrency) | A client supplies the expected stream revision; mismatch fails, and a multi-event append succeeds or fails as one transaction. | Use as the industrial persistence alignment for exact expected-version CAS and atomic ordered batches. Keep our port smaller and provider-neutral. |

## Findings and required amendments

### 1. Define version and return shapes precisely

The direction was correct but “CAS on expected version” was underspecified. A
Session version is the number of records in its authoritative contiguous
stream. `ExpectedVersion` must equal that number. A successful batch of `N`
events advances the version to `ExpectedVersion + N`; `Append` returns exactly
that newly recorded batch. `Load` returns the complete authoritative stream in
this milestone. A missing stream is an empty result; Application use cases own
the decision to report `session_not_found`.

This follows the expected-revision shape proven by EventStoreDB without adding
its transport, database, global position, subscription, or idempotency surface.

### 2. State atomicity without inventing transactional generators

Store atomicity means no record and no version becomes visible before the final
assignment. It does not make injected Clock or IDGenerator implementations
transactional. Requests rejected by context, request validation, CAS, or an
injected pre-commit fault must consume neither clock nor IDs. A candidate append
reads the clock exactly once. A later replay or source failure may leave opaque
generated Event IDs unused; committed record sequences remain gapless because
they derive from committed stream length.

Requiring gap-free Event IDs would add a fake transaction protocol to the port
and make future UUID/ULID/database adapters harder without improving correctness.

### 3. Strengthen the reusable contract and adapter fault evidence

The contract suite must cover both declared one-shot controls. Add
`FailNextLoad`, prove per-Session isolation and one-time consumption, prove no
state change, then prove the next Load succeeds with a defensive copy. Also
assert that Append returns only the new batch and that an absent stream loads as
empty.

Memory-adapter tests, separate from the generic store suite, must use
counting/failing sources to prove:

- nil Clock/IDGenerator rejection without panic;
- exactly one clock read on successful append;
- zero clock/ID calls for rejection before candidate construction;
- unchanged records/version when the Nth Event ID or clock validation fails;
- preservation of wrapped causes through `errors.Is` and `errors.As`.

### 4. Keep replay authoritative and snapshots subordinate

Tasks 3–4 expose no snapshot port. Application reconstructs state by replaying
the complete stream. A future snapshot is a discardable projection/cache: it
cannot decide CAS acceptance, stream version, or recorded sequence. Codex,
Maka, Kimi, Pi, and Reasonix all provide evidence for separating history from
live/context projections; none justifies making a projection authoritative.

### 5. Preserve retry and error ownership

The Store never reloads, re-decides, or retries a conflict. A future retry must
be an explicit caller workflow: Load → Replay → Decide → Append, with explicit
command-ID/idempotency semantics. That is deferred because model calls and tools
will make blind retries costly or unsafe.

`VersionConflictError` remains typed and discoverable. Application maps it to
`CategoryConflict`; other Load/Append failures map to `CategoryPersistence`
while preserving their cause. Stable `Error()` output must never concatenate
raw injected/provider error text.

### 6. Keep persistence independent from notification backpressure

No EventStore subscription API is added. Codex notification opt-out, Kimi
transcript subscriptions, and Pi event streams demonstrate that delivery has a
different lifecycle from durable facts. RuntimeSink and future server adapters
own bounded delivery, catch-up, and backpressure; they cannot block, roll back,
or redefine an atomic append.

## Adopt, reject, defer

| Decision | Contract |
| --- | --- |
| Adopt | Application-owned ports; one command authority; full-stream replay; exact per-Session expected-version CAS; atomic ordered batch; one UTC batch timestamp; distinct opaque Event IDs; defensive copies; typed conflict; explicit retry ownership; deterministic one-shot faults. |
| Reject | Adapter-side Decide; internal CAS retry; snapshot/projection authority; persisted token deltas; subscriber success as commit semantics; gap-free/transactional ID-generator promises; inferring undocumented guarantees from another agent. |
| Defer | Production EventStore, idempotency keys, snapshots/checkpoints, migrations, subscriptions, catch-up cursors, backpressure, global ordering, multi-stream transactions, compaction, retention, and automatic retry policy. |

## Final Tasks 3–4 contract

Tasks 3–4 may begin only after the accepted design and English/Chinese plans
carry these amendments:

1. Define Session version, exact CAS, full Load, absent-stream, append-return,
   and version-advance semantics.
2. Define snapshots/projections as non-authoritative and absent from this port.
3. Define Store atomicity separately from allowed unused opaque IDs on late
   pre-commit failure.
4. Test zero source calls on early rejection and one clock read on success.
5. Cover one-shot Load faults and append return shape in the reusable suite.
6. Cover nil dependencies, failing ID sources, invalid clocks, cause chains,
   concurrency, and defensive copies in adapter tests.
7. Keep Store retry-free and EventStore free of subscription/backpressure APIs.

With these amendments incorporated, the gate is **READY**.
