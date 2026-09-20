# Tool Policy Startup Extensibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an experimental, startup-selected tool-policy SDK whose decisions remain bounded by the core guard, carry optional durable identity, and are reproducible through ACP evaluation without changing builtin event bytes.

**Architecture:** `sdk/toolpolicy` owns only standard-library DTOs, canonical non-secret configuration, and a trusted callback contract. Composition adapts a startup-local registration into the internal policy engine, Application applies the mandatory guard and owns decision recording, and Domain owns an optional identity value independent of the SDK. Builtin modes continue through the existing path with nil identity; custom evaluation is ACP-only and freezes policy config plus the launcher binary.

**Tech Stack:** Go 1.26, standard library, existing Domain/EventStore v2/Application/Composition/ACP/Eval packages, `go test`, `go vet`, and the race detector.

**Spec:** [docs/superpowers/specs/2026-09-18-tool-policy-startup-extensibility-design.md](../specs/2026-09-18-tool-policy-startup-extensibility-design.md)

## Global Constraints

- `sdk/toolpolicy` imports only the Go standard library; Domain and Application never import it.
- IDs and versions are 1–128 ASCII characters from `[A-Za-z0-9._+-]`; `builtin` is reserved.
- Config is one non-secret JSON object of at most 64 KiB, rejects duplicate keys/trailing data, preserves number spelling, and hashes canonical bytes as 64 lowercase hexadecimal SHA-256 characters.
- Public decision `RuleID` is nonblank valid UTF-8 at most 128 bytes; `Reason` is nonblank valid UTF-8 at most 256 bytes; `PathLiteral` is at most 4096 bytes.
- Core denials run before custom code; output validation runs after it; approval remains a separate Application port.
- Builtin/default and every existing builtin mode keep `Policy == nil` and byte-identical `policy.decision.recorded` JSON.
- Custom selection is startup-only, local to one launcher invocation, immutable, trusted in-process code, and valid only with builtin mode `default`.
- No hot reload, global registry, Go binary plugin, policy chaining, panic recovery, hard callback termination, OTel redesign, transcript projection, or untrusted-code claim.
- Every production behavior follows red-green-refactor; run the named failing test before writing its implementation.

---

### Task 1: Public tool-policy contract and canonical configuration

**Files:**
- Create: `sdk/toolpolicy/policy.go`
- Create: `sdk/toolpolicy/config.go`
- Create: `sdk/toolpolicy/config_test.go`
- Create: `sdk/toolpolicy/doc_test.go`
- Modify: `internal/harness/architecture/dependencies_test.go`

**Interfaces:**
- Produces: `toolpolicy.Risk`, `toolpolicy.Effect`, `toolpolicy.Input`, `toolpolicy.Decision`, `toolpolicy.Policy`, `toolpolicy.Registration`.
- Produces: `toolpolicy.ValidLabel(string) bool` and `toolpolicy.CanonicalConfig(json.RawMessage) (json.RawMessage, string, error)`.
- Produces constants: `DefaultID`, `MaxConfigBytes`, `MaxRuleIDBytes`, `MaxReasonBytes`, and `MaxPathLiteralBytes`.

- [ ] **Step 1: Write failing configuration and package-isolation tests**

Create `sdk/toolpolicy/config_test.go` with real canonicalization assertions:

```go
package toolpolicy

import (
    "strings"
    "testing"
)

func TestCanonicalConfigPreservesOneIdentity(t *testing.T) {
    first, firstDigest, err := CanonicalConfig([]byte(`{ "z": [1,{"b":2,"a":1}], "a": true }`))
    if err != nil { t.Fatal(err) }
    second, secondDigest, err := CanonicalConfig([]byte(`{"a":true,"z":[1,{"a":1,"b":2}]}`))
    if err != nil { t.Fatal(err) }
    if string(first) != string(second) || firstDigest != secondDigest {
        t.Fatalf("identity differs: (%s,%s) != (%s,%s)", first, firstDigest, second, secondDigest)
    }
    if len(firstDigest) != 64 || firstDigest != strings.ToLower(firstDigest) {
        t.Fatalf("digest = %q", firstDigest)
    }
}

func TestCanonicalConfigRejectsInvalidDocuments(t *testing.T) {
    tests := [][]byte{
        []byte(`null`), []byte(`[]`), []byte(`{"a":1,"a":2}`),
        []byte(`{"a":{"b":1,"b":2}}`), []byte(`{} {}`),
        append([]byte(`{"x":"`), append(make([]byte, MaxConfigBytes), []byte(`"}`)...)...),
    }
    for _, raw := range tests {
        if _, _, err := CanonicalConfig(raw); err == nil { t.Fatalf("accepted %q", raw) }
    }
}

func TestValidLabel(t *testing.T) {
    for _, value := range []string{"policy.v1", "deny_tools+1", strings.Repeat("a", 128)} {
        if !ValidLabel(value) { t.Fatalf("rejected %q", value) }
    }
    for _, value := range []string{"", "has space", "slash/name", strings.Repeat("a", 129)} {
        if ValidLabel(value) { t.Fatalf("accepted %q", value) }
    }
}
```

Add `sdk/toolpolicy/doc_test.go` as an external-package compile test constructing every public DTO and a `Policy` implementation with `Decide(context.Context, Input)`.

Extend the architecture suite with `sdk/toolpolicy` beside `sdk/contextpolicy`, asserting that all non-test imports are standard-library paths and that no public field aliases an `internal/` type.

- [ ] **Step 2: Run the tests and verify RED**

Run:

```bash
GOCACHE=/tmp/och-tool-policy-gocache go test ./sdk/toolpolicy ./internal/harness/architecture -count=1
```

Expected: FAIL because `sdk/toolpolicy` and its declared symbols do not exist.

- [ ] **Step 3: Implement the minimal public contract**

Create `policy.go` with the exact spec signatures and constants:

```go
package toolpolicy

import (
    "context"
    "encoding/json"
)

const (
    DefaultID           = "builtin"
    MaxConfigBytes      = 64 * 1024
    MaxRuleIDBytes      = 128
    MaxReasonBytes      = 256
    MaxPathLiteralBytes = 4096
)

type Risk string
const ( RiskRead Risk = "read"; RiskWrite Risk = "write"; RiskExec Risk = "exec" )
type Effect string
const ( EffectAllow Effect = "allow"; EffectDeny Effect = "deny"; EffectRequireApproval Effect = "require_approval" )

type Input struct { Name string; Risk Risk; Mutates, WorkspaceIn bool; PathLiteral string }
type Decision struct { Effect Effect; RuleID, Reason string }
type Policy interface { Decide(context.Context, Input) (Decision, error) }
type Registration struct { ID, Version string; Factory func(json.RawMessage) (Policy, error) }
```

Implement `config.go` using `json.Decoder.Token`, `UseNumber`, recursive object/array decoding, duplicate-key rejection, single-value enforcement, `json.Marshal`, SHA-256, and lowercase hex. Use tool-policy-specific error messages and treat empty input as `{}`.

- [ ] **Step 4: Verify GREEN and formatting**

Run:

```bash
gofmt -w sdk/toolpolicy/*.go internal/harness/architecture/dependencies_test.go
GOCACHE=/tmp/och-tool-policy-gocache go test ./sdk/toolpolicy ./internal/harness/architecture -count=1
git diff --check
```

Expected: PASS.

- [ ] **Step 5: Commit the public contract**

```bash
git add sdk/toolpolicy internal/harness/architecture/dependencies_test.go
git commit -m "feat(toolpolicy): define experimental decision contract"
```

---

### Task 2: Context-aware internal engine and bounded guard

**Files:**
- Modify: `internal/harness/policy/engine.go`
- Modify: `internal/harness/policy/engine_test.go`
- Modify: `internal/harness/application/pipeline.go`

**Interfaces:**
- Consumes: the limits and semantic values fixed by Task 1, without importing `sdk/toolpolicy`.
- Produces: `policy.Engine.Decide(context.Context, policy.Input) (policy.Decision, error)`.
- Produces: stable `RulePolicyFailed = "policy_failed"` and `ReasonPolicyFailed = "policy_failed"` for Application's fail-closed event.
- Produces: stable `RuleInvalidMetadata = "invalid_metadata"` and `ReasonInvalidMetadata = "invalid_metadata"` for malformed or oversized metadata rejected before a strategy runs.
- Preserves: `policy.New(mode)` returns a guarded builtin engine.

- [ ] **Step 1: Add failing context propagation and output-bound tests**

Change the test `scriptedEngine` to accept a context and capture a marker. Add:

```go
func TestGuardPropagatesContext(t *testing.T) {
    type key struct{}
    ctx := context.WithValue(context.Background(), key{}, "marker")
    strategy := &scriptedEngine{decision: Decision{Effect: EffectAllow, RuleID: "custom.read", Reason: "ok"}}
    guarded, err := Guard(strategy)
    if err != nil { t.Fatal(err) }
    if _, err := guarded.Decide(ctx, Input{Name: "read_file", Risk: domain.RiskRead, WorkspaceIn: true}); err != nil { t.Fatal(err) }
    if strategy.ctx.Value(key{}) != "marker" { t.Fatal("context was not propagated") }
}

func TestGuardRejectsOversizedStrategyOutput(t *testing.T) {
    tests := []Decision{
        {Effect: EffectAllow, RuleID: strings.Repeat("r", 129), Reason: "ok"},
        {Effect: EffectAllow, RuleID: "ok", Reason: strings.Repeat("r", 257)},
    }
    for _, decision := range tests {
        guarded, _ := Guard(&scriptedEngine{decision: decision})
        if got, err := guarded.Decide(context.Background(), Input{Name: "read_file", Risk: domain.RiskRead, WorkspaceIn: true}); err == nil || got != (Decision{}) {
            t.Fatalf("Decide = %#v, %v", got, err)
        }
    }
}

func TestGuardRejectsOversizedPathBeforeStrategy(t *testing.T) {
    strategy := &scriptedEngine{decision: Decision{Effect: EffectAllow, RuleID: "custom.read", Reason: "ok"}}
    guarded, _ := Guard(strategy)
    decision, err := guarded.Decide(context.Background(), Input{Name: "read_file", Risk: domain.RiskRead, WorkspaceIn: true, PathLiteral: strings.Repeat("p", 4097)})
    if err != nil || decision != (Decision{Effect: EffectDeny, RuleID: RuleInvalidMetadata, Reason: ReasonInvalidMetadata}) || strategy.calls != 0 {
        t.Fatalf("decision=%#v calls=%d err=%v", decision, strategy.calls, err)
    }
}
```

Add parallel cases for invalid UTF-8 in `PathLiteral`, `RuleID`, and `Reason`; the path cases must deny before the strategy, while invalid strategy output must return an error and the zero decision.

- [ ] **Step 2: Run the focused test and verify RED**

```bash
GOCACHE=/tmp/och-tool-policy-gocache go test ./internal/harness/policy -run 'TestGuard(PropagatesContext|Rejects)' -count=1
```

Expected: compile failure because `Engine.Decide` has no context, followed by behavioral failures until both bounds are enforced.

- [ ] **Step 3: Implement the context-aware guarded engine**

Update the interface and implementations:

```go
type Engine interface { Decide(context.Context, Input) (Decision, error) }

func (engine guardedEngine) Decide(ctx context.Context, input Input) (Decision, error) {
    if decision, denied := coreDeny(input); denied { return decision, nil }
    decision, err := engine.strategy.Decide(ctx, input)
    if err != nil { return Decision{}, fmt.Errorf("policy: strategy decision: %w", err) }
    if !validDecision(decision) { return Decision{}, fmt.Errorf("policy: strategy returned an invalid decision") }
    return decision, nil
}
```

Use byte length, not rune count, for the exact bounds. Reject an oversized or invalid-UTF-8 path in `coreDeny` before delegation with `RuleInvalidMetadata/ReasonInvalidMetadata`. Update builtin engines, the test helper/callers in `engine_test.go`, and `application/pipeline.go` to pass a real context; do not add `context.Background()` inside production Application code.

- [ ] **Step 4: Verify the policy and Application compile suites**

```bash
gofmt -w internal/harness/policy/*.go internal/harness/application/*.go
GOCACHE=/tmp/och-tool-policy-gocache go test ./internal/harness/policy ./internal/harness/application -count=1
```

Expected: PASS with existing builtin decision values unchanged.

- [ ] **Step 5: Commit the core evolution**

```bash
git add internal/harness/policy internal/harness/application
git commit -m "refactor(policy): propagate decision context through guard"
```

---

### Task 3: Domain-owned durable tool-policy identity

**Files:**
- Create: `internal/harness/domain/tool_policy.go`
- Create: `internal/harness/domain/tool_policy_test.go`
- Modify: `internal/harness/domain/commands.go`
- Modify: `internal/harness/domain/events.go`
- Modify: `internal/harness/domain/decide.go`
- Modify: `internal/harness/domain/record.go`
- Modify: `internal/harness/domain/codec.go`
- Modify: `internal/harness/domain/codec_test.go`
- Modify: `internal/harness/domain/decide_test.go`
- Modify: `internal/harness/domain/historical_oracle_test.go`
- Modify: `internal/harness/domain/compact_equivalence_test.go`

**Interfaces:**
- Produces: `domain.ToolPolicyIdentity{ID, Version, ConfigDigest string}`.
- Produces: `domain.ValidateToolPolicyIdentity(*ToolPolicyIdentity) error` and `domain.CloneToolPolicyIdentity(*ToolPolicyIdentity) *ToolPolicyIdentity` for internal callers.
- Extends: `RecordPolicyDecision.Policy` and `PolicyDecisionRecorded.Policy` with `json:"policy,omitempty"` on the event.

- [ ] **Step 1: Add failing byte-compatibility, strict-codec, and clone tests**

Keep the existing policy golden unchanged and add a second custom golden:

```go
func TestPolicyDecisionIdentityRoundTripAndLegacyBytes(t *testing.T) {
    legacy := codecTestRecord(PolicyDecisionRecorded{TurnID: "turn-1", ItemID: "item-1", CallID: "call-1", Name: "read_file", Effect: PolicyEffectAllow, RuleID: "default.read", Reason: "in_workspace"})
    legacyJSON, err := MarshalRecordedEvent(legacy)
    if err != nil { t.Fatal(err) }
    if bytes.Contains(legacyJSON, []byte(`"policy"`)) { t.Fatalf("legacy bytes gained attribution: %s", legacyJSON) }

    identity := &ToolPolicyIdentity{ID: "deny_tools", Version: "1.0.0", ConfigDigest: strings.Repeat("a", 64)}
    custom := codecTestRecord(PolicyDecisionRecorded{Policy: identity, TurnID: "turn-1", ItemID: "item-1", CallID: "call-1", Name: "exec", Effect: PolicyEffectDeny, RuleID: "deny_tools.name", Reason: "configured_deny"})
    encoded, err := MarshalRecordedEvent(custom)
    if err != nil { t.Fatal(err) }
    decoded, err := UnmarshalRecordedEvent(encoded)
    if err != nil { t.Fatal(err) }
    got := decoded.Event.(PolicyDecisionRecorded)
    if got.Policy == identity || *got.Policy != *identity { t.Fatalf("identity clone = %#v", got.Policy) }
}
```

Add table cases rejecting empty/oversized/invalid labels, non-hex/uppercase/wrong-length digests, unknown nested keys, missing nested keys, and duplicate nested keys. Add a clone mutation test that mutates the source identity after `CloneEvent` and proves the clone is unchanged.

- [ ] **Step 2: Run Domain tests and verify RED**

```bash
GOCACHE=/tmp/och-tool-policy-gocache go test ./internal/harness/domain -run 'ToolPolicy|PolicyDecision' -count=1
```

Expected: compile failure because `ToolPolicyIdentity` and the two `Policy` fields do not exist.

- [ ] **Step 3: Implement identity validation, cloning, and strict JSON**

Implement `tool_policy.go` with the exact 128-byte label and 64-lowercase-hex digest rules. In `decideRecordPolicyDecision` and the historical oracle, clone the command identity into the event. In `CloneEvent`, clone `PolicyDecisionRecorded.Policy`. Before decoding a policy decision, inspect an optional nested `policy` object with `validateStrictJSONObject(raw, "id", "version", "configDigest")`; reject duplicate and unknown nested keys. Call identity validation from `validatePolicyDecisionPayload`.

The resulting event shape is:

```go
type PolicyDecisionRecorded struct {
    Policy *ToolPolicyIdentity `json:"policy,omitempty"`
    TurnID TurnID `json:"turnID"`
    ItemID ItemID `json:"itemID"`
    CallID string `json:"callID"`
    Name string `json:"name"`
    Effect string `json:"effect"`
    RuleID string `json:"ruleID"`
    Reason string `json:"reason"`
}
```

- [ ] **Step 4: Verify Domain parity and durable compatibility**

```bash
gofmt -w internal/harness/domain/*.go
GOCACHE=/tmp/och-tool-policy-gocache go test ./internal/harness/domain ./internal/harness/adapters/memory ./internal/harness/adapters/sqlite -count=1
```

Expected: PASS, including the untouched legacy JSON golden.

- [ ] **Step 5: Commit the durable identity**

```bash
git add internal/harness/domain
git commit -m "feat(domain): attribute custom tool policy decisions"
```

---

### Task 4: Application injection, mandatory guarding, and fail-closed events

**Files:**
- Modify: `internal/harness/application/service.go`
- Modify: `internal/harness/application/pipeline.go`
- Modify: `internal/harness/application/loop_test.go`
- Create: `internal/harness/application/tool_policy_test.go`

**Interfaces:**
- Consumes: context-aware `policy.Engine` from Task 2 and `domain.ToolPolicyIdentity` from Task 3.
- Produces internal configuration fields: `Config.PolicyStrategy policy.Engine` and `Config.PolicyIdentity *domain.ToolPolicyIdentity`.
- Guarantees: an injected strategy is always wrapped with `policy.Guard`; builtin construction remains `policy.New(PolicyMode)`.

- [ ] **Step 1: Write failing production-path tests**

Create an Application-local scripted strategy and test the real outcomes through `RunTurn`:

```go
type appPolicyStrategy struct {
    decision policy.Decision
    err error
    calls int
}
func (s *appPolicyStrategy) Decide(context.Context, policy.Input) (policy.Decision, error) {
    s.calls++
    return s.decision, s.err
}
```

Tests must assert:

1. a custom deny records the configured decision and `ToolPolicyIdentity`, continues the turn, and performs zero tool writes;
2. custom allow executes an admitted read, while custom `require_approval` still follows the existing allow and deny Approver outcomes;
3. a permissive custom strategy is never called for an out-of-workspace request and the recorded rule is the core scope denial;
4. a live-context strategy error or invalid result records `deny/policy_failed/policy_failed` with the selected identity and executes zero tools; a sentinel secret in the callback error appears in neither durable events, transcript, telemetry fields, nor diagnostics;
5. cancellation while inside `Decide` produces the existing caller-canceled tool/turn facts and no synthetic `policy_failed` decision;
6. one concurrency-safe strategy instance can decide two sessions concurrently without cross-session attribution or races;
7. `NewService` rejects injected strategy without identity, identity without strategy, typed-nil strategy, and custom strategy combined with `read_only`/`allow_writes`/`deny_all`.

Use the existing sequence-model and counting filesystem helpers rather than asserting only mock call counts.

- [ ] **Step 2: Run the focused tests and verify RED**

```bash
GOCACHE=/tmp/och-tool-policy-gocache go test ./internal/harness/application -run 'TestCustomToolPolicy|TestToolPolicyConfiguration' -count=1
```

Expected: compile failure because Application config has no strategy/identity fields.

- [ ] **Step 3: Implement guarded injection and attributed recording**

In `NewService`:

```go
var policyEngine policy.Engine
if config.PolicyStrategy != nil && isNilValue(config.PolicyStrategy) {
    return nil, applicationError(CategoryValidation, "invalid_configuration", false, nil)
}
if config.PolicyStrategy != nil {
    if config.PolicyIdentity == nil || config.PolicyMode != "" && config.PolicyMode != policy.ModeDefault || domain.ValidateToolPolicyIdentity(config.PolicyIdentity) != nil {
        return nil, applicationError(CategoryValidation, "invalid_configuration", false, nil)
    }
    policyEngine, err = policy.Guard(config.PolicyStrategy)
} else {
    if config.PolicyIdentity != nil { return nil, applicationError(CategoryValidation, "invalid_configuration", false, nil) }
    policyEngine, err = policy.New(config.PolicyMode)
}
```

Copy the identity into Service-owned config. In `invokeTool`, call `service.policy.Decide(ctx, input)`. If the caller context is canceled after the callback, enter `cancelOwnedTurn` before synthesizing a decision. For other errors use exactly:

```go
decision = policy.Decision{Effect: policy.EffectDeny, RuleID: policy.RulePolicyFailed, Reason: policy.ReasonPolicyFailed}
```

Pass `Policy: domain.CloneToolPolicyIdentity(service.config.PolicyIdentity)` into `RecordPolicyDecision`. Preserve the existing append-before-approval/execute ordering.

- [ ] **Step 4: Verify Application and Runtime behavior**

```bash
gofmt -w internal/harness/application/*.go
GOCACHE=/tmp/och-tool-policy-gocache go test ./internal/harness/application ./internal/harness/runtime -count=1
GOCACHE=/tmp/och-tool-policy-gocache go test -race ./internal/harness/application -run 'ToolPolicy|ToolDenials' -count=2
```

Expected: PASS and zero race reports.

- [ ] **Step 5: Commit the Application boundary**

```bash
git add internal/harness/application internal/harness/policy
git commit -m "feat(application): inject guarded tool policy strategies"
```

---

### Task 5: Startup-local composition, launcher flags, and public launcher SDK

**Files:**
- Create: `internal/harness/composition/tool_policy.go`
- Create: `internal/harness/composition/tool_policy_test.go`
- Modify: `internal/harness/composition/config.go`
- Modify: `internal/harness/composition/assembly.go`
- Modify: `internal/harness/composition/lifecycle_test.go`
- Modify: `internal/launcher/run.go`
- Modify: `internal/launcher/main_test.go`
- Modify: `cmd/och/main.go`
- Modify: `sdk/och/run.go`
- Modify: `sdk/och/external_test.go`
- Modify: `internal/harness/architecture/dependencies_test.go`
- Modify: `internal/harness/architecture/sdk_surface_test.go`

**Interfaces:**
- Consumes: `toolpolicy.Registration`, `application.Config.PolicyStrategy`, and `domain.ToolPolicyIdentity`.
- Produces: `composition.ToolPolicy{ID, Version, Config string}` and `Config.ToolPolicies []toolpolicy.Registration`.
- Produces: internal `launcher.Extensions{ContextPolicies []contextpolicy.Registration; ToolPolicies []toolpolicy.Registration}`.
- Extends: `sdk/och.Extensions` with `ToolPolicies []toolpolicy.Registration`.
- Produces flags: `-tool-policy`, `-tool-policy-version`, and `-tool-policy-config`.

- [ ] **Step 1: Write failing resolver and flag tests**

Mirror the context-policy resolver tests but assert the tool-specific rules:

```go
func TestResolveToolPolicyFreezesIdentityAndConfig(t *testing.T) {
    var received []byte
    registration := toolpolicy.Registration{ID: "deny_tools", Version: "1.0.0", Factory: func(raw json.RawMessage) (toolpolicy.Policy, error) {
        received = append([]byte(nil), raw...)
        return publicScriptedPolicy{}, nil
    }}
    config := Config{Policy: policy.ModeDefault, ToolPolicy: ToolPolicy{ID: "deny_tools", Version: "1.0.0", Config: `{"b":2,"a":1}`}, ToolPolicies: []toolpolicy.Registration{registration}}
    strategy, identity, err := resolveToolPolicy(config)
    if err != nil || strategy == nil { t.Fatalf("resolve = %#v %#v %v", strategy, identity, err) }
    if string(received) != `{"a":1,"b":2}` || identity.ID != "deny_tools" || identity.Version != "1.0.0" { t.Fatalf("config=%s identity=%#v", received, identity) }
}
```

Table tests reject duplicate IDs, `builtin`, unknown selection, version mismatch, nil/typed-nil policy, builtin plus nonempty config/version, and custom selection plus non-default mode. A mutation test changes the factory's received byte slice after return and proves the frozen identity/config is unaffected.

Extend launcher parity tests to parse the three flags into Composition config. Add an SDK copy test: mutate caller registration slices after `och.Run` starts and prove the launcher uses its copied slice.

Extend `lifecycle_test.go` with a blocking public tool policy whose `Decide` waits on `ctx.Done()` and then on an explicit cleanup release. Prove both `Assembly.Close` and lease loss cancel the callback, keep resources open until cooperative cleanup returns, preserve `context.Canceled`, and reject new work while draining. This mirrors the existing context-policy lifecycle matrix and pins shutdown behavior at the real Composition/Runtime boundary.

- [ ] **Step 2: Run resolver/launcher tests and verify RED**

```bash
GOCACHE=/tmp/och-tool-policy-gocache go test ./internal/harness/composition ./internal/launcher ./sdk/och -run 'ToolPolicy|BindAssemblyFlags|Extensions' -count=1
```

Expected: compile failures for missing config, extensions, and flags.

- [ ] **Step 3: Implement resolver and DTO adapter**

`resolveToolPolicy` must validate all registrations before examining selection, canonicalize config before factory invocation, reserve `builtin`, reject ambiguous builtin/custom mode combinations, and return `(nil, nil, nil)` for builtin selection.

Implement an adapter that does value conversion only:

```go
type publicToolPolicyAdapter struct { policy toolpolicy.Policy }
func (adapter publicToolPolicyAdapter) Decide(ctx context.Context, input corepolicy.Input) (corepolicy.Decision, error) {
    decision, err := adapter.policy.Decide(ctx, toolpolicy.Input{
        Name: input.Name, Risk: toolpolicy.Risk(input.Risk), Mutates: input.Mutates,
        WorkspaceIn: input.WorkspaceIn, PathLiteral: input.PathLiteral,
    })
    return corepolicy.Decision{Effect: corepolicy.Effect(decision.Effect), RuleID: decision.RuleID, Reason: decision.Reason}, err
}
```

Call the resolver immediately after `config.Validate()`/defaults and before credential, sandbox, Host, Store, Provider, or MCP construction. Pass the returned strategy and identity to Application config.

- [ ] **Step 4: Implement the launcher extension struct and flags**

Replace positional registration slices with:

```go
type Extensions struct {
    ContextPolicies []contextpolicy.Registration
    ToolPolicies []toolpolicy.Registration
}
```

Update `Run` and `compactSession` to copy both slices. Bind all three tool-policy flags. The stock `cmd/och` passes zero Extensions. `sdk/och.Run` deep-copies both registration slices into `launcher.Extensions`; factories and policy values remain trusted immutable references.

- [ ] **Step 5: Verify startup ordering and public isolation**

```bash
gofmt -w internal/harness/composition/*.go internal/launcher/*.go cmd/och/*.go sdk/och/*.go
GOCACHE=/tmp/och-tool-policy-gocache go test ./internal/harness/composition ./internal/launcher ./sdk/och ./internal/harness/architecture -count=1
```

Expected: PASS. Failure-path tests must prove an invalid tool policy creates no database, provider request, MCP process, or runtime host.

- [ ] **Step 6: Commit the startup selection path**

```bash
git add internal/harness/composition internal/launcher cmd/och sdk/och internal/harness/architecture
git commit -m "feat(launcher): compose startup tool policies"
```

---

### Task 6: Freeze custom tool policy in Evaluation and ACP argv

**Files:**
- Create: `internal/harness/eval/tool_policy.go`
- Create: `internal/harness/eval/tool_policy_test.go`
- Create: `internal/harness/eval/tool_policy_evidence.go`
- Create: `internal/harness/eval/tool_policy_evidence_test.go`
- Modify: `internal/harness/eval/model.go`
- Modify: `internal/harness/eval/model_test.go`
- Modify: `internal/harness/eval/acp_argv.go`
- Modify: `internal/harness/eval/inprocess.go`
- Modify: `internal/harness/eval/evidence.go`
- Modify: `internal/harness/eval/evidence_identity.go`
- Modify: `internal/harness/eval/audit_verifier.go`

**Interfaces:**
- Produces: `eval.SubjectToolPolicy{ID, Version, ConfigDigest string; Config json.RawMessage}`.
- Extends: `SubjectPolicy.ToolPolicy *SubjectToolPolicy` with JSON tag `toolPolicy,omitempty`.
- Consumes: `toolpolicy.ValidLabel` and `toolpolicy.CanonicalConfig`.

- [ ] **Step 1: Write failing Subject, argv, and executor tests**

Create a valid custom value from canonical bytes:

```go
config := json.RawMessage(`{"names":["exec"]}`)
_, digest, err := toolpolicy.CanonicalConfig(config)
if err != nil { t.Fatal(err) }
subject := validSubject()
subject.Policy.Mode = string(policy.ModeDefault)
subject.Policy.ToolPolicy = &SubjectToolPolicy{ID: "deny_tools", Version: "1.0.0", ConfigDigest: digest, Config: config}
```

Assert:

- canonical key order produces identical Subject JSON/digest;
- wrong digest, reserved/invalid ID, invalid version/config, or non-default `Mode` fails `Subject.Validate`;
- `NormalizedArgv` contains each tool-policy flag exactly once with canonical config;
- `BuildConfig` rejects the Subject before constructing any resource;
- a Subject with nil `ToolPolicy` retains the exact JSON asserted by `TestSubjectJSONOmitsUnsetOptionalFields` in `model_test.go`;
- collection refuses an audit policy decision whose attribution is missing or disagrees with the frozen Subject;
- evidence readback rejects the same mismatch even if an old or tampered manifest already exists, while a no-tool stream remains valid and does not invent an event.

- [ ] **Step 2: Run Eval tests and verify RED**

```bash
GOCACHE=/tmp/och-tool-policy-gocache go test ./internal/harness/eval -run 'ToolPolicy|Subject|NormalizedArgv|BuildConfig' -count=1
```

Expected: compile failure for missing `SubjectToolPolicy` and field.

- [ ] **Step 3: Implement Subject identity and canonical marshaling**

Follow `SubjectContextPolicy`'s pattern in a separate file:

```go
type SubjectToolPolicy struct {
    ID string `json:"id"`
    Version string `json:"version"`
    ConfigDigest string `json:"configDigest"`
    Config json.RawMessage `json:"config"`
}
```

Validation recomputes canonical config/digest, rejects `builtin`, and requires `SubjectPolicy.Mode == "default"`. `MarshalJSON` substitutes canonical config bytes. Add the optional field with `omitempty`; do not reorder existing fields.

- [ ] **Step 4: Implement ACP-only execution and evidence agreement**

Append the three canonical flags in `NormalizedArgv`. At the start of `BuildConfig`, reject either custom context policy or custom tool policy with a fixed message that stock in-process execution has no registrations.

In `audit_verifier.go`, extract the current line/envelope decoder into a reusable `decodeAuditEvents` helper without changing verifier behavior. In `tool_policy_evidence.go`, add one comparison function that decodes every `PolicyDecisionRecorded` and requires its optional Domain identity to equal `Subject.Policy.ToolPolicy` exactly (ID, version, and digest). A builtin Subject therefore requires nil attribution; a custom Subject requires matching attribution on every policy decision; zero policy decisions is valid for either.

Call that comparison in both integrity paths:

1. `evidence.go`, immediately after `collectTranscriptAndAudit` and before Outcome/manifest publication, reading only the core-exported collected audit files beneath `directories.Evidence`; malformed or mismatched audit returns an error and publishes no scoreable manifest.
2. `evidence_identity.go`, after `readEvidenceDocuments` has decoded the frozen Subject, using `readAuditEvents(reader)`; if audit is absent because collection is partial, preserve the existing partial-evidence behavior, but if collected audit is readable its attribution must agree before the documents are returned.

This is an evidence-integrity invariant, not an optional scoring criterion and not a parity-only check.

- [ ] **Step 5: Verify Eval identity and evidence integrity**

```bash
gofmt -w internal/harness/eval/*.go
GOCACHE=/tmp/och-tool-policy-gocache go test ./internal/harness/eval -count=1
```

Expected: PASS with all pre-existing Subject fixtures unchanged unless they opt in.

- [ ] **Step 6: Commit evaluation identity**

```bash
git add internal/harness/eval
git commit -m "feat(eval): freeze custom tool policy identity"
```

---

### Task 7: Independent launcher example, end-to-end proof, and documentation

**Files:**
- Create: `examples/deny-tools/go.mod`
- Create: `examples/deny-tools/main.go`
- Create: `examples/deny-tools/README.md`
- Create: `examples/deny-tools/policy_test.go`
- Create: `sdk/och/tool_policy_external_test.go`
- Modify: `README.md`
- Modify: `docs/README.md`
- Modify: `docs/architecture/startup-extensibility.md`
- Modify: `docs/architecture/startup-extensibility.zh-CN.md`
- Modify: `docs/architecture/tool-authorization-guard-evidence.md`
- Create: `docs/architecture/tool-policy-startup-extensibility-evidence.md`

**Interfaces:**
- Consumes: `sdk/och.Extensions.ToolPolicies` and `sdk/toolpolicy` only.
- Produces: an independent `deny_tools` launcher with version `1.0.0` and config `{"names":[...]}`.
- Produces: auditable ACP evidence for custom denial and identity.

- [ ] **Step 1: Write the failing external-module and ACP tests**

Add a test that builds the example module and launches it against the existing loopback fixture provider. Drive an ACP prompt that offers `exec`; configure `{"names":["exec"]}`; assert the tool is not executed, the turn continues, and the canonical SQLite/audit stream contains:

```go
domain.PolicyDecisionRecorded{
    Policy: &domain.ToolPolicyIdentity{ID: "deny_tools", Version: "1.0.0", ConfigDigest: expectedDigest},
    Name: "exec", Effect: domain.PolicyEffectDeny,
    RuleID: "deny_tools.configured_name", Reason: "configured_deny",
}
```

Also assert the session transcript and ACP load updates contain no `policy.decision.recorded` row, preserving the UX contract.

- [ ] **Step 2: Run the external test and verify RED**

```bash
GOCACHE=/tmp/och-tool-policy-gocache go test ./sdk/och -run 'TestExternalToolPolicy' -count=1
```

Expected: FAIL because `examples/deny-tools` does not exist.

- [ ] **Step 3: Implement the example policy**

Create `examples/deny-tools/go.mod` with `go 1.26`, the repository module requirement, and the same local-development replacement used by `examples/keep-last-n`:

```go
module example.com/deny-tools

go 1.26

toolchain go1.26.6

require github.com/SongYii/open-code-harness v0.0.0

replace github.com/SongYii/open-code-harness => ../..
```

Run `go mod tidy` in that module after its source and tests exist; commit the generated `go.sum` if Go produces one.

The factory strictly decodes:

```go
type config struct { Names []string `json:"names"` }
```

Reject unknown keys, duplicate/blank names, more than 64 names, or a name over 128 bytes. Copy the set into an immutable map. `Decide` returns `deny_tools.configured_name/configured_deny` for configured names; otherwise it reproduces the safe default table: read allows, write/exec require approval, and unknown input returns an error (the core should already have rejected it). It performs no I/O and checks `ctx.Err()` before deciding.

The custom `main` calls `och.Run` with both stream ownership and:

```go
och.Extensions{ToolPolicies: []toolpolicy.Registration{{ID: "deny_tools", Version: "1.0.0", Factory: newPolicy}}}
```

- [ ] **Step 4: Verify the independent module and end-to-end path**

```bash
cd examples/deny-tools
GOWORK=off GOCACHE=/tmp/och-tool-policy-gocache go test ./... -count=1
cd ../..
GOCACHE=/tmp/och-tool-policy-gocache go test ./sdk/och -run 'TestExternalToolPolicy' -count=1
```

Expected: PASS. No paid provider call is permitted; the test uses only the existing loopback fixture.

- [ ] **Step 5: Update contracts and evidence**

Document the public package, flags, example, durable identity, old-reader rollback, Eval ACP-only rule, transcript omission, trusted-code limitation, and experimental stability. The evidence ledger records exact test commands, mutation outcomes, commit IDs after they exist, and explicitly says the project-owned example is not external adoption.

- [ ] **Step 6: Run documentation and architecture gates**

```bash
GOCACHE=/tmp/och-tool-policy-gocache go test ./internal/docsguard ./internal/harness/architecture -count=1
git diff --check
```

Expected: PASS.

- [ ] **Step 7: Commit example and documentation**

```bash
git add examples/deny-tools sdk/och/tool_policy_external_test.go README.md docs
git commit -m "docs(toolpolicy): prove startup extension end to end"
```

---

### Task 8: Mutation evidence and final verification

**Files:**
- Modify temporarily, then restore: `internal/harness/policy/engine.go`
- Modify temporarily, then restore: `internal/harness/domain/tool_policy.go`
- Modify temporarily, then restore: `internal/harness/application/pipeline.go`
- Modify temporarily, then restore: `internal/harness/eval/tool_policy_evidence.go`
- Final update: `docs/architecture/tool-policy-startup-extensibility-evidence.md`

**Interfaces:**
- Consumes: all prior tasks.
- Produces: no new runtime API; only verified falsification results and final evidence.

- [ ] **Step 1: Prove the pre-strategy core guard is load-bearing**

Temporarily replace the `coreDeny` branch in `guardedEngine.Decide` with a direct call to `engine.strategy.Decide(ctx, input)`, then run:

```bash
GOCACHE=/tmp/och-tool-policy-gocache go test ./internal/harness/policy ./internal/harness/application -run 'CoreDenials|OutOfWorkspace|CustomToolPolicy' -count=1
```

Expected: FAIL because a permissive custom strategy is called and its allow result survives. Restore the production code and rerun to PASS.

- [ ] **Step 2: Prove output validation and attributed failure are load-bearing**

Temporarily delete the `validDecision` rejection branch so `guardedEngine.Decide` returns strategy output unchecked, then run:

```bash
GOCACHE=/tmp/och-tool-policy-gocache go test ./internal/harness/policy ./internal/harness/application -run 'InvalidDecisions|PolicyFailed' -count=1
```

Expected: FAIL for unknown effect, blank/oversized fields, or the missing attributed deny. Restore and rerun to PASS.

- [ ] **Step 3: Prove identity copy and default omission are load-bearing**

First change `CloneToolPolicyIdentity` to `return identity` and run `TestCloneToolPolicyIdentity`; expect FAIL after source mutation. Restore it. Then set a synthetic `Policy` in the builtin Application recording path and run `TestPolicyDecisionIdentityRoundTripAndLegacyBytes`; expect FAIL because bytes changed. Restore and rerun both tests to PASS.

- [ ] **Step 4: Prove Eval/runtime identity agreement is load-bearing**

Temporarily make `validateToolPolicyAuditIdentity` return nil before decoding events, run `TestToolPolicyEvidenceRejectsIdentityMismatch`, and expect FAIL because both collection and readback accept the mismatched event. Restore and rerun to PASS.

- [ ] **Step 5: Run complete verification on the restored final tree**

```bash
gofmt -w sdk/toolpolicy/*.go sdk/och/*.go internal/harness/policy/*.go internal/harness/domain/*.go internal/harness/application/*.go internal/harness/composition/*.go internal/launcher/*.go internal/harness/eval/*.go examples/deny-tools/*.go
GOCACHE=/tmp/och-tool-policy-gocache go test ./... -count=1
GOCACHE=/tmp/och-tool-policy-gocache go test -race ./... -count=1
GOCACHE=/tmp/och-tool-policy-gocache go vet ./...
git diff --check
git status --short
```

Expected: all commands exit 0; status contains only intended tracked changes. External-process/loopback tests may require normal local process and socket permissions, but no network provider or paid model call.

- [ ] **Step 6: Record final evidence and commit**

Update the evidence ledger with the actual restored-tree outputs and mutation failures, then run the docs guard once more:

```bash
GOCACHE=/tmp/och-tool-policy-gocache go test ./internal/docsguard -count=1
git add docs/architecture/tool-policy-startup-extensibility-evidence.md
git commit -m "test(toolpolicy): record startup extension evidence"
```

Do not claim external adoption, hostile-code containment, hot reload, or SDK stability.
