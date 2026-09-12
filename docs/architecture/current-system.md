# Current System Architecture

- Status: Implemented contract
- Last reconciled with code: 2026-09-12
- Stability: internal topology; public protocol stability is stated by each
  protocol contract
- Design: [Current system architecture and boundary closure](../superpowers/specs/2026-09-09-current-system-architecture-design.md)
- Foundational direction: [architecture charter](../superpowers/specs/2026-08-11-open-code-harness-architecture-design.md)

This document is the map of the system that exists now. The charter states
where the product intends to go; subsystem contracts define detailed local
behavior; this contract owns the cross-subsystem topology, package ownership,
authority boundaries, and dependency direction.

## 1. System and process boundary

```text
 independent ACP clients                         agent process
┌─────────────────────────┐       stdio       ┌──────────────────────────┐
│ acp-client / web client │ <── ACP v1 ─────> │ ACP adapter              │
│ internal/client/*       │                   │          │               │
└─────────────────────────┘                   │          v               │
                                              │ Application orchestration│
 direct eval ───────────────────────────────> │   │ model  │ tool        │
                                              │   v        v             │
                                              │ Engine   Policy/Tools    │
                                              │ Provider  builtins/MCP   │
                                              │          │               │
                                              │          v               │
                                              │ Domain commands/events   │
                                              │          │               │
                                              │ SQLite EventStore        │
                                              └──────────────────────────┘
                                                        │
                                              derived JSONL/transcripts
                                                        │
                                          optional metadata-only OTLP traces
```

ACP is the only public client protocol. The browser bridge transports ACP
frames and does not become a second Harness protocol. MCP is an external-tool
adapter behind the same internal tool, Policy, approval, redaction, and audit
path as builtins. Provider-specific HTTP is behind `engine.Model`.

The executable composition root is the only general owner allowed to name
concrete adapters. `runtime` has one deliberate historical exception: it may
name the SQLite adapter because the Runtime Host owns the canonical store's
lease and lifecycle.

## 2. Package ownership and allowed internal dependencies

Every production directory below `internal/harness` must match one row. A new
unmatched directory fails `TestProductionDependencyBoundaries` before its
imports are considered. “May depend on” is an allowlist for direct internal
imports; standard-library and external-library restrictions are enforced
separately by the same test.

| Owner/package root | Responsibility | May depend directly on |
| --- | --- | --- |
| `domain` | Commands, events, state transitions, replay invariants | none |
| `redact` | Pure secret-pattern redaction | none |
| `engine` | Model and streaming ports | `domain` |
| `policy` | Pure risk decision table | `domain` |
| `tools` | Tool specifications and execution ports | `domain` |
| `agentinstructions` | Fixed system prompt and bounded instruction rendering | `domain` |
| `contextengine` | Context measurement, projection, planning, summaries, checkpoints | `domain`, `redact` |
| `telemetry` | Closed metadata-only trace port and vocabulary | none |
| `application` | Turn/step orchestration and transaction boundaries | `agentinstructions`, `contextengine`, `domain`, `engine`, `policy`, `redact`, `telemetry`, `tools` |
| `adapters/acp` | ACP server-side validation and event projection | `application`, `domain`, `engine`, `tools` |
| `adapters/openaicompat` | Provider HTTP/SSE mapping | `domain`, `engine`, `redact` |
| `adapters/workspacefs` | Workspace-confined file operations | `domain`, `tools` |
| `adapters/localexec` | Confined subprocess execution | `domain`, `tools` |
| `adapters/mcp` | External MCP discovery/call projection | `domain`, `tools` |
| `adapters/otel` | Bounded OTLP/HTTP trace export; sole OTel SDK owner | `telemetry` |
| `adapters/memory` | Deterministic in-memory ports | `application`, `contextengine`, `domain`, `telemetry` |
| `adapters/sqlite` | Canonical durable event/checkpoint store | `application`, `contextengine`, `domain` |
| `adapters/system` | Wall clock and ID generation | `application`, `domain` |
| `runtime` | Lease, recovery, heartbeat, exporter lifecycle | `adapters/sqlite`, `application`, `domain`, `telemetry` |
| `transcript` | Read-only session projection/export | `application`, `domain` |
| `composition` | Production construction and shutdown | adapters plus `application`, `contextengine`, `domain`, `engine`, `policy`, `runtime`, `tools`, `transcript` |
| `eval` | Scenario execution and evidence/scoring | `application`, `composition`, `domain`, `engine`, `policy`, `redact`, `tools`, `transcript` |

Sibling adapters cannot import one another. MCP therefore declares a confined
command port that Composition fills with `localexec`; it does not import
`localexec`. Eval consumes Composition but cannot construct a concrete adapter
itself. Domain, Engine, Application, pure policy/tool/context packages, and
test-independent projections cannot perform host/network I/O unless their row
has a specific adapter exception.

`internal/harness/testkit` and the named `modeltest`, `porttest`,
`eventstoretest`, and `enginescenariotest` directories are test support, not
production owners.

## 3. Independent clients and entrypoints

Everything below `internal/client` is on the client side of the ACP process
boundary and must not import `internal/harness`; Harness production packages
must not import it in reverse. This applies to both the Go ACP client and the
browser bridge/client and automatically covers future child packages.

| Entrypoint | Role |
| --- | --- |
| `cmd/och` | Production agent process assembled through Composition |
| `cmd/och-eval` | Evaluation runner; enters production paths through Eval/Composition |
| `cmd/acp-client` | Independent terminal ACP client; no Harness implementation import |
| `cmd/acp-web-bridge` | Independent ACP stdio/WebSocket relay and web asset host; no Harness implementation import |

Detailed protocol behavior belongs to the [ACP adapter](acp-v1.md),
[ACP-native client](acp-native-client.md), and [web trajectory UI](web-trajectory-ui.md)
contracts.

## 4. Turn control flow

For a normal ACP prompt:

1. the ACP adapter validates protocol and workspace admission, then calls
   Application;
2. Application obtains the Session authority, reconciles workspace instruction
   state, and asks Context Engine to materialize the request;
3. Application invokes the model through the `engine.Model` port;
4. model output is assembled into text or tool intent;
5. every tool intent is schema-checked and decided by Policy; risky work goes
   through the approval port;
6. a permitted builtin or MCP tool runs through its adapter; durable tool
   output is redacted before the completion command is appended;
7. Domain decides commands into events and the EventStore atomically appends
   them with optimistic concurrency; and
8. ACP projects accepted events to the client. UI delivery is not the commit
   boundary.

Application owns the loop and append order. Adapters own external I/O, never
business decisions. Domain owns legal state transitions, never orchestration.
See [Engine](engine-vertical-slice.md), [Tool runtime](tool-runtime.md),
[MCP](mcp-client.md), [Context Engine](context-engine.md), and
[system/workspace instructions](system-prompt-workspace-instructions.md).

## 5. Data and lifecycle authority

| Concern | Authority | Derived or non-authoritative forms |
| --- | --- | --- |
| Session/Turn facts | Canonical EventStore append log | Domain replay state, ACP updates, transcripts |
| Production persistence | SQLite EventStore | Memory adapter is a conformance/test implementation |
| Context compaction | Validated checkpoint anchored to event coverage/digest | Materialized request and summaries are projections |
| Audit replica | SQLite audit-chain state until verified publication | Exported JSONL is a replica, never a second writer |
| Workspace bytes | Host filesystem | Observed read fingerprint guards permission to mutate; it is not file content authority |
| Tool permission | Policy decision plus approval result | Model tool intent is only a request |
| Adapter construction | Composition | Application ports contain no concrete adapter selection |
| Store lease/recovery | Runtime Host | Client connection lifetime does not own recovery |
| Evaluation result | Frozen Scenario/Subject/Executor plus committed evidence | A judge score cannot override failed deterministic prerequisites |
| Operational timing | no new authority; traces are sampled diagnostics | OTLP spans cannot prove commit, replay, or evaluation success |

Event append is the publication boundary for Harness facts. Checkpoints and
exports may accelerate or expose replay but cannot rewrite accepted history.
Workspace mutation has a separate external-state boundary described by
[observed-state safe file mutation](observed-file-mutation.md).

## 6. Restart and recovery flow

Composition opens dependencies; Runtime acquires the SQLite lease, reconciles
incomplete durable work, starts bounded lease renewal/export ownership, and
only then admits commands. A lost lease fences new writes. Recovery emits
deterministic events instead of editing old rows. Context checkpoints are
accepted only after coverage and digest verification; otherwise replay falls
back to canonical events. See [Runtime Host](runtime-host.md),
[EventStore v2](eventstore-v2.md), [SQLite](sqlite-eventstore.md), and
[JSONL audit replica](jsonl-audit-replica.md).

## 7. Boundary change procedure

A change that adds a production package or direct internal dependency must:

1. select an existing owner or define a new owner in the registry;
2. update the executable allowlist and focused regression first;
3. update this ownership table and any affected subsystem contract;
4. explain any upward or sibling dependency as a deliberate exception; and
5. pass architecture, documentation, race, and relevant contract tests.

An unclassified package is never temporarily accepted. If the proper owner is
unclear, the architecture decision is unfinished.

## 8. Current exclusions

This map does not make the pre-v0 system GA. It does not add the fuller
TypeScript TUI, Windows runtime enforcement, native OTel metrics/logs, remote daemon/A2A,
Streamable HTTP MCP/OAuth, or another provider family. Their absence is a
product-scope question, not permission to cross the boundaries above.
