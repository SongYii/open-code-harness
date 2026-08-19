# Open Code Harness

An open, model-neutral, protocol-aligned, industrial-grade harness for building,
evaluating, and operating code agents.

Open Code Harness is being built for real production use, not as a tutorial or
throwaway demo. Delivery is intentionally incremental: a milestone may expose a
small capability, but every completed capability must have explicit contracts,
deterministic verification, bounded resources, stable failure semantics, and a
credible path to production adapters.

The project is currently pre-v0 and architecture-first; it is not yet a general
availability release. Start with the [documentation authority map](docs/README.md)
and the [foundational architecture charter](docs/superpowers/specs/2026-08-11-open-code-harness-architecture-design.md).

## Current status

The numbered milestone list lives in
[docs/README.md](docs/README.md#milestone-status). Completed slices so far:

- Domain events and the Session/Turn state machine: implemented and verified.
- Industrial Engine vertical slice: implemented and verified through reusable
  scenario, replay, concurrency, race, and dependency-boundary gates.
- EventStore v2 contract: implemented and verified. The memory adapter is a
  conformance reference, not durable production storage.
- Provider adapter (`adapters/openaicompat`): implemented and verified; not GA.
  Thin OpenAI-compatible Chat Completions SSE client behind `engine.Model`,
  not a vendor SDK or plugin kernel.
- Tool Runtime, Policy, and four builtin workspace tools: implemented and
  verified; not GA. Application-owned Step loop and a pure Policy Decide
  table behind ports; not a plugin kernel.
- SQLite canonical EventStore (`adapters/sqlite`): implemented and verified;
  not GA. Pure-Go durable adapter behind the EventStore v2 port, passing the
  adapter-neutral conformance suite unchanged: verified open profile, WAL,
  full-shape migrations, append transaction with exact retry, pinned reads,
  fencing lease primitive, and verified backup.
- JSONL audit replica: implemented and verified; not GA. Audit codec v1 with
  chain maintenance inside the append transaction, codec-v1 backfill, a
  crash-convergent exporter, consistent export, and eight-step verified
  import.
- Runtime Host and crash recovery (`internal/harness/runtime`): implemented
  and verified; not GA. Single host performing startup reconciliation with
  deterministic recovery appends, a bounded heartbeat with fencing reaction,
  matching-pair lease release, and exporter ownership.

ACP, TUI, MCP, the Context Engine, evaluation, and OpenTelemetry are not yet
implemented. The project remains pre-v0.

## Development

The implemented internal Session and Turn contract is documented in
[Domain Events and State Machine](docs/architecture/domain-events.md). The
executable Application/Engine contract is documented in
[Implemented Engine Vertical Slice](docs/architecture/engine-vertical-slice.md)
and its [Chinese reading copy](docs/architecture/engine-vertical-slice.zh-CN.md).
The current Store contract is documented in
[Implemented EventStore v2 Contract](docs/architecture/eventstore-v2.md)
and its [Chinese reading copy](docs/architecture/eventstore-v2.zh-CN.md),
with auditable results in the
[EventStore v2 evidence ledger](docs/architecture/eventstore-v2-evidence.md).
The Provider contract is documented in
[Implemented Provider Adapter](docs/architecture/provider-adapter.md),
with auditable results in the
[Provider adapter evidence ledger](docs/architecture/provider-adapter-evidence.md).
The Tool Runtime contract is documented in
[Implemented Tool Runtime](docs/architecture/tool-runtime.md)
and its [Chinese reading copy](docs/architecture/tool-runtime.zh-CN.md),
with auditable results in the
[Tool Runtime evidence ledger](docs/architecture/tool-runtime-evidence.md).
The durable Store adapter is documented in
[Implemented SQLite Canonical EventStore](docs/architecture/sqlite-eventstore.md)
and its [Chinese reading copy](docs/architecture/sqlite-eventstore.zh-CN.md),
with auditable results in the
[SQLite EventStore evidence ledger](docs/architecture/sqlite-eventstore-evidence.md).
The audit replica contract is documented in
[Implemented JSONL Audit Replica](docs/architecture/jsonl-audit-replica.md)
and its [Chinese reading copy](docs/architecture/jsonl-audit-replica.zh-CN.md),
with auditable results in the
[JSONL audit replica evidence ledger](docs/architecture/jsonl-audit-replica-evidence.md).
The host lifecycle contract is documented in
[Implemented Runtime Host and Crash Recovery](docs/architecture/runtime-host.md)
and its [Chinese reading copy](docs/architecture/runtime-host.zh-CN.md),
with auditable results in the
[Runtime Host evidence ledger](docs/architecture/runtime-host-evidence.md).
The ten-task Engine sequence is retained in the
[implemented plan](docs/superpowers/plans/2026-08-12-engine-vertical-slice.md),
with auditable results in the bilingual
[Engine completion evidence ledger](docs/architecture/engine-vertical-slice-evidence.md).

```bash
gofmt -w .
go vet ./...
go test -race ./... -count=1
```
