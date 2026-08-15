# Implemented EventStore v2 Contract

- Status: Implemented internal contract
- Stability: `experimental` until v1.0
- Maturity: pre-v0; not a general availability release
- Scope: Slice 1 contract migration only. The in-memory adapter is a
  conformance reference, not durable production storage.
- Normative design: [EventStore v2 contract migration](../superpowers/specs/2026-08-13-eventstore-v2-contract-design.md)
- Implemented plan: [EventStore v2 contract migration implementation plan](../superpowers/plans/2026-08-13-eventstore-v2-contract.md)
- Completion evidence: [EventStore v2 evidence ledger](eventstore-v2-evidence.md)
- Chinese reading copy: [已实现 EventStore v2 合同](eventstore-v2.zh-CN.md)

This document records behavior enforced by the current code and tests. It is
an internal Go contract, not a stable public protocol. Pre-v0 changes still
require the design, implementation, tests, and this document to move together.

## Delivered capability

Application owns append identity, event metadata, request admission, and
unknown-outcome resolution. The Store assigns only per-Session sequence and
global commit position. `RunTurn` admits a caller-stable request exactly once,
resolves a lost acknowledgement without a second model call, and lets a
retained completed or failed intent beat a late cancel.

SQLite durability, JSONL replica/import, Runtime host/recovery, ACP, and TUI
are not implemented.

## Store interface

```go
type EventStore interface {
    ReadStream(context.Context, ReadStreamRequest) (StreamPage, error)
    Append(context.Context, AppendRequest) (CommitReceipt, error)
    ResolveAppend(context.Context, ResolveAppendRequest) (AppendResolution, error)
    FindCommandRequest(context.Context, FindCommandRequestRequest) (CommandRequestLookup, error)
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
```

`Load` and `Append(...) ([]domain.RecordedEvent, error)` no longer exist.
Temporary names `EventStoreV2` and `AppendRequestV2` are deleted. The
architecture AST gate rejects those production surfaces.

## Identity ownership

| Identity | Owner | Assigned |
| --- | --- | --- |
| `RunTurnRequestID` | caller | before the first Store call |
| `AppendID`, `CommandID`, `EventID` | Application | before the first Store call |
| Event schema version and UTC `OccurredAt` | Application | once per atomic batch |
| Stream `Sequence` and global `CommitPosition` | Store | on commit |
| `RuntimeID` and non-zero `FencingToken` | composition / writer authority | on every mutating request |

Reusing an `AppendID` with a different digest is `append_identity_mismatch`.
Event IDs remain required and globally unique; they do not replace a batch
receipt.

## Canonical digest

`DigestAppendRequest` is SHA-256 over a version-1 framed encoding. Covered
fields, in order:

```text
format-version
session-id
expected-version
command-id
admission-present
[request-id, request-digest, turn-id, item-id]
event-count
for each event: event-id, event-type, schema-version, RFC3339Nano UTC time,
                canonical payload length and bytes
```

`AppendID` and `WriterAuthority` are validated but excluded from the digest.
`DigestRunTurnRequestV1` covers Session ID and exact UTF-8 input only.

## Error algebra

| Code | Meaning |
| --- | --- |
| `invalid_read` / `invalid_append` | rejected before mutation |
| `version_conflict` | expected version did not match stream head |
| `append_identity_mismatch` | same `AppendID`, different digest |
| `command_request_conflict` | same request ID already admitted |
| `command_identity_mismatch` | request ID reused with a different digest or Session |
| `domain_identity_conflict` | historical Turn/Item uniqueness |
| `writer_fenced` | authority token rejected |
| `store_unavailable` | definite non-commit |
| `commit_outcome_unknown` | commit may have succeeded; only this code may set `MayHaveCommitted` |
| `store_corrupt` | fail closed |

Application maps a post-append unknown outcome to `append_outcome_unknown`.
It never translates unknown into absence.

## Pagination

The first page captures `HeadVersion`. Later pages repeat that value and return
only records at or before it. No read transaction remains open between pages.
A changed head, inverted cursor, or non-progressing page is a contract
violation.

| AfterSequence | Limit | HeadVersion | Result |
| --- | ---: | --- | --- |
| 0 | 1–256 | nil or current | first page from sequence 1 |
| last returned `NextAfterSequence` | 1–256 | pinned first-page head | next immutable page |
| `HeadVersion` | any | same pinned head | empty terminal page, `End=true` |
| greater than pinned head | any | pinned | `invalid_read` |

## Admission and live execution

`RunTurn` requires `RequestID`. Before allocating Command, Turn, Item, Append,
or Event IDs it digests the request and calls `FindCommandRequest`.

| Lookup | Behavior |
| --- | --- |
| `not_found` | elect one live owner and attempt one atomic admission |
| `identity_mismatch` | conflict; no model call |
| `found` terminal | reconstruct the durable result; no model call |
| `found` running with a local owner | wait for that owner's terminal result |
| `found` running without a local owner | `reconciliation_required`; no model call |

One registry entry exists per Request ID. Waiters do not start a second
resolver, allocate identities, or call the model. A Session that retains an
unresolved unknown append rejects a different new admission.

## Compact write state

Production `domain.Session` retains only identity, workspace, status, version,
and at most one active Turn with at most one active Item. Completed transcript
is not write-side state. Historical Turn/Item uniqueness is a Store integrity
index. The full-history aggregate exists only as a test oracle.

## Unknown-outcome resolution

Defaults: `AppendResolutionTimeout = 5s` and
`AppendResolutionMaxOperations = 4` Store operations after the initial unknown
result. Each cycle calls `ResolveAppend`. `committed` returns the validated
receipt. `not_found` permits one exact `Append` of the retained request.
`identity_mismatch` fails closed. Exhaustion returns `append_outcome_unknown`
and keeps the unresolved entry.

An unknown admission is resolved before any model call. If that admission
committed and the caller already canceled, Application appends
`request_abandoned` and does not call the model. An unknown terminal append
retains and resolves the exact completed or failed intent.

## Cancellation winner

| Phase | Cancel effect |
| --- | --- |
| `running` | may append `caller_canceled` |
| `terminal_append_in_flight` | stops delivery only |
| retained completed/failed intent | beats a late cancel |
| CAS loser | reloads and reports the durable winner if one exists |

`process_crash` remains reserved for Slice 4.

## Resource bounds

| Resource | Limit |
| --- | ---: |
| Canonical event payload | 8 MiB |
| Events per append | 64 |
| Encoded append digest | 16 MiB |
| Read page | 256 records |
| Assistant UTF-8 output | 1 MiB |
| Resolution operations after unknown | 4 |
| Resolution timeout | 5 s |

Canonical facts are rejected, never truncated.

## Exclusions

This implemented contract does not provide SQLite, JSONL export/import,
durable Runtime leases or crash recovery, ACP, TUI, tools, providers, or
context management. The memory adapter is a deterministic reference used by
the shared conformance suite. It is not production persistence.
