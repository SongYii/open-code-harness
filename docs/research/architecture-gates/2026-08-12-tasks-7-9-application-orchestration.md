# Tasks 7–9 Architecture Gate: Application Orchestration

- Date: 2026-08-12
- Scope: Application plan Tasks 7–9; Session use cases and one durable `RunTurn`
- Evidence rule: official primary sources; explicitly labeled community projects may appear only as non-authoritative context
- Implementation state at review: Tasks 1–6 implemented; Tasks 7–9 not started
- Verdict: **READY**
- Chinese reading copy: `2026-08-12-tasks-7-9-application-orchestration.zh-CN.md`

## Review method and evidence boundary

This gate distinguishes three things throughout:

1. **Observed evidence** is a behavior or contract stated in an official source.
2. **Inference** is a conclusion for Open Code Harness; it is not attributed to
   the reference project.
3. **Local contract** is a guarantee we choose and must prove ourselves even
   when no reference project exposes the same guarantee.

The compared projects do not publish one common transaction model. In
particular, their use of the words session, turn, item, transcript, event, and
retry is not interchangeable with this repository's domain vocabulary.

## Primary-source evidence

| Project | Observed evidence | Inference for Open Code Harness |
| --- | --- | --- |
| OpenAI Codex | The [app-server contract](https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md) defines `Thread -> Turn -> Item`, explicit `turn/started` and terminal `turn/completed`, and `item/started -> delta* -> item/completed`. Interruption finishes with `turn/completed(status=interrupted)`, and clients are told to rely on that terminal notification. `clientUserMessageId` is echoed on the user item; the document does **not** claim it is an idempotency key. The [rollout recorder](https://github.com/openai/codex/blob/main/codex-rs/rollout/src/recorder.rs) serializes canonical items through one writer and offers an awaited flush; its documented idempotence concerns recorder materialization/retry, not exactly-once `turn/start`. | Adopt explicit terminal state, one lifecycle authority, ordered canonical recording, and durable-before-terminal-notification ordering. Do not infer command idempotency, CAS, or atomic multi-event Turn commits from Codex. |
| Kimi Code | The repository [package map](https://github.com/MoonshotAI/kimi-code/blob/main/AGENTS.md) separates app/server/SDK, agent engine, provider, execution environment, and transcript. The [transcript contract](https://github.com/MoonshotAI/kimi-code/blob/main/packages/transcript/AGENTS.md) owns idempotent projection operations, per-session/agent monotonic op-batch sequence, and cold rebuild from persisted `wire.jsonl`; it also labels live-only fields that cold rebuild cannot recover. | Adopt consumer-owned projection contracts, scoped sequence, and explicit live-versus-rebuildable evidence. Do not use transcript operation idempotence as proof of domain-command or EventStore idempotence. |
| Pi | The current [agent loop](https://github.com/earendil-works/pi/blob/main/packages/agent/src/agent-loop.ts) awaits lifecycle emission, carries an abort signal, and produces terminal agent/turn events. [AgentSession](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/core/agent-session.ts) is shared by interactive/print/RPC modes, persists completed messages from its event handler, waits for idle on abort, and owns auto-retry/compaction policy. | Adopt one application seam for all surfaces, awaited cancellation, and injected execution. Reject persistence as an incidental UI/event-listener side effect and keep automatic retry outside this milestone. |
| Maka | Maka is explicitly [log-first and projection-driven](https://github.com/maka-agent/maka-agent/blob/main/ARCHITECTURE.md): Runtime remains execution authority and committed events are fact authority. Its current [resume architecture](https://github.com/maka-agent/maka-agent/blob/main/docs/architecture/runtime-resume-architecture.md) separates repair, continuation, and retry; requires terminal semantic facts before terminal headers; uses fresh execution identities for continuation; rejects blind replay under uncertainty; and states that exact retries must match bytes and identity. It also uses short durable commits around, rather than one long transaction across, external side effects. | Adopt short pre-effect and post-effect commits, durable terminal facts before projections/signals, explicit uncertain/running states, and no blind retry. We do not adopt Maka's recovery subsystem in this milestone. |
| MiniMax | The public [MiniMax Code repository](https://github.com/MiniMax-AI/minimax-code) states that it only collects desktop-app issue reports, so it provides no implementation evidence. The official [Mini-Agent](https://github.com/MiniMax-AI/Mini-Agent) calls itself a demo; its [loop](https://github.com/MiniMax-AI/Mini-Agent/blob/main/mini_agent/agent.py) is bounded, checks cancellation at safe points, logs requests/results, catches provider errors, and returns user-facing error strings. | Retain bounded execution and runnable tests. Do not derive transaction, terminal durability, error normalization, or idempotency contracts from MiniMax Code or Mini-Agent. |
| DeepSeek-Reasonix | The community project's [architecture](https://github.com/esengine/DeepSeek-Reasonix/blob/main/docs/ARCHITECTURE.md) separates an immutable prefix, ordered append-only log, and volatile scratch; it keeps tool-result append order even when safe calls execute concurrently. It also embeds DeepSeek-specific repair, escalation, and cost policy. | Use append-only versus transient data as supporting evidence for durable facts versus deltas. Keep provider retry/repair/escalation out of Application. This is not an official DeepSeek repository and supplies no CAS or terminal-commit guarantee. |
| KurrentDB | The official [append documentation](https://docs.kurrent.io/clients/python/v1.3/appending-events) defines atomic batches, stream-version consistency checks, and idempotent retries only when the same consistency checks and event IDs identify the same append. It explicitly motivates this with the case where commit succeeds but the response is lost. | Keep exact CAS and atomic batches. A correlation `CommandID` alone is not an idempotency protocol. The current in-process port may rule out ambiguous outcomes, but a future remote store must add stable exact-retry identity or an explicit unknown-commit result. |
| Go standard library | [`context.WithoutCancel`](https://pkg.go.dev/context#WithoutCancel) retains values but removes cancellation, deadline, `Err`, and `Cause`; [`WithTimeout`](https://pkg.go.dev/context#WithTimeout) adds a new bound and requires its cancel function to be called. | A detached terminalization context is valid only when immediately re-bounded by `TerminalCommitTimeout` and canceled on every path. It is for durable cleanup, not permission to continue model or ordinary delivery work. |

## Findings and required amendments

### 1. Make admission one atomic batch

The current design and plan append `turn.started` and
`assistant.message.started` separately. Nothing external occurs between those
two writes. The split therefore creates an avoidable durable state in which a
Turn is running without its planned assistant Item. Task 9 then adds tests and
cleanup code solely for that artificial boundary.

Add one domain command, named `StartAssistantTurn` or equivalently, which
decides exactly these ordered events:

```text
turn.started
assistant.message.started
```

Application appends them in one CAS batch before invoking the model. The batch
shares one command ID and occurrence timestamp. If it fails or the caller is
canceled before commit, neither event is visible and the model call count is
zero. A successful batch moves the loaded version by two.

This is a local inference from the established atomic-batch contract. It is not
claimed as an implementation detail of Codex, Kimi, or Pi. It aligns with
Maka's general rule that a short commit should establish the complete known
pre-effect boundary before external work.

Consequences for the current plan:

- replace the two start appends in Task 8 with one admission append;
- delete the Task 9 branch "cancellation after `turn.started` but before Item
  start";
- replace its Item-start append-failure case with atomic admission failure;
- add the composite domain command and tests before implementing `RunTurn`.

### 2. Finish all reversible preflight before admission

The exact pre-admission order must be:

```text
validate request and typed-nil dependencies
  -> Load complete stream
  -> Replay and validate authoritative state
  -> domain.CheckStartAssistantTurnEligibility
  -> generate TurnID, ItemID, CommandID
  -> validate every generated ID
  -> construct the Emitter
  -> Decide(StartAssistantTurn)
  -> Append atomic admission batch
  -> call TurnRunner
```

In particular, `NewEmitter` must happen before the first durable append. An
invalid ID returned without an error by an `IDGenerator`, a typed-nil sink, or
an Emitter constructor failure must not strand a running Turn.

Request validation occurs before Load and before any ID call. After Replay and
before generating run IDs, Application calls the pure domain
`CheckStartAssistantTurnEligibility`. Its finite scope is Session
existence/active status, complete structural validity, and no running Turn or
Item; it does not inspect request input or not-yet-generated IDs.
`Decide(StartAssistantTurn)` calls the same predicate before command-field
validation, so Application does not duplicate domain invariants. A missing,
closed, corrupt, or already-running Session therefore consumes no IDs. Tests
reuse one eligibility table and counting sources to prove the ordering.

Error ownership must be precise:

- caller input or `domain.Decide` rejection -> `CategoryValidation`;
- empty loaded stream -> `CategoryValidation/session_not_found`;
- `ctx.Err() != nil` at a boundary -> `CategoryCanceled/canceled`;
- ordinary Load/Append dependency error -> `CategoryPersistence`;
- `VersionConflictError` at any append, including terminal cleanup ->
  `CategoryConflict/version_conflict`;
- Replay/Apply failure on records supplied by the Store ->
  `CategoryInternal/store_contract_violation`, never caller validation;
- ID source error -> `CategoryInternal/id_generation_failed`;
- syntactically invalid ID returned with nil error ->
  `CategoryInternal/id_generator_contract_violation`.

A dependency merely wrapping `context.Canceled` does not become caller
cancellation unless the supplied context is actually canceled. This prevents a
storage adapter from accidentally changing error ownership.

The same preflight discipline applies to Task 7 Session use cases:

- `CreateSession` validates `WorkspaceRoot`, generates and validates Session and
  Command IDs, constructs the pristine command, then appends at version zero;
  any source failure occurs before persistence;
- `LoadSession` validates the Session ID before Load, maps an empty stream to
  `session_not_found`, maps corrupt replay to `store_contract_violation`, and
  returns a deep defensive state copy;
- `CloseSession` validates the Session ID, loads/replays, decides whether close
  is legal, then generates and validates its Command ID immediately before the
  append; a running Turn is a domain rejection and no retry occurs.

### 3. Define the application-side append acceptance check exactly

The plan currently says the helper verifies count and contiguous metadata. That
is insufficient: a broken Store could return the right count and sequence but
different event payloads.

Before treating an append as committed in local state, Application verifies:

1. returned count equals requested event count;
2. sequences are exactly `ExpectedVersion+1 .. ExpectedVersion+N` without
   overflow;
3. every record has the requested Session ID and Command ID;
4. schema version, Event ID, timestamp, and event shape satisfy domain record
   validation;
5. returned event type, payload, and order exactly equal the requested events;
6. all records in one batch share the same non-zero UTC occurrence time;
7. applying them in order succeeds and the final Version equals
   `ExpectedVersion + N`.

Any mismatch is `CategoryInternal/store_contract_violation`, preserves the
underlying/apply cause when one exists, and does not let the model start.
Application cannot undo a Store that lied about a commit; this check detects
the violated adapter contract and fails closed.

The EventStore port must also state one milestone-specific assumption:

> A non-nil `Append` error means the requested batch did not commit. Once a
> batch commits, the adapter returns the committed records even if the caller
> context is canceled concurrently after the commit point.

This is implementable by the in-process MemoryEventStore and required by the
fallback logic below. A future remote adapter that can lose the acknowledgement
after commit cannot implement this shape honestly; it must extend the port with
exact idempotent retry identity or an explicit unknown-commit outcome.

### 4. Specify the four orchestration phases and context ownership

`RunTurn` has four irreversible phases:

| Phase | Durable boundary | Context | Required outcome |
| --- | --- | --- | --- |
| Preflight | none | caller context | failure returns no records and invokes no model |
| Admission | atomic started Turn + Item | caller context | success establishes the only legal model-call boundary |
| Execution | runtime started/deltas only | caller context | Engine owns stream cancellation and Close |
| Terminalization | atomic terminal Item + Turn | caller context for success; bounded detached context for failure/interruption | exactly one terminal batch, or an explicit running persistence/conflict result |

For every Engine failure after admission, build the terminal commit context in
the same stack frame:

```go
cleanupBase := context.WithoutCancel(ctx)
cleanupCtx, cancel := context.WithTimeout(cleanupBase, s.config.TerminalCommitTimeout)
defer cancel()
```

Use it only for the failure/interruption append. RuntimeSink delivery continues
to use the original caller context; cancellation may suppress post-commit
attempts and becomes a delivery warning. Model execution, retries, and ordinary
success work never use the detached context.

For model success, check caller cancellation before the completed batch and use
the caller context for that append. The result matrix is:

- completed batch returns records -> durable completion wins, even if
  cancellation is observed afterward;
- completed append fails while `ctx.Err() != nil` -> by the no-ambiguous-error
  Store rule, attempt the interrupted pair with the bounded cleanup context;
- completed append fails for another persistence error or conflict -> do not
  invent a second terminal outcome; return the durable running boundary.

### 5. Durable terminal facts authorize terminal runtime signals

Application, not Engine, remains the terminal authority. Engine emits only
`model.stream.started` and deltas. Application may emit
`append.completed` followed by exactly one of completed/failed/interrupted only
after the corresponding terminal batch has been accepted and applied.

Required runtime orders are:

```text
success:     model.stream.started, delta*, append.completed, model.stream.completed
failure:     model.stream.started?, delta*, append.completed, model.stream.failed
interrupted: model.stream.started?, delta*, append.completed, model.stream.interrupted
```

No terminal-append success means no `append.completed` and no model terminal
signal. A terminal signal delivery failure never changes durable state. It is
recorded as `DeliveryWarning`; if there is no earlier execution error the
returned application error is Delivery, otherwise the earlier category stays
primary and the delivery cause is joined.

The one Emitter and its ordinal space span the complete call. Failed sink
attempts consume ordinals; payload validation and pre-attempt cancellation do
not. `RunTurnResult` must not imply that absence of a post-cancel runtime
terminal signal means absence of the durable terminal fact.

### 6. Publish a complete result/error algebra

`RunTurn` returns a value plus error deliberately. The following shapes are
normative:

| Outcome | Result status/text | `TerminalCommitted` | Error category |
| --- | --- | --- | --- |
| completed and delivered | completed / exact text | true | nil |
| completed, terminal delivery failed or suppressed | completed / exact text, warning set | true | delivery |
| model startup/stream/close failure, terminal batch committed | failed / empty | true | model |
| invalid provider stream, terminal batch committed | failed / empty | true | model (`invalid_stream`) |
| output limit, terminal batch committed | failed / empty | true | output_limit |
| caller cancellation, terminal batch committed | interrupted / empty | true | canceled |
| pre-terminal sink failure, interruption committed | interrupted / empty | true | delivery |
| admission/load/terminal persistence or conflict failure | absent or running / empty | false | persistence, conflict, or internal |
| request validation failure | zero result | false | validation |

`Records` is the defensive, ordered concatenation of every batch known to have
been committed by this call, including on error. After atomic admission it
therefore contains two start records; after successful terminalization it also
contains the two terminal records. `Text` is non-empty or empty exactly as the
completed assistant output; failed/interrupted results never persist or return
partial deltas as final text.

Error precedence is:

1. Store contract violation/conflict/persistence preventing terminalization;
2. original model/output/canceled/delivery execution category;
3. post-terminal delivery warning.

When terminalization fails, preserve the original execution cause and the
terminal append cause with `errors.Join`, but expose the terminalization
category because durable state remains running. Provider prose may remain
inspectable through deliberate error unwrapping; it never enters stable error
text or domain events.

Application `Error`, `IsCategory`, and `VersionConflictError` must be nil-safe
and traverse every branch of joined errors, matching the Engine error standard.
Tests include nested joins, a matching later sibling, direct typed nil, and a
typed nil inside a join.

### 7. Keep correlation identity separate from idempotency

One generated `CommandID` may continue to correlate both `RunTurn` append
batches and all runtime events. Its contract must explicitly say:

- it identifies one application invocation/correlation lineage;
- it is not a Store deduplication key;
- reusing it across admission and terminal batches means `(SessionID,
  CommandID)` cannot uniquely identify one append request;
- Service performs no automatic reload, re-decision, model retry, or append
  retry;
- a caller must not blindly retry `RunTurn` after an uncertain response.

KurrentDB's primary contract shows why exact retry requires the same expected
revision and event IDs, not merely a correlation value. Maka similarly requires
exact bytes and identity and creates fresh identities for a continuation.

Future public/API idempotency therefore needs a separate, caller-stable request
or operation identity plus exact batch identity. It must define whether a retry
returns the previous result, resumes from a safe boundary, or creates a new
Turn. That feature is deferred, but the current naming must not imply it already
exists.

### 8. Make concurrency authority and race outcomes explicit

Atomic admission CAS is the linearization point for same-Session `RunTurn`.

- Two calls loading the same version may both finish preflight, but exactly one
  admission batch commits; the loser invokes the model zero times.
- A call loading an already-running Turn is rejected by the shared pure domain
  eligibility predicate before ID generation and invokes the model zero times;
  `Decide` reuses the same predicate.
- `CloseSession` racing with admission is decided by CAS: only one valid append
  wins; no automatic retry follows.
- Different Sessions may execute concurrently through one Service/TurnRunner;
  the shared Model and RuntimeSink contracts from Tasks 5–6 apply.
- A conflict during terminalization is not retried and cannot be rewritten as
  model failure; the returned status is running/unknown from this call's local
  authority, `TerminalCommitted` is false, and the caller must reload.

Tests use store barriers around Load, admission commit, terminal-entry, and
terminal-return. No test infers a race outcome from sleeps.

### 9. Treat recovery gaps as explicit evidence, not silent repair

This milestone intentionally may leave one durable running boundary after:

- process death after admission;
- terminal append persistence failure;
- a terminal conflict or Store contract failure.

Tasks 7–9 do not add startup repair or continuation. They must nevertheless
make the boundary inspectable: returned records/status are accurate, replay
preserves running state, no success terminal signal is emitted, and no hidden
retry calls the model again. Documentation must label production
reconciliation as a blocking capability before general availability.

This follows Maka's observed distinction between repair, continuation, and
retry. It does not import Maka's resolver or claim crash recovery is delivered
here.

## Exact amendments to the accepted design and plan

Before Task 7 implementation, update both English and Chinese copies:

1. Replace the two successful-flow start appends with one atomic
   `StartAssistantTurn` admission batch.
2. Add the shared pure domain eligibility preflight before IDs, then generated-ID
   validation and Emitter construction before admission.
3. Separate request/Decide rejection from corrupt Replay/Apply and adapter
   contract errors.
4. Strengthen append-return verification to exact events and batch metadata,
   ordered Apply success, and final Version `ExpectedVersion + N`; do not invent
   an independent expected-state oracle.
5. Add the milestone Store rule that non-nil Append error means no commit, plus
   the future remote-store limitation.
6. Restrict detached bounded context to post-admission failure/interruption
   terminal persistence; keep RuntimeSink on caller context.
7. Replace cancellation and failure branches with the phase/result matrices in
   this report.
8. Define `Records`, `Text`, `Status`, `TerminalCommitted`, warning, cause, and
   category behavior on every return path.
9. Define CommandID as correlation, not idempotency, and retain no automatic
   retry.
10. Add typed-nil/joined-error, generator-contract, exact-returned-event,
    atomic-admission, barrier-race, and running-replay tests.

## Adopt, reject, defer

| Decision | Contract |
| --- | --- |
| Adopt | One Application authority; validate/load/replay before IDs; one atomic admission batch; one atomic terminal batch; model only after admission; exact append-return acceptance; durable facts before terminal runtime signals; bounded detached terminal persistence; complete result/error algebra; CAS linearization; defensive results; full-tree typed-nil-safe errors. |
| Reject | Separate no-side-effect start appends; Emitter construction after durable start; replay corruption as caller validation; terminal signal before terminal commit; cancellation rewriting a committed success; Store/model retry hidden in Service; CommandID presented as idempotency; persistence from UI/listener callbacks; sleep-based races; partial delta as final durable text. |
| Defer | Public idempotency key and result cache; ambiguous remote-commit protocol; production EventStore; startup repair/reconciliation; continuation/resume; runtime event persistence/catch-up; retry/rate-limit/provider policy; OpenTelemetry. |

## Verdict

The overall authority split remains strong: Application owns command and
durability, Engine owns one bounded stream, Domain owns legal transitions, and
EventStore CAS owns concurrency. The pre-amendment Tasks 7–9 plan was **not
ready to implement unchanged** because its split admission append, incomplete
append-return check, replay-error ownership, cancellation phase boundaries, and
idempotency wording create avoidable ambiguity.

The ten original amendments and architecture review round 1 are incorporated
consistently. The gate is **READY** for independent architecture re-review
before production implementation.

Incorporation note (2026-08-12): amended the normative English design,
`docs/superpowers/specs/2026-08-12-engine-vertical-slice-design.md`, its Chinese
reading copy, and the English/Chinese implementation plans under
`docs/superpowers/plans/`. These four documents now define atomic admission,
exact preflight/append acceptance, the Store milestone assumption, four-phase
context ownership, complete result/error algebra, correlation-only CommandID,
barrier concurrency evidence, and the explicit running-recovery gap.

Review-round-1 incorporation note (2026-08-12): all four normative documents
now add one shared pure domain eligibility predicate before ID generation;
assign Task 7 the existing `application/ports.go`, `ports_test.go`,
`application/eventstoretest/suite.go`, and
`adapters/memory/event_store_test.go` changes needed to deliver and barrier-test
the no-ambiguous-error contract; use append-acceptance option B (exact returned
records, ordered Apply success, final Version) without an independent state
oracle; and limit primary evidence to official sources while retaining
DeepSeek-Reasonix only as explicitly non-authoritative community context.
