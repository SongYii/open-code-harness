# Trace-only Observability and OpenTelemetry

- Status: Implemented contract
- Implemented: 2026-09-12
- Design: [trace-only observability design](../superpowers/specs/2026-09-12-observability-otel-design.md)
- Evidence: [completion evidence](observability-otel-evidence.md)
- Chinese reading copy: [OpenTelemetry 可观测性](observability-otel.zh-CN.md)

## What this solves

The event log says what durably happened, but it does not show where a live
Turn spent time or which model, policy, approval, tool, append, and compaction
operations belonged together. Optional traces add that timing and parent-child
view without becoming another source of truth.

## Operator-visible behavior

Tracing is off unless `-otel-traces-endpoint` is set. When enabled, `och`
exports OTLP/HTTP protobuf to an explicit URL ending in `/v1/traces`.
`-otel-traces-sample-ratio` controls head sampling; its enabled default is 1.
Plain HTTP is rejected except for a literal loopback IP with
`-otel-allow-insecure-loopback`.

Example for a local Collector:

```text
och ... \
  -otel-traces-endpoint http://127.0.0.1:4318/v1/traces \
  -otel-allow-insecure-loopback
```

The trace tree uses only these stable operation names:

```text
och.turn
├── och.context.prepare
│   └── och.context.compact
│       └── och.model.request
├── och.model.request
├── och.policy.decide
├── och.approval.wait
├── och.tool.execute
└── och.store.append

och.runtime.reconcile
└── och.store.append
```

Manual compaction starts a root `och.context.compact` because it has no Turn.
Duplicate calls distinguish owner, joined waiter, and durable replay so one
model execution is not counted three times.

## How it is implemented

`internal/harness/telemetry` is a small project-owned port. It accepts only a
closed span-name, outcome, and attribute-key vocabulary. Arbitrary attribute
names, prose, exceptions, stack traces, and raw errors cannot be passed to an
exporter through this interface. Application and Runtime depend only on this
port.

`internal/harness/adapters/otel` is the only package allowed to import the
OpenTelemetry implementation. It owns a local tracer provider rather than
changing OTel globals. Composition constructs it, passes the port downward,
and shuts it down after Runtime and MCP resources.

The adapter has one bounded worker: queue 256, batch 64, one-second flush,
three-second export deadline, no exporter retry, no proxy environment, at most
32 attributes of 256 bytes, and no span events or links. A full queue drops a
span instead of blocking a Turn. Export failures produce only a rate-limited,
content-free stderr message and never replace a Harness result.

`och.store.append` surrounds one logical publication. If the initial write has
an unknown outcome, the same span remains open across the bounded exact-result
resolution instead of pretending each low-level attempt is a new operation.

## Privacy and compatibility boundary

Traces contain correlation IDs, closed classifications, counts, token usage,
and outcomes. They never contain prompts, model text, summaries, tool
arguments/results, paths, argv, approval reasons, raw errors, API keys,
provider/Collector URLs, or MCP metadata. There is no `traceparent`, baggage,
or other propagation into Provider, ACP, MCP, or subprocess traffic.

Tracing cannot change the model prompt cache. The implementation test captures
the complete OCH-owned Provider method, URL, selected headers, and JSON body
with tracing off and on and requires byte equality. A raw-OTLP canary test also
proves prompt, model output, workspace, credential, and endpoint values do not
appear in the exported bytes.

## Problems found while implementing it

- A content-canary test first used a value shaped like a legal tool name. That
  tested metadata, not content. The canary was changed to real prose/secret
  shapes and was also driven through the actual Application and Provider path.
- The first full test run had two `localexec` platform-probe failures caused by
  concurrent host capability state. Both passed immediately in focused reruns;
  no telemetry package is on that call path.
- The first forced-drop benchmark left its fake HTTP handler waiting while the
  test server closed. A separate release signal now ends the fixture after the
  measured Turn loop; the production export deadline was unchanged.
- Invalid end metadata originally caused the safety wrapper to skip `End`,
  leaving an incomplete span. It now closes once with the bounded
  `dropped/invalid_telemetry_metadata` result.

## Current limits

This slice exports traces only. It has no native metrics, OTel logs, profiles,
dashboard, bundled Collector, remote propagation, dynamic configuration, or
telemetry query API. The canonical EventStore, audit replica, and evaluation
documents remain the authorities. The dependency and binary cost is material
and recorded in the evidence ledger rather than hidden.

