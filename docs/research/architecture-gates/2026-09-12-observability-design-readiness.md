# Observability and OpenTelemetry Design-Readiness Review

**Status:** Complete research evidence; recommendations are not an accepted
normative design

**Date:** 2026-09-12

**Scope:** Turn the seven open questions in the
[2026-09-09 observability architecture gate](2026-09-09-observability.md)
into evidence-backed recommendations for Open Code Harness. This review adds
the current OpenTelemetry specification, Go implementation, GenAI/MCP semantic
conventions, Collector operations, a measured dependency experiment, and an
explicit mapping to this repository. It does not add a dependency or implement
telemetry.

English is the research record. The
[Chinese reading copy](2026-09-12-observability-design-readiness.zh-CN.md) is
provided for accessibility.

## Executive conclusion

Open Code Harness already has durable observability: canonical domain events,
a verifiable JSONL audit replica, session transcripts, and evaluation evidence.
OpenTelemetry should not duplicate those records or become another source of
truth. Its useful role is narrower: a bounded, lossy, operational view of
**duration and causality while work is in flight**.

The recommended first slice is therefore:

1. **Traces only.** Go traces are stable; Go logs are still release candidate,
   and OTel's GenAI and MCP conventions are still Development.^1 ^2 ^3
2. **Metadata only.** No prompt, assistant text, reasoning, tool arguments,
   tool results, file paths, command argv, error messages, or baggage leave the
   process. OTel itself says GenAI content is sensitive and large and should
   not be captured by default.^4
3. **A project-owned port with `noop`, `memory`, and OTLP/HTTP adapters.** This
   follows DeepSeek Harness's closest-fit seam and Pi's first-class test/no-op
   implementations, while keeping OTel types out of domain, application,
   engine, tools, context, and runtime packages.
4. **Explicit opt-in and an operator-owned consumer.** Disabled constructs no
   SDK state. Enabled means an operator supplied an OTLP Collector/backend and
   acknowledged that metadata leaves the process. The repository does not
   bundle a backend.
5. **Bounded and fail-open during operation.** Invalid enabled configuration
   fails at startup; an unavailable exporter never fails a Turn. Export uses a
   bounded batch queue and bounded shutdown. Dropped telemetry is reported as
   a local diagnostic, not hidden and not retried through domain logic.
6. **No wire propagation in the first slice.** Do not inject trace context into
   external model HTTP, ACP, or MCP messages. OTel warns that outbound context
   to external services can expose internal information, and MCP propagation is
   still a Development convention.^5 ^6
7. **No direct metrics or OTel logs initially.** A Collector can derive RED
   metrics from spans through the span-metrics connector. Add native metrics
   only when a concrete dashboard/SLO cannot be served that way.^7

This slice would answer questions the audit log cannot answer cheaply: which
step owns wall-clock latency, what the critical path was, whether an operation
was still in flight when a process stopped, and how a Turn crossed runtime,
provider, tool, MCP, and persistence boundaries. It must not be used for
recovery, evaluation verdicts, billing truth, or authorization.

## Research basis

### Existing six-project comparison

The 2026-09-09 gate inspected pinned checkouts of Codex, Grok Build, DeepSeek
Harness, Kimi Code, Pi, and Maka Agent. Its primary findings remain valid:

| Project | Shape | OTel | Approximate production size |
| --- | --- | --- | ---: |
| Codex | Dedicated subsystem, traces and metrics, multiple exporters | Yes | 3,731 lines |
| Grok Build | OTel plus fastrace, Sentry, profiles, process/session metrics and unified logs | Yes | 20,329 lines |
| DeepSeek Harness | Telemetry port plus loadable OTel adapter | Yes | 528 + 332 lines |
| Kimi Code | Own telemetry client/transport/sink | No | 1,196 lines |
| Pi | Own package with no-op and memory implementations | No | 935 lines |
| Maka Agent | Cost and invocation accounting only | No | 458 lines |

Three lessons survive the comparison:

- OTel adoption is not universal even in mature agent harnesses.
- DeepSeek Harness has the best boundary shape for this repository.
- Grok Build demonstrates how quickly “add tracing” can become a large product
  of its own. The first slice needs a size ceiling and explicit exclusions.

Codex also provides the strongest privacy warning for this repository: its
default tool-result telemetry includes call arguments and a 2 KiB output
preview. That is a legitimate product choice under its threat model, but it is
not compatible with OCH's current default. OCH's exporter must be content-free
by construction, not merely content-enabled with a redaction callback.

### Current standard maturity

OpenTelemetry has three primary signals: traces, metrics, and logs.^8 As of this
review, the Go implementation marks traces and metrics Stable and logs Release
Candidate.^2 The separate GenAI semantic-conventions repository marks model
spans, agent spans, metrics, and MCP conventions Development.^3

That distinction matters. The trace API/SDK is stable enough to depend on, but
the agent-shaped vocabulary is not stable enough to become OCH's internal
contract. A first implementation should use a small versioned OCH mapping and
keep semantic-convention translation inside the OTel adapter. It may emit the
currently useful `gen_ai.*` fields experimentally, but domain events and port
types must never import those names.

OTel's event guidance also fits OCH's existing split: events represent
point-in-time occurrences and state changes, while spans represent operations
with meaningful duration.^9 OCH already owns durable state-change events. The
new subsystem should create spans around operations, not export one OTel log or
span per canonical event.

### Measured Go dependency footprint

The earlier gate required measurement before design. On 2026-09-12, with Go
1.26.6 and OpenTelemetry Go v1.46.0, two clean temporary modules were measured:

| Imported surface | Non-standard-library packages | Modules |
| --- | ---: | ---: |
| `go.opentelemetry.io/otel/trace` API | 9 | 3 |
| Trace SDK + OTLP/HTTP trace exporter | 172 | 21 |

Against OCH's current 47-module graph, the SDK/exporter experiment introduced
19 module paths not already present. The HTTP exporter still pulls the OTLP
protobuf/gateway stack and gRPC-related modules; choosing HTTP does not mean a
tiny dependency graph. The experiment changed no repository file and lived
under `/tmp`.

This does not prove runtime or binary-size cost. A normative design must still
measure:

- `och` binary-size delta for disabled and enabled builds;
- allocations and time for a no-op Turn and a representative tool Turn;
- queue saturation behavior;
- shutdown at an unreachable endpoint;
- `go list -deps`, license, vulnerability, and cross-build results at the exact
  pinned version selected for implementation.

The recommended production-code ceiling is **1,200 non-test lines** for the
first slice, excluding documentation and generated dependency code. Exceeding
it should reopen scope rather than silently growing toward the Codex or Grok
Build surfaces.

## What OCH already observes, and what is missing

| Question | Existing canonical/audit/eval data | OTel trace value |
| --- | --- | --- |
| What happened? | Strong, lossless, replayable | Secondary and sampled |
| Was state committed correctly? | Strong: EventStore + audit chain | Must never decide this |
| Did an eval pass? | Strong: bound evidence and Score | Must never decide this |
| What did a model request contain? | Strong: `ModelRequestRecorded` | Content intentionally excluded |
| Token use/provider latency | Durable `ModelUsageRecorded` | Useful for live correlation |
| Which operation dominated wall time? | Expensive to reconstruct; some intervals absent | Primary benefit |
| What was still running at process death? | Recovery can determine durable state afterward | Useful in real time; lossy at crash |
| Cross-component critical path | IDs permit offline joins | Natural parent/child view |
| Fleet-wide rates and percentiles | Requires separate aggregation | Natural backend/Collector use |

The systems are complementary because they make opposite tradeoffs:

- canonical records favor completeness, replay, and integrity;
- telemetry favors low-latency search, aggregation, and causal visualization;
- evaluation favors controlled experiments and verdict evidence.

No telemetry callback may sit inside an EventStore transaction, affect a
domain decision, or change an evaluation result.

## Answers to the seven design questions

### 1. Does tool content leave the process?

**Recommendation: no, with no first-slice opt-in.**

The denylist is not sufficient because future event fields can create new
leaks. The telemetry port should accept a closed metadata structure that has no
content-bearing fields. Explicitly excluded:

- system/user/assistant messages and reasoning;
- tool arguments, results, failure messages, command argv and environment;
- filesystem paths, workspace contents and diffs;
- API keys, exporter headers, URLs with query strings;
- raw provider responses and provider error bodies;
- policy/approval free-form reasons;
- OTel baggage.

Allowed metadata can include closed status/error classes, counts, byte/token
sizes, duration, source class (`builtin` or `mcp`), risk class, model adapter
family, request purpose, finish reason, trigger/strategy, and correlation IDs.
Identifiers are permitted on spans for audit correlation but forbidden as
metric dimensions.

Collector-side redaction remains useful as defense in depth, but OTel states
that the implementer is responsible for sensitive data and recommends data
minimization; a Collector processor is not a substitute for preventing content
at the source.^10

### 2. Port/adapters or direct dependency?

**Recommendation: project-owned port and adapters.**

Suggested dependency direction:

```text
domain / engine / tools / context / runtime
                  │ project-owned metadata only
                  ▼
        internal/harness/telemetry (port)
             ▲                    ▲
             │                    │
      memory/noop adapter     OTel adapter
             ▲                    ▲
             └──── composition ───┘
```

The port may pass `context.Context` to preserve in-process parentage, but must
not expose OTel `Tracer`, `Span`, `attribute.KeyValue`, or semantic-convention
types. Composition owns construction and shutdown. Tests use the memory
adapter; disabled production uses a true no-op and creates no goroutine, queue,
exporter, or global provider.

The Go module will still list OTel dependencies once the adapter is committed.
“Disabled” should promise **no runtime SDK state or network activity**, not a
dependency-free source tree. A separate build tag/module/binary is unjustified
until binary-size or supply-chain measurement establishes a real need.

### 3. Traces, metrics, or logs?

**Recommendation: traces first.**

- Traces uniquely add operation duration, nesting, and causal path.
- Logs would largely duplicate canonical events, while Go logs are not yet
  Stable.
- Native metrics create a second aggregation vocabulary and cardinality risk.
  A Collector can derive call count, error rate, and duration histograms from
  spans. Its span-metrics connector also exposes an aggregation-cardinality
  limit.^7

Native metrics become justified only by a named consumer and SLO that spans
cannot serve economically. OTel's stable metrics SDK recommends a default
cardinality limit of 2,000 and an overflow series; OCH should set a much smaller
explicit allowlist if it later emits metrics.^11

### 4. What is the size ceiling?

**Recommendation: 1,200 production lines and one signal.**

First-slice exclusions should include logs, native metrics, profiles, Sentry,
tail sampling, a bundled Collector/backend, a telemetry query API, UI panels,
content capture, dynamic plugins, remote configuration, and semantic-convention
code generation. Tests and documentation are not constrained by the production
line ceiling.

### 5. What happens when export fails?

**Recommendation: configuration fail-closed; operation fail-open.**

| Failure | Required behavior |
| --- | --- |
| Enabled but endpoint/scheme/queue limits invalid | `composition.Open` fails before durable resources |
| Collector unavailable after startup | Turns continue; bounded queue may drop spans |
| Queue full | Never block application; increment/rate-limit a local drop diagnostic |
| Export timeout | Cancel export; never reuse application retry policy |
| Shutdown timeout | Return/join a bounded `Close` diagnostic after application resources settle |
| No telemetry configured | Construct true no-op; no warning per Turn |

The stable trace SDK specifies bounded batch queues and timeouts; its defaults
are queue 2,048, batch 512, five-second schedule, and 30-second export timeout.^12
Those defaults are too implicit for OCH. The design should expose smaller,
range-validated limits and share the existing overall shutdown budget without
letting telemetry delay lease release. Telemetry should shut down **after**
application/MCP/host work has ended so terminal spans can be queued, but under
its own remaining bound.

OTel's Collector documentation is explicit that queues can overflow, retries
can expire, and crashes without persistence lose data.^13 This confirms why
telemetry cannot be evidence or recovery authority.

### 6. What does disabled carry?

**Recommendation: accept source dependencies, prohibit runtime activation.**

The measured dependency cost is real: 19 new module paths relative to this
repository for the SDK + OTLP/HTTP experiment. A design should pin one exact
version and record that graph in `SECURITY.md`. Runtime-disabled mode must
construct none of it. Build-tag or nested-module complexity is deferred until
binary and vulnerability measurements show it buys more than it costs.

### 7. Who consumes the traces?

**Recommendation: an operator with an OTLP endpoint; no implicit vendor.**

The first concrete reader is a developer/operator diagnosing a local or
deployed `och` process using an operator-managed Collector and backend (for
example Jaeger or another OTLP-capable system). OTel recommends sending to a
Collector in production and describes OTLP as the loss-minimizing,
backend-flexible export path.^14

Acceptance evidence must include a real local Collector-compatible receiver or
an OTLP conformance fixture that shows the same Turn tree visible outside the
process. A memory-only test would prove the port but not the product value.

## Recommended first trace model

The internal vocabulary should be small and versioned. Names below are research
recommendations, not frozen API:

| Span | Parent | Useful metadata (content-free) |
| --- | --- | --- |
| `och.turn` | root | outcome, step/tool-call counts, stable correlation IDs |
| `och.context.prepare` | Turn | trigger, token estimates, pruning count, decision ID |
| `och.context.compact` | Turn or manual root | trigger, strategy, tokens before/after, chunk count, outcome |
| `och.model.request` | Turn/compaction | adapter family, purpose, attempt index, finish reason, token counts, outcome |
| `och.policy.decide` | Turn | risk class, effect, closed rule ID |
| `och.approval.wait` | Turn | decision class, timeout/cancel outcome |
| `och.tool.execute` | Turn | builtin/MCP source, risk class, mutation flag, outcome, result bytes |
| `och.store.append` | owning operation | event count, conflict/error class, outcome |
| `och.runtime.reconcile` | startup root | inspected/repaired counts, outcome |

Span names must be static; dynamic session IDs, tool names, models, paths, and
errors never belong in names. High-cardinality correlation IDs can be span
attributes but must be excluded from Collector-derived metrics. OTel applies
attribute-count limits but defaults attribute value length to unlimited, so OCH
must set its own finite value limit even though first-slice fields are
metadata.^15

Do not emit a span for every streaming text delta or every canonical event.
Doing so adds volume without operational structure and risks content exposure.

## Context propagation and prompt-cache impact

The recommended design has **no model prompt-cache impact**:

- it does not alter system/user/assistant messages;
- it does not add tool definitions or request-body fields;
- it does not add provider HTTP headers in the first slice;
- trace sampling and export occur after/beside request construction.

In-process `context.Context` propagation changes only Go call context. It is not
model-visible. Therefore, identical model requests remain byte-identical and
provider prompt-cache keys are unaffected.

Cross-process propagation is deliberately deferred:

- ACP v1 stdio has no adopted OCH trace-context extension;
- external model endpoints need no OCH trace parent and may be untrusted;
- OTel MCP `_meta.traceparent` guidance exists but remains Development.^5

If MCP propagation is later added, it requires its own compatibility/security
decision and fixture proving that it changes only protocol metadata, never tool
arguments. Baggage should remain disabled because OTel warns it can carry
sensitive data across trust boundaries.^6

## Configuration and disclosure

An accepted design should prefer explicit OCH configuration over silently
honoring every environment variable. At minimum it must name:

- disabled/enabled mode;
- OTLP/HTTP endpoint and HTTPS rule (loopback exception, if any);
- exporter headers sourced from secret environment variables and never logged;
- sampling ratio;
- queue, batch, schedule and export/shutdown limits;
- service name/version/instance resource attributes;
- a fixed statement: “metadata leaves this process; content capture is off.”

The general OTel specification defines `OTEL_SDK_DISABLED`, but the current Go
exporter documentation says Go does not yet support it automatically.^14 OCH
should not claim an environment switch the chosen Go packages do not honor.
Its own disabled mode must be testable at Composition.

## Verification obligations for a later implementation

A complete slice needs more than “spans appeared”:

1. Port conformance shared by no-op, memory, and OTel adapters.
2. Exact parent/child tree for success, model failure, tool denial, approval
   timeout, cancellation, compaction, recovery, and shutdown.
3. A mutation proving content fields cannot be added through the port.
4. A hostile exporter fixture proving Turns do not block or fail.
5. Queue saturation and bounded drop evidence under race testing.
6. Shutdown with reachable, unreachable, and hanging receivers.
7. No goroutine/network activity when disabled.
8. Provider request-byte equality with telemetry disabled versus enabled,
   proving prompt-cache neutrality.
9. A real OTLP receiver/Collector-compatible end-to-end trace.
10. Dependency, binary-size, allocation, latency, license, vulnerability, and
    Linux/macOS cross-build evidence at the pinned version.
11. Documentation guard tying every “metadata-only” claim to an executable
    attribute allowlist test.
12. Preservation of EventStore/audit/eval behavior with the telemetry adapter
    replaced by a failing implementation.

## Recommendation and sequencing

This research is sufficient to write a normative design. It does not establish
that implementation is the project's highest-value next module. The correct
sequence is:

1. merge the research after review;
2. name the first real consumer and endpoint used for acceptance;
3. write a normative trace-only design resolving configuration, lifecycle,
   attribute allowlist, package placement, and size ceiling;
4. measure the exact pinned dependency/binary baseline in the design PR;
5. implement only after that design is accepted.

If no operator actually intends to run an OTLP consumer, stop after research.
Adding an exporter that nobody reads would repeat the dormant-mechanism mistake
already documented by the evaluation variance work.

## Sources

1. OpenTelemetry. [Semantic conventions for generative AI systems](https://github.com/open-telemetry/semantic-conventions-genai/blob/main/docs/gen-ai/README.md). Status: Development; accessed 2026-09-12.
2. OpenTelemetry. [OpenTelemetry Go](https://opentelemetry.io/docs/languages/go/). Signal stability table; accessed 2026-09-12.
3. OpenTelemetry. [GenAI agent spans](https://github.com/open-telemetry/semantic-conventions-genai/blob/main/docs/gen-ai/gen-ai-agent-spans.md) and [MCP conventions](https://github.com/open-telemetry/semantic-conventions-genai/blob/main/docs/gen-ai/mcp.md). Status: Development; accessed 2026-09-12.
4. OpenTelemetry. [Generative AI client spans: capturing instructions, inputs, and outputs](https://github.com/open-telemetry/semantic-conventions-genai/blob/main/docs/gen-ai/gen-ai-spans.md). Accessed 2026-09-12.
5. OpenTelemetry. [Semantic conventions for Model Context Protocol](https://github.com/open-telemetry/semantic-conventions-genai/blob/main/docs/gen-ai/mcp.md). Accessed 2026-09-12.
6. OpenTelemetry. [Context propagation: security best practices](https://opentelemetry.io/docs/concepts/context-propagation/). Accessed 2026-09-12.
7. OpenTelemetry Collector Contrib. [Span Metrics Connector](https://github.com/open-telemetry/opentelemetry-collector-contrib/blob/main/connector/spanmetricsconnector/README.md). Accessed 2026-09-12.
8. OpenTelemetry. [Signals](https://opentelemetry.io/docs/concepts/signals/). Accessed 2026-09-12.
9. OpenTelemetry. [Semantic conventions for events](https://github.com/open-telemetry/semantic-conventions/blob/main/docs/general/events.md). Accessed 2026-09-12.
10. OpenTelemetry. [Handling sensitive data](https://opentelemetry.io/docs/security/handling-sensitive-data/). Accessed 2026-09-12.
11. OpenTelemetry. [Metrics SDK: cardinality limits](https://opentelemetry.io/docs/specs/otel/metrics/sdk/#cardinality-limits). Accessed 2026-09-12.
12. OpenTelemetry. [Tracing SDK: batching processor](https://opentelemetry.io/docs/specs/otel/trace/sdk/#batching-processor). Accessed 2026-09-12.
13. OpenTelemetry. [Collector resiliency](https://opentelemetry.io/docs/collector/resiliency/). Accessed 2026-09-12.
14. OpenTelemetry. [Go exporters](https://opentelemetry.io/docs/languages/go/exporters/). Accessed 2026-09-12.
15. OpenTelemetry. [Common specification concepts: attribute limits](https://opentelemetry.io/docs/specs/otel/common/#attribute-limits). Accessed 2026-09-12.
16. Open Code Harness. [Observability Architecture Gate](2026-09-09-observability.md). Pinned six-project source review, 2026-09-09.
