# Domain Events and State Machine

The `internal/harness/domain` package is the deterministic, internal source of
truth for session and turn lifecycle. Commands are validated into uncommitted
typed events; recorded events are then applied or replayed to reconstruct
state. This document describes the current milestone contract.

## State Machines

```text
nonexistent --session.created--> active --session.closed--> closed
```

```text
absent --turn.started--> running --turn.completed----> completed
                              |--turn.failed---------> failed
                              `--turn.interrupted----> interrupted
```

Sessions have only `active` and `closed` states. Turns have only `running`,
`completed`, `failed`, and `interrupted` states. Terminal turn transitions are
mutually exclusive: a terminal turn cannot transition again.

## Stable Catalog

### Commands

| Command name | Typed command | Resulting event |
| --- | --- | --- |
| `session.create` | `CreateSession` | `session.created` |
| `turn.start` | `StartTurn` | `turn.started` |
| `turn.complete` | `CompleteTurn` | `turn.completed` |
| `turn.fail` | `FailTurn` | `turn.failed` |
| `turn.interrupt` | `InterruptTurn` | `turn.interrupted` |
| `session.close` | `CloseSession` | `session.closed` |

### Events

- `session.created`
- `turn.started`
- `turn.completed`
- `turn.failed`
- `turn.interrupted`
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
- `sequence_mismatch`

Consumers and tests should assert these stable codes through `IsCode`, rather
than matching error-message prose.

## Invariants and Recorded Events

- A session has at most one running turn. Starting a second turn while one is
  running fails, and a session cannot close while a turn is running.
- Recorded event sequences start at `1` and must be contiguous for a session:
  each applied record must have `Sequence == state.Version + 1`. Replay does
  not sort input to repair an out-of-order stream.
- A recorded timestamp is supplied by the caller, is required, is normalized to
  UTC, and is encoded as RFC3339 with nanosecond precision (`RFC3339Nano`).
- Applying an event returns a new state: it must not mutate the input session's
  maps or slices. Replaying records must not mutate the input record slice or
  shared event payloads, so an immutable stream is safe to replay concurrently.
- Recorded-event JSON uses schema version `1`. Its envelope contains event and
  command IDs, a session ID, a positive sequence, an occurrence timestamp, the
  stable event type, and event data. Unknown fields, unsupported schema or
  event types, invalid metadata, and trailing JSON are rejected.

The canonical deterministic replay fixture is
[`internal/harness/domain/testdata/session_lifecycle.jsonl`](../../internal/harness/domain/testdata/session_lifecycle.jsonl).

## Scope and Compatibility

This milestone is deliberately limited to the internal domain event model,
session/turn state machine, JSON fixture codec, and in-memory deterministic
replay. It does not implement a production append-only event store or the
executable Engine vertical slice.

ACP v1 remains the planned public client protocol, but these internal events
are not ACP messages and are not a public compatibility promise before v1.0.
The package imports no ACP or MCP types and has no model SDK, filesystem,
clock, randomness, logging, storage, provider adapter, TUI, tool, approval,
item, persistence-backend, or OpenTelemetry dependency. Those capabilities
remain outside this milestone.
