# Trace-only Observability and OpenTelemetry Implementation Plan

**Design:** [Trace-only Observability and OpenTelemetry](../specs/2026-09-12-observability-otel-design.md)

1. Add the dependency-free typed `telemetry` port, no-op tracer, validation,
   and deterministic memory recorder; extend fail-closed package ownership.
2. Add the OTel adapter with an exact-pinned trace SDK/OTLP-HTTP exporter,
   explicit resource/limits, disabled retry/proxy discovery, and a bounded
   non-blocking processor with observable drops.
3. Add fail-closed Composition/CLI configuration and lifecycle ownership;
   disabled assemblies construct no telemetry runtime.
4. Instrument the owning Application and Runtime boundaries for Turn,
   context, model, policy, approval, tool, append, and reconciliation spans,
   without changing Domain/protocol/provider types.
5. Prove trace topology and outcomes with the memory recorder, including
   owner/join/replay and compaction paths.
6. Decode real OTLP/HTTP protobuf in adapter tests and run content canaries
   through prompts, model/tool data, paths, argv, errors, credentials, and MCP
   metadata; prove bounded failure/drop/shutdown behavior.
7. Capture the provider wire with tracing off/on and require byte-identical
   OCH-owned method, URL, headers, and body.
8. Record an opt-in external Collector-compatible validation, exact dependency
   and binary deltas, production-line count, benchmarks, vulnerabilities, and
   architecture mutations in the implemented contract/evidence ledger.
9. Run formatting, targeted/race/full tests, vet, docsguard, vulncheck, cross
   builds, and diff checks; update PR #189 without adding metrics, logs,
   propagation, content capture, or a bundled backend.
