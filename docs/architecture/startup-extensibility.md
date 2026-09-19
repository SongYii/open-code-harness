# Startup Extensibility — Architecture and First Slice

**Status:** Implemented context and tool-policy slices; not GA. **Date:** 2026-09-19.

**Public API stability:** `sdk/och`, `sdk/contextpolicy`, and `sdk/toolpolicy`
are **experimental**; source compatibility is not yet promised. Pin a tested
revision. Implementation status and GA readiness are distinct from this API
stability level.

The Chinese [reading copy](startup-extensibility.zh-CN.md) covers the same contract.
This document updates the startup-extension and lifecycle boundary; it does not
replace the event, checkpoint, tool-security, or telemetry contracts.

## Decision and architectural assessment

Lifecycle and history-retention fixes have value independently of the SDK.
The SDK is an experiment serving the requested startup-pluggability use case,
not a stable API justified by independent adoption. There is currently no
documented real external adopter. The project-owned example proves external
module compilation and ACP integration, not independent production use.
Before promotion to stable, require two real implementations and at least one
real external consumer with documented usage and compatibility feedback. A
self-authored example does not satisfy that adoption gate. Experimental status
does not weaken durable-event integrity, replay, or the rollback rules below.

Support third-party Go extensions selected once at startup, through a custom
compiled launcher. Do not add runtime registration, hot reload, Go binary
plugins, an arbitrary event hook bus, or a second agent loop. The first public
extensions control context compression boundaries and tool authorization
decisions. They do not own storage, execution, approval, or recovery.

The structural advantage is separation of executable behavior from durable
facts: admitted requests, append identity/resolution, fencing, replay, guarded
file mutation and ACP already have independent contracts. Extensions can change
decisions without owning history or recovery. This is an advantage for audit and
reproducibility, not evidence of superiority in latency, throughput, ecosystem
size, or model quality. There are no new comparative benchmarks in this slice.

The previous obstacles were concrete: public consumers could not import Go
`internal` ports; assembly returned a raw service outside host admission;
heartbeat cancellation did not govern those calls; a blocked renewal delayed
fencing; lease loss could reopen the same instance; command-runner resources
were not owned by assembly teardown; planning had several direct call sites.
Publishing internal aliases or introducing a universal plugin kernel would not
fix these boundaries.

The [2026-08-15 comparison](../research/architecture-gates/2026-08-15-deepseek-harness-and-roadmap.md)
remains historical evidence, not a blanket ban on startup composition. This
slice adopts explicit startup composition, not DeepSeek types/runtime code or
its client/product surface. ACP remains the client boundary.

## Ownership map

```text
custom main / stock cmd/och (signals)
  -> sdk/och.Run (public DTOs and local registrations)
    -> internal/launcher (shared flags, commands, streams)
      -> composition (validate, resolve, construct, own resources)
        -> runtime.Host (admit, cancel, drain, fence)
        -> managed Service -> application -> contextengine
                              |              -> sdk/contextpolicy.Policy
                              -> policy.Guard -> sdk/toolpolicy.Policy
        -> adapters: SQLite, provider, workspace, localexec, MCP, OTel
```

Both policy SDK packages import only the standard library. Domain owns its own
attribution DTOs, never SDK aliases. Application/contextengine can import the
policy contracts, not the launcher SDK. Only composition constructs adapters;
runtime retains its pre-existing narrow SQLite dependency. Architecture tests
inspect the SDK packages and internal launcher as well as the harness. Separate
example modules are external-module compile gates, not external adoption.

## Public launch contract

`och.Run(ctx, args, och.Streams{In, Out, Err}, och.Extensions{ContextPolicies,
ToolPolicies})` uses the same parser, ACP server, `compact-session`,
`export-session`, and teardown as the stock binary. The caller owns signals;
Run owns the assembly. ACP owns/closes its input during the call; output carries
protocol frames only. Diagnostics use the supplied error writer. No public
Service, Store, Domain, raw engine, or Eval SDK is exposed.

Each registration has an ID, version and JSON factory. The registry is local to
one launch; duplicate/reserved IDs, unknown selection, nil factories/results,
invalid configuration and version mismatches fail before database/process
construction. Factories validate configuration and return an immutable,
concurrency-safe policy. Nothing is registered globally.

- `-context-policy`: empty or `builtin` preserves the current algorithm.
- `-context-policy-config`: nonsecret JSON object, at most 64 KiB; default `{}`.
- `-context-policy-version`: optional expected registered version, used by eval.
- `-tool-policy`: empty or `builtin` preserves the current authorization table.
- `-tool-policy-config`: nonsecret JSON object, at most 64 KiB; default `{}`.
- `-tool-policy-version`: optional expected registered version, used by eval.

The host canonicalizes whitespace/object-key order, rejects duplicate keys and
trailing values, preserves number spelling, and hashes the canonical bytes with
SHA-256. Config is not a credential container: it appears in argv/eval artifacts,
and hashing low-entropy secrets would not protect them. Builtin takes no custom
config/version; its current budget flags remain the configuration mechanism.

See the independent [`keep_last_n_turns`](../../examples/keep-last-n/README.md)
and [`deny_tools`](../../examples/deny-tools/README.md) launchers. Each compiles
as its own module against public SDK packages. Merely registering a policy does
not select it.

## Context policy contract and invariants

`Policy.Plan(context.Context, Input) (Decision, error)` receives detached
metadata: trigger, force, budgets, current request estimate, previous checkpoint
coverage, and core-generated candidates. It receives no raw message/event,
provider, filesystem, store handle or checkpoint mutation capability.

Each candidate identifies a contiguous prefix ending at a whole-turn boundary,
with counts and an estimated retained-request cost (excluding the replacement
summary, which does not yet exist). Candidate enumeration is a single pass over
the already scanned units; it adds no store reads. The latest/active turn and
the existing protected-tail floor remain protected, and balanced tool units
are not split. A policy may retain more, never less, than that floor.

Decision can preserve all input or name exactly one candidate offered for that
call. Go value semantics isolate Force and the slice header, including its
length; the local pre-callback copies do not add another mutation barrier.
Candidate elements share array storage, but validation uses a separate,
core-owned map, never those mutable IDs. Thus input mutation can neither grant
an unoffered ID nor revoke an originally valid one. Automatic compaction may happen
earlier or later than builtin, but a policy cannot veto a manual request,
provider overflow, existing usage-anchor force, or estimated hard-budget
pressure when a safe candidate exists. No candidate means there is no legal cut;
the existing hard-budget/overflow failure path still applies.

Ordinary/pre-turn, mid-turn, manual, invalid-checkpoint replan, usage-anchor and
overflow paths all call the same planning entry. Builtin goes directly to the
legacy selector. Invalid candidates/callback failures yield the classified
`context_policy_invalid` error; there is no silent fallback to builtin.
Cancellation propagates normally. A valid plan still uses the existing
summarizer, checkpoint validation, deterministic reset ladder, append protocol,
materialization, pruning and request recording.

**Commit, not intention, determines coverage.** Materialization removes only
units covered by the active committed checkpoint. If rolling summary fails
below the hard budget, the old checkpoint plus *all* its subsequent raw history
is retained; a speculative newer cut is never adopted. This closes a real
pre-existing history-loss path.

Policies and factories are trusted in-process Go code: deterministic, no I/O,
no background work, concurrency-safe and cooperative with cancellation. The
host cannot forcibly stop a hung Go function, contain arbitrary panics, or
prove third-party determinism. Untrusted execution requires a future process
boundary, not a misleading timeout goroutine around every callback.

## Tool authorization policy contract and invariants

`toolpolicy.Policy.Decide(context.Context, Input)` receives detached catalog
metadata only: tool name, risk class, mutation bit, workspace-membership fact,
and an optional bounded path literal. It receives no arguments object, event,
filesystem, process runner, provider, store, or approver. The core guard runs
first and cannot be replaced: empty names, network access, unknown/inconsistent
risk metadata, invalid path metadata, and out-of-workspace access are denied
without invoking custom code. Returned effects and bounded UTF-8 rule/reason
fields are validated before use; callback errors or invalid output fail closed
as an attributed `policy_failed` denial.

A custom decision can tighten or reproduce the safe table, but cannot execute a
tool. `require_approval` still enters Application's separate approver; it is not
permission by itself. The selected identity `{id, version, configDigest}` is
copied onto every durable `policy.decision.recorded` event. Builtin decisions
omit it byte-for-byte. Policy decisions remain deliberately absent from ACP
updates and session transcript projection; the canonical database/audit stream
is the authority.

The same trusted-code limitation applies as for context policy. Registration is
startup-local and immutable for one Assembly. Runtime hot swap and hostile-code
containment are not provided.

## Admission and shutdown

Host admission checks readiness and registers an operation under one mutex.
Its context follows both the caller and the permanent host work context. The
registration ends after callback/application cleanup, including terminal
appends. Composition exposes a managed service interface and managed external
store accessor; internal use-case calls and terminal appends use the raw service
and store without reentering a now-closed gate. ACP follows host cancellation.

Assembly exposes only `Ready()` and receive-only `Done()` for lifecycle
observation, not the Host object. `Done()` signals stopped admission, not
completed teardown; callers must still call `Close()`. Previously obtained
service/store facades remain subject to admission. The complete exported
Assembly surface is guarded against additional capabilities or exported fields;
planning-reference guards reject selector bypasses and require application
planning to enter through `planContext`, including function-value references.
The facade rejection matrix enumerates every Service/EventStore method under
caller cancellation, stopped admission, and completed teardown; a validation
error alone is not accepted as evidence that admission ran.

Normal order is: close admission -> cancel/drain operations while keeping
renewal alive -> close MCP servers -> close localexec runner -> close Provider -> stop host loops
and release matching lease/close store -> shut down existing telemetry adapter.
Concurrent Close callers share one result. Construction failures also close the
command runner and Provider once acquired, through the same `Assembly.Close`
path as normal shutdown. Builtin Provider close releases only its private HTTP
transport, not borrowed nonstandard transports. No OTel subsystem or event attributes were
redesigned.

Heartbeat uses one renewal worker plus an independent monotonic-time watchdog.
A fenced response or time since last confirmation exceeding the deadline
permanently cancels work and stops export/admission. A renewal stuck in SQLite's
mutex cannot delay that reaction; late success never reopens the instance.
The worker remains accounted for, so shutdown cannot falsely claim it exited.
Only a new host/process can acquire and run the existing recovery pass.

Operation drain and leaf teardown share the configured shutdown bound. On
timeout/unproven teardown the caller gets an error, admission remains closed,
renewal stops, and the host does not explicitly release ownership or close a
store still possibly in use. Resources may remain until process termination;
Close is not a retry/restart mechanism. Lease expiry still follows the database
lease rules. The caller/supervisor must terminate the old process before
starting a replacement, never interpret a timeout as proof of quiescence.

## Durable/eval compatibility and rollback

Custom context policies add optional `{id, version, configDigest}` under
`policy` on `context.prepared` and `context.compaction.started`, including
transcript projection. Custom tool policies add the same optional identity only
to canonical `policy.decision.recorded` events. Builtins omit it: old payloads
and hashes remain unchanged. Domain validates and defensively clones both
identities. Event-envelope and checkpoint formats stay unchanged; existing logs
are not rewritten.

New readers read old logs; absence means legacy builtin. Checkpoints can be
reused after existing core validation even if the selected policy changes.
Historical replay never invokes the plugin. **Old strict readers cannot read
new custom-policy facts.** Before first use, make a verified backup. Rollback
means using a reader supporting these fields, or restoring that pre-extension
backup (losing subsequent work); do not strip fields or rewrite the audit chain.

Eval's optional `SubjectContext.Policy` and `SubjectPolicy.ToolPolicy` freeze
ID/version/config digest/config in Subject identity. Canonical JSON normalizes
nested config key order. ACP argv includes selection, version pin and config;
the launcher binary hash freezes the compiled implementation. Collection and
readback require every durable tool-policy decision to match the frozen Subject
before evidence is scoreable. Custom policy evaluation is ACP-only in this
slice. Stock in-process BuildConfig explicitly rejects either custom policy;
there is no evaluation plugin registry or new Eval SDK.

## Verification and known limits

Tests cover legacy/default parity, all forced triggers, candidate validation,
mutated DTOs, cancellation, registry isolation/config rejection, strict identity
round-trip/clone, prior-checkpoint summary rollback, blocked renewal, permanent
fencing, drain timeout, close during turn/manual callbacks, concurrent Close,
eval identity/argv/evidence agreement, independent-module ACP compaction/export,
and an independent `deny_tools` run that proves denial, continued execution,
durable attribution, and transcript/ACP omission. Existing replay, SQLite,
transcript, CLI and architecture suites remain required.

The implementation run observed two platform-dependent localexec failures:
an unconditional “no backend” expectation and a namespace PID versus registered
host PID comparison. A subsequent reviewer reported a full-suite pass without
reproducing them. These are environment-specific observations, not universal
failures; retain both with attribution in the [evidence ledger](startup-extensibility-evidence.md).
Do not weaken production sandboxing to align test outcomes. External ACP tests
need loopback/subprocess permissions.

No measured speedup is claimed. Scan still accumulates the scanned window;
first-load/invalid-checkpoint full scans and full application replay are not
made bounded-memory by this change. No runtime hot swap, alternate summarizer,
checkpoint schema, or arbitrary event-writing extension is introduced.

## Subsequent slices and internal progress

The [Provider contract and architecture review](../superpowers/specs/2026-09-15-provider-startup-extensibility-design.md)
separates accepted internal conformance/lifecycle work from the still-draft
public extension. The [internal evidence](provider-internal-closure-evidence.md)
records shared semantic tests and resource ownership. Public SDK work still
requires a concrete external integration need before its API is frozen.

| Order | Extension | Required boundary before publication |
| --- | --- | --- |
| 2 | Provider | Public request/response DTOs and adapter around existing engine port; preserve capability/usage/failure contracts, no internal aliases |
| 3 | Execution environment | Internal process ownership slice removes MCP's raw `exec.Cmd` dependency; localexec owns managed stdio lifecycle. Filesystem/one-shot command contracts, truthful enforcement and guarded writes remain unchanged. Public/remote execution is still deferred; see the [slice plan](../superpowers/plans/2026-09-15-mcp-process-ownership.md). |
| 4 | Tool authorization policy | The guard and experimental startup SDK are implemented: pure detached DTOs, pre-strategy core denials, validated output, separate approval, frozen durable identity, and one independent-module ACP proof. There is no hot swap or hostile-code containment; see the [boundary evidence](tool-authorization-guard-evidence.md) and [startup evidence](tool-policy-startup-extensibility-evidence.md). |
| 5 | Eval/storage if justified | Offline evaluator inputs from canonical evidence; storage replacement only after full append/resolve/fencing/audit/recovery conformance, not a generic plugin interface |

Before promoting any experimental public API to stable, demonstrate two real
implementations and a real external consumer, not only a project-owned example.
This slice has not met that adoption gate. Keep one orchestrator,
one source of durable truth and one lifecycle owner throughout.
