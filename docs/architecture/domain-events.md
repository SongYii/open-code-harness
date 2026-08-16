# Domain Events, Atomic Decisions, and State Machines

The `internal/harness/domain` package is the deterministic, internal source of
truth for session, turn, and Item lifecycle. Commands are validated into
ordered batches of uncommitted typed events; recorded events are then applied
or replayed to reconstruct state. This document describes the current
milestone contract.

## State Machines

```text
nonexistent --session.created--> active --session.closed--> closed
```

```text
absent --turn.started--> running --turn.completed----> completed
                              |--turn.failed---------> failed
                              `--turn.interrupted----> interrupted
```

```text
absent --assistant.message.started--> running --assistant.message.completed----> completed
                                            |--assistant.message.failed---------> failed
                                            `--assistant.message.interrupted----> interrupted
```

```text
absent --tool.call.started--> running --tool.call.completed----> completed
                                    |--tool.call.failed---------> failed
                                    `--tool.call.interrupted----> interrupted
```

Sessions have only `active` and `closed` states. Turns have only `running`,
`completed`, `failed`, and `interrupted` states. Terminal turn transitions are
mutually exclusive: a terminal turn cannot transition again. Write-side Item
kinds are `assistant_message` and `tool_call`. Approval is not a third Item
kind: `approval.requested` and `approval.resolved` are version-only facts on
the running tool Item. A running assistant Item has no durable partial text. A
completed assistant Item contains the exact final UTF-8 text and may carry
optional `toolCalls`. Failed and interrupted Items contain a stable
machine-readable code and an optional safe display message. `validateSession`
accepts `assistant_message | tool_call`; a crash-left running tool Item makes a
different Request ID fail with `item_already_running` / `turn_already_running`,
not an invalid session structure.

## Stable Catalog

### Commands

| Command name | Typed command | Ordered resulting events |
| --- | --- | --- |
| `session.create` | `CreateSession` | `session.created` |
| `turn.start` | `StartTurn` | `turn.started` |
| `turn.complete` | `CompleteTurn` | `turn.completed` |
| `turn.fail` | `FailTurn` | `turn.failed` |
| `turn.interrupt` | `InterruptTurn` | `turn.interrupted` |
| `assistant.turn.start` | `StartAssistantTurn` | `turn.started`, `assistant.message.started` |
| `assistant.message.start` | `StartAssistantMessage` | `assistant.message.started` |
| `assistant.message.complete` | `CompleteAssistantMessage` | `assistant.message.completed` |
| `assistant.turn.complete` | `CompleteAssistantTurn` | `assistant.message.completed`, `turn.completed` |
| `assistant.turn.fail` | `FailAssistantTurn` | `assistant.message.failed`, `turn.failed` |
| `assistant.turn.interrupt` | `InterruptAssistantTurn` | `assistant.message.interrupted`, `turn.interrupted` |
| `model.request.record` | `RecordModelRequest` | `model.request.recorded` |
| `model.usage.record` | `RecordModelUsage` | `model.usage.recorded` |
| `tool.call.start` | `StartToolCall` | `tool.call.started` |
| `tool.call.complete` | `CompleteToolCall` | `tool.call.completed` |
| `tool.call.fail` | `FailToolCall` | `tool.call.failed` |
| `tool.turn.interrupt` | `InterruptToolTurn` | optional `approval.resolved`, `tool.call.interrupted`, `turn.interrupted` |
| `tool.turn.fail` | `FailToolTurn` | `tool.call.failed`, `turn.failed` |
| `policy.decision.record` | `RecordPolicyDecision` | `policy.decision.recorded` |
| `approval.request` | `RequestApproval` | `approval.requested` |
| `approval.resolve` | `ResolveApproval` | `approval.resolved` |
| `session.close` | `CloseSession` | `session.closed` |

The four `*AssistantTurn` commands are composite use-case commands.
`StartAssistantTurn` admits a Turn and its initial assistant Item as one
ordered decision batch, eliminating a durable split-start state. It calls the
same pure `CheckStartAssistantTurnEligibility` predicate used by Application
preflight before it validates request fields or generated IDs. The predicate
validates the complete existing Session/Turn/Item structure, requires an
active Session, and rejects any running Turn or Item; it never inspects input
or not-yet-generated identities. Complete structure includes RFC3339-
representable lifecycle timestamps and an exact replay-possible Version:
`session.created`, every Turn/Item start and terminal fact represented by the
state, plus `session.closed` when closed. A structurally impossible in-memory
state is rejected before Application allocates run IDs.

`CompleteAssistantMessage` is item-only: it emits `assistant.message.completed`
(optionally with `toolCalls`) and leaves the Turn running so a tool Item or a
later assistant Step can start. `CompleteAssistantTurn` remains the final-Step
composite and still emits item completed then `turn.completed`.

The assistant terminal composites terminalize the active assistant Item and
its owning Turn as one ordered decision batch. `InterruptAssistantTurn`
copies its stable `Code` into
`TurnInterrupted.Reason`; the existing Turn event encoding therefore remains
schema-compatible. `InterruptToolTurn` / `FailToolTurn` are the matching
composites when the active Item is `tool_call`. Optional `ApprovalID` on
`InterruptToolTurn` prepends `approval.resolved{decision=canceled}`. There is
no standalone `InterruptToolCall`. The implemented interruption codes are
`caller_canceled`, `runtime_delivery_failed`, and `request_abandoned`.
`process_crash` remains reserved for later crash-recovery work.

Bare `CompleteTurn` / `FailTurn` / `InterruptTurn` still reject a running Item
with `item_already_running`. Application must not Decide those commands while
`ActiveItem != nil`.

`StartTurn` and `StartAssistantMessage` remain available as lower-level domain
compatibility commands. Application orchestration must not use them as two
separate admission branches; its sole assistant execution boundary is the
atomic `StartAssistantTurn` batch.

### Events

- `session.created`
- `turn.started`
- `turn.completed`
- `turn.failed`
- `turn.interrupted`
- `assistant.message.started`
- `assistant.message.completed`
- `assistant.message.failed`
- `assistant.message.interrupted`
- `model.request.recorded`
- `model.usage.recorded`
- `tool.call.started`
- `tool.call.completed`
- `tool.call.failed`
- `tool.call.interrupted`
- `policy.decision.recorded`
- `approval.requested`
- `approval.resolved`
- `session.closed`

`assistant.message.completed` may include optional `toolCalls`.
`model.request.recorded` may include optional `tools`. Each `messages[]`
object requires `role` and `text` and may include `toolCalls`, `toolCallID`,
and `name`. The codec uses an allowed-versus-required key split: old fixtures
without those extras still decode; documented extras decode; any other key
fails. `encoding/json` `omitempty` is not the compatibility story.
`model.usage.recorded.finishReason` is the closed set `stop|length|unknown|tool_calls|""`.

### Error codes

- `invalid_id`
- `invalid_command`
- `invalid_event`
- `session_already_exists`
- `session_not_found`
- `session_closed`
- `turn_already_running`
- `turn_not_running`
- `turn_mismatch`
- `turn_already_exists`
- `item_already_running`
- `item_not_running`
- `item_mismatch`
- `item_already_exists`
- `sequence_mismatch`

Consumers and tests should assert these stable codes through `IsCode`, rather
than matching error-message prose.

## Invariants and Recorded Events

- A session has at most one running turn. Starting a second turn while one is
  running fails, and a session cannot close while a turn is running.
- Production write-side `Session` is compact: it retains at most one active
  Turn and at most one active Item. Completed transcript is not part of the
  command aggregate. Historical Turn/Item uniqueness is enforced by the Store
  identity index, not by retaining completed collections in write state.
- A running Turn has at most one running Item. Starting a duplicate Item,
  starting a second Item, or terminalizing a different Item is rejected with a
  stable Item error code.
- A Turn cannot become terminal while an Item remains running. The plain
  `CompleteTurn`, `FailTurn`, and `InterruptTurn` commands reject that state;
  applying or replaying a Turn terminal event rejects it as `invalid_event`.
- A successful `CompleteAssistantTurn`, `FailAssistantTurn`, or
  `InterruptAssistantTurn` decision returns exactly two events, with the Item
  terminal fact first and the Turn terminal fact second. `InterruptToolTurn`
  returns optional `approval.resolved` then `tool.call.interrupted` then
  `turn.interrupted`. `FailToolTurn` returns `tool.call.failed` then
  `turn.failed`. The caller must append that whole batch atomically. Partial
  application is not a valid domain history.
- `CompleteAssistantMessage`, `CompleteToolCall`, and `FailToolCall` are
  item-only. After a tool-bearing assistant complete or a continuing tool
  terminal, compact state is a running Turn with no active Item.
- `policy.decision.recorded`, `approval.requested`, `approval.resolved`,
  `model.request.recorded`, and `model.usage.recorded` are version-only on the
  running Item (`tool_call` for policy/approval; `assistant_message` for model
  request/usage).
- Memory `buildBatch` and EventStore identity indexes treat `tool.call.started`
  like `assistant.message.started` for historical ItemID uniqueness.
- A successful `StartAssistantTurn` decision returns `turn.started` followed
  by `assistant.message.started`. Both facts share one append, command ID, and
  occurrence time; partial durable admission is not a valid Application
  history.
- Recorded event sequences start at `1` and must be contiguous for a session:
  each applied record must have `Sequence == state.Version + 1`. Replay does
  not sort input to repair an out-of-order stream.
- A recorded timestamp is supplied by the caller, is required, is normalized to
  UTC, and is encoded as RFC3339 with nanosecond precision (`RFC3339Nano`).
- The domain does not manufacture event IDs, command IDs, sequence values, or
  timestamps. An EventStore append assigns distinct event IDs and contiguous
  sequences, and calls its clock once so every record produced from one atomic
  decision batch has one command ID and one exact occurrence timestamp.
- Applying an event returns a new state: it must not mutate the input session's
  maps or slices. Replaying records must not mutate the input record slice or
  shared event payloads, so an immutable stream is safe to replay concurrently.
- Recorded-event JSON uses schema version `1`. Its envelope contains event and
  command IDs, a session ID, a positive sequence, an occurrence timestamp, the
  stable event type, and event data. Unknown fields, unsupported schema or
  event types, invalid metadata, and trailing JSON are rejected.

The canonical deterministic replay fixtures are:

- [`internal/harness/domain/testdata/session_lifecycle.jsonl`](../../internal/harness/domain/testdata/session_lifecycle.jsonl), which preserves the original Session/Turn schema-v1 history.
- [`internal/harness/domain/testdata/assistant_lifecycle.jsonl`](../../internal/harness/domain/testdata/assistant_lifecycle.jsonl), which proves Item replay, exact Unicode assistant text, ordered Item/Turn terminal facts, and one command ID plus one timestamp for the atomic completion pair.

## Scope and Compatibility

This document covers the internal domain event model, Session/Turn/Item state
machines, JSON fixture codec, deterministic replay, and atomic decision
semantics. The append-only EventStore and executable Engine are separate
internal packages; they consume these facts without changing Domain's
authority.

ACP v1 remains the planned public client protocol, but these internal events
are not ACP messages and are not a public compatibility promise before v1.0.
The package imports no ACP or MCP types and has no model SDK, filesystem,
clock, randomness, logging, storage, provider adapter, TUI, tool executor,
approval UI, persistence-backend, or OpenTelemetry dependency. Domain records
tool, policy, and approval facts; it does not execute tools or decide policy.
Runtime token deltas remain transient signals and are intentionally absent
from durable domain facts. The Application Step loop is outside this
milestone.
