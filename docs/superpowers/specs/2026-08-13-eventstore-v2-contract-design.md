# EventStore v2 Contract Migration

**Status:** Proposed for review

**Date:** 2026-08-13

**Parent:** [Production Runtime Persistence, Recovery, and Client Boundary](2026-08-13-runtime-persistence-recovery-client-design.md)

**Evidence:** [EventStore v2 Contract Architecture Gate](../../research/architecture-gates/2026-08-13-eventstore-v2-contract.md)

## 1. Decision summary

This slice replaces the current complete-stream, adapter-owned-metadata
EventStore with an exact, paginated, receipt-bearing v2 contract before any
SQLite code is written. Application owns stable append and event identities,
event timestamps, schema versions, and caller request admission. The Store
owns only stream sequence and global commit position.

The migration is intentionally breaking. Every Application use case, memory
adapter, test double, fixture, error mapping, and shared conformance suite moves
to the new contract in one reviewed slice. No compatibility adapter preserves
the v1 ambiguity that every non-nil append error means definite non-commit.

## 2. Goals

- Define exact retry using `AppendID` and a digest of the complete append.
- Make lost acknowledgement an explicit, resolvable state.
- Provide pinned-head paginated stream reads.
- Persist caller-stable `RunTurnRequestID` admission with its events.
- Prevent exact duplicate requests from starting a second model call.
- Move event ID, schema, and occurrence-time ownership into Application.
- Replace unbounded write-side history with a compact command aggregate.
- Preserve deterministic decisions and historical Turn/Item uniqueness.
- Supply one adapter-neutral conformance suite and deterministic fault matrix.

## 3. Non-goals

- SQLite schema, migrations, PRAGMA configuration, backup, or driver behavior.
- JSONL outbox, segments, manifests, export, or import.
- Runtime lease acquisition, heartbeat, takeover, or crash reconciliation.
- Transcript projection, snapshot optimization, ACP adapter, or TypeScript TUI.
- Model retry, tool execution, context management, or remote transport.

The v2 request carries Runtime identity and fencing token because all adapters
must accept the final authorization shape. This slice tests those fields and a
deterministic memory-owner check; durable lease lifecycle belongs to Slice 2.

## 4. Normative decisions

### EV2-01 — Identity ownership

- The caller owns `RunTurnRequestID` and reuses it for one logical request.
- Application allocates `CommandID`, one `AppendID` per atomic append, and one
  `EventID` per proposed Event before the first Store call.
- Application captures one UTC `OccurredAt` per atomic decision batch and sets
  Event schema version `1`.
- The Store assigns per-Session `Sequence` and global `CommitPosition`.
- Runtime composition supplies a non-zero `RuntimeID` and `FencingToken`.

`CommandID` correlates the admission and terminal appends of one `RunTurn`; it
is not an idempotency key. An exact retry reuses the `AppendID`, proposed Event
IDs, timestamps, schema versions, payloads, admission, and expected version.

### EV2-02 — New domain identifiers

`domain.AppendID` and `domain.RunTurnRequestID` are validated opaque UTF-8
strings using the same no-empty/no-padding boundary as existing IDs. Runtime ID
is an Application/storage identity, not a Domain aggregate identity;
`RuntimeID` is a validated opaque string with the same boundary, and a fencing
token must be greater than zero.

The ID generator becomes:

```go
type IDGenerator interface {
    NewSessionID() (domain.SessionID, error)
    NewTurnID() (domain.TurnID, error)
    NewItemID() (domain.ItemID, error)
    NewCommandID() (domain.CommandID, error)
    NewAppendID() (domain.AppendID, error)
    NewEventID() (domain.EventID, error)
}
```

### EV2-03 — EventStore interface

```go
type EventStore interface {
    ReadStream(context.Context, ReadStreamRequest) (StreamPage, error)
    Append(context.Context, AppendRequest) (CommitReceipt, error)
    ResolveAppend(context.Context, ResolveAppendRequest) (AppendResolution, error)
    FindCommandRequest(context.Context, FindCommandRequestRequest) (CommandRequestLookup, error)
}

type ReadStreamRequest struct {
    SessionID     domain.SessionID
    AfterSequence uint64
    Limit         uint32
    HeadVersion   *uint64
}

type StreamPage struct {
    Records           []domain.RecordedEvent
    HeadVersion       uint64
    NextAfterSequence uint64
    End               bool
}

type WriterAuthority struct {
    RuntimeID    RuntimeID
    FencingToken uint64
}

type AppendRequest struct {
    AppendID        domain.AppendID
    SessionID       domain.SessionID
    ExpectedVersion uint64
    CommandID       domain.CommandID
    Authority       WriterAuthority
    Admission       *CommandAdmission
    Events          []ProposedEvent
}

type ProposedEvent struct {
    ID            domain.EventID
    SchemaVersion uint32
    OccurredAt    time.Time
    Event         domain.Event
}

type CommandAdmission struct {
    RunTurnRequestID domain.RunTurnRequestID
    RequestDigest    Digest
    TurnID           domain.TurnID
    ItemID           domain.ItemID
}

type CommitReceipt struct {
    AppendID       domain.AppendID
    CommitPosition uint64
    FirstSequence  uint64
    LastSequence   uint64
}
```

`Digest` is a comparable 32-byte SHA-256 value with strict lower-case hex text
encoding. All returned records, receipts containing references, and lookup
records are defensive values. Nil contexts, zero limits, invalid IDs, zero
timestamps, non-UTC timestamps, empty batches, unsupported schema versions,
unknown Event types, and configured size-limit violations are rejected before
mutation.

### EV2-04 — Canonical append digest

`DigestAppendRequest(AppendRequest) (Digest, error)` is shared by Application,
adapters, and conformance tests. Format version `1` uses explicit unsigned
big-endian length framing and includes, in order:

```text
format-version
session-id
expected-version
command-id
admission-present
[request-id, request-digest, turn-id, item-id]
event-count
for each event: event-id, event-type, schema-version, RFC3339Nano UTC time,
                canonical event payload length and bytes
```

`AppendID` and `WriterAuthority` are excluded: the former is the receipt key;
the latter authorizes a new commit but does not change immutable request
identity. Canonical payload bytes reuse the strict Domain codec rules. No
unframed string concatenation or map-dependent JSON encoding is permitted.

### EV2-05 — Exact append semantics

For a new `AppendID`, validation, owner check, admission uniqueness, expected
version, historical identity reservation, event recording, and receipt creation
form one atomic decision in every adapter.

- Same `AppendID` and same digest returns the original receipt, even after the
  stream advances and regardless of current writer ownership.
- Same `AppendID` and different digest returns `AppendIdentityMismatch`.
- A new append requires exact `ExpectedVersion`; conflict is not retried.
- `EventID` is globally unique; duplicate IDs inside a batch or any committed
  stream reject the whole new append.
- A batch is wholly visible or wholly absent.
- Cancellation observed before the commit point means no mutation.
- After the commit point, cancellation cannot change a success into a definite
  non-commit result.

Slice 1 memory storage allocates monotonically increasing global commit
positions and keeps append receipts permanently for the lifetime of the store.
It is constructed with one current `WriterAuthority`; exact receipt lookup runs
before that authority check, while every new append from another Runtime ID or
token returns `WriterFenced`. Changing the deterministic owner increments the
token and cannot make an older token valid again.

### EV2-06 — Error algebra

The Store exposes stable typed codes:

```text
invalid_read
invalid_append
version_conflict
append_identity_mismatch
command_request_conflict
command_identity_mismatch
domain_identity_conflict
writer_fenced
store_unavailable
commit_outcome_unknown
store_corrupt
```

Each error states whether the attempted append is definitely absent or may
have committed. `VersionConflict` includes expected and observed versions.
Identity errors expose only the request's Session and identity kind; a lookup
must not leak another Session's record. Application maps these codes to stable
categories without collapsing `CommitOutcomeUnknown` into `append_failed`.
The typed Store error includes `MayHaveCommitted`; it is true only for
`commit_outcome_unknown` and false for every other code.

### EV2-07 — Receipt resolution

```go
type ResolveAppendRequest struct {
    AppendID      domain.AppendID
    RequestDigest Digest
}

type AppendResolutionKind string

const (
    AppendResolutionCommitted        AppendResolutionKind = "committed"
    AppendResolutionNotFound         AppendResolutionKind = "not_found"
    AppendResolutionIdentityMismatch AppendResolutionKind = "identity_mismatch"
)
```

`ResolveAppend` is read-only. Only `Committed` includes a receipt. Storage
failure is an error, never `NotFound`. Resolution is deliberately independent
of current writer authority so an old or successor Runtime can determine the
outcome without creating a new commit.

### EV2-08 — Pinned-head pagination

- `AfterSequence` is exclusive.
- A first request has nil `HeadVersion`; the Store captures the current head.
- Later pages repeat exactly that head and return only records with
  `AfterSequence < Sequence <= HeadVersion`.
- `Limit` is in `1..256`; returned record count never exceeds it.
- `NextAfterSequence` is the last returned sequence, or the input cursor for an
  empty page.
- `End` is true exactly when `NextAfterSequence == HeadVersion`.
- Missing streams have head `0`, empty records, cursor `0`, and `End=true`.
- Cursor greater than head, supplied head greater than current stream head, or
  a supplied head lower than the cursor is `InvalidRead`.

No connection or transaction is retained across pages. `ReadWholeStreamPinned`
is an Application helper that follows this protocol with a caller deadline and
replays records incrementally; it is not an EventStore method.

### EV2-09 — Durable command request admission

```go
type FindCommandRequestRequest struct {
    RunTurnRequestID domain.RunTurnRequestID
    SessionID        domain.SessionID
    RequestDigest    Digest
}

type CommandRequestRecord struct {
    RunTurnRequestID  domain.RunTurnRequestID
    RequestDigest     Digest
    SessionID         domain.SessionID
    CommandID         domain.CommandID
    TurnID            domain.TurnID
    ItemID            domain.ItemID
    AdmissionAppendID domain.AppendID
}
```

Lookup kinds are exactly `found`, `not_found`, and `identity_mismatch`; only
`found` includes the record. `RunTurnRequestID` is globally unique. An admission
record is immutable and is committed only with its Turn/Item start events.

`RunTurnRequest` requires `RequestID`. Its version-1 digest covers Session ID and
the exact UTF-8 Input. `RuntimeSink`, deadlines, and cancellation are delivery
concerns and are excluded. Future execution-semantic fields must be added by a
new digest version before they affect behavior.

### EV2-10 — Duplicate and unknown-outcome Application behavior

Before allocating Command, Turn, Item, Append, or Event IDs, `RunTurn` computes
the request digest and calls `FindCommandRequest`.

- `not_found`: create identities and attempt one atomic admission.
- `identity_mismatch`: return stable validation/conflict error; do not call the
  model.
- `found` terminal: read the pinned stream, reconstruct, and return the durable
  result without a model call.
- `found` running with a matching live execution: wait for its terminal result;
  duplicate callers do not receive historical deltas in this slice.
- `found` running without a live execution: return `reconciliation_required`;
  do not call the model. Startup recovery belongs to Slice 4.

The live registry has one entry per Request ID and these phases:

```text
admission_in_flight -> running -> terminal_append_in_flight
        |                |                 |
        v                v                 v
admission_unknown   cancel_won       terminal_unknown
        |                                  |
        `-------> terminal_committed <-----'
```

An unknown admission is resolved before any model call. A committed admission
continues only if its original live owner remains active; otherwise it appends
`request_abandoned`. An unknown terminal append retains and resolves the exact
terminal intent; it never invokes the model again. Resolution uses a bounded
Service configuration: `AppendResolutionTimeout` defaults to 5 seconds and
`AppendResolutionMaxOperations` defaults to 4 Store operations after the
initial unknown result. Each cycle calls `ResolveAppend`; `NotFound` permits one
exact `Append` of the retained request, while unavailable or unknown results
consume the next operation. The caller deadline, resolution timeout, or
operation count—whichever expires first—ends the attempt. Exhaustion returns
stable `append_outcome_unknown` and keeps
new admission for that Session blocked while the live process retains the
unresolved intent.

Exactly one resolver owns an unresolved registry entry. Later callers with the
same Request ID/digest may wait for that entry within their own context, but do
not start another resolver, allocate identities, or issue a model call.

Cancellation wins only while phase is `running`. Once terminal append enters
`terminal_append_in_flight`, cancellation stops delivery but cannot replace the
retained completed/failed intent with an interruption. CAS losers reload and
report the durable winner.

Slice 1 adds the Domain interruption code `request_abandoned`. It is used only
when admission committed but the original live owner canceled before any model
effect. `process_crash` remains reserved for Slice 4 startup reconciliation.

### EV2-11 — Compact command aggregate

`domain.Session` becomes bounded write state containing Session identity,
workspace, status, version, and at most one active Turn with at most one active
Item. Completed Turn collections, Item collections, transcript text, and order
arrays are removed after equivalence tests exist.

`ApplyCompact`/`ReplayCompact` process the same immutable recorded events.
Terminal events validate the active identity and then discard completed payload
from write state. Transcript reconstruction remains a read concern.

Historical Turn and Item reuse is rejected atomically by the EventStore through
a derived identity index. The memory adapter maintains equivalent sets. A
`DomainIdentityConflict` identifies `turn` or `item`; Application maps it to the
existing stable Domain duplicate-ID error.

Golden equivalence tests replay every current fixture through v1 and compact
logic and compare all decisions reachable with fresh IDs. Dedicated tests prove
that the new Store identity rule preserves duplicate historical-ID rejection.
The v1 oracle is frozen in test-only code, not retained as a production
compatibility path. Only after these tests pass may the old unbounded fields be
removed.

### EV2-12 — Resource bounds

Contract defaults are:

| Resource | Limit |
| --- | ---: |
| Canonical encoded Event payload | 8 MiB |
| Events per Append | 64 |
| Encoded Append request | 16 MiB |
| Read page | 256 records |
| Assistant UTF-8 output | existing 1 MiB Application limit |

Limits are validated before mutation. Canonical facts are never truncated.

## 5. Package and file boundaries

The implementation plan may refine filenames but must preserve these units:

| Area | Responsibility |
| --- | --- |
| `internal/harness/domain/ids.go` | Append and caller-request ID validation |
| `internal/harness/domain/state.go` | Compact command aggregate |
| `internal/harness/domain/apply.go` | Compact deterministic transitions |
| `internal/harness/application/ports.go` | EventStore v2 and writer authority |
| `internal/harness/application/digest.go` | Versioned append and RunTurn request digests |
| `internal/harness/application/read_stream.go` | Pinned pagination and incremental replay |
| `internal/harness/application/execution_registry.go` | Duplicate/unknown live phases and Session gate |
| `internal/harness/application/append.go` | Stable proposed-event construction, append, resolution, and apply |
| `internal/harness/adapters/memory/event_store.go` | Deterministic v2 reference adapter and fault hooks |
| `internal/harness/application/eventstoretest/` | Adapter-neutral conformance suite |

Domain remains independent of Application and adapters. Engine remains unaware
of EventStore, request receipts, and persistence resolution.

## 6. Verification contract

### 6.1 Domain

- deterministic compact replay and terminal irreversibility;
- v1 fixture decision equivalence before unbounded fields are removed;
- historical identity conflict parity;
- ID, UTF-8, timestamp, and codec fuzz tests.

### 6.2 EventStore conformance

- exact CAS and all-or-none multi-event batches;
- same-ID/same-digest receipt replay and different-digest rejection;
- admission identity races and privacy-preserving mismatch;
- commit position and per-stream sequence ordering under concurrency;
- pinned pagination while another goroutine appends;
- defensive copies, context cancellation, owner fencing, limits, and corrupt
  state fail-closed behavior;
- injected pre-commit failure, committed-but-ack-lost, resolution unavailable,
  and exact retry.

### 6.3 Application scenarios

- no ID allocation before request lookup;
- two simultaneous equal requests produce one admission and one model call;
- same Request ID with changed input produces no model call;
- unknown admission resolves before the model;
- unknown terminal append never repeats the model;
- cancellation at every live phase obeys the winner table;
- running admission without live execution returns reconciliation required;
- existing session create/load/close and successful/failing/canceled Turn
  scenarios migrate without weakened terminal durability.

All tests run with `go test ./... -count=1` and `go test -race ./... -count=1`.
Fuzz smoke and architecture dependency tests are part of the completion gate.

## 7. Delivery sequence

The later implementation plan must use TDD and separate reviewer gates for:

1. identifiers, digest codec, types, and errors;
2. pinned reads and compact aggregate equivalence;
3. v2 memory adapter and shared conformance suite;
4. Application append construction and resolution;
5. durable request admission and duplicate execution registry;
6. unknown-outcome/cancellation state machine;
7. migration of all use cases, fixtures, architecture tests, and docs.

Each task ends in a small commit and independent verification. Slice 2 cannot
start while any v1 EventStore call, adapter, or test double remains.

## 8. Acceptance criteria

- All EV2 decisions are implemented and linked from the documentation index.
- No production package calls v1 `Load` or expects Append to return records.
- Every EventStore implementation passes the shared v2 conformance suite.
- Application never generates a second model call for one Request ID/digest.
- Unknown commit outcome is observable and resolvable, never mapped to absence.
- Compact replay preserves current decision behavior and bounded write state.
- Normal, race, fault, fuzz-smoke, and architecture tests pass.
- Completion evidence lists task commits, exact commands, exclusions, and the
  remaining SQLite/JSONL/Runtime/ACP/TUI blockers.
