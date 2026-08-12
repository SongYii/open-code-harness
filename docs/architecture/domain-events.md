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

Sessions have only `active` and `closed` states. Turns have only `running`,
`completed`, `failed`, and `interrupted` states. Terminal turn transitions are
mutually exclusive: a terminal turn cannot transition again. The initial Item
kind is `assistant_message`; its lifecycle has the same one-way terminal rule.
A running assistant Item has no durable partial text. A completed Item contains
the exact final UTF-8 text, while failed and interrupted Items contain a stable
machine-readable code and an optional safe display message.

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
| `assistant.turn.complete` | `CompleteAssistantTurn` | `assistant.message.completed`, `turn.completed` |
| `assistant.turn.fail` | `FailAssistantTurn` | `assistant.message.failed`, `turn.failed` |
| `assistant.turn.interrupt` | `InterruptAssistantTurn` | `assistant.message.interrupted`, `turn.interrupted` |
| `session.close` | `CloseSession` | `session.closed` |

The four `*AssistantTurn` commands are composite use-case commands.
`StartAssistantTurn` admits a Turn and its initial assistant Item as one
ordered decision batch, eliminating a durable split-start state. It calls the
same pure `CheckStartAssistantTurnEligibility` predicate used by Application
preflight before it validates request fields or generated IDs. The predicate
validates the complete existing Session/Turn/Item structure, requires an
active Session, and rejects any running Turn or Item; it never inspects input
or not-yet-generated identities.

The three terminal composite commands terminalize the active assistant Item
and its owning Turn as one ordered decision batch. `InterruptAssistantTurn`
copies its stable `Code` into
`TurnInterrupted.Reason`; the existing Turn event encoding therefore remains
schema-compatible. The initial interruption codes are `caller_canceled` and
`runtime_delivery_failed`.

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
- `session.closed`

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
- A running Turn has at most one running Item. Every Item belongs to exactly one
  Turn, appears exactly once in that Turn's order, and the active Item ID must
  identify the sole running Item. Starting a duplicate Item, starting a second
  Item, or terminalizing a different Item is rejected with a stable Item error
  code.
- A Turn cannot become terminal while an Item remains running. The plain
  `CompleteTurn`, `FailTurn`, and `InterruptTurn` commands reject that state;
  applying or replaying a Turn terminal event rejects it as `invalid_event`.
- A successful `CompleteAssistantTurn`, `FailAssistantTurn`, or
  `InterruptAssistantTurn` decision returns exactly two events, with the Item
  terminal fact first and the Turn terminal fact second. The caller must append
  that whole batch atomically. Partial application is not a valid domain
  history.
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
clock, randomness, logging, storage, provider adapter, TUI, tool, approval,
persistence-backend, or OpenTelemetry dependency. Runtime token deltas remain
transient signals and are intentionally absent from durable domain facts.
Those capabilities remain outside this milestone.
