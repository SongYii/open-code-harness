# Tool Policy Startup Extensibility Design

- **Status:** Accepted for implementation
- **Date:** 2026-09-18
- **Scope:** startup-selected, trusted Go tool-authorization policies
- **Reading copy:** [中文方案](2026-09-18-tool-policy-startup-extensibility-design.zh-CN.md)
- **Existing contracts:** [Startup extensibility](../../architecture/startup-extensibility.md),
  [Tool Runtime and Policy](2026-08-16-tool-runtime-policy-design.md), and
  [Tool authorization guard evidence](../../architecture/tool-authorization-guard-evidence.md)

## 1. Decision

Publish an **experimental** `sdk/toolpolicy` contract and add it to the existing
custom-launcher startup composition surface. A launcher may register trusted Go
policies and select exactly one before resources open. Selection is immutable
for the run. This is not a plugin kernel: there is no global registry, runtime
registration, hot reload, binary plugin loading, hook waterfall, service
locator, or alternate tool loop.

The core remains the final authorization authority. Every selected policy is
wrapped by `internal/harness/policy.Guard`; blank tool names, network risk,
unknown or inconsistent risk metadata, and out-of-workspace access are denied
before extension code runs. Strategy errors and malformed results fail closed
after it returns. Approval remains a separate Application-owned port, so a
policy can request approval but cannot grant it.

Custom decisions carry optional durable implementation attribution. Builtin
policy modes omit attribution and retain their exact historical event bytes.
The public API stays experimental until two real implementations and one real
external adopter provide compatibility feedback. A repository-owned example
is a compile and integration gate, not evidence of independent adoption.

## 2. Why the slice is combined

An attribution-only change would have no production writer because the current
Application constructs only builtin table policies. An injection-only change
would make custom decisions impossible to identify during audit and evaluation.
The minimum useful slice therefore includes four inseparable pieces:

1. a metadata-only public decision contract;
2. startup-local registration and selection;
3. non-bypassable core guarding and separate approval;
4. durable and evaluation identity.

The design deliberately does not generalize Provider, execution-environment,
storage, Eval, or event-writing extension points.

## 3. Trust, authority, and data boundary

Policies are trusted in-process Go code. They must be deterministic,
concurrency-safe, cooperative with cancellation, and perform no I/O, process
creation, network access, background work, or mutation after returning. The
host cannot contain arbitrary Go code, forcibly stop a blocked callback, or
prove determinism. A panic is not converted into a normal deny; process-level
supervision remains responsible for hostile or defective code. Untrusted
policies require a future process boundary.

The policy receives a detached value containing only:

- tool name;
- risk class (`read`, `write`, or `exec`);
- the mutation bit already checked against risk;
- the core-computed in-workspace bit; and
- an optional bounded path literal used for path-sensitive rules.

It receives no tool arguments object, file contents, model messages, event or
catalog pointer, filesystem, command runner, MCP client, approver, store,
provider, lease, telemetry handle, or mutation capability. Network and invalid
inputs never cross the public callback boundary because the core rejects them
first. Strings are immutable values and the DTO contains no slices, maps,
pointers, or interfaces supplied by the core.

The strategy may choose `allow`, `deny`, or `require_approval`. It may be more
or less permissive than a builtin mode for otherwise core-admitted input, but it
cannot override a core denial or the separate approval result. Filesystem
freshness, OS sandboxing, MCP transport security, catalog validation, and
observed-state write guards remain independent authorities.

## 4. Public contract

Create a standard-library-only `sdk/toolpolicy` package. Exact signatures are:

```go
package toolpolicy

type Risk string

const (
    RiskRead  Risk = "read"
    RiskWrite Risk = "write"
    RiskExec  Risk = "exec"
)

type Effect string

const (
    EffectAllow           Effect = "allow"
    EffectDeny            Effect = "deny"
    EffectRequireApproval Effect = "require_approval"
)

type Input struct {
    Name        string
    Risk        Risk
    Mutates     bool
    WorkspaceIn bool
    PathLiteral string
}

type Decision struct {
    Effect Effect
    RuleID string
    Reason string
}

type Policy interface {
    Decide(context.Context, Input) (Decision, error)
}

type Registration struct {
    ID      string
    Version string
    Factory func(json.RawMessage) (Policy, error)
}
```

`builtin` is reserved. IDs and versions use the existing policy-label grammar:
1–128 ASCII letters, digits, `.`, `_`, `-`, or `+`. Configuration is one
non-secret JSON object, at most 64 KiB, with duplicate keys and trailing values
rejected. The host canonicalizes object key order without changing JSON number
spellings and computes a lowercase hexadecimal SHA-256 digest. Factory input is
a private copy of the canonical bytes. Nil factories, nil or typed-nil results,
duplicate/reserved IDs, unknown selections, version mismatches, malformed
configuration, and factory errors fail before durable resources open.

The package exports `ValidLabel` and `CanonicalConfig` with the same semantics
as the context-policy package so custom launchers and Eval validate one byte
identity instead of reimplementing it.

The callback receives the admitted request context. There is no callback
timeout goroutine: cancellation is cooperative and a timed-out wrapper would
not prove that the in-process code stopped. Returned decisions accept only the
three declared effects, valid UTF-8, nonblank fields, a `RuleID` of at most 128
bytes, and a `Reason` of at most 256 bytes. `PathLiteral` is at most 4096 bytes,
matching the existing tool path bound. An invalid result or callback error
yields no usable decision.

## 5. Internal boundary and data flow

The internal `policy.Engine` becomes context-aware:

```go
type Engine interface {
    Decide(context.Context, Input) (Decision, error)
}
```

Builtin table engines ignore the context. `Guard` remains the mandatory final
authority and validates both the input before delegation and output after it.
Application, not Composition, applies the guard to every injected strategy so
an internal constructor cannot accidentally bypass it.

The startup flow is:

```text
sdk/och.Run
  -> launcher parses -tool-policy* flags
  -> composition freezes the per-run registration list and canonical config
  -> selected public Policy is adapted to internal policy.Engine
  -> application.NewService wraps it with policy.Guard
  -> pipeline constructs detached metadata and calls Decide(ctx, input)
  -> core records attributed decision before approval or execution
```

`sdk/och.Extensions` gains `ToolPolicies []toolpolicy.Registration`. The
internal launcher replaces its positional context-policy slice with an
internal extension struct so future fields cannot be confused by position.
Composition owns conversion between public and internal DTOs; neither Domain
nor Application imports the SDK.

Application configuration gains an internal strategy and optional domain-owned
identity. With no injected strategy it constructs the existing builtin mode.
With an injected strategy, the existing `PolicyMode` must be empty/default and
the identity must be valid and non-nil. This prevents ambiguous “custom plus
read_only” layering. The stock binary registers no custom policy, so naming one
through flags fails at startup rather than silently falling back.

The CLI surface is:

```text
-policy                  existing builtin mode; default/read_only/allow_writes/deny_all
-tool-policy             startup-registered custom policy ID; empty uses builtin mode
-tool-policy-version     optional expected registration version
-tool-policy-config      non-secret JSON object; default {}
```

Registration alone never selects a policy. Custom selection rejects a
non-default `-policy`; builtin selection rejects custom config or version.

## 6. Failure semantics

| Failure | Required behavior |
| --- | --- |
| Invalid registration/config/selection | Fail startup before store, provider, MCP, or host construction |
| Core-denied input | Record the existing core deny; do not call the custom policy |
| Policy returns an error while the request context remains live | Record fixed `policy_failed` rule/reason values, attribute the deny to the selected policy, execute nothing |
| Policy returns an invalid decision | Same fixed `policy_failed` deny path; execute nothing |
| `require_approval` | Persist the decision, then use the existing Approver port; missing/denied/timeout remains deny |
| Caller cancellation while deciding | Use the existing caller-canceled turn path rather than manufacturing a policy failure; never execute |
| Policy blocks or panics | No false containment claim; host/process supervision owns recovery |
| Decision append outcome is unknown | Preserve the existing append-resolution rule; execute zero tools until commitment is proven |

Policy error text is not persisted as `Reason` and must not reach diagnostics
unredacted. Existing policy effect/rule telemetry remains unchanged; this slice
does not add identity, configuration, or path attributes to OTel.

## 7. Durable identity and compatibility

Domain owns a new value independent of the SDK:

```go
type ToolPolicyIdentity struct {
    ID           string `json:"id"`
    Version      string `json:"version"`
    ConfigDigest string `json:"configDigest"`
}
```

`RecordPolicyDecision` and `PolicyDecisionRecorded` gain
`Policy *ToolPolicyIdentity` with JSON tag `policy,omitempty`. Domain validates,
clones, and strict-decodes exactly `id`, `version`, and `configDigest`. The
digest is exactly 64 lowercase hexadecimal characters. Application copies its
startup-frozen identity into every custom decision, including a fail-closed
deny caused by a strategy error. A policy cannot supply or alter its identity.

Builtin/default and all existing builtin modes use `nil`. Golden codec tests
must prove their event bytes are unchanged; `omitempty` alone is not evidence.
New readers accept old histories. Historical replay validates facts but never
loads or invokes policy code.

An old strict reader may reject new opt-in custom-policy events even though the
event envelope version is unchanged. Before first custom-policy use, make a
verified backup. Rollback requires a reader supporting the optional field or
restoring that backup and losing later writes. Never strip the field, rewrite
events, or recompute an audit chain.

`policy.decision.recorded` remains a canonical audit/version fact and remains
explicitly omitted from the session transcript and ACP trajectory projection.
The JSONL audit replica already carries canonical event bytes and therefore
captures the identity without changing the UX transcript contract.

## 8. Evaluation identity

`SubjectPolicy` gains an optional `ToolPolicy *SubjectToolPolicy`. The value
freezes ID, version, canonical non-secret config, and its digest. A custom tool
policy is valid only with builtin mode `default`; absence preserves every
existing Subject byte. Subject decoding re-canonicalizes config and verifies
the digest.

The ACP executor emits `-tool-policy`, `-tool-policy-version`, and canonical
`-tool-policy-config` arguments. The executor's existing binary digest pins the
compiled custom implementation. The stock in-process executor rejects custom
tool policies; it must never silently run builtin policy instead. Audit
verification checks that every observed attributed policy decision agrees with
the frozen Subject identity. A scenario that executes no tools need not
manufacture a policy event.

No new Eval plugin registry or evaluator API is introduced.

## 9. Example and stability

Add an independent Go module under `examples/deny-tools`. It imports only
`sdk/och` and `sdk/toolpolicy`, registers a versioned policy configured with a
bounded list of denied tool names, and starts the shared launcher. The example
demonstrates startup selection, a core-safe custom decision, durable identity,
approval separation, and ACP interoperability.

The example is a compilation and integration consumer owned by this project.
It does not satisfy the stability gate. `sdk/toolpolicy` and the expanded
`sdk/och.Extensions` remain experimental and may change incompatibly.

## 10. Verification and falsification

Required tests include:

- public SDK import isolation and an independent-module build;
- canonical config equivalence, duplicate-key rejection, the 64 KiB limit,
  label validation, duplicate/reserved registrations, version mismatch, and
  nil/typed-nil factory results;
- core-denied inputs never calling a permissive public policy;
- strategy errors and invalid outputs producing an attributed, durable deny
  and zero tool execution;
- custom allow/deny/approval decisions with the existing Approver semantics;
- exact legacy event-byte goldens with absent attribution;
- strict custom identity codec, clone, replay, JSONL audit, and malformed-field
  rejection;
- Subject canonicalization/digest checks, ACP argv freezing, in-process
  rejection, and identity/evidence agreement;
- cancellation, concurrent decisions, shutdown during a callback, and the
  independent ACP example.

Mutation evidence must remove the pre-strategy core deny, post-strategy output
validation, identity cloning, default omission, and Subject/runtime identity
agreement one at a time and observe the intended focused test fail. A mutation
masked by another check is not evidence.

## 11. Explicit exclusions

This slice does not add runtime hot swap, Go binary plugins, global
registration, policy chaining, hook ordering, remembered grants, policy-owned
approval, argument/content inspection, event writing, alternate execution,
storage or Eval implementations, arbitrary network permission, panic recovery,
hard callback termination, or an untrusted-code sandbox. It does not stabilize
the SDK or claim external adoption.

## 12. Delivery sequence

1. Public DTO/config contract and context-aware internal engine.
2. Domain identity, strict codec, clone, byte-compatibility, and audit tests.
3. Application injection with mandatory guard and attributed failure paths.
4. Composition/launcher registration, selection flags, and startup rejection.
5. Eval Subject/ACP identity freezing and verification.
6. Independent example, end-to-end ACP proof, docs, mutation checks, race suite,
   and evidence ledger.

Each step is test-first. The implementation plan may split commits at these
review boundaries but must not publish a selection path before durable identity
and mandatory guarding are exercised together.
