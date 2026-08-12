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
- Industrial Engine vertical slice: design accepted; implementation plan ready.
- Provider, Tool/Policy, ACP, TUI, persistence, and recovery milestones: not yet implemented.

## Development

The implemented internal Session and Turn contract is documented in
[Domain Events and State Machine](docs/architecture/domain-events.md).

```bash
gofmt -w .
go vet ./...
go test -race ./... -count=1
```
