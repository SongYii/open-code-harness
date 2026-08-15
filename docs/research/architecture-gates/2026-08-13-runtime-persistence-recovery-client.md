# Runtime Persistence, Recovery, and Client Boundary Architecture Gate

**Status:** Complete research evidence
**Date:** 2026-08-13
**Scope:** Production EventStore, audit export, crash recovery, ACP boundary,
and client/runtime separation for Open Code Harness.

This document records primary-source evidence used by the accepted runtime
persistence and client-boundary design. It is evidence, not a compatibility
promise and not a claim that every referenced system provides the same
correctness contract.

## Decision questions

1. Should SQLite or JSONL be the physical commit authority?
2. How should an append remain exact when a commit acknowledgement is lost?
3. How should incomplete execution be reconciled after process death?
4. Should a TypeScript TUI share runtime state with the Go core, or be a
   protocol client?
5. Which practices from coding agents are reusable, and which depend on weaker
   durability or platform assumptions?

## Evidence comparison

| System | Primary evidence | Observed design | Adopt | Do not infer or copy |
| --- | --- | --- | --- | --- |
| OpenAI Codex | [thread-store README](https://github.com/openai/codex/blob/main/codex-rs/thread-store/README.md), [live writer](https://github.com/openai/codex/blob/main/codex-rs/thread-store/src/local/live_writer.rs), [writer lock](https://github.com/openai/codex/blob/main/codex-rs/thread-store/src/local/writer_lock.rs), [state migrations](https://github.com/openai/codex/blob/main/codex-rs/state/src/migrations.rs) | Canonical rollout JSONL is written and flushed before a rebuildable SQLite metadata view; per-thread cross-process locks, backfill, and migration checksums support that choice. | Keep human-readable lossless history, explicit writer ownership, checksummed migrations, and rebuildable projections. | JSONL authority is not automatically the best greenfield choice for exact CAS, lost-ACK retries, and three-OS behavior. Its lock, scan, drift, and repair machinery is part of the cost. |
| OpenCode | [session schema](https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/session/session.sql.ts), [CLI database and session commands](https://opencode.ai/docs/cli/) | SQLite stores normalized Session, Message, Part, Todo, Session Message, and Permission records and is the durable recovery source behind session listing, export, and database inspection. Runtime bus and SSE events notify consumers but are not themselves the durable history. | Explicit database tooling, normalized product projections, server-owned durable state, and separate transient delivery events. | Mutable message/part rows and notification events do not establish an immutable domain-event authority, exact append receipt, fencing, or lost-ack recovery contract. The fast-moving `dev` schema must be re-verified before implementation reuse. |
| Goose | [session manager and SQLite storage](https://github.com/aaif-goose/goose/blob/main/crates/goose/src/session/session_manager.rs) | `SessionManager` routes session, conversation, and usage-ledger reads and writes through SQLite. Schema initialization uses `BEGIN IMMEDIATE` to serialize concurrent first-run writers, and legacy sessions are imported into the database. | Serialized writer admission, bounded database waiting, WAL-oriented operation, transactional message/session updates, explicit migrations, and one-way legacy import. | `replace_conversation` and mutable transcript CRUD are product persistence semantics, not an append-only audit or domain-event contract. |
| Crush | [repository architecture](https://github.com/charmbracelet/crush/blob/main/AGENTS.md), [session service](https://github.com/charmbracelet/crush/blob/main/internal/session/session.go) | Go services perform Session CRUD against SQLite through sqlc and migrations; session reads come from the database and multi-table deletion uses a transaction. Explicit UI-only estimated usage remains in memory rather than competing as a durable fact. | Go/sqlc repository boundaries, migration discipline, transaction-scoped multi-table changes, and a clear distinction between durable facts and ephemeral UI state. | Mutable Session CRUD, internal pub/sub, and transactional deletion do not provide immutable event replay, expected-version append, or uncertain-effect reconciliation. |
| Hermes Agent | [session persistence documentation](https://github.com/NousResearch/hermes-agent/blob/main/website/docs/user-guide/sessions.md) | SQLite stores full session metadata and message history, and the documentation explicitly names it the canonical store for gateway messages. JSONL is an export format; legacy mirrors are compatibility artifacts rather than a second authority. | State the authority boundary in product documentation, keep exports subordinate to the database, and combine resume, routing continuity, and FTS-backed history around one canonical store. | Canonical transcript storage does not by itself prove immutable batch events, expected-version CAS, AppendID receipts, writer fencing, or no-replay recovery for uncertain external effects. |
| Kimi Code | [data locations](https://github.com/MoonshotAI/kimi-code/blob/main/docs/en/configuration/data-locations.md), [package map](https://github.com/MoonshotAI/kimi-code/blob/main/AGENTS.md) | `wire.jsonl` is a complete replay/resume record; application/server, engine, provider, execution environment, and transcript packages are separated. | Consumer-owned protocol projections, recorded-order replay, and explicit package boundaries. | Transcript idempotence does not establish atomic EventStore CAS or exact commit retry. |
| Maka | [architecture](https://github.com/maka-agent/maka-agent/blob/main/ARCHITECTURE.md), [runtime core draft](https://github.com/maka-agent/maka-agent/blob/main/docs/architecture/runtime-core-architecture-draft.md), [resume architecture](https://github.com/maka-agent/maka-agent/blob/main/docs/architecture/runtime-resume-architecture.md) | A semantic runtime event log is fact authority; UI, context, and recovery are projections. Recovery separates repair, continuation, and retry and rejects blind replay under uncertainty. | Immutable facts, short durable commits around external effects, terminal facts before projections/signals, and no automatic retry of uncertain effects. | The public design does not prove our SQLite CAS, AppendID, or cross-platform file contract. |
| DeepSeek-Reasonix | [v2 specification](https://github.com/esengine/DeepSeek-Reasonix/blob/main-v2/docs/SPEC.md) | Go, CGO-free distribution, append-only transcript, and complete JSONL session persistence. | Pure-Go portability, full saved sessions, and testable transcript behavior. | A transcript format alone is not a transactional multi-process EventStore. This is community project evidence, not a normative standard. |
| Pi | [monorepo](https://github.com/badlogic/pi-mono), [SDK](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/sdk.md), [session format](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/session-format.md), [RPC mode](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/rpc.md) | Separate model API, agent core, coding-agent session, TUI, and web UI packages. `AgentSession` owns lifecycle/history/compaction/events. Integrators choose an in-process SDK or line-framed subprocess RPC. Sessions are versioned JSONL trees with `id`/`parentId`, enabling branching in one file. | A small composable runtime API, UI-independent subscriptions, process-isolated protocol mode, strict line framing, versioned history, and first-class branching concepts. | Pi optimizes for hackability and local inspectability. Direct JSONL session ownership and load-time migration do not provide our required transactional CAS, AppendID receipt, fencing, or atomic cross-session coordination. Its custom RPC is useful evidence but ACP remains our public standard. |
| Grok Build | [repository/build support](https://github.com/xai-org/grok-build), [shell/ACP/session documentation](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-shell/README.md), [contribution policy](https://github.com/xai-org/grok-build/blob/main/CONTRIBUTING.md) | A Rust composition root separates TUI, agent shell/runtime, tools, workspace, sampling, ACP helpers, chat state, crash handling, and PTY tests. The product supports interactive, headless, and `grok agent stdio` ACP modes. `updates.jsonl` is authoritative conversation history; raw model chat, plans, rewind points, compaction checkpoints, signals, feedback, and subagents occupy separate durable forms. Tool time/output limits and trusted configuration layers are explicit. | Composition-root separation, ACP stdio as an editor boundary, headless parity, bounded tools, workspace isolation, crash detection, PTY black-box tests, and client/runtime separation even inside one language. | Multiple durable files create reconciliation obligations. The public README labels Windows builds from this tree best-effort, below our required CI contract. The public contribution policy does not accept external contributions, so it is not the community-governance model for this project. |
| KurrentDB / EventStoreDB | [appending events](https://docs.kurrent.io/clients/python/v1.3/appending-events), [projection tutorial](https://docs.kurrent.io/getting-started/use-cases/time-travel/tutorial-3) | Atomic expected-revision appends and stable event identities make a retry after a lost acknowledgement exact. Projection state and checkpoints are rebuildable and updated together. | Expected-version CAS, immutable event identity, exact retry receipts, atomic batch semantics, and rebuildable projections. | A specialized distributed event database is not required for a local-first v0. |
| Temporal | [History Service architecture](https://github.com/temporalio/temporal/blob/main/docs/architecture/history-service.md) | Immutable workflow history is the logical authority while mutable state and transfer/timer tasks are updated transactionally for serving and dispatch. | Separate semantic authority from physical format; use transactionally registered outbox work rather than dual writes. | Temporal's distributed shard ownership and queues are far beyond the local-first milestone. |
| SQLite | [atomic commit](https://sqlite.org/atomiccommit.html), [WAL](https://sqlite.org/wal.html), [backup API](https://sqlite.org/backup.html) | Local transactions provide atomic commit and WAL recovery; the Online Backup API produces consistent copies. WAL is database recovery machinery, not a domain event log, and has filesystem constraints. | SQLite transaction as the sole commit point, local-filesystem-only operation, WAL with deliberate durability settings, and Online Backup for primary backups. | A SQLite WAL file is not a public audit log and must not be exposed as one. Live databases on NFS/SMB/synchronization folders are unsupported. |
| modernc SQLite | [Go package documentation](https://pkg.go.dev/modernc.org/sqlite) | A database/sql SQLite driver implemented in C translated to Go supports a CGO-free build path across the target operating systems. | Use it as the default production driver and verify every target in CI. | Driver portability does not by itself prove our transaction, pragma, backup, or filesystem behavior; those remain adapter contracts. |
| Transactional outbox | [Debezium outbox documentation](https://debezium.io/documentation/reference/stable/transformations/outbox-event-router.html) | Business state and an outbound publication record are committed in one database transaction; publication is asynchronous and idempotent. | Register every JSONL export batch in the same SQLite transaction as its events. | Synchronous SQLite-plus-file dual write cannot provide one portable atomic commit. |

## SQLite authority assessment

The term "SQLite authority" covers two materially different contracts. OpenCode,
Goose, Crush, and Hermes demonstrate **session/transcript authority**: after a
restart, durable Session and Message facts are read from SQLite rather than from
an in-memory bus, UI state, search index, or export. This is strong evidence
that a local coding agent can make SQLite its product recovery source without
creating an unusual operational model.

Open Code Harness requires the stricter **runtime domain authority** contract.
The immutable event stream must decide every accepted lifecycle fact, while
mutable heads, transcript rows, snapshots, and JSONL remain derived. Among the
public implementations reviewed here, none documents the full combination of
atomic multi-event append, expected-version CAS, caller-stable `AppendID` plus
request digest, post-commit receipt resolution, runtime fencing, transactional
audit outbox, and no-replay recovery for uncertain effects. Absence of public
evidence is not proof that an unlisted internal mechanism does not exist; it
means those guarantees cannot be inherited from the comparison.

The comparison therefore strengthens, rather than changes, the selected
architecture. Goose supplies concrete SQLite operational mechanics; Crush is
the closest Go implementation reference; Hermes supplies the clearest
canonical-store product language; and OpenCode supplies a rich normalized
session schema and database operations surface. Their mutable transcript models
remain references for projections and tooling, not replacements for the
canonical immutable EventStore.

Codex is the useful counterexample: its source explicitly treats SQLite as a
rebuildable view that may lag but never lead canonical JSONL. Merely finding a
SQLite database in an agent is therefore insufficient evidence of SQLite
authority; recovery ordering and write precedence decide the classification.

## Pi assessment

Pi is valuable because its architecture stays small enough to inspect. The
model layer, generic agent core, coding-agent session, TUI, and web UI remain
separate packages. `AgentSession` exposes prompt, abort, compaction, navigation,
and events without requiring the built-in TUI. Its RPC mode is explicitly for
cross-language and process-isolated integration and specifies LF framing closely
enough to call out generic line-reader hazards.

Its session format makes a different trade-off from Open Code Harness. A
versioned append-only JSONL tree makes branching and manual inspection natural,
and automatic migration favors usability. The published contract does not
claim exact expected-version CAS, atomic multi-event batches, post-commit
receipt resolution, persistent writer epochs, or a verified replica manifest.
We therefore adopt Pi's composability and fixtures, not its persistence contract.

## Grok Build assessment

Grok Build is especially relevant to the Go-core/TypeScript-client decision.
Even though both its runtime and built-in TUI are Rust, the repository separates
the composition root, pager/TUI, shell/runtime, tools, workspace, sampler,
protocol helpers, chat state, and crash handling. The same shell supports TUI,
headless, and ACP stdio entry points. This validates one runtime with several
outer delivery modes rather than duplicating the agent loop in each UI.

Its public session layout also shows the cost of a rich local agent: ACP updates,
raw model history, plans, rewind data, compaction checkpoints, signals,
feedback, and subagents are persisted separately. `updates.jsonl` is named the
authoritative conversation log, while other files serve specialized consumers.
That is effective for inspection and product iteration, but it requires clear
rules for disagreement and recovery. Open Code Harness instead commits domain
facts, the AppendID receipt, minimal projections, and the export outbox in one
SQLite transaction; JSONL remains lossless and portable without becoming a
second online authority.

Grok Build also provides positive operational examples: tool timeouts and byte
limits, trusted configuration layers for executable credential helpers,
headless support, ACP compatibility, PTY testing, and telemetry that is disabled
in public-source builds unless configured. We adopt these principles while
requiring stronger Linux/macOS/Windows verification than its public build tree
currently states.

## Storage alternatives considered

### A. SQLite authoritative state tables plus incidental logs

Rejected because mutable state tables would obscure the semantic event source
and weaken deterministic replay.

### B. SQLite-backed canonical event log plus transactional JSONL replica

Selected. Immutable event rows are the semantic and physical online authority.
An outbox row is committed in the same transaction, and a background exporter
produces verifiable JSONL batch envelopes. This preserves inspectability without
creating two commit authorities.

### C. JSONL canonical log plus SQLite projection

Valid and proven by several agents, but rejected for this project. Under the
required pure-Go Linux/macOS/Windows contract it requires a cross-platform WAL:
writer fencing, partial-tail repair, corruption policy, exact receipts, segment
publication, manifests, compaction, projection checkpoints, and drift repair.
That cost is justified only if independently writable raw JSONL is itself a
product requirement.

## Client boundary alternatives considered

### A. Embed the Go core in the TypeScript TUI

Rejected. It either requires FFI/CGO, embeds a JavaScript runtime in Go, or
duplicates lifecycle and state ownership.

### B. Invent a project-specific JSONL RPC

Rejected as the public boundary. Pi demonstrates that this can be small and
effective, but it creates a new ecosystem contract and duplicate client work.

### C. ACP v1 over stdio

Selected. The TypeScript TUI uses the official ACP SDK and launches the pure-Go
agent binary. The Go adapter maps ACP to Application use cases. Domain events,
SQLite records, and internal runtime signals do not become public ACP types.

## Adopted decisions

1. SQLite immutable events are the sole live commit authority.
2. JSONL is a lossless, verifiable, rebuildable audit replica registered through
   a transactional outbox.
3. Caller-stable `AppendID` and request digest provide exact retry after a lost
   commit acknowledgement.
4. One active Runtime Host owns a database through a lease and monotonically
   increasing fencing token.
5. Startup recovery terminalizes stale running Item/Turn pairs with
   `process_crash`; it never automatically repeats a model or tool call.
6. The production reader is paginated. A compact command aggregate and a
   transcript projection replace unbounded full-history state loading.
7. The Go core is a CGO-free single binary. The TypeScript TUI is a separate ACP
   client and release artifact.
8. ACP v1 stdio is the only stable v0 client transport. Draft remote transports
   remain experimental and outside the compatibility promise.
9. Every implementation milestone repeats a focused primary-source architecture
   gate and re-verifies the then-public implementations directly relevant to
   that slice, including Pi, Grok Build, OpenCode, Goose, Crush, and Hermes when
   applicable.

## Evidence limitations

- Public code and documentation describe observable implementation choices, not
  unlisted production operations or guarantees.
- Absence of a documented invariant is recorded as unknown, not as proof that a
  project lacks the invariant.
- Grok Build is periodically synchronized from a larger private monorepo; only
  the public tree is evidence here.
- Pi's repository/package identity has changed over time; links above identify
  the source path used for this review.
- OpenCode's public `dev` branch changes quickly; the linked schema is dated
  evidence, not a promise that every released version has the same tables.
- No referenced project is treated as a code donor. License and provenance
  review remains mandatory before any implementation reuse.
