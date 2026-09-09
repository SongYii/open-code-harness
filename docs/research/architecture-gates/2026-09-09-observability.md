# Observability Architecture Gate

**Status:** Complete research evidence

**Date:** 2026-09-09

**Scope:** Milestone 10's remaining half. `docs/README.md` records
"OpenTelemetry remains undesigned and outside the eval contract", and the
[2026-09-01 combined gate](2026-09-01-context-engine-evaluation-observability-tui.md)
said each of its four areas "still needs its own subsystem-specific
architecture gate re-verifying then-current primary sources before a
normative design". Context Engine and Evaluation got theirs; this is
Observability's. TUI's is still outstanding.

This gate reads how the six comparison-set projects actually instrument
themselves, measures what that costs them, and records the questions a design
must answer. **It designs and implements nothing.**

English is normative. The Chinese file is a synchronized reading copy.

## Pinned sources

Read directly from the gitignored `.reference/` checkouts at these commits.

| Project | Commit | Checkout date |
| --- | --- | --- |
| codex | `67cc3c318d` | 2026-09-01 |
| grok-build | `bb7f39d` | 2026-08-31 |
| deepseek-harness | `dd6322d6` | 2026-08-31 |
| kimi-code | `ab565e081` | 2026-09-01 |
| pi | `853a80d26` | 2026-08-28 |
| maka-agent | `afbcabdc7` | 2026-09-01 |

## The short version

| Project | Approach | OTel | Size |
| --- | --- | --- | --- |
| **codex** | Dedicated `otel` crate: traces *and* metrics, four exporter choices, W3C trace context injected into its own HTTP client | yes | ~3,731 lines |
| **grok-build** | `xai-grok-telemetry` crate: an OTel layer plus fastrace, span profiles, Sentry, process metrics, and its own unified log | yes | **20,329 lines** |
| **deepseek-harness** | A telemetry **port** with OTel as one loadable **adapter** behind it | yes, behind a seam | 528 (port) + 332 (adapter) |
| **kimi-code** | Own telemetry package — bootstrap, client, transport, sink, region-aware endpoint | no | 1,196 lines |
| **pi** | Own telemetry package, with `noop.ts` and `memory.ts` implementations | no | 935 lines |
| **maka-agent** | Runtime telemetry narrowed to cost: pricing, LLM-call usage, tool-invocation records | no | 458 lines |

**Three of six wire OpenTelemetry, not two.** The 2026-09-01 gate wrote that
"only two of six reference projects (Codex, Grok Build) wire real distributed
tracing". DeepSeek Harness ships
`@deepseek-ai/dsh-session-telemetry-otel`, a published package whose stated
job is to hand "captured session records to the OTel JS SDK's log pipeline".
That correction matters for sequencing, because the third example is also the
one whose shape fits this project best.

## codex — traces and metrics, and tool output goes with them

The `otel` crate carries both signals. `OtelExporter` offers `None`,
`Statsig` (metrics only, internal), `OtlpGrpc`, and `OtlpHttp`, and the
built-in Statsig default is deliberately off in debug builds. Trace context is
propagated as W3C headers injected by codex's own HTTP client
(`http-client/src/client.rs`), so a request is traceable across the process
boundary into the backend.

The metric surface is agent-shaped rather than generic: `record_turn_ttft`,
`record_turn_cost`, `record_startup_phase`, plus counters, histograms, and
timers.

**The finding a design here has to answer.** `emit_tool_result` sends the tool
name, the **call arguments**, the MCP server origin, duration, success, and a
**preview of the tool's output** into telemetry. `ToolResultLogConfig`
defaults `max_bytes` to 2 KiB, so the default is not "no output" but "the
first 2 KiB of it".

For this repository that is the whole question in one line. This project
already scans tool results, tool failure messages, and final assistant text
for secret shapes and redacts them before persistence, audit replication, or
ACP projection
([secret redaction](../../architecture/secret-redaction.md)), and `SECURITY.md`
splits what is enforced from what is not. A telemetry exporter that ships tool
output to a network endpoint is a new egress path that every one of those
decisions would have to be re-argued against — not because codex is careless,
but because codex's threat model and this project's are not the same document.

## grok-build — the scale warning

`xai-grok-telemetry` is **20,329 lines**. It is not only OTel: it carries
fastrace, an OTLP HTTP path, span profiles, Sentry, process metrics, session
metrics, startup and subagent-spawn events, memory telemetry, sampling logs,
a unified log, and `redact_common.rs`.

The OTel layer is filtered by an environment variable
(`otel_layer/mod.rs:81`) with a compiled-in default filter, and a failed OTLP
client build degrades to a warning with span export disabled rather than
failing startup.

The number is the finding. This is the area where a project that starts with
"add tracing" ends up owning a subsystem larger than several of this
repository's own packages combined. Any design here should state its own
ceiling before writing code, the way the MCP slice sized itself against the
reference layers before starting.

## deepseek-harness — the shape that fits this project

`SessionTelemetryBackend` is an abstract port (528 lines including its
coordinator) and `OpenTelemetrySessionBackend` is one loadable adapter behind
it (332 lines). Three properties are worth taking:

**A disabled mode that is a real mode.** `SessionTelemetryMode.DISABLED`
constructs no SDK state at all and installs a listener that warns when
recorded feedback stays local. Disabled is not "an exporter that drops"; it is
an absence with a stated consequence.

**Sharing is a disclosed, mandatory property.** Every backend must implement
`readonly sharing: SessionTelemetrySharingStatus`, described as the
"deployment-selected session-sharing policy, disclosed for acknowledgement
surfaces that report whether recorded feedback leaves the process". A consumer
renders "not configured" only when no telemetry service is mounted at all. The
seam owns that vocabulary so the disclosure cannot vary by backend.

That is the same instinct as this repository's own
`CostStatusUnavailable`-is-not-zero rule: the honest state has a name, and no
implementation may quietly stand in for it.

**Configuration fails at load, including around a real SDK defect.** The
adapter rejects a missing or non-`http(s)` exporter URL, and rejects a
non-positive `maxExportBatchSize` because — in their own comment — the SDK
accepts it and then "splices empty batches without consuming the queue", so
`dispose` would hang forever with records queued.

## kimi-code, pi, maka-agent — three ways of not adopting OTel

**kimi-code** (1,196 lines) has bootstrap, client, transport, sink, crash and
system-metrics modules, and a region-aware endpoint resolved by its
composition root rather than by the telemetry package.

**pi** (935 lines) ships `noop.ts` and `memory.ts` alongside its real
implementation, so the no-op and the in-memory recorder are first-class rather
than test-only.

**maka-agent** (458 lines) is the narrowest and the most interesting for this
project: its runtime telemetry is `pricing.ts`, `cost.ts`,
`llm-call-usage.ts`, `record-llm-call.ts`, and `record-tool-invocation.ts`.
It answers "what did this cost and what ran" without any tracing vocabulary.

This project already has maka's answer, in a stricter form. `ScorerUsage`
carries an explicit `CostStatus`, a `PriceTable` is frozen and digest-bound,
and an unavailable price is never published as a computed zero
([evaluation contract](../../architecture/evaluation.md)).

## What this project has today

No `internal/harness/telemetry` package, and no OpenTelemetry dependency —
`grep -rn "OpenTelemetry\|otel" --include=*.go internal/ cmd/` returns
nothing.

What exists instead is the canonical event store and its JSONL audit replica
with an exporter, hash-chained and verifiable, plus per-Attempt evidence
manifests in the eval system. The charter's "Observable" attribute has a
working implementation today, and the 2026-09-01 gate said so.

The honest framing for any design is therefore **not** "add observability" but
"add a second, lossy, network-egress view of facts that are already recorded
losslessly and locally" — and to say what that second view buys.

## Open questions a design must resolve

1. **Does tool content leave the process at all?** codex's default sends
   arguments and 2 KiB of output. This repository redacts secrets before
   persistence and jails the workspace. Is telemetry metadata-only —
   names, durations, counts, verdicts, token counts — or does it carry
   content under redaction? "Under redaction" means the redactor becomes a
   security boundary for a network egress path it was not designed for.

2. **Port with adapters, or a direct dependency?** DeepSeek Harness's seam is
   the only reference shape compatible with this project's architecture guard,
   which forbids adapter-to-adapter imports and confines third-party clients to
   `adapters/`. A `telemetry` port with `noop`, `memory` (pi's precedent), and
   `otel` adapters would fit; a direct OTel dependency in `application` would
   not.

3. **Traces, metrics, or logs — which signal, and why that one?** codex takes
   traces and metrics; DeepSeek Harness deliberately targets the OTel **log**
   pipeline for session records. This project's facts are already events, which
   is closer to the log shape than to spans.

4. **What is the size ceiling?** grok-build's 20,329 lines is the warning.
   The MCP slice sized itself against reference layers before starting; this
   should too, and should state the number in the design rather than discover
   it.

5. **What happens when the exporter is unreachable?** grok-build degrades to a
   warning with export disabled. This repository's charter is fail-closed
   almost everywhere, but a telemetry backend that fails a Turn because a
   collector is down would make observability a liability. This is a place
   where fail-open is probably right and therefore has to be argued
   explicitly rather than inherited.

6. **Does a disabled build carry the dependency?** The MCP re-verification
   found that importing the SDK pulled six transitive modules including
   `golang.org/x/oauth2` even though that slice was stdio-only, and
   `SECURITY.md` had to be corrected. The OTel Go SDK's footprint must be
   measured with `go list -deps` before a design commits, not estimated.

7. **Is there a consumer?** The variance mechanism shipped dormant because
   nothing reached it, and that is recorded as the anomaly it was. Naming who
   reads these traces — and whether that reader exists today — belongs in the
   design's first paragraph, not its risk table.
