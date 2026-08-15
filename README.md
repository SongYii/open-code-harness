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

- Domain events and the Session/Turn state machine: implemented and verified.
- Industrial Engine vertical slice: implemented and verified through reusable
  scenario, replay, concurrency, race, and dependency-boundary gates.
- EventStore v2 contract: implemented and verified. The memory adapter is a
  conformance reference, not durable production storage.
- Provider, Tool/Policy, ACP, TUI, production persistence, and recovery
  milestones: not yet implemented.

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
The ten-task sequence is retained in the
[implemented plan](docs/superpowers/plans/2026-08-12-engine-vertical-slice.md),
with auditable results in the bilingual
[completion evidence ledger](docs/architecture/engine-vertical-slice-evidence.md).

```bash
gofmt -w .
go vet ./...
go test -race ./... -count=1
```
