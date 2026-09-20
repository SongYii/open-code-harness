# Provider Startup Extensibility: Proposed Contract and Architecture Review

- Status: Internal slices 1–2 accepted for implementation; public SDK sections remain draft
- Date: 2026-09-15
- Reviewed baseline: `bd762556b9039996aa2059207397c66d6d6fa0b6`
- Scope: startup-selected, trusted Go Provider implementations
- Reading copy: [中文方案](2026-09-15-provider-startup-extensibility-design.zh-CN.md)
- Existing contracts: [Startup extensibility](../../architecture/startup-extensibility.md),
  [Provider replay](../../architecture/provider-replay.md),
  [Provider adapter](../../architecture/provider-adapter.md)

## 1. Recommendation and current status

The user authorized internal steps 1–2 on 2026-09-15. The bounded implementation
plan is [internal Provider conformance and ownership](../plans/2026-09-15-provider-internal-closure.md).
Those internal steps are now implemented and locally verified; see the
[working-tree evidence](../../architecture/provider-internal-closure-evidence.md).
This approval does not freeze or authorize the proposed public API. The current
builtin constructors perform no network I/O; their actual resource is a private
HTTP transport. No asynchronous factory or new startup timeout is needed for
this internal slice. Model-wide close follows drained streams and closes only
owned transports; injected nonstandard transports remain caller-owned.

Make the model transport replaceable without replacing the agent. Keep request
preparation, tool execution, compaction, overflow recovery, event commitment and
recovery in the existing core. A factory registry alone does not achieve this:
the missing contracts are replay identity, resource ownership and conformance.

Start with shared semantic tests for the two existing adapters. Then close the
internal resource lifecycle using those actual implementations. Do not publish
`sdk/provider` merely to finish an abstraction diagram. Before implementing a
new public extension point, identify a real consuming project and its concrete
integration requirement. A repository-owned example is a compilation test,
not evidence of independent adoption. This follows the
[charter's non-goal](2026-08-11-open-code-harness-architecture-design.md#4-非目标).

Today `sdk/och.Extensions` exposes only context-policy registration. The
DeepSeek Chat Completions and Messages routes are internal implementations,
not public Provider plugins. Nothing in this draft changes that status. Native
Claude replay, hot swapping, a second tool loop, alternate summarizers, storage
extensions, execution-environment extensions and OTel changes are out of scope.

## 2. Review findings from current code

These are obstacles to opening the extension boundary, not claims that every
item is a defect in the current fixed-adapter deployment.

| Finding | Repository evidence | Consequence for this design |
| --- | --- | --- |
| The core already has the right narrow execution port | [engine/model.go](../../../internal/harness/engine/model.go): `Model`, `ModelStream` | Preserve one request/stream port; do not export Application or Domain |
| Implementation identity and replay family cannot be the same identifier | [engine/profile.go](../../../internal/harness/engine/profile.go): `RequestIdentity`; [application/loop.go](../../../internal/harness/application/loop.go): `acceptProviderState` compares state protocol, model and endpoint | A registration ID must not replace `AdapterFamily`; add separate attribution only when the public slice is accepted |
| Replay is typed and core-visible, not an arbitrary adapter blob | [domain/provider_state.go](../../../internal/harness/domain/provider_state.go): validation and cloning; [Provider replay contract](../../architecture/provider-replay.md) | Only existing replay profiles in the first slice; new state formats require a separate core review |
| Composition does not own a generic Provider resource | [composition/assembly.go](../../../internal/harness/composition/assembly.go): constructor selection, failure cleanup and `Assembly.Close` | A custom client with owned connections/work needs startup-failure cleanup and drain-before-close ownership |
| The named transport-neutral suite assumes exact chunks | [engine/modeltest/contract.go](../../../internal/harness/engine/modeltest/contract.go): exactly three events and two text fragments | Extract semantic assertions; keep native framing checks in adapter suites |
| Messages deliberately validates the complete response before exposing output | [adapters/anthropic/stream.go](../../../internal/harness/adapters/anthropic/stream.go): `Next` | Do not weaken Messages admission or invent chunk boundaries to fit the existing test fixture |
| Conversation and compaction share one model/runner | [composition/assembly.go](../../../internal/harness/composition/assembly.go): summarizer construction | Inject at Composition, so both paths use the selected Provider without separate routing |
| Evaluation selection is currently closed | [eval/model.go](../../../internal/harness/eval/model.go): `SubjectProvider`; [eval/acp_argv.go](../../../internal/harness/eval/acp_argv.go): `NormalizedArgv` | Public selection needs frozen evaluation identity and explicit unsupported-path rejection, not a runtime-only flag |

The useful architectural advantage is the existing separation between the
model port and durable orchestration. The remaining work is to preserve that
separation when an implementation is supplied by somebody else. This review
does not establish superiority over other projects, latency improvement or a
new live-provider certification.

## 3. Trust and authority

A Provider necessarily sees model-visible messages, tool schemas/results and
the private protocol state needed for replay. It also receives the credential
for its selected route. Unlike the context-policy DTO, its input is **not
metadata-only**. It must be treated as trusted, content-bearing code.

The public boundary must not hand it a Service, EventStore, event pointers,
workspace filesystem, approval callback, tool executor or lease handle. The
core passes detached request values and copies accepted outputs into its own
values; no `internal/` aliases or third-party SDK types escape through the
public package. Mutable slices, raw argument bytes and optional state blocks
need deep copies, including nil-versus-empty distinctions where meaningful.

These are API and ownership guarantees, not an in-process security sandbox.
Compiled Go code can access OS/network facilities directly. Deep copying
cannot make a provider safe if it concurrently mutates a returned value while
the core copies it. The contract must prohibit mutation after handing values
back and require independent state for concurrent requests. Containing hostile
code would require a separately designed process boundary.

## 4. Proposed boundary

### 4.1 Separate registration from protocol

Use three distinct identities:

1. Implementation registration: a namespaced ID, implementation version and
   digest of canonical, non-secret configuration. This answers **which code
   was selected**. An evaluation executable hash additionally pins the built
   artifact; a version string alone does not do so.
2. Route descriptor: model, normalized non-secret endpoint, capability profile
   and explicit request controls. This answers **what was requested**.
3. Replay profile: a core-recognized protocol/version. This answers **how
   retained state must be validated and sent back**.

The first slice retains the current family values and their semantics:

| Current selection | Existing `AdapterFamily` / proposed replay profile | State contract |
| --- | --- | --- |
| empty or `openaicompat` | `openai_compat` | No private Provider state for this route |
| `deepseek` | `deepseek_thinking_v1` | Typed DeepSeek reasoning state, route-bound |
| `deepseek-messages` | `deepseek_messages_v1` | Typed ordered Messages blocks, route-bound |

An external implementation may implement one of these contracts. A new
registration name does not create a new replay protocol. Arbitrary JSON state,
plugin-supplied state validators and “all Anthropic endpoints are compatible”
are not part of this proposal. Native Claude prefix/signature constraints stay
behind the [separate research gate](../../research/architecture-gates/2026-09-14-claude-native-replay.md).

Freeze and validate the descriptor once before accepting work; never ask a
mutable plugin getter for capabilities on every attempt. Profile limits must
produce a valid core context budget and support the assembly's tool catalog.
Unknown/unsupported controls fail startup instead of being silently dropped.
Changing a registration does not authorize migration of retained state. A
profile/model/endpoint mismatch must fail closed; even identical descriptors
do not prove cross-implementation replay equivalence. Test the intended switch
explicitly or use a new session; never silently remove incompatible state.

### 4.2 Candidate API responsibilities, not frozen Go signatures

Keep the first proposal small. Exact names and configuration schema are
review inputs after the consumer gate, not a promise to ship these types.

| Surface | Allowed responsibility | Excluded authority |
| --- | --- | --- |
| Registration / descriptor validation | Pure startup selection, version/config validation and frozen descriptor | Network activity, global registration, discovery or runtime replacement |
| Factory `Open` | Construct the selected model resource using bounded startup context and the selected credential | Store/lease access, running turns, alternate routing loops |
| Provider `Stream` | Start one attempt from a detached request; allow concurrent independent attempts | Tool execution, compaction, autonomous SDK retries |
| Stream `Next` / `Close` | Return validated event shapes and tear down per-attempt I/O under single ownership | Events after ownership ends, unaccounted decoding workers |
| Provider `Close(ctx)` | Close provider-wide owned resources after all admitted calls drain | Releasing the runtime lease or closing the store |

Public value types should mirror the existing semantic request fields:
correlation IDs, input/messages, tool schemas, attribution purpose, explicit
output limit and reasoning effort. Only the existing typed replay variants
are representable. The SDK package should be standard-library-only; Composition
owns conversion into engine/domain types. It must not grow a generic options
map into a second unvalidated model API.

Use bounded, schema-validated non-secret configuration; reserve builtin IDs,
reject duplicate IDs/version mismatches before resources open, and copy the
per-launch registration list. Credential values stay outside configuration
digests, argv, events and diagnostics. For the initial slice reuse the selected
credential environment mechanism, not a general secret-store callback. If the
actual consumer needs rotating credentials or another auth scheme, review that
requirement before fixing the public factory signature.

### 4.3 Request, completion and failure semantics

- `Purpose` remains attribution only. Request body changes must come from
  explicit fields, not hidden conversation/compaction branching.
- Preserve `text_delta* tool_call* completed`. Concatenated text and ordered
  assembled tool offers are semantic; chunk count is not. Usage and replay
  state belong only on successful completion. Tool IDs, argument bytes,
  output bytes, state blocks and token arithmetic retain core bounds.
- Transient text is not a durable completed assistant. The core validates
  completion and requires successful stream cleanup before committing success
  or executing a tool. Invalid output must not become tool side effects.
- Keep one owner for `Next` and `Close`, with the same request context used for
  `Stream` and iteration. Cancellation must unblock I/O; the core cannot force
  an arbitrary blocked Go function to stop. Do not claim a timed-out wrapper
  goroutine proves cleanup.
- A public classified failure maps into the existing bounded failure
  vocabulary. The bridge chooses startup versus stream phase from where the
  call failed, not from a plugin-supplied phase. Unknown errors get fixed safe
  diagnostics; never persist raw SDK errors, bodies or arbitrary `SafeMessage`
  text. Validate bounded status/code/request metadata before admission.
- Disable SDK retries. The core remains the owner of any retry and the bounded
  startup `context_overflow` compaction path. A late stream failure cannot
  masquerade as startup overflow; the provider may not silently truncate,
  summarize, alter replay blocks or retry a changed prompt.
- If attempt statistics are exposed, copy a terminal snapshot, preserve
  unknown-versus-zero usage and require agreement with completed usage when
  both exist. Failure statistics must not manufacture a successful usage fact.

### 4.4 Lifecycle and incomplete cleanup

The intended ownership sequence is:

```text
validate/freeze registration + descriptor (no resources)
  -> acquire existing runtime host ownership
  -> open Provider resource -> construct runner, summarizer and other leaves
  -> expose managed Service
  -> stop admission / cancel / drain (keep ownership for terminal writes)
  -> close owned leaves, including Provider
  -> stop Host / release matching lease / close store -> existing telemetry close
```

`Open` success transfers resource ownership to Assembly; all later construction
failures must close that resource. `Open` failure must leave no live resource:
the factory owns cleanup of its partial construction. Reject nil-success and
resource-plus-error pairs at the bridge, and account for any returned resource
during failure cleanup rather than dropping it.

Before implementing a public factory, define how an `Open` failure reports
**unproven partial teardown**. It cannot be an ordinary retryable startup error
that releases ownership while provider work might continue. Such a result, a
stuck `Open`, or a failed/timed-out Provider close must enter the existing
abandon/terminate-old-process discipline. Do not announce clean shutdown,
release the lease explicitly, or reopen in the same process on that assumption.
The lease can still expire naturally; a supervisor must terminate the old
process before launching a successor. A typed failure cannot detect a lying
implementation; this remains a trusted-code obligation.

Provider close is once-owned, after drain and before host/store release, using
the existing shared shutdown budget rather than adding a fresh full timeout
per resource. Preserve the existing MCP/localexec ordering; append Provider
cleanup to the leaf phase and all matching startup-failure paths. Do not change
OTel ownership or attributes. Builtin resource wrappers must close only clients
they own, never a process-global shared HTTP transport.

The factory startup context bounds construction, not the lifetime of the
returned Provider. Per-attempt work uses the core's request context. The first
slice does not authorize background model calls or independent work loops;
idle client resources remain owned until Provider close. Startup timeout
selection and the unproven-teardown representation must be settled explicitly
in the accepted lifecycle design, not hidden in a goroutine helper.

## 5. Durability and evaluation

Propose a separate optional implementation-attribution value on
`model.request.recorded`: ID, version and canonical non-secret config digest.
The exact field name/schema is not frozen. Domain owns its own value type;
never alias an SDK type into durable events. Mirror it through clone/validation,
strict codec, transcript/export and evaluation evidence.

Builtin/default selection omits the new value entirely. Require golden tests
for legacy request bodies, event payload bytes and audit hashes. Adding an
`omitempty` field is not proof that every path still omits it.

New readers must read old histories. Old strict readers can reject new custom
attribution even if the envelope version is unchanged. Before first opt-in,
make a verified backup. Rollback requires a reader that supports those fields
or restoring the pre-extension backup, losing later work. Never strip fields,
rewrite historical facts or recompute audit chains to fake compatibility.
Historical replay must not load or invoke an extension.

For the first public slice, evaluate a custom launcher through Agent Client
Protocol (ACP). Freeze registration/version/canonical non-secret config and
digest in Subject identity, keep the existing model/endpoint/profile fields,
and pin the compiled executable hash in executor identity. Reject a custom
selection through the stock in-process executor until explicitly supported.
Do not add a second evaluation registry. Reuse the selected Provider for
conversation and core summarization; this does not automatically extend the
separately configured quality judge.

## 6. Acceptance and falsification matrix

Separate shared semantics, native protocol framing and adversarial bridge
tests. A fixture should be able to produce the relevant failure; identical
wire frames or chunk counts across adapters are not required.

| Gate | Required evidence | Counterexample that must fail |
| --- | --- | --- |
| Shared model semantics | Both real adapters: concatenated Unicode text, ordered tools, terminal completion, cancellation, independent concurrent calls | Reorder/drop text or a tool; admit success after truncated input |
| Protocol specifics | Each native suite: malformed framing, missing terminal marker, state projection, usage totals, bounds | Messages partial decode exposes a tool; cached-token addition overflows |
| Bridge value/shape boundary | Detached nested requests/results; nil-success, stream-plus-error and cleanup accounting | Remove a deep copy and mutate that exact slice; allow invalid pair without closing its resource |
| Phase and safe errors | Startup/late/close failures, fixed diagnostics, cancel and bounded overflow | Let a late failure trigger overflow retry; echo a seeded credential from raw error text |
| Lifecycle | Startup failure after each acquisition, Close during active calls, blocked I/O, close timeout/failure, fencing and concurrent Close | Release lease before Provider teardown; report shutdown while work remains |
| Replay and compaction | Tool continuation after restart; selected Provider used by summary; failed summary retains old-checkpoint history | Swap protocol/endpoint; silently drop private state or post-checkpoint history |
| Default compatibility | Builtin request/event/hash goldens, old fixtures, strict new attribution round trips | Accidentally attribute defaults; accept unknown or malformed durable fields |
| Consumer and evaluation | Real external integration plus separate-module compile/ACP tests; reproducible Subject and binary identity | Permit an unregistered provider or silently route a custom Subject to builtin in-process code |

For each claimed safety guard, remove that guard or violate exactly the
protected invariant and observe the intended test fail. A green mutation does
not count as proof; investigate whether a different earlier check masked the
assertion. Compilation-only examples prove package isolation, not actual
consumer fit, safe I/O or malicious-code containment.

## 7. Proposed delivery sequence

This is a reviewable sequence, **not an implementation plan for an already
approved public API**. Each accepted slice needs its own implementation tasks,
contract/evidence and bilingual guide updates.

1. **Internal semantic conformance.** Work in `engine/modeltest` and the two
   adapter test packages. Extract chunk-independent assertions; exercise both
   real adapters with local fixtures while retaining their native strictness.
   Exit: shared success/cancel/concurrency semantics and explicit native
   negative cases pass; no SDK, flags, wire or durable schema changes.
2. **Internal Provider resource ownership.** Work in Composition and the
   existing adapters, without a generic registration framework. Specify actual
   client ownership, bounded startup and failed-teardown behavior; implement
   drain-before-close and all constructor rollback paths. Exit: lifecycle fault
   matrix and mutation evidence; default runtime semantics remain unchanged.
3. **Consumer checkpoint.** Record the external project's owner/repository or
   concrete private integration, required profile/auth/config/lifecycle and a
   runnable acceptance scenario. If it only needs a different compatible URL,
   use current configuration instead of inventing an SDK. If it needs a new
   replay protocol, handle that separate prerequisite first. Without this
   evidence, stop public API work after the useful internal slices.
4. **Accept and implement the smallest public vertical slice.** Freeze DTOs,
   selection/configuration, cleanup-failure representation and durable
   attribution together. Candidate locations are `sdk/provider`, `sdk/och`,
   launcher/Composition bridging, Domain/codec/transcript and eval identity.
   Update architecture ownership/import guards; no internal or vendor aliases.
   Ship selection, safe conversion, lifecycle, restart/compaction and ACP
   evidence together, not a registry that cannot complete a recoverable turn.
5. **Independent integration and stability review.** Run the consumer scenario,
   collect API/compatibility feedback and publish pinned instructions. Label
   the public surface experimental with no source compatibility promise.
   Stable promotion still requires two real implementations and a real external
   consumer; neither self-authored examples nor passing fixtures substitute
   for that adoption evidence.

No paid provider calls are necessary for steps 1–2. Any later live verification
requires an explicit call/cost budget and bounded claim; earlier DeepSeek
fixtures/live evidence do not certify a new implementation.

## 8. Decisions still needed

After internal steps 1–2, the next decision is the consumer checkpoint, not
automatic SDK publication. Identify **which external project wants to supply its own model
implementation, and what it cannot achieve with the existing endpoint flags**.
That answer determines whether the extension point is needed at all.

The proposed public API is not ready to freeze until the consumer requirement,
startup deadline/partial-teardown contract, exact non-secret configuration
schema and durable attribution schema have been reviewed. This draft records
those remaining decisions rather than presenting them as solved code.
