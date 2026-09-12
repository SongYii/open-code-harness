# How the Implemented System Works

- Status: Maintained plain-language guide
- Last reconciled: 2026-09-12
- Chinese reading copy: [项目实现通俗导读](how-it-works.zh-CN.md)
- Architecture map: [Current system architecture](current-system.md)

This is the short route into the repository. Each section answers the same
five questions. Detailed types and edge cases stay in the linked implemented
contract; commits and reproducible tests stay in the evidence ledger.

Evidence is dated. A statement such as “SQLite was not implemented” can be
true for an old slice and false for the current project. This guide describes
the current repository.

<!-- contract: docs/architecture/current-system.md -->
## Current system architecture

### Problem

Many individually correct modules can still become one tangled system if
nobody records who may call whom and who owns each fact.

### Visible result

A contributor has one map for the running system, package ownership, request
flow, recovery flow, and durable state.

### Implementation

The map is [the current-system contract](current-system.md). Its package matrix
is enforced by `internal/harness/architecture/dependencies_test.go`.

### Problems found and fixes

The audit found that unknown production packages received a permissive
fallback and that web-client isolation was not checked. Ownership now fails
closed and the whole client tree is scanned. See the [evidence](current-system-evidence.md).

### Still missing

Import rules cannot prove runtime correctness; subsystem tests still own that
job. The product remains pre-v0.

<!-- contract: docs/architecture/domain-events.md -->
## Domain events and state machine

### Problem

Crashes and retries become ambiguous if a Session or Turn is changed by loose
field updates with no legal transition rules.

### Visible result

Every accepted change has a named command and event. Replaying the event stream
reconstructs the same state and illegal transitions return stable errors.

### Implementation

`internal/harness/domain` owns commands, decisions, event application, codecs,
and replay. The exact catalog is in the [contract](domain-events.md).

### Problems found and fixes

The domain grew from simple Session/Turn state into model, tool, context, and
instruction facts while preserving replay. This first milestone predates the
evidence-ledger convention, so its development-problem narrative is weaker
than later modules; that gap is explicitly recorded rather than invented.

### Still missing

Domain correctness does not provide storage durability or external I/O; those
belong to EventStore and adapters.

<!-- contract: docs/architecture/engine-vertical-slice.md -->
## Engine vertical slice

### Problem

Model streams can end, fail, or be cancelled at awkward times. Without one
owner for cleanup and final state, a Turn can appear complete while work is
still running.

### Visible result

A model attempt has bounded output, deterministic cleanup, classified failure,
and one terminal result.

### Implementation

`internal/harness/engine` defines the model/stream ports; Application drives
them and records results. See the [contract](engine-vertical-slice.md) and
[evidence](engine-vertical-slice-evidence.md).

### Problems found and fixes

Implementation and later composition review exposed an ambiguous stream
context contract. Cleanup order and cancellation snapshots were pinned by
tests; the remaining ambiguity is kept visible instead of hidden.

### Still missing

The engine is an internal execution boundary, not a public plugin API, and it
does not choose concrete providers.

<!-- contract: docs/architecture/eventstore-v2.md -->
## EventStore v2

### Problem

After a timeout, a caller may not know whether a write committed. Blind retry
can duplicate facts; blind failure can lose accepted work.

### Visible result

Writes use expected versions and stable append identities. The caller can
resolve an unknown outcome and distinguish conflict from an exact retry.

### Implementation

Application owns the four-method Store port and append protocol; Domain owns
the canonical digest. See the [contract](eventstore-v2.md) and dated
[evidence](eventstore-v2-evidence.md).

### Problems found and fixes

The slice replaced a broader v1 surface with a compact protocol and added
explicit unknown-outcome resolution. Its old evidence says SQLite had not
started; that is a historical snapshot, not current status.

### Still missing

The contract alone is not durable. Production durability is supplied by the
SQLite adapter.

<!-- contract: docs/architecture/provider-adapter.md -->
## Provider adapter

### Problem

Provider HTTP formats and errors would otherwise leak vendor-specific branches
through the agent loop.

### Visible result

OpenAI-compatible streaming responses become one model-neutral stream, with
bounded parsing, usage facts, and stable error classes.

### Implementation

`internal/harness/adapters/openaicompat` implements `engine.Model`; capability
profiles describe supported behavior. See the [contract](provider-adapter.md)
and [evidence](provider-adapter-evidence.md).

### Problems found and fixes

The dated implementation ledger records no design deviation and mostly lists
tests, not the development story. Later work added native tool messages and
secret redaction without moving vendor logic into Application.

### Still missing

Only one OpenAI-compatible provider family exists. There is no provider
routing, vendor SDK layer, or live-key CI.

<!-- contract: docs/architecture/tool-runtime.md -->
## Tool runtime

### Problem

A model request to read, write, or execute code is untrusted input. Directly
running it would bypass scope checks, permission, limits, and audit.

### Visible result

Tool calls are validated, risk-scored, optionally approved, executed through a
bounded adapter, and recorded before the loop continues.

### Implementation

Application owns the step loop; `policy` decides risk; `tools` defines ports;
workspace and process adapters perform I/O. See the [contract](tool-runtime.md)
and [evidence](tool-runtime-evidence.md).

### Problems found and fixes

The first slice openly shipped only partial OS enforcement. Later slices added
sandboxing, quotas, guarded file mutation, and MCP while keeping the same
Policy/approval path.

### Still missing

Shell execution is not protected by the file observation guard, and platform
enforcement remains capability-dependent.

<!-- contract: docs/architecture/observed-file-mutation.md -->
## Observed-state safe file mutation

### Problem

An agent can overwrite a file it never read, or overwrite somebody else's
change made after it read the file.

### Visible result

Destructive file tools require a hidden version from the last observed read.
Unseen or changed files are refused with a useful retry instruction.

### Implementation

Application keeps per-session observations; `workspacefs` compares them and
publishes writes through a staged rename/link path. See the
[contract](observed-file-mutation.md) and [evidence](observed-file-mutation-evidence.md).

### Problems found and fixes

Three initial tests proved the wrong thing, a replacement could expand 10 KiB
into 327 MiB, and one create-conflict case was missing. The tests were changed
to use independent instances and dangerous “helpful refresh” mutations; output
is now bounded.

### Still missing

The check-to-publication window is not a filesystem transaction, external
writers do not share the protocol, and `exec` can modify files outside it.

<!-- contract: docs/architecture/sqlite-eventstore.md -->
## SQLite EventStore

### Problem

The in-memory Store cannot survive a process restart or coordinate two writers.

### Visible result

Session facts and checkpoints survive restart, writes are transactional, and a
fencing lease prevents an old process from continuing after takeover.

### Implementation

`internal/harness/adapters/sqlite` implements the Store, checkpoint, audit
chain, backup, and reader paths. See the [contract](sqlite-eventstore.md) and
[evidence](sqlite-eventstore-evidence.md).

### Problems found and fixes

An intermittent test was first blamed on CPU starvation. Reproduction showed
that opening SQLite without the Runtime Host gives a hard 30-second lease with
no heartbeat. Tests were made deterministic and ownership was clarified.

### Still missing

Power-loss and long wall-clock soak evidence remain limited; a standalone
Store user must understand that Runtime owns lease renewal.

<!-- contract: docs/architecture/jsonl-audit-replica.md -->
## JSONL audit replica

### Problem

Operators need portable, inspectable audit data without creating a second
writer or weakening the canonical database.

### Visible result

The system exports chained JSON Lines generations, detects tampering or torn
output, resumes after crashes, and verifies imports before accepting them.

### Implementation

SQLite maintains the audit chain in the append transaction; the exporter uses
stage, seal, manifest, and checkpoint publication. See the
[contract](jsonl-audit-replica.md) and [evidence](jsonl-audit-replica-evidence.md).

### Problems found and fixes

The implementation largely matched its design; the recorded deviation is a
short digest prefix in filenames while manifests retain full digests. Crash
leftovers and lost replicas received explicit convergence tests.

### Still missing

There is no long-running adversarial import/export soak or device-level
power-loss proof.

<!-- contract: docs/architecture/runtime-host.md -->
## Runtime Host and crash recovery

### Problem

A durable Store is not enough: somebody must own startup repair, lease renewal,
background export, fencing reaction, and shutdown order.

### Visible result

The process repairs incomplete work before accepting commands, stops admission
after losing authority, and releases only its own lease on shutdown.

### Implementation

`internal/harness/runtime` owns reconciliation and lifecycle; Composition
launches it before serving clients. See the [contract](runtime-host.md) and
[evidence](runtime-host-evidence.md).

### Problems found and fixes

The original ledger reports no design deviation. Later SQLite failures made an
important boundary visible: heartbeat belongs to Runtime, not the database
adapter, so tests that open SQLite alone cannot assume renewal.

### Still missing

Kill-9 recovery, clock jumps, and long real-time lease soak need broader
evidence.

<!-- contract: docs/architecture/composition-root.md -->
## Composition root

### Problem

If every package constructs adapters, tests can pass with wiring that the real
binary never uses and sibling layers become coupled.

### Visible result

One production assembly chooses adapters, validates configuration, starts
Runtime, exposes ACP, and shuts resources down in order.

### Implementation

`internal/harness/composition` is the general concrete-adapter owner. See the
[contract](composition-root.md) and [evidence](composition-root-evidence.md).

### Problems found and fixes

The slice found missing production Clock/ID implementations, an “unowned means
unrestricted” dependency hole, and an adapter deny-list that future adapters
could bypass. These became production implementations and exhaustive tests.

### Still missing

Composition does not turn internal ports into a stable public extension API.

<!-- contract: docs/architecture/acp-v1.md -->
## Agent Client Protocol v1 adapter

### Problem

Clients need a stable wire protocol without becoming coupled to internal
Domain events or owning the agent loop.

### Visible result

Independent clients can create/load sessions, prompt, cancel, handle permission
requests, and receive live or replayed updates over JSON-RPC on stdio.

### Implementation

`internal/harness/adapters/acp` validates Agent Client Protocol (ACP) v1 and
projects Application events. See the [contract](acp-v1.md), base
[evidence](acp-v1-evidence.md), and lifecycle
[evidence](acp-session-lifecycle-evidence.md).

### Problems found and fixes

The base evidence ledger recorded little problem narrative. The later session
lifecycle work corrected the literal state chart and added a duplex wire-state
machine plus mutation proof.

### Still missing

There is no ACP v2 or authentication, and stdio remains tied to the parent
process lifetime.

<!-- contract: docs/architecture/session-transcript.md -->
## Session transcript

### Problem

Raw internal events are durable but inconvenient and unsafe as a client-facing
conversation export.

### Visible result

`och export-session` emits a bounded, versioned JSON Lines transcript with a
snapshot, known fact catalog, and completion trailer.

### Implementation

`internal/harness/transcript` reads EventStore facts and projects a separate
format. See the [contract](session-transcript.md) and combined
[evidence](conversation-and-transcript-evidence.md).

### Problems found and fixes

The early evidence emphasizes mapping tests and hashes rather than a narrative.
Later lifecycle work added the deleted-session fact without turning transcript
JSONL into a writable EventStore format.

### Still missing

Transcript import, richer runtime facts, subagent origin, and some redaction
coverage remain outside the format.

<!-- contract: docs/architecture/acp-native-client.md -->
## ACP-native terminal client

### Problem

Testing the ACP server only with its own fixtures cannot prove that an
independent program can use it.

### Visible result

`acp-client` launches an agent, manages sessions, renders a trajectory, and
handles permission prompts through ACP alone.

### Implementation

`internal/client/acp` is deliberately isolated from `internal/harness`; the
binary lives at `cmd/acp-client`. See the [contract](acp-native-client.md) and
[evidence](acp-native-client-evidence.md).

### Problems found and fixes

Mapping-table and real-process tests caught differences between wire updates
and the client's trajectory reducer. The final client proves interoperability,
not merely shared Go types.

### Still missing

It is a minimal terminal client, not the fuller TypeScript TUI milestone.

<!-- contract: docs/architecture/secret-redaction.md -->
## Secret redaction

### Problem

Tool output or model text can contain API keys that would otherwise become
durable events and exports.

### Visible result

Recognized secrets are replaced before tool completion/failure and final
assistant text are persisted.

### Implementation

`internal/harness/redact` is a small pure package; Application calls it at the
two persistence boundaries. See the [contract](secret-redaction.md) and
[evidence](secret-redaction-evidence.md).

### Problems found and fixes

Mutation tests removed patterns and bypassed each call site to prove both the
detector and its placement matter. Provider logging reused the same package
instead of keeping a second pattern set.

### Still missing

Streaming deltas and tool-call arguments are not comprehensively redacted, and
pattern matching cannot detect every unknown secret form.

<!-- contract: docs/architecture/web-trajectory-ui.md -->
## Web trajectory UI

### Problem

A browser cannot safely speak raw agent stdio, but adding a second application
protocol would duplicate ACP and its security rules.

### Visible result

A local web page shows turn-grouped activity and permission prompts while all
agent semantics still travel as ACP frames.

### Implementation

`cmd/acp-web-bridge` relays whole frames with Origin and invocation-token
checks; TypeScript under `web/src` owns the independent client and UI. See the
[contract](web-trajectory-ui.md) and [evidence](web-trajectory-ui-evidence.md).

### Problems found and fixes

Browser end-to-end tests and mutations proved the bridge checks both origin
and the per-run token and does not secretly import Harness internals.

### Still missing

Only one active local viewer is supported; there is no non-loopback deployment,
multi-viewer fan-out, or full session manager.

<!-- contract: docs/architecture/context-engine.md -->
## Context Engine

### Problem

Long conversations exceed a model's input limit, and silently trimming text
would make behavior impossible to explain or recover.

### Visible result

The system budgets history, cuts only at safe Turn boundaries, prunes large
tool results, summarizes in bounded chunks, and resumes from verified durable
checkpoints.

### Implementation

`internal/harness/contextengine` is pure planning/projection code; Application
owns four triggers; SQLite and memory store checkpoints. See the
[contract](context-engine.md) and [evidence](context-engine-evidence.md).

### Problems found and fixes

Implementation found a reset digest that never covered source content, a
linear rescan, an unwired usage anchor, an unplumbed pruning limit, and missing
multi-chunk summarization. Each received a focused test and follow-up fix.

### Still missing

Quality across real models and repositories needs broader live evidence; the
mechanism is implemented but not GA-calibrated.

<!-- contract: docs/architecture/observability-otel.md -->
## Trace-only observability

### Problem

The event log proves what committed, but it does not make a slow live Turn's
model, policy, approval, tool, compaction, and storage time easy to see.

### Visible result

An operator can opt into a bounded OTLP trace tree and see where one Turn
spent time. With no endpoint flag, nothing is exported.

### Implementation

A project-owned metadata-only port keeps OTel out of business packages. One
adapter queues at most 256 spans and exports OTLP/HTTP without retries or
propagation. See the [contract](observability-otel.md) and
[evidence](observability-otel-evidence.md).

### Problems found and fixes

The first canary was accidentally legal metadata, invalid end data could leave
a span unfinished, and a forced-drop benchmark fixture could hang on close.
The tests were strengthened and the end path now always closes safely.

### Still missing

There are no native metrics/logs, dashboard, bundled Collector, or remote
propagation. External Collector evidence and several trace-topology variants
remain open; the binary-size increase is material and published.

<!-- contract: docs/architecture/system-prompt-workspace-instructions.md -->
## System prompt and workspace instructions

### Problem

Repository instructions can change during a session. Re-reading and appending
them blindly wastes prompt cache and can let stale instructions survive restart
or compaction.

### Visible result

The model receives one versioned system prompt and bounded hierarchical
`AGENTS.md` instructions; changes become set/replace/remove facts and survive
summary, reset, and restart.

### Implementation

`internal/harness/agentinstructions` discovers/renders instructions;
Application observes changes; Domain and checkpoints preserve identity. See
the [contract](system-prompt-workspace-instructions.md) and
[evidence](system-prompt-workspace-instructions-evidence.md).

### Problems found and fixes

The design adopted append-only deltas so unchanged turns keep a stable request
prefix. Live DeepSeek calls ran, but deterministic prerequisites prevented a
judge call; the evidence correctly labels that result partial.

### Still missing

Real-model quality is not yet proven, and repository instructions never grant
tool authority.

<!-- contract: docs/architecture/mcp-client.md -->
## Model Context Protocol client adapter

### Problem

External tools should join the agent without each server receiving a custom
integration or bypassing Policy and audit.

### Visible result

Configured stdio Model Context Protocol (MCP) servers contribute bounded,
qualified tools that use the normal approval, execution, redaction, and event
path.

### Implementation

`internal/harness/adapters/mcp` wraps the pinned official SDK behind a confined
command port supplied by Composition. See the [contract](mcp-client.md) and
[evidence](mcp-client-evidence.md).

### Problems found and fixes

Implementation overturned five design clauses: SDK negotiation and dependency
assumptions, untrusted-name handling, confinement ownership, and shutdown
behavior. Two first mutations proved nothing and were redesigned before the
claims were accepted.

### Still missing

There is no Streamable HTTP/OAuth transport, server restart, or measured
large-catalog capacity.

<!-- contract: docs/architecture/evaluation.md -->
## Evaluation system

### Problem

An end-to-end score alone cannot tell whether a failure came from the model,
context, tools, protocol, evidence collection, or the judge itself.

### Visible result

Frozen scenarios run through in-process and ACP paths, publish committed
evidence, support offline rescoring, and keep live model/judge calls explicitly
consent-gated.

### Implementation

`internal/harness/eval` owns Scenario/Subject/Executor identities, evidence
selection, deterministic verifiers, judge scoring, and variance documents.
See the [contract](evaluation.md) and [evidence](evaluation-evidence.md).

### Problems found and fixes

The full Context matrix accidentally ran four times per PR, two judge fixtures
proved nothing, an uncited verdict was trusted, and an absence verifier could
pass after checking no evidence. Wiring guards and stronger fixtures/verifiers
now catch those errors.

### Still missing

There is still no successful live judge sample, calibrated variance policy, or
second provider family. OpenTelemetry is separate from evaluation and cannot
be used as score evidence.
