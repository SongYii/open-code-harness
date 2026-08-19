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

ACP, TUI, production persistence (SQLite), crash recovery, MCP, evaluation,
and OpenTelemetry are not yet implemented. The project remains pre-v0.

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
The ten-task Engine sequence is retained in the
[implemented plan](docs/superpowers/plans/2026-08-12-engine-vertical-slice.md),
with auditable results in the bilingual
[Engine completion evidence ledger](docs/architecture/engine-vertical-slice-evidence.md).

```bash
gofmt -w .
go vet ./...
go test -race ./... -count=1
```

Testing with `-race` requires cgo, so a C toolchain must be installed.

## Security

Open Code Harness executes model-proposed tool calls against a local
workspace, and the model is treated as untrusted input. [SECURITY.md](SECURITY.md)
states which boundaries the code enforces today and which it does not — most
importantly, `exec` is bounded `os/exec`, not a kernel sandbox. Report
vulnerabilities privately through GitHub Security Advisories, not public
issues.

## License

Licensed under the [Apache License 2.0](LICENSE).
