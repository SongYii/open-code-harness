# Production Runtime Persistence, Recovery, and Client Boundary

**Status:** Accepted design
**Date:** 2026-08-13
**Authority:** Normative
**Companion:** `2026-08-13-runtime-persistence-recovery-client-design.zh-CN.md`

## 1. Decision summary

Open Code Harness will use a **SQLite-backed canonical event log with a
transactional export outbox and a verifiable JSONL audit replica**.

- Immutable domain events in SQLite are the sole live commit authority.
- Mutable read models, session heads, and snapshots are rebuildable.
- JSONL is lossless, human-readable, portable, and sufficient to rebuild a new
  database only when a complete export verifies against its manifest. It is not
  a second online authority.
- Every atomic append has a caller-stable `AppendID`, exact expected-version
  CAS, a canonical request digest, and a durable receipt.
- One Runtime Host owns a database through a renewable lease and monotonically
  increasing fencing token.
- Startup recovery turns stale running Assistant Item/Turn pairs into an atomic
  `process_crash` interruption. It never automatically repeats a model or tool
  effect.
- The core remains a pure-Go, CGO-free, cross-platform single binary.
- The TypeScript TUI is a separate ACP v1 client. ACP is an outer adapter, not
  the internal domain model.
- Production stream reads are paginated. A compact command aggregate and
  transcript projections replace unbounded full-history loading.

The accepted comparison evidence is recorded in
`docs/research/architecture-gates/2026-08-13-runtime-persistence-recovery-client.md`.

## 2. Context

The implemented Engine vertical slice already establishes:

- pure Domain decisions and replay;
- Session, Turn, and Assistant Message Item state machines;
- Application-owned command and durability boundaries;
- atomic admission and terminal batches;
- exact expected-version behavior in the in-memory EventStore;
- one bounded model call per `RunTurn`;
- explicit completion, failure, interruption, cancellation, and runtime
  delivery behavior;
- deterministic adapters and reusable contract suites.

It intentionally does not provide production persistence, crash recovery, ACP,
or a TUI. Its current EventStore also states that every non-nil append error
means no commit and that `Load` returns the complete stream. Those assumptions
are valid for the completed in-memory milestone but are too strong or unbounded
for a production database. This design defines their deliberate v2 evolution.

## 3. Goals

1. Provide local production persistence with explicit atomicity, concurrency,
   idempotency, corruption, backup, and recovery contracts.
2. Preserve event sourcing: domain state is derived from immutable facts, not
   from mutable status tables.
3. Make every failure boundary reproducible through contract tests, fault
   injection, replay fixtures, and cross-platform tests.
4. Keep session evidence inspectable and portable without implementing a second
   file-based transaction authority.
5. Prevent split-brain Runtime Hosts and stale post-crash writers.
6. Expose a standard ACP v1 client boundary suitable for the project TUI and
   third-party IDEs.
7. Keep Go Core and TypeScript client releases independent.
8. Preserve complete synchronized Chinese documentation for architecture,
   plans, contracts, research, and evaluation evidence.

## 4. Non-goals

- A distributed or multi-node EventStore.
- Live database files on NFS, SMB, cloud-sync, or network filesystems.
- JSONL as a concurrent online write authority.
- Automatic merge of audit files into a live database.
- Automatic retry of a model call or external tool after an uncertain crash.
- Remote ACP transport as a stable v0 promise.
- A2A, cloud control plane, multi-tenancy, billing, or remote agent clusters.
- Embedding Node, Bun, or a TypeScript runtime inside the Go binary.
- A single physical artifact containing both Go Core and TypeScript TUI.
- A general plugin ABI or Go dynamic plugins.
- Context selection, compaction policy, production Provider behavior, Tool
  Runtime effects, or MCP implementation; those retain separate design gates.

## 5. Architectural invariants

1. **One commit authority:** a live domain fact exists if and only if its SQLite
   append transaction committed.
2. **One execution authority:** at most one non-expired fenced Runtime Host may
   append to a database.
3. **Exact retry:** an `AppendID` names one immutable request. Reuse with
   different bytes is an error.
4. **Atomic lifecycle:** a terminal Assistant Item and its Turn terminate in one
   append batch.
5. **Facts before delivery:** durable terminal facts are committed before ACP or
   other runtime terminal notifications.
6. **Projections are disposable:** deleting a projection, snapshot, export
   checkpoint, or active JSONL staging file cannot delete canonical facts.
7. **No blind effects:** uncertain model/tool outcomes are made explicit; they
   are never silently repeated.
8. **Bounded resources:** every stream, batch, payload, queue, transaction,
   shutdown, and external effect has a bound or deadline.
9. **Fail closed:** unknown schema, broken event invariants, digest mismatch, or
   unresolved ownership stops mutation instead of guessing.
10. **Protocol isolation:** ACP, SQLite, provider, tool, and TUI types never enter
    the Domain package.

## 6. System shape

```text
┌───────────────────────────────────────────────────────────────┐
│ Clients                                                       │
│ TypeScript TUI · Zed · JetBrains · other ACP v1 clients      │
└───────────────────────────┬───────────────────────────────────┘
                            │ JSON-RPC 2.0 / stdio in v0
┌───────────────────────────▼───────────────────────────────────┐
│ ACP Adapter                                                   │
│ schema · capabilities · validation · projection · backpressure│
├───────────────────────────────────────────────────────────────┤
│ Runtime Host                                                  │
│ composition · lease/fencing · recovery · worker lifecycle     │
├───────────────────────────────────────────────────────────────┤
│ Application                                                   │
│ commands · orchestration · append identity · transactions     │
├───────────────────────────────────────────────────────────────┤
│ Domain                                                        │
│ commands · events · compact aggregate · replay invariants     │
├───────────────┬────────────────┬──────────────────────────────┤
│ Engine        │ Provider/Tools │ Persistence                  │
│ bounded loop  │ adapters       │ SQLite · projections · audit │
├───────────────┴────────────────┴──────────────────────────────┤
│ Evaluation and Observability                                  │
│ contract suites · replay · fault injection · OTel adapters    │
└───────────────────────────────────────────────────────────────┘
```

Dependencies point inward. Composition code may import all required adapters;
Domain imports none of them.

## 7. Canonical storage model

### 7.1 `store_metadata`

A singleton row contains:

- storage format version;
- current global `head_commit_position`;
- current global `head_audit_digest` used by the next outbox envelope;
- database creation and last migration metadata.

SQLite serializes the metadata-row update inside the write transaction, so two
appends to different Session streams still receive one deterministic global
order.

The append transaction increments `head_commit_position` itself. A rolled-back
transaction does not consume a position, so committed append batches have a
contiguous global order. `head_audit_digest` supplies the new envelope's
`previousDigest` and is replaced by that envelope's digest in the same
transaction.

### 7.2 `event_streams`

One row per Session:

- `session_id` primary key;
- current stream `version`;
- creation and last-append commit positions.

The version is the last committed per-stream event sequence. It is a CAS head,
not domain state.

### 7.3 `event_appends`

One row per atomic append batch:

- `append_id` unique;
- global `commit_position` unique;
- `session_id`;
- `expected_version`;
- `first_sequence`, `last_sequence`, and `event_count`;
- `command_id`;
- `request_digest`;
- `writer_fencing_token`;
- audit format version, previous audit digest, and batch audit digest;
- `committed_at`.

This row is both the batch header and durable idempotency receipt. A separate
receipt table would duplicate the same identity and range.

### 7.4 `events`

Each immutable event contains:

- `session_id` and assigned `sequence`, unique together;
- globally unique `event_id`;
- owning `append_id` and order within the batch;
- `command_id`;
- event type and event schema version;
- UTC `occurred_at`;
- canonical JSON payload bytes and SHA-256 payload digest.

The store assigns stream sequence and global commit position. Application owns
`AppendID`, `CommandID`, `EventID`, event schema version, occurred-at time, and
the event payload before the first call, so the exact request is stable across
retries.

### 7.5 `export_outbox`

One row per committed append contains:

- `commit_position` and `append_id`;
- audit format version;
- canonical batch envelope bytes;
- envelope digest;
- export-attempt diagnostic state.

The row is inserted in the append transaction. The exact canonical envelope is
retained while publication is pending, so the exporter does not re-encode a
live append differently.

Because envelopes are encoded inside a global append transaction, the hash
chain is created by commit-position order, not by an asynchronous exporter.

After a sealed segment and manifest are verified and their SQLite checkpoint is
committed, the exact outbox envelope may be pruned. The permanent append row,
event bytes, format version, and expected digests remain. Regeneration uses the
frozen codec for that format and must reproduce the stored digest exactly or
fail closed. The audit codec registry is versioned with the binary,
`event_appends.audit_format_version` is its sole selection key, and a codec for
any committed format cannot be removed from a supported upgrade path. A missing
codec is `StoreCorrupt`; export and import fail closed, and permanent round-trip
fixtures cover every format. This avoids retaining a second full copy of every
payload forever.

### 7.6 Admission and historical-identity indexes

This durable identity table is written only for command admission and is not a
projection. It stores `RunTurnRequestID`, versioned request digest, Session,
Command, Turn, Item, and admission `AppendID`. The unique request ID prevents
two admission transactions from starting the same logical request. Terminal
status is reconstructed from the canonical stream, not independently asserted
by this table.

`domain_identities` preserves the current Domain's historical uniqueness rule
without retaining completed Turns in the compact aggregate. For each creation
event it records `(session_id, identity_kind, identity_id, introducing_event_id)`
with a unique constraint on `(session_id, identity_kind, identity_id)`. The same
Append transaction inserts the `turn` or `item` identity before committing its
creation event; a duplicate maps to the existing Domain error and aborts the
whole Append. This is a synchronous integrity index derived from canonical
events: it can be rebuilt and verified offline, but a live Store may not omit or
bypass it.

### 7.7 Rebuildable tables

- `session_heads`: minimal status and active Turn/Item candidate index;
- `transcript_entries`: paginated history for TUI and future Context consumers;
- `snapshots`: validated aggregate load acceleration;
- `export_checkpoints`: rebuildable exporter progress.

These tables are never accepted as independent proof of domain state. Recovery
candidates are discovered through `session_heads` and confirmed by authoritative
stream replay.

### 7.8 Runtime ownership

`runtime_leases` stores database ownership:

- singleton database scope;
- `runtime_id`;
- monotonically increasing `fencing_token`;
- `lease_expires_at`;
- `last_heartbeat_at`.

SQLite's own `unixepoch('subsec')` result is the lease time authority; callers do
not supply wall time. Every new domain append verifies this exact predicate in
the same write transaction:

```text
runtime_id = request.runtime_id
AND fencing_token = request.fencing_token
AND lease_expires_at >= sqlite_now
```

Takeover uses the same `BEGIN IMMEDIATE` serialization and only increments the
token when the previous lease is expired. A forward wall-clock jump may revoke
a host early and a backward jump may delay takeover, but neither permits two
tokens to write. Lease-related clock anomalies are diagnosed and favor safety
over availability.

`export_leases` separately coordinates the background audit exporter. It has
expiry and an exporter fencing token but does not authorize domain appends.
Consistent exports write a distinct target directory and do not share this
lease.

### 7.9 SQLite operating profile

- `modernc.org/sqlite` is the default driver so production builds remain pure Go
  and `CGO_ENABLED=0`.
- WAL mode, `synchronous=FULL`, foreign-key enforcement, a bounded busy timeout,
  and explicit checkpoint policy are configured and verified on open.
- A dedicated serialized writer connection owns `BEGIN IMMEDIATE` transactions;
  reads use a bounded pool and explicit read transactions when consistency
  across pages matters.
- All operation waits are bounded by caller context and configuration; a busy
  database never causes an unbounded hidden retry.
- The live database is supported only on a local filesystem. Startup rejects or
  prominently diagnoses known network or synchronization locations.

## 8. EventStore v2 contract

```go
type EventStore interface {
    ReadStream(context.Context, ReadStreamRequest) (StreamPage, error)
    Append(context.Context, AppendRequest) (CommitReceipt, error)
    ResolveAppend(context.Context, ResolveAppendRequest) (AppendResolution, error)
    FindCommandRequest(context.Context, FindCommandRequestRequest) (CommandRequestLookup, error)
}

type ReadStreamRequest struct {
    SessionID    domain.SessionID
    AfterSequence uint64
    Limit        uint32
    HeadVersion  *uint64
}

type StreamPage struct {
    Records           []domain.RecordedEvent
    HeadVersion       uint64
    NextAfterSequence uint64
    End               bool
}

type AppendRequest struct {
    AppendID       domain.AppendID
    SessionID      domain.SessionID
    ExpectedVersion uint64
    CommandID      domain.CommandID
    RuntimeID      RuntimeID
    FencingToken   uint64
    Admission      *CommandAdmission
    Events         []ProposedEvent
}

type CommandAdmission struct {
    RunTurnRequestID RunTurnRequestID
    RequestDigest    Digest
    TurnID           domain.TurnID
    ItemID           domain.ItemID
}

type ProposedEvent struct {
    ID            domain.EventID
    SchemaVersion uint32
    OccurredAt    time.Time
    Event         domain.Event
}

type CommitReceipt struct {
    AppendID       domain.AppendID
    CommitPosition uint64
    FirstSequence  uint64
    LastSequence   uint64
}

type ResolveAppendRequest struct {
    AppendID      domain.AppendID
    RequestDigest Digest
}

type AppendResolution struct {
    Kind    AppendResolutionKind
    Receipt *CommitReceipt
}

type FindCommandRequestRequest struct {
    RunTurnRequestID RunTurnRequestID
    SessionID        domain.SessionID
    RequestDigest    Digest
}

type CommandRequestRecord struct {
    RunTurnRequestID  RunTurnRequestID
    RequestDigest     Digest
    SessionID         domain.SessionID
    CommandID         domain.CommandID
    TurnID            domain.TurnID
    ItemID            domain.ItemID
    AdmissionAppendID domain.AppendID
}

type CommandRequestLookup struct {
    Kind   CommandRequestLookupKind
    Record *CommandRequestRecord
}
```

The exact names may change mechanically in the implementation spec, but the
ownership and semantics above are normative.

`AppendResolutionKind` is exactly `Committed`, `NotFound`, or
`IdentityMismatch`; `Receipt` is non-nil only for `Committed`.
`CommandRequestLookupKind` is exactly `Found`, `NotFound`, or
`IdentityMismatch`; `Record` is non-nil only for `Found`. Both operations may
return `StoreUnavailable` or `StoreCorrupt` as an error, but never encode those
conditions as absence. `FindCommandRequest` compares both Session and digest;
an existing request ID with either mismatch returns `IdentityMismatch` without
revealing another Session's record. A `Found` record proves committed admission
identity only. Application reads the canonical Session stream at a pinned head
to derive whether the request is running or terminal.

`command_requests.run_turn_request_id` is globally unique. Within one Session,
Turn and Item uniqueness is independently enforced by `domain_identities`; the
record fields and admission receipt are immutable after insertion.

### 8.1 Identity roles

- `CommandID` correlates one Application command. One `RunTurn` may use it for
  admission and terminal appends. It is not a storage idempotency key.
- `AppendID` identifies exactly one atomic EventStore request. Every append of a
  command uses a different `AppendID`; retries of that append reuse it.
- `EventID` identifies one immutable event and is stable before the first append.
- `commit_position` orders committed batches globally.
- `sequence` orders events inside a Session stream.

### 8.2 Request digest

The request digest is SHA-256 over a versioned, length-delimited canonical
envelope. Its format version and ordered event count are included explicitly;
concatenating unframed strings is forbidden. It covers, in order:

```text
request digest format version
sessionID
expectedVersion
commandID
hasAdmission
if admitted: runTurnRequestID
if admitted: runTurnRequestDigest
if admitted: turnID
if admitted: itemID
ordered event count
eventID
event type
event schema version
occurredAt
canonical payload
```

The Admission presence bit is always encoded. When present, all four
`CommandAdmission` fields are encoded even when Turn/Item IDs also appear in an
event payload; therefore changing any persistent side effect under the same
`AppendID` produces `AppendIdentityMismatch`. The `AppendID` is the key for the
receipt and is not included in its own request digest. Runtime ID and fencing
token authorize a new commit but are not part of the immutable request identity,
so they are also excluded. Canonical JSON rules, UTF-8 validation, timestamp
precision, and field ordering are versioned and shared by all adapters.

### 8.3 Transaction algorithm

```text
BEGIN IMMEDIATE
  look up append_id
    existing + same complete request digest -> return original receipt
    existing + different digest    -> AppendIdentityMismatch
  verify writer lease and fencing token for a new append
  when command admission is present, look up run_turn_request_id
    existing + same Session/digest -> CommandRequestConflict
    existing + different identity  -> CommandIdentityMismatch
  read event_streams.version
    mismatch                        -> VersionConflict
  validate IDs, payloads, limits, schema, and event uniqueness
  reserve creation-event Turn/Item IDs in domain_identities
  increment global commit position
  allocate contiguous stream sequences
  insert command_requests admission when present
  insert event_appends receipt
  insert complete events batch
  update event_streams
  update minimal synchronous projections
  insert exact export_outbox envelope
  update head_commit_position and head_audit_digest
COMMIT
```

The batch is wholly visible or wholly absent. JSONL does not participate in the
transaction and cannot change its success.

Receipt resolution deliberately precedes fencing validation. A fenced process
may learn that its exact request already committed, but it cannot create a new
commit. This also lets a successor resolve an unknown outcome with a new fencing
token without changing the immutable request identity.

### 8.4 Exact retry

- Same `AppendID` and same digest returns the original receipt, even when the
  stream has advanced since that commit.
- Same `AppendID` and different digest returns `AppendIdentityMismatch`.
- Event or sequence uniqueness is not used as a substitute for the receipt.
- The store never re-decides a command or retries a CAS conflict.

### 8.5 Error algebra

- `InvalidAppend`: request invalid; definitely not committed.
- `VersionConflict`: exact CAS rejected; definitely not committed.
- `AppendIdentityMismatch`: `AppendID` reused incorrectly; current request not
  committed.
- `CommandRequestConflict`: another Append admitted the same Request ID and
  identity; current Append did not commit, and Application must read the winner.
- `CommandIdentityMismatch`: a Request ID was reused with a different Session or
  digest; current request did not commit.
- `DomainIdentityConflict`: a creation event reused a historical Turn/Item ID in
  the Session; the batch did not commit and Application maps the identity kind
  to the existing Domain error.
- `WriterFenced`: Runtime Host no longer owns the database; not committed.
- `StoreUnavailable`: failure before any commit attempt; not committed.
- `CommitOutcomeUnknown`: COMMIT may have succeeded but the receipt could not be
  resolved because the database remained unavailable.
- `StoreCorrupt`: a storage invariant failed; mutation is fail-closed.

Pre-COMMIT failures may return a definite non-commit error. Once COMMIT is
attempted, an error is never converted to definite absence merely because a
second connection cannot yet see the receipt. The adapter finalizes or
quarantines the original connection according to verified driver behavior, then
performs one bounded receipt lookup on a fresh connection. A matching digest
returns success; absence or an unavailable lookup returns
`CommitOutcomeUnknown`. The caller may only resolve or retry the exact request
with the same `AppendID`.

SQLite result-code tests cover busy, full, I/O, interrupted, and close/rollback
behavior at COMMIT. No unbounded hidden retry is allowed. `ResolveAppend`
provides read-only lookup by `AppendID` plus request digest and returns
`Committed(receipt)`, `NotFound`, or `IdentityMismatch`; inability to perform
the lookup is `StoreUnavailable`, never `NotFound`.

### 8.6 Application unknown-outcome state machine

Every `RunTurn` receives an application-level, caller-stable
`RunTurnRequestID`. The request identity and digest are registered in
`command_requests` in the same transaction as admission. Our TypeScript TUI
sends it through the namespaced ACP `_meta.openCodeHarness.requestId` extension.
Other ACP clients remain conformant but receive no cross-connection exactly-once
promise unless they negotiate and supply that extension.

An exact duplicate request never starts another model call:

- a terminal request returns its reconstructed terminal result;
- a request owned by a live execution attaches to or observes that execution;
- a running request with no local execution waits for startup/live
  reconciliation and cannot create a new Turn;
- the same request ID with a different digest returns
  `CommandIdentityMismatch`.

Before allocating new execution identities, Application calls
`FindCommandRequest`. If absent, it creates IDs and includes `CommandAdmission`
only on the admission append. A uniqueness race returns
`CommandRequestConflict`, after which Application reads the winner; it does not
call the model until it owns the committed admission. The versioned request
digest covers Session ID, prompt content, attachments, selected mode, and every
option that can change execution semantics.

When an append returns `CommitOutcomeUnknown`, Application retains the complete
immutable append intent in the live execution registry, sets an operational
`append_outcome_unknown` admission gate for that Session (not a Domain state),
and blocks new admission for that Session. A bounded
resolver repeatedly performs `ResolveAppend` or exact `Append` using the same
identity; it never re-decides the command and never repeats the model. The
original ACP prompt stays pending while its connection and resolution budget
remain available. If resolution cannot finish, ACP returns a stable unknown
outcome error that clients must not treat as permission to resend with a new
request ID.

Resolution is stage-aware. An unknown admission is always resolved before the
model call. If it committed, the model proceeds only while the original live
request still owns execution and is not canceled; otherwise Application commits
a `request_abandoned` interruption without calling the model. An unknown
terminal append occurs after the model effect and is resolved to its original
terminal receipt or exact terminal append; the model is never called again.

Cancellation and disconnect use the following winner rule. The live registry
atomically advances from `running` to `terminal_append_in_flight` before the
terminal Append attempt, so cancellation observes one unambiguous phase:

| Observed phase | Cancellation action | Durable winner |
| --- | --- | --- |
| `running` | stop model delivery and append `client_disconnected` or the requested cancellation | the first terminal CAS; a loser reloads and returns the winner |
| `terminal_append_in_flight` | stop delivery and record only an operational cancel intent | resolve or exactly retry the original terminal Append; no interruption append is allowed |
| `terminal_outcome_unknown` | keep the Session admission gate and resolve the retained terminal intent | the original completed/failed terminal outcome always wins |
| `terminal_committed` | no Domain mutation | the committed terminal outcome |

Thus a disconnect after a model result cannot overwrite that result with
`client_disconnected`. If a running-phase interruption loses CAS to a terminal
Append, Application reloads the pinned stream and reports the committed
terminal outcome for that Request ID. The fault matrix exercises cancellation
before the terminal attempt, during COMMIT, after a lost acknowledgement, and
after receipt resolution.

After process death, no separate file is used to persist an uncertain intent:
the authoritative database either contains the terminal receipt/events or it
does not. Startup replay returns the committed terminal result when present;
otherwise it closes the still-running execution as `process_crash`. A reconnect
with the same `RunTurnRequestID` observes that durable outcome and never invokes
the model again.

## 9. Paginated replay, aggregate, and projections

The existing complete-stream `Load` and historical `Session.Turns` map are not
production-scale contracts.

### 9.1 Command aggregate

The write-side aggregate retains only facts required to validate new commands:

- Session identity, workspace, status, and version;
- active Turn and active Item state;
- bounded lifecycle metadata needed for invariants;
- no unbounded transcript text or completed Turn collection.

Historical Turn/Item uniqueness remains part of the Domain v2 command contract,
but is enforced by the transactionally maintained `domain_identities` index
rather than by scanning the compact aggregate.

Historical messages belong to the transcript projection. Queries do not use the
command aggregate as a read API.

### 9.2 Page reader

`AfterSequence` is exclusive. The first request omits `HeadVersion`; one SQLite
read transaction captures the stream head and returns it with records no later
than that head. Every subsequent request repeats that fixed `HeadVersion`, and
the store returns only `AfterSequence < sequence <= HeadVersion`. Concurrent
appends may advance the live stream but cannot alter the pinned view because
events are immutable. `NextAfterSequence` is the last returned sequence, or the
unchanged cursor on an empty page; `End` is true exactly when that cursor equals
the pinned head. A client-supplied head above the current stream head, or any
other impossible cursor/head combination, is `InvalidRead`; only an internally
recorded read view whose canonical events have disappeared is `StoreCorrupt`.
No read transaction or connection remains open across calls.

Changing the current full-history `Session` into this compact aggregate is an
explicit Domain v2 breaking migration, not a storage-only refactor. It first
defines `ApplyCompact` and proves command-decision equivalence against current
replay fixtures before Application, adapters, and test doubles migrate.

### 9.3 Snapshot

A snapshot contains:

- aggregate schema version;
- Session ID and covered event sequence;
- source event digest or chain head;
- encoded compact aggregate;
- creation implementation version.

Snapshot load verifies identity, schema, sequence, and digest. Invalid or unknown
snapshots are ignored and rebuilt from events. Snapshot creation never changes
the authoritative stream and can be disabled without changing behavior.

## 10. JSONL audit replica

### 10.1 Role

JSONL is:

- a complete, lossless audit replica;
- human-readable diagnostic material;
- a stable interchange format;
- a verified disaster-recovery source;
- a public surface for community analysis tools.

It is not an online commit point, a peer authority, or an input that may silently
overwrite a live database.

### 10.2 Batch envelope

One line represents one atomic append, not one isolated event:

```json
{
  "formatVersion": 1,
  "commitPosition": 42,
  "appendId": "append_...",
  "commandId": "command_...",
  "sessionId": "session_...",
  "expectedVersion": 8,
  "firstSequence": 9,
  "lastSequence": 10,
  "committedAt": "2026-08-13T10:00:00Z",
  "previousDigest": "sha256:...",
  "events": [],
  "batchDigest": "sha256:..."
}
```

`batchDigest` authenticates the canonical envelope fields excluding itself;
`previousDigest` creates an ordered hash chain. Manifest and segment checksums
detect deletion, insertion, reordering, truncation, and mutation.

### 10.3 Layout

```text
audit/
├── manifest.json                         # disposable latest-generation hint
├── manifests/
│   └── 000000002000-<head-digest>.json  # immutable generation
├── segments/
│   ├── 000000000001-000000001000-<digest>.jsonl
│   └── 000000001001-000000002000-<digest>.jsonl
└── staging/
    └── <exporter-id>.partial
```

- Sealed segments are immutable.
- A filename records first and last commit positions.
- Each immutable manifest generation records format, all segment ranges, byte
  sizes, SHA-256 digests, and global chain head. `manifest.json` is only a
  replaceable hint; startup can discover the highest valid generation.
- Staging files are not part of a valid replica and are disposable.
- File `Sync`, close, byte count, and digest verification precede publication.
- Cross-platform rename atomicity is not a domain correctness requirement.

### 10.4 Export algorithm

1. Acquire a bounded exporter lease through SQLite.
2. Read the next committed outbox rows in commit-position order.
3. Verify stored envelope digests.
4. Write a bounded staging segment.
5. sync, close, reopen, and verify the segment.
6. Publish the sealed segment.
7. write, sync, and verify a new immutable manifest generation, then best-effort
   update the disposable latest hint.
8. update the SQLite export checkpoint last.

Execution is at least once; publication is idempotent by commit range and
digest. The same range and digest is already complete. The same range with a
different digest quarantines the replica and triggers rebuild. Export failure
never rolls back or falsifies a domain append.

### 10.5 Exporter restart state machine

Exporter startup does not trust one mutable checkpoint:

1. discard incomplete staging files; they are derived data;
2. scan and verify immutable manifest generations and their referenced sealed
   segments against SQLite append/outbox digests;
3. choose the unique highest continuous valid generation no later than the
   SQLite head; two conflicting valid generations at the same head quarantine
   the replica;
4. for a sealed segment not yet named by that generation, adopt it only when its
   filename, bytes, range, chain predecessor, and SQLite digests are the exact
   next range; otherwise quarantine it;
5. regenerate any missing or invalid derived segment from canonical SQLite
   bytes and the frozen audit codec;
6. recompute and transactionally set `export_checkpoints` from the verified
   generation; a checkpoint ahead or behind the manifest is evidence to repair,
   not authority;
7. resume at the next commit position.

A crash after segment publication, after manifest generation publication, or
before checkpoint update therefore converges through the same inventory
algorithm. Directory synchronization is used where the platform exposes a
verified implementation; lack of an equivalent primitive may lose derived files
after power failure but cannot create domain facts, and restart regenerates them.
The conformance matrix exercises every publication boundary on all supported
platforms.

### 10.6 Consistent export and import

`export --consistent` fixes a target commit position in a SQLite read snapshot,
emits all batches through that position, and verifies a self-contained manifest.
Plain file copy is not a supported export procedure.

Import writes only a new or empty database and verifies, in order:

1. manifest and segment digests;
2. continuous commit positions and batch hash chain;
3. event payload digests;
4. continuous per-Session sequence;
5. expected-version to last-sequence transition;
6. known schema and deterministic upcasters;
7. complete domain replay invariants;
8. rebuilt heads and transcript projections.

Automatic merge into an active database is forbidden.

### 10.7 Divergence policy

| Condition | Required action |
| --- | --- |
| SQLite ahead of JSONL | exporter catches up |
| JSONL segment missing | regenerate from SQLite/outbox |
| JSONL digest mismatch | quarantine and rebuild replica; do not modify SQLite |
| JSONL contains a batch absent from SQLite | declare replica invalid; never auto-import |
| manifest damaged | rebuild from verified segments and SQLite |
| SQLite damaged, complete verified export exists | import into a new database and switch explicitly |
| both damaged | fail closed |

### 10.8 Backup and privacy

The primary backup is a consistent SQLite Online Backup API copy, optionally
paired with a verified JSONL export. JSONL alone is called a backup only when
its manifest is complete through the declared head.

Data directories default to owner-only permissions. Lossless audit export and
shareable redacted export are different commands. Redaction writes new files and
never modifies canonical audit segments. Raw prompts, model output, paths, tool
arguments, and secrets are excluded from telemetry by default.

## 11. Runtime Host and crash recovery

### 11.1 One host and fencing

One SQLite database admits one active Runtime Host. A host may manage several
Sessions, but v0 stdio exposes one client connection per process. A second
process that cannot acquire the database lease exits with a stable diagnostic.

Acquisition or takeover increments the fencing token in a transaction. The
current host heartbeats with bounded deadlines. Failure to confirm ownership
stops admission of new executions and cancels local work. A resumed stale process
cannot append because every transaction validates its old token.

### 11.2 Startup order

```text
open database
→ verify format and run migrations
→ acquire Runtime lease and fencing token
→ enumerate running candidates
→ ReadStream + replay each candidate
→ append recovery terminal facts
→ begin accepting commands
→ start background JSONL exporter
```

Commands are unavailable until reconciliation completes. Audit export lag does
not block Runtime readiness.

### 11.3 Recovery transition

Authoritative replay that ends with an active Session, running Turn, and running
Assistant Item produces one atomic batch:

```text
assistant.message.interrupted(code = "process_crash", message = "")
turn.interrupted(reason = "process_crash")
```

- Session remains active.
- The original `RunTurn` `CommandID` remains the correlation lineage.
- Recovery uses a deterministic `AppendID` derived with a fixed namespace from
  Session ID, Turn ID, Item ID, and `process_crash`.
- Exact retry after a lost recovery acknowledgement reuses that `AppendID`.
- Duplicate reconciliation returns the receipt or observes the existing terminal
  state; it cannot add a second terminal pair.
- No long-lived `recovering` domain state is introduced.

A valid legacy/general stream may contain a running Turn with no active Item
because the current Domain supports `StartTurn` independently. Recovery appends
only `turn.interrupted(reason = "process_crash")` for that shape. Its stable
`AppendID` uses the same namespace with an explicit `no_item` sentinel. A running
Turn with a missing, terminal, mismatched, or multiple active Item reference is
not repaired; replay or reconciliation returns `StoreCorrupt`. Domain v2 may
later remove the standalone transition only through an explicit migration.

### 11.4 No automatic model or tool replay

A running event cannot reveal whether the old process crashed before sending a
request, during a stream, after provider completion, during terminal commit, or
after commit with a lost acknowledgement. Automatic repetition can duplicate
cost, answers, file edits, shell commands, or remote effects.

Recovery only closes the uncertain execution. A new user intent with a new
`RunTurnRequestID` creates a new Turn, `CommandID`, `AppendID`, and Event IDs and
may record `retryOfTurnID` as lineage. A transport retry with the same Request ID
only observes the original durable outcome.
Future Tool Runtime design must add invocation identity, effect classification,
prepare/start/result boundaries, reconciliation adapters, and explicit
safe-retry policy before any tool is automatically retried.

## 12. Go Core and TypeScript TUI boundary

### 12.1 Repository layout

```text
cmd/open-code-harness/          Go composition-root binary
internal/harness/domain/        pure commands, events, invariants
internal/harness/application/   use cases and transaction boundaries
internal/harness/engine/        bounded agent execution
internal/harness/runtimehost/   lease, recovery, worker lifecycle
internal/harness/adapters/
  sqlite/                       canonical EventStore
  jsonlaudit/                   export/import
  acp/                          ACP agent adapter
  providers/                    provider adapters
  tools/                        built-in and MCP tool adapters
contracts/acp/                  pinned upstream stable schema
contracts/extensions/           namespaced Harness extensions
contracts/fixtures/             cross-language fixtures
clients/tui/                    TypeScript ACP client
evals/                          scenario and regression evidence
```

### 12.2 ACP role

ACP v1 is a public client projection. It owns initialization, version and
capability negotiation, session setup/load, prompt, update, cancel, permission,
filesystem, terminal, and protocol error mapping. It does not own the Agent loop,
domain state, storage transactions, or tool policy.

| ACP operation | Internal authority |
| --- | --- |
| `initialize` | protocol/capability negotiation |
| `session/new` | `Application.CreateSession` |
| `session/load` | authoritative replay plus client projection |
| `session/prompt` | `Application.RunTurn` |
| `session/update` | projection of runtime signals and durable facts |
| `session/cancel` | idempotent cancellation of current execution |
| permission request | Policy/Approval use case |
| fs/terminal request | Tool Runtime adapter after policy, never a bypass |

The internal Session ID may be used as ACP's opaque `sessionId`. Turn, Item,
Append, Event, and fencing identities remain internal unless a reviewed ACP
projection or namespaced extension exposes them.

### 12.3 Delivery ordering and disconnects

```text
commit domain terminal events
→ send terminal session/update
→ return session/prompt result
```

Notification failure cannot undo durable facts. A reconnect uses `session/load`
and the transcript projection; the server does not promise to reproduce every
ephemeral token delta.

- stdout contains protocol frames only; logs and diagnostics use stderr.
- one serialized writer owns stdout.
- output queues have both item and byte bounds.
- high-frequency text deltas may coalesce before enqueue, but lifecycle,
  permission, and terminal updates cannot be silently dropped.
- sustained blockage cancels a still-`running` execution and attempts a durable
  `client_disconnected` interruption; once terminal append begins, the §8.6
  terminal-winner rule applies instead.
- abrupt process death is reconciled as `process_crash` on the next startup.
- `session/cancel` is idempotent and is a no-op when no Turn is running.

### 12.4 Protocol source of truth

- The pinned official stable ACP v1 schema is the Go wire source of truth.
- Go wire types are generated or mechanically verified from that schema and
  isolated inside the ACP adapter.
- The TypeScript TUI uses the official `@agentclientprotocol/sdk`.
- Project extensions use ACP `_meta`, custom capabilities, and underscore-prefixed
  methods without changing standard method semantics.
- generated artifacts are committed but never hand-edited; CI verifies upstream
  artifact checksum and generation drift.
- ACP v2 Draft stays in an experimental package and cannot appear in stable v0
  capability declarations.

### 12.5 Release artifacts

- `open-code-harness`: pure-Go, CGO-free, single-file Core binary for Linux,
  macOS, and Windows.
- `@open-code-harness/tui`: independently versioned TypeScript ACP client.

An installer or platform bundle may install both. The Go single-binary guarantee
does not require embedding the TUI runtime.

## 13. Resource bounds

Initial defaults are explicit and configurable downward or upward within hard
validated ranges:

| Resource | Default limit |
| --- | ---: |
| single canonical encoded event payload | 8 MiB |
| events in one append | 64 |
| encoded append request | 16 MiB |
| EventStore read page | 256 records |
| ACP output queue | 256 items and 8 MiB |
| active JSONL segment | 64 MiB or 10,000 batches |
| SQLite write operation | caller deadline required |
| Runtime shutdown | bounded graceful deadline |

Exceeding a limit returns a stable error. No component truncates canonical data,
grows an unbounded queue, or relies on process OOM as control flow.

The existing 1 MiB assistant UTF-8 output limit remains an Application limit.
The larger encoded-event limit covers deterministic JSON escaping and wrapper
overhead. Codec tests prove that every valid maximum-size assistant output can
be terminalized; future payload shapes must provide the same bound proof or use
explicit chunk events before admission.

## 14. Observability

Three evidence planes remain distinct:

1. Domain audit: SQLite events and verified JSONL; recoverable facts.
2. Operational diagnostics: structured stderr logs with Runtime, Session, Turn,
   Item, Command, Append, and trace correlation where available.
3. Metrics/traces: replaceable OpenTelemetry adapters; content attributes off by
   default.

Minimum metrics cover append latency/conflicts/unknown outcomes, replay latency,
recovery candidates/results, export lag/failure, active Turns, cancellation
latency, ACP queue pressure/coalescing, provider latency/usage/failure, and tool
approval/denial/failure. User content and raw identifiers are not high-cardinality
metric labels.

## 15. Migration and compatibility

- SQLite migrations are forward-only, ordered, checksummed, and transactional.
- A migration-specific exclusive SQLite transaction and migration ledger prevent
  concurrent migrators. The transaction also refuses schema mutation while a
  Runtime lease remains valid. Migration completes before the new Runtime lease
  is acquired and before command admission.
- A consistent backup precedes a destructive migration.
- Committed event bytes are never rewritten in place.
- Canonical storage and audit always retain the original event bytes, schema,
  and digest. Deterministic pure upcasters run only in the decode/replay layer;
  they never rewrite imported or existing event rows. Permanent fixtures map
  raw historical bytes to current logical events and verify original digests.
- Unknown event type or schema version is fail-closed.
- Automated downgrade is unsupported; rollback uses a compatible backup or a
  verified export imported into a new database.
- Project SemVer, event schema version, audit format version, migration version,
  ACP version, and ACP schema artifact version remain separate numbers.

## 16. Security and privacy

- Data directories and audit files default to owner-only permissions.
- Workspace roots and additional directories are explicit and canonicalized
  before scope checks.
- Live database locations are rejected or warned when known to be unsupported
  network/synchronization filesystems.
- Credentials, prompt content, model output, tool arguments, shell commands, and
  file contents are absent from telemetry unless explicitly enabled.
- Executable configuration is accepted only from trusted configuration layers.
- Audit export and shareable redacted export are separate, explicit commands.
- Plugins, ACP clients, and MCP servers cannot bypass Policy or Runtime fencing.
- Recovery never repeats a high-risk side effect without a future explicit
  reconciliation contract.

## 17. Verification strategy

### 17.1 Domain properties

State transitions, terminal irreversibility, sequence continuity, atomic
Item/Turn termination, deterministic replay, and compact-aggregate equivalence
use table, property, and fuzz tests without a database or network.

### 17.2 EventStore conformance

Every adapter runs one shared suite covering CAS, atomic batch, exact AppendID
retry, identity mismatch, commit receipt, post-commit acknowledgement loss,
context cancellation, fencing, concurrency, pagination, defensive ownership,
and corruption detection.

### 17.3 Crash and fault matrix

Inject failures at begin, validation, receipt lookup, event insertion,
projection update, outbox insertion, COMMIT, acknowledgement, segment write,
sync, publish, manifest update, checkpoint update, lease heartbeat, takeover,
and recovery append. Subprocess kill/restart tests complement mocks.

### 17.4 Replay, migration, and round trip

All historical schema fixtures remain replayable. SQLite to JSONL to new SQLite
must preserve events, stream heads, append receipts, aggregate state, transcript
projection, and declared digests.

### 17.5 Protocol and cross-language

Use schema golden tests, malformed-message tests, in-memory duplex tests, actual
stdio subprocess black-box tests, and Go Agent/official TypeScript SDK tests.
Test slow clients, full queues, cancellation, EOF, reconnect/load, stdout
pollution, and terminal-delivery failure.

### 17.6 Platform and performance

CI builds with `CGO_ENABLED=0` and tests Linux, macOS, and Windows. It runs race
tests where supported, fuzz smoke, static analysis, migration fixtures, and
platform file-behavior tests. Benchmarks persist results for append, replay,
startup recovery, export, import, and ACP streaming. A release makes no
throughput or scale claim without stored evidence.

## 18. Documentation and open-source quality

Every major architecture, research gate, implementation plan, implemented
contract, and evaluation ledger has:

- an English normative document when it defines requirements;
- a complete synchronized Chinese reading copy, never a summary;
- matching section structure, decision identifiers, links, and status;
- an index entry declaring authority and implementation state.

English is the mechanical authority if translations diverge, so international
contributors have one conflict rule. Divergence is a documentation defect and
must be fixed before release.

Public release additionally requires contribution instructions, security policy,
code of conduct, ADR/RFC process, stability and deprecation policy, compatibility
matrix, reproducible builds, dependency/license inventory, SBOM, signatures,
checksums, and data upgrade/backup/recovery documentation.

## 19. Implementation decomposition

This design is not one implementation plan. It is delivered through six
sequential industrial slices, each with a focused primary-source architecture
gate, approved bilingual specification, bilingual plan, TDD implementation,
independent review, and completion evidence:

1. **EventStore v2 contract** — AppendID, proposed event metadata, receipt,
   error algebra, paginated reads, compact aggregate boundary, durable
   RunTurn-request admission, and the Application unknown-outcome state machine.
   This is an explicit breaking migration of every adapter, test double,
   append helper, ID/time ownership boundary, Domain state, codec fixture, and
   error mapping; it completes before SQLite implementation begins.
2. **SQLite canonical EventStore** — migrations, transaction/CAS, exact retry,
   fencing, projections, backup.
3. **JSONL audit and import** — outbox, envelope, segments, manifest, consistent
   export, verification, new-database import.
4. **Runtime Host and crash recovery** — lease, heartbeat, takeover, startup
   reconciliation, graceful shutdown.
5. **ACP v1 adapter** — stdio, capability negotiation, mapping, cancellation,
   backpressure, conformance.
6. **TypeScript TUI** — official SDK, view state, approval UX, transcript-driven
   tests, packaging.

Each later subsystem gate re-verifies the then-public Pi, Kimi Code, Grok
Build, Codex, Maka, and official DeepSeek Harness implementations that are
directly relevant to that slice, together with subsystem-specific
authoritative systems. DeepSeek-Reasonix remains community context only.
Evidence from this gate does not replace that re-verification and never
bypasses local contracts, tests, license review, or architecture review.
Completing Slice 1 does not obligate immediately implementing slices 2–6;
see the [DeepSeek Harness comparison and delivery sequencing](../../research/architecture-gates/2026-08-15-deepseek-harness-and-roadmap.md).

## 20. Completion criteria

A slice is marked implemented only when:

- design and plan are accepted in English and Chinese;
- primary-source architecture evidence is recorded;
- normal, failure, cancellation, concurrency, race, crash, recovery, replay,
  and resource-limit behavior relevant to the slice is tested;
- public schemas and fixtures are versioned;
- Linux/macOS/Windows CGO-free gates pass where the slice is portable;
- benchmarks and compatibility evidence are stored;
- an independent review has no unresolved correctness findings;
- implemented contracts and documentation index are updated;
- explicit exclusions and remaining GA blockers remain visible.

Running successfully is not completion. A capability without this evidence is
reported as experimental or not implemented.
