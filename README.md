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
  table behind ports; not a plugin kernel. `exec` is now confined by bwrap
  and a cgroup v2 memory and CPU quota on Linux, or Seatbelt, RLIMIT_AS,
  and RLIMIT_CPU on macOS, when available, with a fail-closed startup
  gate and a named, logged escape hatch otherwise; see the
  [SECURITY.md](SECURITY.md) threat model and the
  [exec sandboxing evidence ledger](docs/architecture/exec-sandboxing-resource-quotas-evidence.md).
  Tool call results, failure messages, and the final assistant message
  text are scanned for a small, hardcoded set of secret shapes and
  redacted before persistence, audit replication, or ACP projection; see
  the [secret redaction contract](docs/architecture/secret-redaction.md)
  and its
  [evidence ledger](docs/architecture/secret-redaction-evidence.md).
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

- Composition root (`internal/harness/composition`, `cmd/och`): implemented
  and verified; not GA. The single place adapters are named, enforced by the
  architecture guard, with an end-to-end test that assembles every slice above
  and runs one tool-calling turn against a real database and a fixture-driven
  provider — no network and no credential.

- ACP v1 adapter (`adapters/acp`): implemented and verified; not GA. JSON-RPC
  2.0 over NDJSON on the composition root (`ServeACP`, `cmd/och -acp`).
  Live and load `session/update` include tool cards with clip bounds and
  workspace admission. Capability-gated `session/list`, `session/resume`,
  `session/close`, and `session/delete` add a non-enumerating, idempotent
  logical delete (`session.deleted`) and a duplex wire-state machine that
  never appends the durable `session.closed` fact on ACP close. ACP v2 and
  authenticate are not included.

- ACP-native client (`internal/client/acp`, `cmd/acp-client`): implemented
  and verified; not GA. A standalone client that spawns an agent over
  stdio, renders a live trajectory from `session/update`, and answers
  `session/request_permission` interactively — the first real proof
  anywhere in this repository that the ACP v1 adapter above interoperates
  with an independent client process. Not milestone 7's fuller
  TypeScript TUI client, which remains unspecified.

- Session transcript (`internal/harness/transcript`, `och export-session`):
  implemented and verified; not GA. Experimental `och.session.transcript`
  JSONL projection of one EventStore session, read through `sqlite.OpenReader`
  without taking the runtime lease.

TUI, MCP, the Context Engine, evaluation, and OpenTelemetry are not yet
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
The assembly contract is documented in
[Implemented Composition Root](docs/architecture/composition-root.md)
and its [Chinese reading copy](docs/architecture/composition-root.zh-CN.md),
with auditable results in the
[Composition root evidence ledger](docs/architecture/composition-root-evidence.md).
The ACP v1 adapter contract is documented in
[Implemented ACP v1 Adapter](docs/architecture/acp-v1.md)
and its [Chinese reading copy](docs/architecture/acp-v1.zh-CN.md),
with auditable results in the
[ACP v1 adapter evidence ledger](docs/architecture/acp-v1-evidence.md). Its
session-lifecycle capabilities (`session/list`/`resume`/`close`/`delete`,
the logical `session.deleted` fact, and the SQLite session head catalog) are
documented with auditable results in the
[ACP session lifecycle (Slice B) evidence ledger](docs/architecture/acp-session-lifecycle-evidence.md).
The ACP-native client contract is documented in
[Implemented ACP-native Client](docs/architecture/acp-native-client.md)
and its [Chinese reading copy](docs/architecture/acp-native-client.zh-CN.md),
with auditable results, including the real interoperability proof's own
output, in the
[ACP-native client evidence ledger](docs/architecture/acp-native-client-evidence.md).
The session transcript contract is documented in
[Implemented Session Transcript](docs/architecture/session-transcript.md)
and its [Chinese reading copy](docs/architecture/session-transcript.zh-CN.md),
with auditable results in the
[conversation and session transcript evidence ledger](docs/architecture/conversation-and-transcript-evidence.md).
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
go run ./cmd/och -help
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
