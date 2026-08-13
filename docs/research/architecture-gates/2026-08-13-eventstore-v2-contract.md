# EventStore v2 Contract Architecture Gate

**Status:** Complete research evidence  
**Date:** 2026-08-13  
**Scope:** Contract migration before a physical SQLite adapter is implemented.

This gate narrows the accepted runtime persistence research to the first
delivery slice. It determines which guarantees must exist in Domain,
Application, adapter contracts, test doubles, and conformance tests before a
database implementation can begin. It does not select SQL schema or driver
mechanics.

## Questions

1. What distinguishes exact append retry from ordinary event-ID deduplication?
2. Which identity is owned by the client, Application, and EventStore?
3. How can a paginated reader observe one immutable stream head while writes
   continue?
4. How should Application react when commit outcome is unknown?
5. Which current coding-agent practices are evidence, and which are too weak
   for the required runtime contract?

## Primary-source comparison

| Source | Observed contract | Adopt | Boundary |
| --- | --- | --- | --- |
| [KurrentDB/EventStoreDB append documentation](https://docs.kurrent.io/clients/tcp/dotnet/21.2/appending) | Expected revision provides optimistic concurrency; retrying the same event identities at the same expected revision can be acknowledged without duplicating events. Disabling the concurrency check weakens idempotence. | Exact expected-version CAS, stable pre-append identities, all-or-none ordered batches, and explicit acknowledgement semantics. | Event-ID comparison alone is insufficient for our batch receipt, admission side effects, request digest, and post-commit resolution requirements. |
| [Temporal History Service](https://github.com/temporalio/temporal/blob/main/docs/architecture/history-service.md) | Workflow history can recover relevant state; mutable state and generated tasks are transactionally coordinated with state transitions. A monotonic range ID fences shard ownership. | Immutable semantic history, derived serving state, transactionally registered side work, and monotonic ownership epochs. | Distributed shards, replication, task queues, and automatic workflow/activity behavior are outside this local contract slice. |
| [Maka runtime and resume architecture](https://github.com/maka-agent/maka-agent/blob/main/docs/architecture/runtime-resume-architecture.md) | Recovery distinguishes repair, continuation, and retry and avoids blind replay when an external effect is uncertain. | Stage-aware recovery and a rule that unknown persistence never authorizes repeating a model or tool effect. | The public design does not define our exact Go interfaces or AppendID receipt semantics. |
| [OpenAI Codex live writer](https://github.com/openai/codex/blob/main/codex-rs/thread-store/src/local/live_writer.rs) | Canonical JSONL is flushed before SQLite materialization; SQLite is explicitly rebuildable and may lag but never lead. | Make authority and projection ordering explicit and testable. | Its JSONL authority and repair path are a different physical design, not an EventStore v2 adapter contract. |
| [OpenCode session schema](https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/session/session.sql.ts), [Goose SessionManager](https://github.com/aaif-goose/goose/blob/main/crates/goose/src/session/session_manager.rs), [Crush session service](https://github.com/charmbracelet/crush/blob/main/internal/session/session.go), [Hermes session documentation](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/sessions.md) | SQLite-backed coding agents commonly use durable Session/Message rows as their recovery source. Goose additionally demonstrates WAL, bounded busy waiting, and `BEGIN IMMEDIATE`; Hermes explicitly calls SQLite canonical. | SQLite authority is operationally conventional; keep durable facts separate from transient bus/UI state. | Mutable transcript CRUD does not prove immutable domain authority, exact append receipts, lost-ack recovery, or no-replay external-effect safety. |

## Findings

### F1. Receipt identity must cover the complete append

One stable `AppendID` identifies one atomic request. Its digest covers the
Session, expected version, Command, optional admission record, and every ordered
proposed Event including ID, schema, occurrence time, type, and canonical
payload. Reusing the ID with any different durable effect is an identity
mismatch. Event IDs remain necessary, but do not replace a batch receipt.

### F2. Unknown outcome is a first-class result

A pre-commit failure can be definitely not committed. A failure after commit is
attempted cannot be translated to absence without resolving the receipt. The
contract therefore distinguishes `StoreUnavailable` from
`CommitOutcomeUnknown`, and exposes read-only `ResolveAppend`.

### F3. Request admission and event append are one transaction

`RunTurnRequestID` is caller-stable and globally unique. Its versioned digest,
Session, Command, Turn, Item, and admission Append are registered with the
admission events. A duplicate request observes the existing execution or result;
it never starts a second model call.

### F4. Pagination pins a logical head, not a connection

The first page captures `HeadVersion`. Later pages repeat that value and return
only records at or before it. No read transaction remains open between calls,
and concurrent appends cannot change the logical view.

### F5. Compact command state and transcript queries are different models

The write aggregate retains only bounded state required for command decisions.
Historical transcript is a projection. Historical Turn/Item uniqueness remains
a synchronous integrity rule and cannot be lost merely because completed
objects leave the compact aggregate.

## Adopted gate decisions

1. Slice 1 is a deliberate breaking contract migration; no compatibility shim
   may preserve the ambiguous v1 `Load`/`Append -> []RecordedEvent` semantics.
2. Domain gains validated `AppendID`; Application owns `AppendID`, `EventID`,
   event schema, occurrence time, and stable request identity before first I/O.
3. The Store owns stream sequence and global commit position only.
4. `ReadStream`, `Append`, `ResolveAppend`, and `FindCommandRequest` form the
   complete EventStore v2 interface.
5. Every adapter and test double must pass one shared conformance suite.
6. Application never re-decides a retained append intent and never repeats a
   model effect to resolve persistence uncertainty.
7. The current full-history aggregate is replaced only after deterministic
   equivalence tests prove the compact form makes identical decisions.
8. SQLite implementation, JSONL export, Runtime lease acquisition, ACP, and TUI
   remain separate slices; their required fields are represented without dummy
   production behavior.

## Evidence limitations

- Public documents show observable behavior, not undisclosed guarantees.
- A missing published invariant is treated as unknown, not disproven.
- External type names are not copied; this project owns its contracts and tests.
- The linked OpenCode `dev` schema is volatile and must be rechecked before any
  implementation-level reuse.
