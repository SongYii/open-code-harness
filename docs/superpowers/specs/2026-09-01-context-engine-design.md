# Context Engine: Budgeted History, Durable Compaction, and Recovery

**Status:** Accepted — 2026-09-01

**Date:** 2026-09-01

**Milestone:** 8, Context Engine

**Stability:** All new Go packages, ports, commands, events, and configuration
are `internal` before v1.0. The `och compact-session` command is
`experimental`. No ACP extension becomes a compatibility promise in this
slice.

**Research authority:** [Context Engine architecture gate](../../research/architecture-gates/2026-09-01-context-engine.md)

English is normative. The Chinese file is a synchronized reading copy.

## 1. Decision summary

Open Code Harness will add one independent Context Engine that constructs the
model-visible request from canonical Session events for both model-only and
tool-enabled Turns. It meters the complete provider-neutral envelope against
the selected model's declared capacity, projects oversized Tool Results into
bounded excerpts, selects a tool-pair-safe history prefix, and replaces that
prefix with a durable checkpoint before a Provider may consume it.

Canonical events and the JSONL audit replica are never compacted. A checkpoint
is a lossy, disposable projection with exact source coverage, SHA-256 digest,
format version, token evidence, and predecessor lineage. The normal variant is
an LLM-generated rolling summary. A deterministic tail-reset variant contains
no historical claims and exists only to recover safely when a hard capacity
limit cannot wait for a trustworthy summary.

Compaction is synchronous and first-class:

```text
context.compaction.started
  -> context.compaction.completed(checkpoint)
  |  context.compaction.failed
```

Automatic preparation runs before a Turn's first Provider call and between
tool Steps. Confirmed startup context overflow may compact and retry under a
strict per-Turn bound. Manual compaction uses the same transaction and refuses
an active Turn. There is no speculative background compactor.

## 2. Problem and current constraints

The current Application has two request paths:

- model-only `runSingleAttempt()` sends `Input` and no history;
- the tool Step loop reads the complete event stream, calls
  `projectPriorTurns()`, appends current input, and rejects JSON larger than
  `MaxProjectionBytes`.

This creates five concrete defects:

1. history behavior changes merely because a Tool Catalog is configured;
2. byte size, not model tokens, decides whether context fits;
3. a long Session fails instead of compacting;
4. request construction loads a complete stream into memory;
5. the durable `ModelRequestRecorded` does not independently equal the full
   request envelope sent by the tool path.

The design must preserve existing invariants:

- the EventStore stream is append-only CAS authority;
- the compact Domain aggregate remains bounded;
- every committed effect has an exact command/event identity;
- append unknown outcomes are resolved rather than blindly retried;
- Runtime Host fencing remains the sole writer authority;
- cancellation, crash recovery, JSONL audit, SQLite import, transcript export,
  and ACP projection remain deterministic;
- no provider name branch enters Application or Domain.

## 3. Goals

The milestone is complete only when it provides all of the following:

1. one history projection path for model-only and tool-enabled requests;
2. deterministic token budgeting of messages and Tool Schemas against the
   selected route's context window and output reserve;
3. bounded two-pass event scanning and safe Turn/Step boundaries;
4. bounded projection of oversized Tool Results without changing source facts;
5. rolling, structured LLM summaries with validation and shrink proof;
6. durable summary and deterministic reset checkpoints;
7. pre-turn, mid-turn, manual, and startup-overflow triggers;
8. exact recording of every request envelope before Provider dispatch;
9. SQLite and memory latest-checkpoint projections with canonical rebuild;
10. crash reconciliation for incomplete compaction attempts;
11. independent fixture evaluation plus failure, cancellation, concurrency,
    race, fuzz, mutation, and long-session evidence;
12. English implemented contract, synchronized Chinese reading copy, evidence
    ledger, and honest milestone-status update after implementation.

## 4. Non-goals

This design does not add:

- vector search, embeddings, RAG, semantic retrieval, or cross-session memory;
- deletion, rewriting, squashing, or storage compaction of canonical events;
- provider-native opaque compaction without a concrete supporting Adapter;
- a background compaction worker or speculative summary generation;
- general retry for authentication, quota, rate-limit, transient, or arbitrary
  permanent Provider failures;
- a model-facing archive read tool for pruned Tool Results;
- context editing, rewind, branching, or user-authored checkpoint mutation;
- MCP, TUI, OpenTelemetry, or the milestone 10 evaluation runner;
- an ACP method or non-standard `session/update` variant for manual or live
  compaction;
- exact tokenizer parity for every endpoint that speaks an OpenAI-compatible
  wire protocol.

## 5. Architectural decisions

| ID | Decision | Reason |
| --- | --- | --- |
| CE-01 | `internal/harness/contextengine` owns pure projection, metering, planning, checkpoint validation, and materialization. | Keeps token and history algorithms independently testable and out of Application orchestration. |
| CE-02 | Application owns compaction lifecycle, Provider calls, CAS appends, cancellation, and overflow retry. | These are use-case and transaction concerns, not pure context logic. |
| CE-03 | The routed `CapabilityProfile.ContextWindowTokens` and `MaxOutputTokens` are capacity authority. | Composition already validates both as positive; no unsafe unknown-window fallback is needed. |
| CE-04 | The generic meter is conservative and deterministic; a matching durable provider-usage observation may raise, but never lower, its estimate. | Model-neutral correctness cannot depend on one vendor tokenizer, while proven same-route evidence should correct systematic underpricing. |
| CE-05 | Every automatic compaction is synchronous at a clean pre-Provider boundary. | Eliminates stale-summary races and late commits while retaining deterministic ownership. |
| CE-06 | Planning uses one pinned scan; compaction/materialization may use a second pinned scan. | Keeps memory bounded despite forward-only paged EventStore reads. |
| CE-07 | Preferred boundaries are complete Turns; closed Steps may split an oversized historical or active Turn. | Preserves recency while never orphaning Tool calls/results. |
| CE-08 | `rolling_summary_v1` is normal; `source_tail_reset_v1` is a named deterministic fallback. | Provides continuity normally and a truthful hard-limit escape hatch when summarization cannot safely finish. |
| CE-09 | Checkpoints are log events plus a rebuildable latest-checkpoint index. | The log remains authority while normal replay stays bounded. |
| CE-10 | Tool Result projection is adjacent request shaping, not another checkpoint mechanism. | Prevents multiple compaction authorities and keeps exact source facts. |
| CE-11 | The complete provider message envelope is recorded before every conversation attempt. | Restores request reconstructability and makes pruning/checkpoint use auditable. |
| CE-12 | This milestone summarizes only through the active conversation route; a separately configured summary Provider is deferred. | The repository has no consumer that justifies a second Provider lifecycle or cross-Provider data path in v1. |
| CE-13 | Only startup overflow before any delivered delta is recoverable. | Retrying after streamed output could duplicate user-visible content or tool intent. |
| CE-14 | Manual compaction is an Application command and `och compact-session`; ACP remains unchanged. | Delivers a real operator surface without inventing protocol semantics. |

## 6. Component model

```text
Application.RunTurn / CompactSession
              |
              v
   Context Orchestrator (Application)
              |
      +-------+------------------------+
      |                                |
      v                                v
contextengine.Engine             ContextSummarizer
  Meter                            active engine.Model
  EventProjector                   purpose=context_summary
  BoundaryPlanner
  ToolResultProjector
  CheckpointValidator
  Materializer
      |
      v
EventStore pages <------> ContextCheckpointStore
 canonical events           derived latest index
      |                         |
      +------------+------------+
                   v
        context.compaction.* append
                   |
                   v
       ContextPrepared + ModelRequestRecorded
                   |
                   v
              Provider Model
```

### 6.1 Pure Context Engine

`internal/harness/contextengine` imports Domain value types but imports no
Application, Adapter, SQLite, ACP, filesystem, clock, randomness, logger, or
Provider SDK. It exposes values and pure/stateful-in-memory operations:

```go
type Meter interface {
    Estimate(Envelope) (Estimate, error)
}

type Planner interface {
    Plan(PlanInput) (Plan, error)
}

type CheckpointValidator interface {
    Validate(Checkpoint, ValidationInput) error
}

type Materializer interface {
    Materialize(MaterializeInput) (PreparedContext, error)
}
```

Concrete implementations use owned copies. Inputs and outputs never alias
EventStore pages, configuration slices, or caller buffers.

### 6.2 Application Context Orchestrator

Application owns the two pinned passes, compaction bracket, summarizer calls,
append intents, unknown-outcome resolution, runtime events, and retry winner
rules. It depends on:

- `EventStore` for canonical reads/appends;
- `ContextCheckpointStore` for the verified latest derived projection;
- `contextengine.Engine`;
- `ContextSummarizer`;
- existing `Clock`, `IDGenerator`, `AuthoritySource`, and execution registry.

It does not import SQLite or the OpenAI-compatible Adapter.

### 6.3 Context Summarizer

`ContextSummarizer` is an Application port implemented by an Engine adapter
over `engine.Model`. It sends no Tools, collects only text under a byte/token
cap, rejects Tool Calls, and uses the same closed Provider failure taxonomy as
conversation attempts. It calls `engine.Model` directly through a shared
bounded stream collector; it does not enter `RunTurn`, emit assistant deltas,
or recursively invoke the Context Engine.

`engine.ModelRequest` gains:

```go
Purpose         ModelRequestPurpose // conversation | compaction
MaxOutputTokens uint32              // positive and <= route maximum
```

The OpenAI-compatible Adapter maps the request-specific output cap to its
configured `max_tokens`/`max_completion_tokens` field and may use `Purpose`
only for non-secret attribution headers. `Purpose` never changes model-visible
semantics inside the generic Provider adapter.

### 6.4 Context checkpoint store

A separate Application read port avoids turning canonical `EventStore` into a
context-specific query interface:

```go
type ContextCheckpointStore interface {
    LoadLatestContextCheckpoint(context.Context, domain.SessionID) (
        ContextCheckpointLookup, error,
    )
}
```

The memory and SQLite EventStore adapters implement both ports. Writes still
go only through `EventStore.Append`; adapters derive their latest-checkpoint
index from committed `ContextCompactionCompleted` events. The read port returns
`none`, `found`, or a classified corrupt/store error; it never fabricates a
checkpoint.

## 7. Vocabulary and durable values

### 7.1 Context unit

A `ContextUnit` is the smallest boundary the planner may retain or cover:

- `TurnUnit`: one completed user Turn and its completed Steps;
- `StepUnit`: one completed assistant message containing Tool Calls plus every
  terminal result for those calls;
- `AssistantUnit`: one completed assistant message with no Tool Calls;
- `CurrentInputUnit`: the not-yet-committed incoming user input during
  pre-turn planning.

Units carry source sequence range, terminal event ID, projected messages,
estimated tokens, and tool-pair balance. Model request/usage, policy, approval,
compaction, and context-prepared events never form conversational units.

An incomplete historical tool pair is a store/domain contract violation. An
incomplete current pair is protected and cannot enter the covered prefix.

### 7.2 Coverage

Every checkpoint carries:

```text
coveredEventCount
coveredTurnCount
throughSequence
throughEventID
sourceDigest
```

Coverage always names an ordered prefix of compactable conversational source
events through a safe unit. `throughSequence` is a Session-stream position and
may pass operational events; those events are excluded from
`coveredEventCount` and `sourceDigest` by a versioned source filter.

`sourceDigest` is an extendable SHA-256 hash chain:

```text
D0 = SHA256("och-context-source-v1\n")
Di = SHA256(
       "och-context-source-step-v1\n"
       || Di-1
       || uint64-big-endian(encoded-length)
       || canonical-event-encoding
     )
sourceDigest = Dn
```

Canonical encoding is `domain.MarshalRecordedEvent` after the source filter.
Length framing prevents concatenation ambiguity. A rolling successor continues
from the prior validated digest and scans only newly covered source events. A
cold rebuild recomputes the chain from `D0`; storing only `Dn` is sufficient to
extend and verify a successor without retaining hash-library internal state.

### 7.3 Checkpoint

```text
ContextCheckpoint
  checkpointID
  sessionID
  kind: rolling_summary_v1 | source_tail_reset_v1
  sourceSchema: och_context_source_v1
  summaryFormat: och_context_summary_v1 | none
  promptVersion: och_context_summary_prompt_v1 | none
  coverage
  previousCheckpointID?
  summary?                 // only rolling_summary_v1
  limitations
  tokensBefore
  checkpointTokens
  retainedTailTokens
  estimatedRequestTokens
  summarizerRoute?         // active non-secret route identity; summary only
  summarizerUsage?
  summaryChunks
  prunedToolResultCount
```

Checkpoint IDs are opaque validated Domain IDs. A successor must advance
coverage, or be an explicit same-coverage rewrite whose
`previousCheckpointID` names the current checkpoint and whose source digest is
identical. A checkpoint never points backward or skips a source unit.

### 7.4 Prepared context

Every conversation attempt has a `ContextDecisionID` and
`ContextPreparedRecorded` evidence containing:

- Session/Turn/Item and attempt index;
- trigger (`pre_turn`, `mid_turn`, `overflow_retry`);
- source head version;
- selected checkpoint ID/kind or none;
- raw tail sequence range;
- budget capacity/source, trigger, target, and hard input values;
- deterministic message, Tool Schema, and total input estimates;
- optional provider-usage anchor evidence: request event ID, Turn/Item/attempt,
  observed input tokens, signed surface delta, and anchored estimate;
- bounded Tool Result rewrite facts: source event ID, original bytes/tokens,
  projected bytes/tokens, and SHA-256 digest;
- final serialized-envelope bytes and meter implementation ID.

`ModelRequestRecorded` references the decision ID and contains the complete
messages and Tool Schemas actually passed to `engine.Model`. No hidden prefix
or suffix is reconstructed only from volatile memory.

## 8. Budget contract

For selected-route context window `W` and maximum output `O`:

```text
safety = max(512, ceil(W * 0.02))
hardInput = W - O - safety
trigger = floor(hardInput * TriggerPercent / 100)
target = floor(hardInput * TargetPercent / 100)
protectedTail = floor(hardInput * TailPercent / 100)
summaryOutputCap = min(O, max(128, floor(hardInput * 0.10)))
```

Composition rejects `O + safety >= W`. Defaults are:

| Setting | Default | Valid range/invariant |
| --- | --- | --- |
| `TriggerPercent` | 80 | 60–95 |
| `TargetPercent` | 55 | 30–80 and `< TriggerPercent` |
| `TailPercent` | 25 | 10–50 and `< TargetPercent` |
| `MaxSummaryChunks` | 8 | 1–16 |
| `MaxOverflowCompactionsPerTurn` | 2 | 1–3 |
| `CompactionTimeout` | 2 minutes | 5 seconds–10 minutes |
| `MaxPrunedToolResultsPerRequest` | 64 | 1–64 |
| `MaxProjectionBytes` | existing 4 MiB | not widenable by Context config |

The trigger compares the complete estimated input envelope: checkpoint/raw
messages plus current input plus Tool Schemas and framing. Output is reserved
before that comparison. `hardInput`, not the provider's nominal window, is the
absolute dispatch gate.

The default deterministic meter is identified as `och_wire_estimate_v1`:

- text/JSON payload: `ceil(UTF-8 bytes / 3)`;
- fixed message framing: 8 tokens per message;
- each Tool Call and Tool Result: 16 additional tokens;
- each Tool Schema: 16 tokens plus `ceil(canonical JSON bytes / 3)`;
- checkpoint framing and prune markers are measured as ordinary text.

This intentionally overprices typical ASCII prose. An exact route-specific
meter may replace it behind the port only if it has contract tests for every
message/tool shape. Meter identity is durable evidence so estimates are not
compared across algorithms as though they were identical.

The dispatch estimate is:

```text
budgetEstimate = max(wireEstimate, anchoredEstimate?)
```

where `wireEstimate` is the complete `och_wire_estimate_v1` envelope. A
provider-usage anchor is eligible only when all of the following are provable
from committed events:

- it is the newest prior completed conversation attempt that has non-zero
  `InputTokens` and satisfies every remaining eligibility rule;
- its `ModelRequestRecorded` and `ModelUsageRecorded` match by
  Session/Turn/Item/`AttemptIndex`;
- adapter family, endpoint, model, request purpose, meter identity, and every
  non-message envelope field including Tool Schemas match the candidate;
- the candidate message surface is derivable from the recorded request surface
  by ordered appends or checkpoint/rewrite replacements that the same meter can
  price exactly.

For an eligible anchor:

```text
anchoredEstimate = max(0, observedInputTokens + signedSurfaceDelta)
```

`signedSurfaceDelta` prices content added, removed, or replaced after the
sampled request, including the persisted assistant output and subsequent Tool
Results. Raw `OutputTokens` is not added because it may include reasoning or
other output absent from the persisted surface. `CachedInputTokens` is a
subset of `InputTokens` under the Engine contract and is audit/billing detail,
not an additional occupancy count.

An absent, zero, mismatched, malformed, or unprovable observation is ignored.
Usage never retroactively changes a committed Context decision, never lowers
the deterministic estimate, and is never the sole estimate for a differently
shaped request. Because both the sampled request, usage, and surface mutations
are canonical events, replay makes the same anchor decision.

## 9. Event projection and safe boundaries

### 9.1 Projection grammar

Canonical events project in sequence order:

```text
TurnStarted                     -> user message
AssistantMessageCompleted       -> assistant message (+ Tool Calls)
ToolCallStarted                 -> remembers call name and Step membership
ToolCallCompleted               -> tool result
ToolCallFailed/Interrupted      -> tool result containing stable failure text
```

Session, model request/usage, policy, approval, context, and terminal Turn
events do not directly add messages. Turn terminal events close boundary
eligibility.

Multiple calls emitted by one assistant message and their terminal results are
one Step unit even if completion events interleave. A boundary is balanced only
after every offered Call ID has exactly one terminal result. Duplicate, unknown,
or missing IDs fail closed as a projection invariant violation.

### 9.2 Cut selection

The planner walks a bounded deque backward from the head until
`protectedTail` is met, then snaps older to the nearest safe boundary.

Priority:

1. retain complete recent Turns;
2. if one retained historical Turn alone exceeds the tail budget, retain its
   newest closed Steps;
3. during mid-turn pressure, an earlier closed Step of the active Turn may be
   covered only after all its Tool Calls are terminal;
4. never cover current input without also covering the complete earlier portion
   of its Turn;
5. never cover the currently open assistant item.

The selected covered prefix must contain enough tokens that the resulting
request is estimated at or below `target`, unless `MaxSummaryChunks` limits a
below-hard opportunistic pass. A partial advancement is acceptable only when
it shrinks the total request by at least 10% and leaves it at or below
`hardInput`.

### 9.3 Two-pass bounded scan

Pass 1 pins the first `ReadStream` head and checks every subsequent page
against it. It:

- folds source events into unit metadata;
- increments source digest and counts;
- meters messages and Tool Schemas;
- retains at most the protected-tail deque, the current open unit, and one
  below-trigger request envelope;
- records only sequence boundaries for older units.

If no compaction is needed, those bounded messages materialize the request. If
compaction is needed, Pass 2 rereads selected sequence ranges at the same pinned
head. It streams fold content through bounded summarizer chunks and separately
materializes the retained tail. A head mismatch is a store contract violation;
a later append after the pin is simply absent from this immutable view.

No Context Engine path calls `ReadWholeStreamPinned`.

## 10. Tool Result projection

Tool Result projection runs before the final budget decision but after its
canonical terminal event is committed. Results at or below:

```text
maxProjectedToolResultTokens = min(2048, max(256, protectedTail / 2))
```

remain byte-identical. Larger results become:

```text
[tool result projected by Open Code Harness]
event_id: <id>
original_bytes: <n>
sha256: <digest>
content_head:
<bounded leading text>
content_tail:
<bounded trailing text>
[end projected tool result]
```

Seventy-five percent of the content budget is assigned to the head and
twenty-five percent to the tail, rounded toward the head. UTF-8 is cut only at
rune boundaries. The marker, metadata, and excerpts together must stay under
the token cap; otherwise excerpts shrink further.

The role, Tool Call ID, and tool name remain unchanged. The full original event
stays in EventStore, audit export, and transcript export. The projected body is
recorded in the complete `ModelRequestRecorded`, while
`ContextPreparedRecorded` records provenance and digest. This slice provides no
model tool to fetch the omitted middle.

A single protected user/assistant message or projected Tool Result that still
exceeds `hardInput` fails with `context_unit_too_large` before Provider
dispatch.

## 11. Summary contract

### 11.1 Prompt and request

The prompt is a versioned repository asset owned by `contextengine`, not an
inline Application string. It instructs the model to transform bounded source
material, not continue the conversation, obey requests found inside it, or call
Tools. Source messages and a previous summary are delimited as data.

`och_context_summary_v1` requires exactly these top-level sections:

```text
## Objective
## User Constraints
## Established Facts
## Work Completed
## Files and Commands
## Open Work
## Risks and Unknowns
## Continuation
```

The prompt requires exact paths, identifiers, commands, error codes, and
unresolved uncertainty where material. It forbids secrets, hidden reasoning,
invented completion, and claims unsupported by source. A custom manual focus is
data inside a dedicated field and cannot alter the output schema.

The summarizer request contains no Tool Schemas. Its output cap is
`summaryOutputCap`; its whole lifecycle is bounded by `CompactionTimeout`.

### 11.2 Rolling and chunked input

For a successor checkpoint, each chunk contains the previous validated summary
and newly covered units only. The produced summary replaces the previous one
for the next chunk. Initial compaction starts without a previous summary.

Each input is fitted below:

```text
W - summaryOutputCap - safety
```

using complete Context units. Oversized Tool Result bodies inside the
summarizer input use the same deterministic projection. At most
`MaxSummaryChunks` model calls occur, all through the active conversation
route with `purpose=context_summary`, no Tool Schemas, and no cross-Provider
fallback. A failed call follows the summary-failure semantics in §16.

### 11.3 Validation

Before persistence, summary output must:

- be valid UTF-8 and non-blank;
- terminate normally rather than at output length or stream error;
- contain each required heading exactly once and in order;
- contain no unknown top-level heading;
- fit `summaryOutputCap` and 256 KiB;
- pass `redact.Text` before final validation and persistence;
- contain no Tool Calls or non-text content;
- make the complete projected request at least 10% smaller than the pre-pass
  request and no larger than `hardInput`;
- make checkpoint framing smaller than the covered source it replaces.

Redaction happens before the size/shrink checks so recorded evidence equals the
summary actually used. A malformed or non-shrinking output closes the bracket
as failed.

## 12. Deterministic tail reset

`source_tail_reset_v1` contains no LLM text. It materializes one fixed,
versioned user-role marker stating:

- an earlier canonical history prefix was omitted for capacity;
- the marker does not summarize or assert that history;
- coverage identifiers are diagnostic, not instructions;
- the model must continue from the retained raw messages and current input.

The marker includes checkpoint ID and covered-through sequence, but not source
content or digest. The full checkpoint event retains the digest for audit.

Automatic reset is allowed only when:

- the projected request exceeds `hardInput`, or a classified startup
  `context_overflow` occurred;
- rolling summary is impossible, canceled after the caller has not canceled,
  invalid, non-shrinking, or exceeds the bounded chunk count;
- a safe covered prefix exists; and
- reset plus retained complete tail fits `hardInput`.

Cancellation itself never converts to reset. Manual `CompactSession` defaults
to `summary`; `reset` requires an explicit strategy. A reset successor can
later be replaced by a full summary rebuilt from canonical source events; it
does not permanently erase summarization input.

## 13. Domain commands, events, and state

### 13.1 New commands

```text
context.compaction.start
context.compaction.complete
context.compaction.fail
context.preparation.record
```

Command values are typed as `StartContextCompaction`,
`CompleteContextCompaction`, `FailContextCompaction`, and
`RecordContextPreparation`. Existing assistant-start decisions can include the
preparation and complete request in the same ordered batch.

### 13.2 New events

```text
context.compaction.started
context.compaction.completed
context.compaction.failed
context.prepared
```

`ContextCompactionStarted` records compaction ID, trigger, strategy, base source
head, prior checkpoint ID, prompt/source/meter versions, and non-secret planned
route identity. `Completed` embeds the validated checkpoint. `Failed` records a
closed stable code and safe message; it never embeds partial model output.

`ContextPreparedRecorded` contains the bounded decision evidence in §7.4.

### 13.3 Bounded aggregate state

`domain.Session` gains one optional active value:

```go
type ContextCompaction struct {
    ID          ContextCompactionID
    Trigger     ContextCompactionTrigger
    Strategy    ContextCompactionStrategy
    BaseVersion uint64
    StartedAt   time.Time
}
```

It contains no summary, source events, messages, or historical checkpoint list.
Start sets it; complete/fail clears it. At most one compaction is active.

Eligibility:

- manual/pre-turn start requires active Session and no active Turn;
- mid-turn/overflow start requires the caller-owned active Turn and assistant
  item at a pre-Provider boundary;
- a new Turn, Session close/delete, and another compaction reject while a
  compaction is active;
- unrelated clients cannot append tool/assistant transitions during a manual
  compaction because Domain state and Store CAS both reject them;
- terminal compaction timestamps cannot precede start.

Context events advance Session version like every event. Completed checkpoints
do not enter the bounded aggregate after clearing the active value; latest
checkpoint lookup belongs to the derived store projection.

### 13.4 Request-event changes

`ModelRequestRecorded` gains `Purpose`, `AttemptIndex`, and
`ContextDecisionID`. Conversation requests store the complete final `Messages`
and `Tools`. `ModelUsageRecorded` gains the same attempt index so overflow
attempts and the successful retry are not conflated.

The admission batch for a production first attempt becomes:

```text
turn.started
assistant.message.started
context.prepared
model.request.recorded
```

Subsequent Step start batches use:

```text
assistant.message.started
context.prepared
model.request.recorded
```

An overflow retry on the same active Item appends a new
`context.prepared + model.request.recorded` pair with the next attempt index.
Legacy internal tests may omit a request only when no RequestIdentity/Context
Engine is configured; the production composition root never does.

## 14. Checkpoint persistence and recovery

### 14.1 SQLite projection

Storage migration 5 adds:

```text
context_checkpoint_heads
  session_id PRIMARY KEY -> event_streams
  checkpoint_event_sequence
  checkpoint_event_id UNIQUE -> events
  checkpoint_id UNIQUE
  covered_through_sequence
  source_digest BLOB(32)
  updated_at_commit_position -> event_appends
```

The row stores only bounded identity/cursors. `LoadLatestContextCheckpoint`
joins the canonical completion event for its payload and verifies row/event
agreement. Before accepting a `ContextCompactionCompleted`, the adapter
independently verifies its coverage boundary and hash chain against canonical
events: an initial checkpoint scans from `D0`; a successor starts from the
indexed predecessor digest and scans only the newly covered range; a
same-coverage rewrite requires an identical digest. The append transaction
updates the row only after that proof passes. Any failure rolls back event and
projection together. This makes normal lookup bounded without asking callers
to trust an Application-supplied digest blindly.

Memory keeps the same bounded row semantics in copied values.

### 14.2 Projection recovery

Migration/backfill, verified import, and an explicit
`RebuildAndVerifyContextCheckpointHeads` path scan canonical events, validate
checkpoint shape and successor rules, choose the furthest valid coverage, and
rebuild the row. Same-coverage rewrites follow valid predecessor lineage;
remaining ties use canonical sequence, never wall-clock alone.

Missing derived state is repairable. A projection that disagrees with its
canonical event is `store_corrupt`; it is not silently trusted. JSONL import
rebuilds the projection only after all existing audit and replay verification
layers pass.

### 14.3 Checkpoint replay validation

Before use, the Context Engine verifies:

- Session/checkpoint IDs and supported schema/kind/format;
- coverage boundary exists at or before the pinned head;
- source identity/digest proof supplied by the checkpoint-store contract;
- predecessor rules;
- summary structure for `rolling_summary_v1`;
- current route capacity and current meter estimate;
- checkpoint plus retained tail fits the current budget.

A previously valid checkpoint may be rejected after a model switch or tighter
configuration. Canonical events are then used to rebuild, advance, reset, or
construct an uncheckpointed request.

### 14.4 Runtime Host recovery

Startup reconciliation extends its compact replay scan:

- an unmatched `ContextCompactionStarted` becomes
  `ContextCompactionFailed{Code: "runtime_recovered"}` under current fencing
  authority;
- a matched completion/failure needs no action;
- no summary or reset is synthesized during recovery;
- unknown recovery append outcome uses the existing stable recovery Append ID
  and resolver pattern;
- reconciliation order closes active compaction before it terminalizes an
  enclosing active Turn, preserving Domain eligibility.

## 15. Operation flows

### 15.1 Pre-turn automatic preparation

```text
validate RunTurn request and idempotency
acquire local request execution ownership
load bounded Session state
allocate Turn/Item/Command/decision IDs
read latest verified checkpoint
Pass 1 pinned plan including incoming input and Tool Schemas
if pressure: run compaction bracket and commit checkpoint
materialize checkpoint + raw tail + incoming input
append admission + context.prepared + full model.request.recorded
dispatch Provider attempt
```

If compaction changes Session version, admission uses the post-compaction
version. Concurrent duplicate `RunTurnRequestID` callers remain joined behind
the existing execution registry until admission is durable.

### 15.2 Mid-turn preparation

After all Tool Result events for a Step commit and before the next assistant
item is dispatched:

```text
allocate next Item/decision IDs
plan at a pinned post-tool head
compact if required while the Turn owner is between Steps
append assistant.message.started + context.prepared + full request
dispatch Provider
```

Closed early Steps of the active Turn may enter coverage; the new open item may
not.

### 15.3 Provider overflow recovery

Application intercepts only an `engine.ProviderFailure` with durable code
`context_overflow` produced before any stream delta/tool call was emitted.

```text
close failed stream and snapshot attempt stats
check caller cancellation and per-Turn recovery count
force overflow plan against latest committed source
require at least 10% estimated reduction
commit summary or reset checkpoint
append a new context.prepared + full request with attemptIndex+1
retry Provider once per recovery, at most 2 recoveries per Turn by default
```

If no safe prefix exists, reduction is insufficient, the cap is exhausted, or
the retry still overflows, the existing assistant/Turn failure path persists
`context_overflow`. Other Provider failures never enter this flow.

### 15.4 Manual compaction

`Service.CompactSession(ctx, request)` accepts Session ID, strategy
(`summary` default or explicit `reset`), and optional focus text bounded to
4 KiB. It acquires Session compaction ownership, requires an active idle
Session, runs the same planner/bracket/checkpoint append, and returns checkpoint
identity plus token evidence. Below trigger, manual summary is still allowed if
a safe prefix exists; it is a no-op when nothing can be covered.

`och compact-session` opens the normal composition root and therefore must
acquire the Runtime lease. It fails rather than operating beside another live
writer. Output is one stable JSON object on stdout; logs remain on stderr.

Manual cancellation closes a started bracket as failed within the configured
cleanup timeout. It never falls through to reset unless the request explicitly
selected reset.

## 16. Failure algebra

New stable internal/Application codes:

| Code | Category | Retryable by caller | Meaning |
| --- | --- | --- | --- |
| `context_budget_invalid` | validation/config | no | Route capacity cannot produce a positive hard input budget. |
| `context_projection_invalid` | internal | no | Canonical events cannot form valid message/tool units. |
| `context_unit_too_large` | context/model | no | One protected projected unit exceeds hard input. |
| `context_compaction_busy` | conflict | yes after owner finishes | Another durable compaction owns the Session. |
| `context_nothing_to_compact` | validation for manual; no-op internally | no | No safe source prefix exists. |
| `context_summary_failed` | model | yes by a later command | Active-route summary call or stream failed. |
| `context_summary_invalid` | model | yes after active-route or prompt-version change | Output failed structure, truncation, redaction, or shrink validation. |
| `context_checkpoint_invalid` | internal/store | no | Candidate or loaded checkpoint violates coverage/lineage/schema. |
| `context_compaction_limit` | model | no in current Turn | Chunk or overflow recovery cap was exhausted. |

Existing `context_overflow`, store codes, `canceled`, and
`commit_outcome_unknown` retain their meanings.

Failure policy:

- before a compaction start append: no compaction event is written;
- after start: exactly one completed or failed terminal is attempted;
- summary failure below hard budget: terminal failure is logged, then the
  source-derived request may proceed;
- summary failure at hard budget: automatic/overflow may attempt deterministic
  reset; manual summary returns its failure;
- checkpoint completion append unknown: resolve exact intent; never summarize
  again while outcome is unknown;
- checkpoint completion version conflict: close/reconcile the old attempt and
  replan once from a new head under the same operation deadline;
- context-prepared/request append failure: no Provider dispatch occurred;
- Provider dispatch failure after the request append follows existing terminal
  semantics, except the bounded startup-overflow path;
- runtime delivery failure never changes whether a checkpoint or model request
  committed.

## 17. Concurrency and cancellation

- The existing RunTurn execution registry continues to deduplicate one
  `RunTurnRequestID`. A Session-scoped compaction registry serializes manual and
  automatic compaction locally; durable aggregate state plus Store CAS protects
  across processes.
- Automatic pre-turn compaction happens after request ownership but before
  admission. A different request cannot start a Turn while durable compaction
  state is active.
- Mid-turn compaction is called only by the current Turn owner. No background
  task may retain a pointer to mutable projection state.
- The summarizer receives the caller signal. Cancellation is checked before and
  after every stream read, summarizer call, pinned page, ID allocation, and
  append.
- After a compaction start commits, cleanup uses `context.WithoutCancel` plus
  the existing bounded terminal-commit pattern to append failed. If cleanup
  outcome is unknown, normal resolver rules apply.
- Cancellation before conversation Provider dispatch cannot leave a recorded
  request that was sent; it may leave a prepared request event that is clearly
  not followed by usage/assistant output and whose Turn is terminalized.
- Manual compaction and Session close/delete are mutually exclusive through
  Domain state. Shutdown waits only within existing Host bounds.

`go test -race` must cover concurrent manual/manual, manual/RunTurn,
RunTurn/close, overflow/cancel, and unknown-append/cancel winner tables.

## 18. Security, privacy, and prompt injection

- Summary requests use the active conversation Provider and credential. This
  milestone introduces no second Provider, credential, transport, or
  cross-Provider history path.
- DeepSeek Harness has a concrete independently configurable
  `summarizationProvider`/`summarizationModel` precedent, and Grok Build has
  a dedicated compaction-model precedent. Open Code Harness deliberately
  defers that surface until a concrete cost, capacity, or compliance consumer
  can define its fallback and data-boundary requirements.
- Summary output passes the existing shape-specific secret redactor before
  persistence and use. Source events remain subject to their existing
  persistence rules; compaction is not retroactive data deletion.
- The summarizer has no Tools and cannot execute instructions found in source.
  Delimiters and prompt language label source, prior summary, and manual focus
  as untrusted data.
- The checkpoint is inserted as a fixed-framed user-role context message, not
  a privileged system policy. It cannot override product/system rules a future
  prompt layer adds.
- Tool Result projection uses canonical byte counts and SHA-256; it escapes
  marker delimiters inside excerpts and never treats result text as metadata.
- All summary/focus/error text has UTF-8, byte, token, and log-display caps.
- Content-bearing compaction events remain local under existing audit/export
  policy; OpenTelemetry export is outside this slice.

## 19. Resource bounds

| Resource | Bound |
| --- | --- |
| EventStore page | existing maximum 256 records |
| In-memory raw conversation | at most one `hardInput` envelope plus one open unit |
| Protected-tail metadata | at most `protectedTail` tokens plus one unit |
| Serialized conversation request | existing 4 MiB `MaxProjectionBytes` |
| Summary output | `summaryOutputCap` and 256 KiB |
| Summary input | selected route input budget; complete-unit fitted |
| Summary chunks | default 8, maximum 16 |
| Summary calls | at most 1 per chunk through the active route |
| Overflow compactions | default 2, maximum 3 per Turn |
| Pruned Tool Results recorded per request | maximum 64 |
| Manual focus | 4 KiB UTF-8 |
| Compaction wall time | default 2 minutes, maximum 10 minutes |
| Active compactions per Session | 1 |
| Latest checkpoint projection | 1 row/value per Session |

The canonical event stream and audit replica intentionally remain unbounded on
disk over Session lifetime; that is durable history, not live working context.
Benchmarks must show heap use depends on model budget/page size, not historical
event count.

## 20. Adapter, protocol, and projection effects

### 20.1 OpenAI-compatible Provider

- accepts full mixed-role message history in both tool and model-only modes;
- accepts request-specific output cap and purpose;
- normalizes `InputTokens` as total prompt occupancy including cached input,
  treats `CachedInputTokens` as a subset, and rejects a cached count greater
  than total input;
- keeps current closed overflow classification;
- rejects summary Tool Calls through the summarizer adapter, not vendor logic;
- records no raw prompts in error text or logs.

### 20.2 Memory and SQLite stores

Both implement identical ContextCheckpointStore conformance. SQLite migration,
backup, audit import, head rebuild, fault injection, and corruption tests cover
the new projection. Memory remains a deterministic reference, not a special
production branch.

### 20.3 JSONL audit and transcript

The strict event codec adds all context events and rejects unknown/extra fields
as today. Audit export/import hashes them normally. Transcript projection adds
bounded facts for compaction start/completed/failed and context preparation;
completed includes checkpoint metadata and summary because the transcript is
an explicit content-bearing export. Golden fixtures and hashes change
intentionally.

### 20.4 ACP

ACP `session/prompt` benefits from the Context Engine because it calls the same
Application service. `session/load` continues to project canonical user,
assistant, and Tool facts; it must not replace visible conversation history
with a checkpoint. Context events are not mapped to fabricated ACP message or
plan updates. A future TUI/protocol design may add a standards-compliant
surface; this slice keeps ACP v1 conformance unchanged.

## 21. Configuration and composition

`composition.Config` gains:

```go
type Context struct {
    TriggerPercent                  uint32
    TargetPercent                   uint32
    TailPercent                     uint32
    MaxSummaryChunks                uint32
    MaxOverflowCompactionsPerTurn   uint32
    MaxPrunedToolResultsPerRequest  uint32
    CompactionTimeout               time.Duration
}
```

Zero scalar values receive §8 defaults. Invalid relationships fail before any
resource construction. The summarizer receives the already-constructed active
conversation Provider through an internal Application dependency; no Provider
registry, second Provider config, or plugin kernel is introduced.

Construction order becomes:

```text
Runtime Host/store
conversation Provider + runner
Context meter/engine + summarizer
workspace tools/catalog
Application service
ACP adapter
```

Every resource constructed after Host launch participates in the existing
release-on-failure path.

## 22. Testing and evaluation

### 22.1 Pure Context Engine tests

- table tests for exact budget math at 8K, 32K, 128K, and near-invalid routes;
- golden meter fixtures for ASCII, Chinese, code, JSON, Tool Calls, Tool
  Results, schemas, checkpoint framing, and prune markers;
- provider-usage anchor matrices for exact matches, append/replacement deltas,
  every identity/envelope mismatch, zero/missing/malformed usage, and the
  non-lowering rule;
- projector fixtures covering complete/failed/interrupted Turns and interleaved
  multi-call Tool results;
- boundary matrices for between-Turn, within-Turn, active Step, no safe prefix,
  prior checkpoint, and model switch;
- fuzz properties: no orphan result, no missing retained Call result, coverage
  is a prefix, cut always advances, materialized estimate respects hard input;
- checkpoint digest, lineage, same-coverage rewrite, current-budget, and clone
  tests;
- summary shape, redaction, truncation, duplicate/missing headings, shrink, and
  reset marker goldens.

### 22.2 Application scenario tests

- history works identically with empty and non-empty Tool Catalogs;
- first request and every Step record the complete envelope they send;
- pre-turn, mid-turn, explicit manual summary/reset, and startup overflow;
- active-route summary success/failure/cancellation and absence of
  cross-Provider fallback;
- provider-usage anchor accept/reject, signed append/replacement deltas,
  route/tool/meter mismatch, zero/missing usage, and cached-input non-addition;
- chunk cap, non-shrinking output, hard-limit reset, and single-unit failure;
- append success/failure/unknown/version conflict at start, completed, failed,
  context-prepared, and terminal Turn phases;
- duplicate RunTurn joins while pre-turn compaction is active;
- no Provider call occurs before its complete request append commits;
- provider startup overflow retries only after reduction; mid-stream errors do
  not retry;
- reconstruction and crash recovery across every durable phase.

### 22.3 Store and recovery conformance

- shared memory/SQLite latest-checkpoint lookup, copying, successor, corruption,
  migration, rebuild, and import cases;
- SQLite transaction rollback when checkpoint projection update faults;
- Runtime Host reconciliation idempotency and unknown-outcome recovery;
- JSONL round-trip, transcript facts, audit digest, backup, and reader/no-lease
  behavior.

### 22.4 Required commands

Implementation completion evidence includes fresh output from:

```text
go test ./...
go test -race ./...
go vet ./...
```

plus targeted fuzz smoke, architecture/doc guards, SQLite fault suites, and
benchmarks over equivalent 100-Turn, 1,000-Turn, and 10,000-Turn streams.

Benchmark acceptance:

- below-trigger and checkpoint-replay live heap is bounded by configured token
  budget, not Turn count;
- normal checkpoint lookup is bounded and does not scan the complete stream;
- cold projection rebuild may be O(history) but is paged and heap-bounded;
- no request exceeds `hardInput` or 4 MiB before Provider dispatch;
- every accepted compaction demonstrates measured shrink;
- no benchmark makes a GA latency claim from a single machine sample.

Mutation evidence must independently kill at least: trigger comparison,
output/safety reserve, tool-pair boundary, source filter/digest, append-before-use,
summary shrink validator, reset hard-limit gate, overflow attempt cap, and
checkpoint current-budget replay check.

## 23. Documentation and completion evidence

Implementation publication must add:

- `docs/architecture/context-engine.md` and synchronized Chinese reading copy;
- `docs/architecture/context-engine-evidence.md` with task commits, mapping
  tables, commands, actual outputs, mutation results, benchmark environment,
  deviations, and remaining blockers;
- authority-table rows and root README links;
- milestone 8 prose that distinguishes already implemented persistence/recovery
  from the newly implemented Context Engine;
- CLI help and getting-started examples for automatic/manual compaction;
- configuration, privacy, failure, and operator guidance.

The milestone remains “not GA” until real-model quality evaluation and wider
provider coverage exist. Passing fixture and fault tests proves the mechanism,
not universal summary quality.

## 24. Implementation boundary and likely file map

The later implementation plan may refine names, but may not collapse the
approved boundaries:

```text
internal/harness/contextengine/
  budget.go
  meter.go
  source.go
  projector.go
  planner.go
  tool_result.go
  checkpoint.go
  materialize.go
  prompt.md
  summarizer_validation.go

internal/harness/domain/
  context events/commands/IDs, codec, apply/decide rules

internal/harness/application/
  context ports, orchestration, manual use case, overflow integration

internal/harness/engine/
  request purpose/output cap and reusable bounded collector

internal/harness/adapters/{memory,sqlite,openaicompat}/
  checkpoint projection, migration/rebuild, request mapping

internal/harness/runtime/
  incomplete-compaction reconciliation

internal/harness/{transcript,adapters/acp}/
  canonical load/transcript behavior and explicit non-projection tests

internal/harness/composition/, cmd/och/
  Context configuration and manual command
```

## 25. Rejected alternatives

### In-memory-only summary

Rejected because restart, audit, and replay would disagree, and every request
could pay to summarize the same source again.

### Rewrite or delete historical events

Rejected because it destroys the canonical recovery/audit source and violates
the project charter.

### Separate Context database as a second authority

Rejected because it creates dual-write and source-of-truth ambiguity. The
SQLite checkpoint table is explicitly a rebuildable index over a canonical
event.

### Background compaction below a block threshold

Rejected for this milestone because it needs source-head leases and stale-result
arbitration merely to reduce foreground latency. Synchronous boundaries are
simpler and deterministic.

### Tail-only truncation as the normal strategy

Rejected because it discards goals and decisions on every long Session. It is
retained only as an explicit, fact-free hard-limit fallback.

### Separate summary Provider in this milestone

Deferred despite real precedent: at pinned commit `0a53fb55`, DeepSeek
Harness exposes a paired `summarizationProvider`/`summarizationModel` in
`packages/compaction/compaction-basic/src/config.ts`; at `bc7f02e`, Grok
Build exposes a dedicated compaction model. This repository has no current
deployment consumer for a second Provider, while adding one now creates
configuration, credential, transport shutdown, failure classification, and
cross-Provider data-boundary semantics. The active conversation route is
sufficient for v1. A later focused design requires a concrete cost, capacity,
or compliance need and must choose strict failure versus fallback without
silently crossing a configured privacy boundary.

### Provider usage as the only token truth

Rejected because usage describes a previous request and may omit Tools,
framing, route changes, or newly appended content. Matching durable usage is
used only as the non-lowering anchor defined in §8.

### Provider-native checkpoint interface without an implementation

Rejected as speculative. The checkpoint union is versioned so a later real
Provider can add one through a focused design.

## 26. Review checklist

Reviewers should reject the design if any implementation can:

- use a summary before its canonical completion append commits;
- split a Tool Call from its terminal result;
- let a checkpoint replace non-prefix source events;
- trust a derived index over a disagreeing event;
- send a Provider request larger than the hard input or byte cap;
- retry after a delivered stream delta;
- route summary history anywhere except the active conversation Provider;
- turn cancellation into a reset or completion;
- make live heap scale with Session lifetime;
- claim the checkpoint is canonical history;
- mark milestone 8 implemented without recovery, conformance, race, mutation,
  benchmark, documentation, and evidence gates.
