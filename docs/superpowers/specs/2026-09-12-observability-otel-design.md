# Trace-only Observability and OpenTelemetry — Design

- **Date:** 2026-09-12
- **Status:** Accepted 2026-09-12 after the operator confirmed the
  design-readiness recommendation
- **Stability:** new, opt-in, pre-GA operational surface
- **Normative language:** English
- **Chinese summary:** [Trace-only 可观测性与 OpenTelemetry 设计（中文摘要）](2026-09-12-observability-otel-design.zh-CN.md)
- **Research authority:** [six-project architecture gate](../../research/architecture-gates/2026-09-09-observability.md) and [design-readiness review](../../research/architecture-gates/2026-09-12-observability-design-readiness.md)
- **Consumer:** a developer or operator who explicitly configures an
  operator-managed OTLP/HTTP Collector or compatible backend to diagnose a
  running `och` process

English is normative. The Chinese file is a synchronized summary, not a
field-for-field translation.

---

## 1. Decision

Add one deliberately small, **trace-only** operational view of work already
owned by the Harness. It is metadata-only, disabled unless an OTLP traces
endpoint is configured, and never becomes an authority for Session state,
recovery, audit, evaluation, billing, or policy.

The first slice has these fixed boundaries:

1. A dependency-free project port, `internal/harness/telemetry`, owns typed
   operation vocabulary and a no-op implementation.
2. `internal/harness/adapters/otel` is the only package that imports
   OpenTelemetry. It maps the project vocabulary to a process-local OTel Go
   `TracerProvider` and exports OTLP over HTTP/protobuf.
3. Application and Runtime create spans only for bounded logical operations.
   They do not create a span for each model delta, event, database row, or
   Context scan unit.
4. Prompt text, model output, tool arguments/results, file paths, command
   arguments, approval reasons, error messages, provider URL, and credentials
   cannot be represented by the port and therefore cannot be exported.
5. Invalid enabled configuration fails before durable resources are opened.
   Once startup validation succeeds, export failure is fail-open: Harness work
   continues and telemetry may be dropped.
6. No trace context crosses a wire in this slice. Model HTTP, ACP, MCP, and
   subprocess environments remain byte-for-byte unchanged; baggage is absent.
7. No OTel logs or native metrics are added. Operators may derive RED metrics
   from spans in their Collector, using only the low-cardinality dimensions
   named in §9.

This is a second, lossy diagnostic view. Canonical events remain the durable
truth and the JSONL audit replica remains the verifiable local audit view.

## 2. Goals and non-goals

### Goals

- Show where a Turn spends time across context preparation, model requests,
  policy, approval, tools, and canonical appends.
- Correlate a trace back to existing Session/Turn/Item/Append identifiers
  without persisting trace identifiers in Domain events.
- Make a Collector outage incapable of blocking, failing, or materially
  slowing a Turn.
- Keep disabled behavior free of telemetry workers, queues, exporter calls,
  environment discovery, or global OTel state.
- Prove, rather than merely claim, that enabling telemetry does not change the
  provider request or model prompt-cache key.

### Non-goals

- No replacement or reconstruction path for the EventStore, audit JSONL,
  transcript, or evaluation evidence.
- No prompt, completion, tool, path, argv, arbitrary exception, stack trace,
  or other content capture, and no future opt-in switch for it in this design.
- No OTel logs, native OTel metrics, profiles, Sentry, dashboard, query API,
  bundled Collector, or bundled backend.
- No tail sampling, remote sampling, dynamic reconfiguration, baggage, links,
  or external trace-context propagation.
- No direct SaaS exporter, authentication headers, OAuth, or credentials in
  `composition.Config`. A vendor that needs authentication sits behind the
  operator's Collector.
- No semantic dependency on the Development-status OTel GenAI or MCP
  conventions. Their later adoption requires a compatibility review.
- No instrumentation of independent ACP clients or the browser bridge.

## 3. Authority and correlation

Telemetry observes an operation; it does not publish a Harness fact.

| Concern | Authority | Telemetry role |
| --- | --- | --- |
| Session/Turn state | canonical EventStore | correlate duration and outcome |
| Recovery decision | Runtime replay and recovery append | show one reconciliation pass |
| Tool permission | Policy decision plus approval result | show time and closed decision |
| Provider usage | recorded model usage event | display copied counters when available |
| Evaluation result | frozen evidence and Score | none |
| Export delivery | operator's Collector/backend | lossy operational transport only |

Trace IDs and span IDs are never written into Domain commands, events,
checkpoints, audit JSONL, transcripts, ACP frames, MCP metadata, model headers,
or evaluation identity. Existing stable identifiers are copied into sampled
spans for one-way correlation back to authoritative records. Unsampled work
remains completely reconstructable from those records.

## 4. Port and type contract

`internal/harness/telemetry` imports only the standard library. It exposes a
small typed port, conceptually:

```go
type Tracer interface {
    Start(context.Context, Start) (context.Context, Span)
}

type Span interface {
    End(End)
}
```

`Start` contains a closed `Kind` plus typed, optional structures for the nine
operations in §7. `End` contains a closed outcome, stable internal error code,
and typed counters appropriate to the same operation. There is deliberately
no `map[string]any`, arbitrary attribute, event, log, exception, or raw-error
field. Constructors validate enums and bounds; adapters receive already-owned
values, never pointers to mutable request/result structures.

`Start`/`End` misuse cannot fail Harness work. An invalid telemetry record
becomes a no-op span and a bounded diagnostic in the enabled adapter. `End` is
idempotent because cleanup and cancellation paths can converge. The no-op
tracer is the default and allocates no background resource.

The allowed scalar vocabulary is limited to:

- existing validated IDs: Session, Turn, Item, Call, Approval, Compaction, and
  Append;
- closed enums already owned by Domain/Application: purpose, source, risk,
  policy effect, approval decision, compaction trigger/strategy, terminal
  status, and stable error code;
- bounded counts and booleans: step, event count, tokens, bytes, truncation,
  summary chunks, coverage, and recovered-candidate counts; and
- configured model identifier and tool name, each capped at 256 UTF-8 bytes
  for telemetry even if another contract permits more.

No free-form `Reason`, `Message`, `Content`, `Input`, `Arguments`, URL, or path
field exists. Values over their telemetry bound are omitted, not truncated,
so a partial secret cannot be produced by telemetry truncation.

## 5. Package placement and dependency direction

The slice adds one port and one adapter:

```text
internal/harness/telemetry/       typed vocabulary, validation, noop
internal/harness/adapters/otel/   OTel SDK, OTLP/HTTP, bounded processor
internal/harness/adapters/memory/ deterministic trace recorder for tests
```

Dependency direction becomes:

```text
application ───────┐
runtime ───────────┼──> telemetry <── adapters/otel <── OTel libraries
composition ───────┤
adapters/memory ───┘
```

`application` and `runtime` may import `telemetry`; `telemetry` imports no
project package. `adapters/otel` imports only `telemetry` among project
packages and cannot import another adapter. The existing `adapters/memory`
owner may implement the port for deterministic tests without importing OTel.
`composition` remains the only owner that constructs the concrete production
adapter. `domain`, `engine`, `policy`, `tools`, `contextengine`, ACP, MCP,
provider, and filesystem/exec adapters do not import OTel or the new port.

The executable architecture registry must classify both packages and prove:

- an unclassified telemetry package fails;
- OTel imports outside `adapters/otel` fail;
- `adapters/otel` cannot import siblings; and
- independent clients cannot import either Harness telemetry package.

The implementation accepts that the compiled `och` binary and `go.mod` carry
the SDK/exporter dependencies even when telemetry is disabled. Disabled means
no runtime machinery or egress, not a dependency-free build. Build tags,
nested modules, and alternate binaries are rejected unless a later measured
binary-size or supply-chain requirement justifies their operational cost.

## 6. Composition configuration

Add this project-owned shape; callers never configure an OTel SDK object:

```go
type Telemetry struct {
    OTLPTraceEndpoint     string
    SampleRatio          float64
    AllowInsecureLoopback bool
}
```

An empty `OTLPTraceEndpoint` disables telemetry. Endpoint presence is the
explicit opt-in; no standard OTel environment variable silently enables or
changes the assembly. When disabled, a non-zero `SampleRatio` or true
`AllowInsecureLoopback` is rejected as ambiguous configuration.

When enabled:

- `OTLPTraceEndpoint` is an absolute URL for the OTLP/HTTP traces resource and
  must end in `/v1/traces`;
- user info, query, and fragment are forbidden;
- HTTPS is required, except HTTP is allowed only when
  `AllowInsecureLoopback` is true and the hostname is a literal loopback IP;
  DNS names are not accepted for the plaintext exception;
- zero `SampleRatio` means the first-slice default `1.0`; otherwise the value
  must be finite and in `(0, 1]`; and
- no custom headers, certificates, proxy setting, compression switch, or
  exporter timeout is exposed in the first slice.

`cmd/och` and the shared full-assembly flag binder add:

```text
-otel-traces-endpoint URL
-otel-traces-sample-ratio RATIO
-otel-allow-insecure-loopback
```

The flags apply equally to normal ACP serving and `compact-session`, because
both open the full assembly. `export-session` remains read-only and does not
open telemetry. Evaluation Subject identity does not gain telemetry fields;
normal eval execution stays disabled so telemetry cannot become hidden
evidence or external test pollution.

Configuration validation runs before the API key is consumed, the OTel
provider is constructed, the SQLite file is opened, or a lease is acquired.
An invalid enabled endpoint therefore leaves no durable resource behind.

## 7. Trace topology

Span names are stable project names. Experimental GenAI/MCP convention names
are not the Domain or port contract.

| Span | Parent | Starts/ends at | Required metadata |
| --- | --- | --- | --- |
| `och.turn` | root | one `RunTurn` invocation | session, request role, terminal status/code; Turn when known |
| `och.context.prepare` | Turn | one pre/mid/overflow preparation | trigger, compacted, estimated tokens, pruned count |
| `och.context.compact` | prepare or root for manual | one summary/reset bracket | strategy, trigger, chunks, coverage/token counts, outcome |
| `och.model.request` | Turn or compact | one `TurnRunner.Run`/`Collect` | purpose, model, item, usage, cached usage, finish class, code |
| `och.policy.decide` | Turn | one tool policy decision | tool, source, risk, effect, rule ID |
| `och.approval.wait` | Turn | time blocked on the approver | approval/call/tool IDs, closed decision |
| `och.tool.execute` | Turn | actual builtin/MCP invocation only | call/item/tool/source/risk, result code, bytes/truncated |
| `och.store.append` | current operation | one Append plus exact-resolution work | append/session IDs, event count, expected version, outcome |
| `och.runtime.reconcile` | root | one startup reconciliation pass | candidates, recovered count, outcome code |

`och.turn` starts before idempotency lookup. A duplicate request that joins an
existing execution gets its own short root with `role=joined`; it does not
claim the owner's child work. The execution owner gets `role=owner`. A replay
of an already-terminal request gets `role=replayed`. These roles describe the
invocation and prevent double-counting one model/tool execution.

Manual compaction has no Turn and therefore starts a root
`och.context.compact`. Pre-turn, mid-turn, and overflow compaction is a child
of `och.context.prepare`, itself a child of the owning Turn. Chunked summaries
create one `och.model.request` child per real provider call, but never a span
per projected message or covered event.

`och.tool.execute` starts only after validation, policy, and any approval. A
rejected or invalid tool offer has policy/append evidence but no execution
span. The span encloses the actual builtin or MCP port call, not persistence of
its eventual result.

`och.store.append` covers the initial append and, for unknown outcome, the
bounded resolution loop as one logical publication operation. It never adds a
span per event or SQLite statement. Runtime recovery appends are children of
the reconciliation span.

No span status is inferred from arbitrary error text. `och.outcome` uses a
closed value (`ok`, `denied`, `canceled`, `timeout`, `failed`, `dropped`) and
`och.error.code` uses the existing stable internal code when one exists.
Expected policy/approval denials remain outcomes rather than OTel exceptions;
no exception event or stack is recorded.

## 8. Adapter, batching, and failure semantics

The adapter owns a process-local `sdktrace.TracerProvider`; it does not call
the OTel global provider, global propagator, or global error handler. Internal
contexts still carry sampled parent spans between Harness calls.

Use fixed first-slice limits, smaller than the OTel SDK defaults measured by
the research:

| Limit | Value |
| --- | ---: |
| queue | 256 completed spans |
| batch | 64 spans |
| scheduled flush | 1 second |
| one export call | 3 seconds |
| span attributes | 32 |
| attribute value | 256 UTF-8 bytes |
| span events and links | 0 |

The adapter supplies its own small non-blocking bounded processor rather than
using an opaque queue whose drops cannot be observed. `OnEnd` performs a
non-blocking enqueue. A full queue increments an atomic dropped counter and
returns immediately. One worker batches up to 64 spans and exports on batch
fullness or the one-second tick. It has no worker per span and no unbounded
slice, channel, retry queue, or retry loop.

Exporter errors are swallowed after a stable, content-free, rate-limited
stderr diagnostic such as `telemetry: export failed; spans may be missing`.
The raw exporter error and endpoint are not printed because either can contain
operator infrastructure details. Queue overflow similarly reports only a
bounded aggregate count. Diagnostics never use stdout, which may carry ACP.

The OTLP exporter's built-in retry is explicitly disabled: one batch gets one
bounded HTTP call. A dedicated HTTP client does not consult `HTTP_PROXY`,
`HTTPS_PROXY`, or other proxy environment, and the adapter does not read the
standard OTel configuration environment. The Collector owns durable queueing,
retry, authentication, filtering, and routing. This process explicitly
accepts telemetry loss on queue overflow, deadline, Collector outage, or
crash.

`ForceFlush`/shutdown drain only within the caller's context. Deadline expiry
drops the remainder, reports one diagnostic, and returns without changing the
Harness shutdown result. Telemetry initialization errors caused by locally
invalid SDK/resource construction fail `composition.Open`; network
unavailability discovered during export does not.

## 9. Sampling and cardinality

The local provider uses parent-based trace-ID-ratio head sampling. With no
incoming wire context, each root decision is local; children inherit it. The
first-slice enabled default is `1.0` because the initial consumer is explicit
diagnosis rather than an always-on fleet deployment. Operators can lower the
ratio without changing model behavior.

High-cardinality IDs are allowed on sampled spans for audit correlation, but
must not be configured as span-metrics dimensions. The documented safe
Collector-derived metric dimensions are only:

```text
span.name
och.outcome
och.model.purpose
och.tool.source
och.tool.risk
och.policy.effect
och.context.trigger
och.context.strategy
```

Session, Turn, Item, Call, Approval, Append, provider request, runtime, model,
tool, rule, and error-code values are excluded from the recommended metric
dimension set. This prevents the trace feature from silently creating an
unbounded metric series vocabulary.

The adapter sets stable resource metadata only: `service.name` is
`open-code-harness`; a non-empty build version may become `service.version`;
and a bounded Runtime ID may become `service.instance.id`. It constructs this
resource explicitly and runs no environment, host, process, container, cloud,
or other resource detector. Workspace, database, endpoint, provider URL, host
filesystem paths, user name, and command line are not resource attributes.

## 10. Lifecycle and construction order

`composition.Open` performs this order:

1. apply defaults and validate the entire config;
2. read the provider credential and pass the sandbox availability gate;
3. construct the no-op or OTel telemetry adapter without dialing;
4. launch Runtime with the tracer so startup reconciliation is visible;
5. construct provider, context, workspace, MCP, catalog, and Application with
   the same tracer; and
6. return the Assembly.

Every failure after step 3 shuts telemetry down after releasing any later
resource. Assembly owns it exactly once.

`Assembly.Close` stops MCP admission/resources, shuts down Runtime and its
background work, and only then flushes and shuts down telemetry, so shutdown
and reconciliation spans can finish before their provider disappears.
Telemetry shutdown shares the existing bounded shutdown context but cannot
turn an otherwise successful Harness shutdown into an error. Close remains
idempotent.

The adapter performs no signal handling, `os.Exit`, global registration, or
background lifecycle outside Assembly ownership.

## 11. Cache neutrality and protocol compatibility

Tracing changes only the in-process `context.Context` passed through existing
calls and sends a separate OTLP request after spans end. It does not change:

- `engine.ModelRequest`, its messages/tools/order, or provider wire hints;
- provider URL, headers, request JSON, streaming grammar, or retry count;
- ACP requests, updates, capabilities, or ordering;
- MCP initialization, `_meta`, tool schema, or call arguments;
- subprocess argv, environment, cwd, stdin/stdout/stderr, or sandbox; or
- Domain event and evaluation document schemas.

Therefore model prompt-cache behavior should be neutral, but acceptance still
requires capturing the provider HTTP request with telemetry disabled and
enabled and asserting byte-identical method, URL, headers owned by OCH, and
body. Merely comparing the logical `ModelRequest` is insufficient.

No `traceparent`, `tracestate`, or baggage is injected into model HTTP, ACP,
MCP, or subprocesses. External propagation is a later threat-model and
compatibility decision, not an automatic consequence of using OTel.

## 12. Semantic-convention compatibility

The port vocabulary and `och.*` span names are the stable project contract.
The adapter may use stable general OTel resource/status keys. It must not make
Development-status GenAI or MCP semantic-convention attribute names part of
Domain, Application, config, tests outside the adapter, or documentation
promises.

A later adapter-only translation may add experimental aliases when all of
these are true: the exact convention version is pinned, its opt-in/stability
requirements are documented, metadata-only policy is preserved, emitted
names are tested on the OTLP wire, and removing/changing the aliases leaves the
project port intact. Content attributes remain forbidden even if a future
convention standardizes them.

## 13. Verification and mutation obligations

Implementation is incomplete without all of the following evidence:

1. No-op tests prove disabled construction creates no goroutine, queue,
   provider, exporter request, or environment-derived behavior.
2. Config tests prove malformed endpoints, insecure non-loopback HTTP,
   userinfo/query/fragment, invalid ratios, and ambiguous disabled fields fail
   before SQLite creation and credential-dependent adapter work.
3. A memory tracer proves exact parent/child structure and terminal outcome for
   normal, tool, approval, compaction, cancellation, failure, duplicate-join,
   and replay paths.
4. A local OTLP/HTTP receiver decodes protobuf and verifies resource data,
   names, parent IDs, numeric counts, and the absence of forbidden fields.
5. Content-canary tests put unique values in prompts, model output, tool
   arguments/results, paths, argv, approval/error text, API keys, provider URL,
   and MCP metadata, then prove none occurs in the serialized OTLP payload or
   diagnostics.
6. Provider-capture tests prove byte-identical OCH-owned HTTP method, URL,
   headers, and request body with telemetry disabled and enabled.
7. A blocked/failing receiver plus more than one queue of spans proves Turn
   completion stays bounded, drops are counted, memory/goroutines remain
   bounded, and no hidden retry storm occurs.
8. Shutdown tests prove drain success, deadline drop, idempotence, and that an
   exporter error does not replace a Harness error.
9. One recorded manual validation uses an exact-pinned official Collector
   image/binary or another named Collector-compatible receiver and observes a
   complete Turn tree outside the process. This is operational evidence, not
   an ordinary credentialed CI prerequisite.
10. Architecture mutations prove moving an OTel import into Application,
    Runtime, Engine, or another adapter fails the executable boundary test.
11. Privacy mutations that add any one forbidden content field fail the wire
    canary test; queue mutations that block on full, remove the cap, or hide
    the drop diagnostic fail overload tests.
12. `go test -race ./...`, docsguard, dependency/vulnerability checks, and
    disabled/enabled benchmarks pass. Benchmarks publish Turn latency and
    allocations for no-op, sampled, and forced-drop modes.

The dependency experiment in the research was temporary. Implementation must
record the actual pinned versions, `go list -deps`/module delta, binary-size
delta, and vulnerability result in the completion evidence and update
`SECURITY.md`; it must not reuse the research count as implementation proof.

## 14. Planned file and size boundary

Expected production changes:

```text
internal/harness/telemetry/{types,noop}.go
internal/harness/adapters/otel/{adapter,processor}.go
internal/harness/adapters/memory/telemetry.go
internal/harness/application/{service,turn,loop,pipeline,append_resolution,context_*}.go
internal/harness/runtime/{host,reconcile}.go
internal/harness/composition/{config,assembly}.go
internal/harness/architecture/dependencies_test.go
cmd/och/main.go
```

Tests, documentation, and evidence are additional. The first slice has a hard
review ceiling of **1,200 non-test production lines**, measured as additions
under production Go files relative to its implementation base. Crossing the
ceiling pauses implementation for a design amendment; it is not waived by
splitting files or generated code.

No Domain migration, SQLite migration, ACP/MCP/provider wire change, or eval
schema change belongs in the slice. Finding one necessary means the design is
wrong or the slice has expanded and must return to review.

## 15. Completion condition

The design is implemented only when a real opt-in `och` process exports the
bounded tree in §7 to an external Collector-compatible receiver, forbidden
content is absent from raw OTLP bytes, provider requests are byte-identical
with tracing off/on, exporter outage and overload cannot fail or stall Harness
work, all lifecycle and architecture gates pass, dependency/size costs are
recorded, and the implemented architecture/evidence/Chinese reading copies are
published.

Until then Milestone 10 observability is **designed, not implemented**.
