# Implemented Tool Runtime Contract

- Status: Implemented internal contract
- Stability: `experimental` until v1.0
- Maturity: pre-v0; not a general availability release
- Scope: Application-owned Step loop, a pure Policy Decide table, and four
  builtin workspace tools behind consumer-owned ports. Not a plugin kernel,
  MCP client, OS sandbox, ACP approval UI, or Runtime Host.
- Normative design: [Tool Runtime, Policy, and minimal workspace tools](../superpowers/specs/2026-08-16-tool-runtime-policy-design.md)
- Completion evidence: [Tool runtime evidence ledger](tool-runtime-evidence.md)
- Chinese reading copy: [已实现 Tool Runtime 合同](tool-runtime.zh-CN.md)

This document records behavior enforced by the current code and tests. It is
an internal Go contract, not a stable public protocol. Pre-v0 changes still
require the design, implementation, tests, and this document to move together.

## Delivered capability

Application owns the Step loop. One admitted `RunTurn` may run
`model → tool* → model` until the model returns no tools or a bound fires.
`engine.TurnRunner` remains one `Stream` / one attempt. Domain stays pure.
Policy is `policy.Engine.Decide(Input) Decision` — a table, not a `next()`
waterfall and not code inside tool bodies. `ModeAllowWrites` ships in
`policy`. `NewService` / `DefaultConfig` stay on `ModeDefault`.

Four builtins exist: `read_file`, `write_file`, `list_dir`, `exec`.
`list_dir` `depth` omitted ≡ 1, maximum 2, 256-entry cap. The pipeline is
`tool.call.started` → validate → lexical → `Resolve` → `Decide` → approval
→ execute. Mid-loop appends use `step_append_*` plus `ResolveAppend`.
Model-visible tool texts are frozen. Cancel or Turn-fail while a tool Item
is active uses `InterruptToolTurn` / `FailToolTurn`.

`workspacefs` is a realpath prefix jail. `localexec` confines commands
under bwrap (Linux) or Seatbelt (macOS) when available, with a fail-closed
`composition.Open` gate and a named escape hatch when neither is.
`openaicompat` sends `tools` and assembles
`tool_call*` when `NativeTools` is `supported` or `required`. EventStore v2
is still four methods. Call uniqueness is on ids. Step k≥2 logs a suffix
envelope. Stream projection is 4 MiB; tool-enabled HTTP `MaxRequestBytes`
is at least 5 MiB.

MCP client, Landlock, Windows OS confinement, ACP approval UI, parallel
tools, Runtime Host, a plugin kernel, and vendor SDKs are not implemented.

## Package authority and dependency direction

```text
headless caller / composition (tests today)
                    |
                    v
internal/harness/application  -----> internal/harness/engine
  command + Step loop                Model port, TurnRunner,
                                     tool_call stream events
                    |
        +-----------+-----------+
        v                       v
internal/harness/policy    internal/harness/tools
  Decide() only              ToolSpec catalog, schema
                             subset, lexical scope, ports
        |
        v
internal/harness/domain
  lifecycle + log-only facts

internal/harness/adapters/workspacefs  ----implements----> tools.FileSystem
internal/harness/adapters/localexec    ----implements----> tools.CommandRunner
internal/harness/adapters/openaicompat ----implements----> engine.Model
internal/harness/testkit               ----implements----> all ports (scripted)
internal/harness/adapters/memory       ----implements----> application.EventStore
```

[`dependencies_test.go`](../../internal/harness/architecture/dependencies_test.go)
enforces these directions (`TestProductionDependencyBoundaries`,
`TestForbiddenImport`, `TestClassifyProductionDirectory`,
`TestOsExecOnlyInLocalExec`, `TestAllowAllProductionException`):

- `domain` and `engine` still cannot import `os`, `os/exec`, `net`,
  `net/http`, or a path segment `provider` / `providers`.
- `application` may import `policy` and `tools`. It cannot import
  `adapters/*`, `testkit`, `os`, `os/exec`, or `net/http`.
- `policy` may import `domain` and stdlib string/json packages. It cannot
  import `application`, `engine`, `tools`, `adapters`, `os`, `os/exec`,
  `net`, or `net/http`.
- `tools` may import `domain` and lexical `path/filepath`. It cannot import
  `policy`, `application`, `engine`, `adapters`, `os`, `os/exec`, `net`, or
  `net/http`.
- `workspacefs` may import `tools`, `domain`, `os`, and `path/filepath`. It
  cannot import `application`, `testkit`, `os/exec`, `net`, `net/http`, or
  another `adapters/*`.
- `localexec` is the only production owner of `os/exec`. It cannot import
  `application`, `testkit`, `net`, `net/http`, or another `adapters/*`.
- `openaicompat` still must not import `os/exec` or `tools`. It receives
  `[]domain.ToolSchema` on `ModelRequest`.
- `policy.AllowAll` is a test constructor. Non-test production files must
  not call it.
- `go.mod` stays module-stdlib. No vendor SDK.

## Application-owned Step loop

Empty catalog keeps the previous one-attempt path: `Messages==nil`,
`Tools==nil`, one `Run`, `CompleteAssistantTurn`
(`TestEmptyCatalogKeepsNilMessagesAndTools`). With a catalog, `RunTurn`
prefixes the current user input with prior turns projected from the
session event log (`TestSecondRunTurnSeesPriorTurnMessages`). The compact
Session aggregate still discards completed turns. A non-empty catalog requires
`RequestIdentity != nil` and `NativeTools ∈ {supported, required}`.
`required` plus empty catalog, `unsupported` plus a catalog, or a catalog
missing `FileSystem` / `CommandRunner` is `CategoryPolicy` /
`invalid_configuration` (`TestNewServiceToolComposition`). Engine does not
consult a profile; it forwards `Messages` and `Tools` as data
(`TestTurnRunnerForwardsMessagesAndToolsWithoutConsultingProfile`).

`DefaultConfig` pins `PolicyMode=ModeDefault`, `MaxSteps=8`,
`MaxToolCallsPerStep=8`. `ModeAllowWrites` is a legal `NewService` mode.
Unknown mode `"yolo"` is `CategoryValidation` /
`invalid_configuration`. Unset `Approver` becomes `tools.DenyApprover`.

When the catalog is enabled, `runStepLoop` (`internal/harness/application/loop.go`):

1. Rejects a serialized messages+tools projection over 4 MiB with
   `envelope_limit` and **zero** additional `Stream`
   (`TestEnvelopeLimitFailsWithoutStream`).
2. Calls `TurnRunner.Run` once for this Step.
3. `len(tool_calls)==0` → `CompleteAssistantTurn` (Turn ends).
4. `1 ≤ n ≤ 8` → `CompleteAssistantMessage` (item only; Turn stays
   running), then sequential pipeline for each call.
5. `n > 8` → `invalid_stream`; **zero** `tool.call.started`; no prefix
   execute (`TestOversizedToolBatchFailsWithoutStarting`).
6. After durable tool terminals, if `step == MaxSteps` and the model still
   returned tools → `step_limit` (`TestMaxStepsFailsAfterDurableTools`).
7. Else commit `assistant.message.started` + `model.request.recorded` for
   Step k≥2 (suffix envelope) and `Stream` once more
   (`TestTwoStepReadFileSuccess`).

One Request ID still admits once. A second invocation reconstructs and
never starts a model call (`TestRunTurnHTTPFindCommandRequestPreventsSecondStream`).
Only the live owner may `Stream` after every tool of the previous Step has
a durable terminal. Crash mid-loop remains `reconciliation_required`.
`DigestRunTurnRequestV1` is still Session ID + exact UTF-8 input.

A two-step `read_file` success reads the fixture, sends the frozen file
text as a tool message on Stream 2, and completes
(`TestTwoStepReadFileSuccess`, `TestRunTurnHTTPReadFileThenCompletes`).

## Policy Decide table

```go
type Engine interface {
    Decide(Input) (Decision, error)
}

type Input struct {
    Name, PathLiteral          string
    Risk                       domain.RiskClass
    Mutates, WorkspaceIn, Network bool
}

type Decision struct {
    Effect Effect // allow | deny | require_approval
    RuleID, Reason string
}
```

`New` accepts `ModeDefault`, `ModeReadOnly`, `ModeAllowWrites`, and
`ModeDenyAll` (`TestNewAcceptsShippedModes`). It rejects `""`,
`allow_all`, `bypass`, `yolo`, and unknown tokens
(`TestNewRejectsUnknownMode`). `PathLiteral` is audit-only; Decide does
not interpret it as a path (`TestDecideDoesNotInterpretPathLiteral`).

In-workspace table (`TestDecideDefaultTable`,
`TestDecideAllowWritesEveryCell`):

| Mode | RiskRead | RiskWrite | RiskExec | RiskNetwork / Network |
| --- | --- | --- | --- | --- |
| `default` | allow | require_approval | require_approval | deny |
| `read_only` | allow | deny | deny | deny |
| `allow_writes` | allow | allow | require_approval | deny |
| `deny_all` | deny | deny | deny | deny |

Out-of-workspace (`WorkspaceIn=false`) is deny for every shipped mode.
Empty or whitespace name is deny. Unknown risk is deny. Network risk or
`Network=true` denies even under `ModeAllowWrites`. `AllowAll()` always
allows (`TestAllowAllIsUnconditional`) and is banned outside tests.

`ModeAllowWrites` lets `write_file` execute without an Approver
(`TestModeAllowWritesExecutesWrite`). `exec` still requires approval in
that mode. Production composition remains `ModeDefault`.

## Four builtins

`tools.DefaultWorkspaceSpecs` is four name-unique `domain.ToolSpec` values
(`TestDefaultWorkspaceSpecsLockedContracts`). Source `builtin` is shipped;
source `mcp` is catalog-legal so a later adapter can project into the same
type. There is no MCP client.

| Tool | Required | Optional | Risk / Mutates | Model-visible success |
| --- | --- | --- | --- | --- |
| `read_file` | `path` | — | read / false | UTF-8 file; over 64 KiB keeps prefix + `\n[truncated]` |
| `write_file` | `path`, `content` | — | write / true | `wrote <n> bytes` (no path) |
| `list_dir` | `path` | `depth` 1–2; omitted ≡ 1 | read / false | relative paths, one per line; 256-entry cap |
| `exec` | `argv` (min 1, no shell) | `cwd`; omitted = workspace root | exec / true | `exit <code>\n` then combined output ≤ 64 KiB |

`list_dir.depth` 0 or 3, a fractional depth, or a string depth is
`invalid_args` (`TestValidateArgsDefaultWorkspaceSpecs`). `exec` rejects a
model `timeout` field and a shell `command` string. Write `content`
`maxLength` is 32 KiB (`invalid_args` if `ValidateArgs` runs). A 32 KiB
`content` plus JSON wrapping already exceeds the Engine 32 KiB argument
cap, so that schema cell is unreachable through `TurnRunner`. Catalog
uniqueness and unsupported schema keywords
fail at `NewCatalog` (`TestNewCatalogRejectsDuplicateNames`,
`TestNewCatalogRejectsUnsupportedSchemaKeywords`). Allowed keywords are
the closed set in `tools/schema.go`: `type`, `properties`, `required`,
`additionalProperties`, `enum`, `minLength`, `maxLength`, `minimum`,
`maximum`, `minItems`, `maxItems`, `items`.

Lexical scope (`tools.CheckScopeLexical`) is no-I/O. Leftover `..`, NUL,
invalid UTF-8, foreign Windows volumes, and absolute prefix mismatch deny
(`TestCheckScopeLexicalDeniesEscapesWithoutIO`). A lexical deny never
calls `Resolve` and never records `policy.decision.recorded`
(`TestToolDenialsContinueTurnWithFrozenText` / `lexical escape`).

## Pipeline

Charter order, after `tool.call.started` is committed
(`internal/harness/application/pipeline.go`):

```text
schema validation
  → lexical scope check          # no I/O
  → Resolve probe                # I/O; not execute
  → policy.Decide(WorkspaceIn)
  → optional approval
  → Read / Write / List / Run    # only after allow | granted
```

`Resolve` is a scope probe. It must not create, truncate, or write
(`TestResolveDoesNotCreateOrTruncate`). Symlink / realpath escape: Resolve
runs, `Decide(WorkspaceIn=false)` is recorded, `Read`/`Write`/`Run` do not
(`TestResolveEscapeDecidesThenScopeDenied`,
`TestResolveSymlinkEscapeDoesNotReadOrWrite`). An `exec` argv0 that
contains `/` or `\` is also scoped; a symlink out never runs
(`TestExecArgvSymlinkOutNeverRuns`).

Policy / approval denials are tool-level failures. The Turn continues and
the next model sees the frozen tool text
(`TestToolDenialsContinueTurnWithFrozenText`,
`TestApprovalTimeoutContinuesTurn`):

| Situation | Execute? | Tool code | Model-visible `Text` |
| --- | --- | --- | --- |
| `allow` | yes | (success) | normalized output |
| `deny` | no | `policy_denied` | `policy denied this tool` |
| `require_approval` + grant | yes | (success) | normalized output |
| `require_approval` + deny | no | `approval_denied` | `approval denied this tool` |
| `require_approval` + timeout | no | `approval_timeout` | `approval timed out` |
| no Approver wired | no | `approval_denied` | `approval denied this tool` |
| unknown tool name | no | `unknown_tool` | `unknown tool` |
| schema invalid | no | `invalid_args` | `invalid tool arguments` |
| lexical out of workspace | no | `scope_denied` | `path is outside the workspace` |
| Resolve escape | no | `scope_denied` | `path is outside the workspace` |
| exec wall timeout | no | `exec_timeout` | `command timed out` |

Tests compare those exact UTF-8 strings. A mid-run `exec` size kill is
not `output_limit`. `localexec` returns `Truncated=true`; the pipeline
completes the tool with prefix + `\n[truncated]`
(`TestOutputCapKillsAndTruncates`). `CodeToolOutputLimit` /
`ToolTextOutputLimit` are unused. The truncation marker is the exact
string `\n[truncated]`. Denial sentences never include path, args, or
env.

## Mid-loop `step_append_*` and ResolveAppend (table A2)

`execution_registry` adds `step_append_in_flight` and
`step_append_unknown`. `retainUnknown` accepts that unknown phase.
`resumeAfterResolvedStepAppend` copies the admission resume contract
(`retained=false`, `ownerActive=true`) and returns to `running`. There is
no `step_append_unknown → terminal_append_in_flight`.

`validExecutionTransition` allows:

| From | To |
| --- | --- |
| `running` | `step_append_in_flight`, `terminal_append_in_flight`, `cancel_won` |
| `step_append_in_flight` | `running`, `step_append_unknown`, `cancel_won` |
| `step_append_unknown` | `running` (resume only), `step_append_in_flight` (exact retry of the retained intent), `cancel_won` |

Every mid-loop batch uses `ResolveAppend` on the retained exact intent
(existing `AppendResolutionTimeout` / `AppendResolutionMaxOperations`).
Pinned interlocks (`TestTableA2UnknownStartedDoesNotExecute`,
`TestTableA2UnknownToolTerminalDoesNotReexecute`,
`TestTableA2UnknownStepStartDoesNotStream`):

1. Never `Read` / `Write` / `List` / `Run` unless `tool.call.started` for
   that `ItemID` is a committed fact.
2. Never re-invoke execute after a committed or unknown tool terminal.
3. Never `Stream` while any append of this Request ID is unresolved, or
   after `append_outcome_unknown`.
4. Budget exhausted ⇒ `append_outcome_unknown`, zero `Stream`, zero
   execute.

EventStore v2 methods are unchanged: `ReadStream`, `Append`,
`ResolveAppend`, `FindCommandRequest`. Tools are not a second admission.
The whole Turn shares one `CommandID`. Each assistant Step and each tool
call allocates a new `ItemID`. Reconstruction `ItemID` stays the
admission assistant Item.

## Frozen envelopes, unique call ids, and bounds

Stream grammar is `text_delta* tool_call* completed`. The `completed`
event has empty `Text` and nil `ToolCall`. `RunResult` may carry both
concatenated text and `ToolCalls`. Uniqueness is on **call ids**, not
names. Two `read_file` calls with distinct ids are legal; a duplicate id
is `invalid_stream`; a text delta after a tool call is `invalid_stream`
(`TestTurnRunnerAcceptsTextThenOneToolCall`,
`TestTurnRunnerAcceptsTwoReadFileCallsWithDistinctIDs`,
`TestTurnRunnerRejectsDuplicateToolCallIDs`,
`TestTurnRunnerRejectsInterleavedDeltaAfterToolCall`). Fail and cancel
clear `ToolCalls` and `FinishReason` to `""`
(`TestTurnRunnerClearsToolCallsAndFinishReasonOnFailAndCancel`). Success
may copy `FinishReason=tool_calls`.

Step 1 logs `[{user, Input}]` (plus tools when the catalog is on).
Step k≥2 logs only the suffix: that Step’s assistant message and its tool
results (`decideStartAssistantStep` uses `projection.Suffix()`). The live
owner rebuilds Stream `Messages` from committed events of this CommandID.
A last `model.request.recorded` at the logged-envelope budget persists;
the event payload stays under 8 MiB
(`TestLoggedEnvelopePersistsAtBudget`). Application checks the serialized
projection **before** `Stream` and fails `envelope_limit` at 4 MiB. A
just-under-4-MiB projection is accepted when `MaxRequestBytes ≥ 5 MiB`
(`TestStreamJustUnder4MiBProjectionAccepted`).

Other pinned bounds:

| Bound | Limit | On exceed |
| ---: | ---: | --- |
| Steps per Turn | 8 | `step_limit`; no further Stream |
| Tool calls per Step | 8 | `n > 8` ⇒ `invalid_stream`; zero started |
| Tool argument JSON | 32 KiB UTF-8 | `invalid_stream`; zero `tool.call.started`; Turn fails (`TestTurnRunnerRejectsInvalidEventsAndBoundsBeforeDelivery`) |
| Tool result / `read_file` / `exec` output | 64 KiB | completed tool; prefix + `\n[truncated]` (`truncated=true`) |
| `write_file` content | 32 KiB schema `maxLength` | `invalid_args` if `ValidateArgs` runs; unreachable via Engine once the JSON wrapper exceeds 32 KiB |
| `list_dir` entries | 256 | `truncated=true` + first 256 lexical paths |
| `exec` wall time | 30 s (`DefaultExecTimeout`; Application always passes this) | `exec_timeout` |
| Approval wait | 30 s default | `approval_timeout` |
| Assistant UTF-8 | 1 MiB | existing `output_limit` |
| Stream projection | 4 MiB | `envelope_limit`; zero Stream |
| Tool-enabled HTTP body | ≥ 5 MiB | adapter `provider_permanent` if over |

Turn-failure display sentences include `turn exceeded the step limit` and
`request envelope exceeded the size limit`.

## InterruptToolTurn / FailToolTurn

`decideInterruptTurn` / `decideFailTurn` / `decideCompleteTurn` still
reject a running Item (`TestDecideToolTurnCompositesAndBareTurnRejection`,
`TestTurnTerminalRejectsRunningItem`). Application must not
`Decide(InterruptTurn)` while `ActiveItem != nil`.

```go
type InterruptToolTurn struct {
    SessionID, TurnID, ItemID  domain IDs
    Code, Message              string
    ApprovalID                 domain.ApprovalID // zero ⇒ no approval.resolved
}
type FailToolTurn struct {
    SessionID, TurnID, ItemID  domain IDs
    Code, Message              string
}
```

`InterruptToolTurn` emits, in order: optional
`approval.resolved{decision=canceled}` (only if `ApprovalID` is set and
`approval.requested` already applied) → `tool.call.interrupted` →
`turn.interrupted`. `FailToolTurn` emits `tool.call.failed` →
`turn.failed`. Cancel during execute produces one legal Domain batch from
`InterruptToolTurn` (`TestCancelDuringExecuteUsesInterruptToolTurn`).
Step-2 stream cancel interrupts the live assistant Item
(`TestStepTwoStreamCancelInterruptsLiveAssistant`). Step-2
`invalid_stream` fails that assistant Item
(`TestStepTwoInvalidStreamFailsLiveAssistant`).

`CompleteAssistantMessage` is item-only and, when `ToolCalls != nil`,
leaves the Turn running
(`TestDecideCompleteAssistantMessageReturnsItemOnly`,
`TestApplyAssistantMessageCompletedWithToolCallsLeavesTurnRunning`).
Policy and approval events are version-only on the running tool Item
(`TestDecidePolicyAndApprovalAreVersionOnly`). Compact `Session` stays
transcript-free. A crash-left running tool Item makes a **different**
Request ID fail with `item_already_running` / `turn_already_running`
(`TestApplyCompactToolItemAndEligibility`,
`TestCheckStartAssistantTurnEligibilityRejectsRunningToolItem`).

## Reconstruction

`ReconstructRequestResult` walks the CommandID subsequence with an
Apply-equivalent machine: `admit_turn` → `open_assistant` →
`idle_in_turn` → `open_tool` → `terminal`. Illegal order is
`store_corrupt`. The old 2/3/4/5/6-event shapes remain legal
(`TestReconstructRequestResultAcceptsExactRequestShapes`). Additional
accepted shapes:

- item-only `assistant.message.completed` with no turn terminal is still
  **running** (`TestReconstructRequestResultAcceptsItemOnlyCompleteAsRunning`);
- two-Step success (`TestReconstructRequestResultAcceptsTwoStepSuccess`);
- `InterruptToolTurn` batch
  (`TestReconstructRequestResultAcceptsInterruptToolTurn`);
- `step_limit` from idle after tools
  (`TestReconstructRequestResultAcceptsStepLimitFromIdleAfterTools`).

`tool.call.started` without a prior item-only complete is corrupt
(`TestReconstructRequestResultRejectsToolStartWithoutItemOnlyComplete`).
`Text` is the last empty-`ToolCalls` assistant complete. Admission
`ItemID` is the stable `RunTurnResult.ItemID`.

## workspacefs and localexec

`adapters/workspacefs` implements `tools.FileSystem` with realpath +
prefix jail. Tests use `t.TempDir()` only. `Resolve` is a probe.
`Read`/`Write` re-check the jail (`TestReadWriteJail`). `List` honors
`depth` 1 vs 2 and the 256-entry cap (`TestListDepthAndCap`) and skips
directory symlink children that leave the workspace
(`TestListSkipsOutOfWorkspaceDirSymlinkChildren`). A file root is
rejected (`TestNewRejectsFileRoot`). A foreign workspace argument is
rejected (`TestResolveRejectsForeignWorkspace`).

`adapters/localexec` implements `tools.CommandRunner`. `Runner.Enforcement()`
reports, per effect, how completely commands are confined —
`Filesystem`/`Network`/`Memory`, each `"full"`, `"partial"`, or `"none"` — a
fact computed from what is actually active, never an assumed promise
(`TestEnforcementReportsNoneWithoutAPlatformBackend` pins the honest
all-`"none"` baseline when no backend is usable). See
[exec sandboxing and resource quotas](../superpowers/specs/2026-08-30-exec-sandboxing-resource-quotas-design.md)
for the accepted design and its
[completion evidence](exec-sandboxing-resource-quotas-evidence.md).

On Linux, `Runner` probes `bwrap` at construction
(`TestIsWSL1Version` distinguishes WSL1, which is also treated as
unavailable) and, when the probe succeeds, wraps every `Run` call in a
bwrap sandbox: unshared user/pid/ipc/uts/cgroup/net namespaces, every
capability dropped, a read-only host view with only the workspace root
rebound read-write (`TestBwrapArgvWrapsTargetWithRequiredNamespaceIsolation`,
`TestRunWrapsArgvInBwrapWhenAvailable`), reporting `Filesystem`/`Network`
as `"full"`. The same probe also gates a cgroup v2 memory quota — one
child cgroup for the `Runner`'s lifetime with `memory.high`/`memory.max`,
monitored via inotify on `memory.events`, killing the process group and
reporting `CommandResult.ResourceLimited` (instead of `TimedOut`,
classified as `CodeResourceLimit`/`ToolTextResourceLimit`) when usage
stays above 90% of `memory.high` after a breach
(`TestRunKillsOnResourceLimitSignal`,
`TestExecResourceLimitedFailsToolWithFrozenText`), reporting `Memory` as
`"full"` when active, `"none"` when the memory controller isn't delegated
to this cgroup.

On Darwin, `Runner` probes hardcoded `/usr/bin/sandbox-exec` (never
PATH-resolved) and, when available, wraps every `Run` call in a Seatbelt
`.sbpl` profile — a Chrome/Codex-derived base policy plus a
deny-writes-except-workspace-root / allow-reads / deny-network layer
(`TestSeatbeltArgvBindsWorkspaceRootAndAppendsTarget`,
`TestRunWrapsArgvInSeatbeltWhenAvailable`) — reporting `Filesystem`/
`Network` as `"full"`. Independent of Seatbelt's own availability, every
command also gets a best-effort `RLIMIT_AS` bound, reported as `Memory =
"partial"` (`TestRlimitEnforcementLevelIsPartialOnDarwin`): this bounds
virtual address space, not resident memory, and a breach surfaces as the
child's own allocator hitting `ENOMEM`, never `ResourceLimited`.

`localexec.Availability()` reports whether the current platform's backend
is usable at all; Windows and any platform with neither backend always
report unavailable. `composition.Open` checks it right after the existing
credential check, before any resource construction: unavailable with
`Config.AllowUnsandboxedExec` false fails `Open` closed with a named
error; unavailable with the flag true logs exactly which guarantee is
absent and proceeds
(`TestOpenFailsClosedWhenSandboxUnavailableAndFlagUnset`,
`TestOpenProceedsAndLogsWhenFlagSetAndSandboxUnavailable`,
`TestOpenSucceedsWithDefaultFlagWhenSandboxIsAvailable`).

The child environment is empty except host `PATH`, `HOME` = workspace
root, and `TMPDIR` = a workspace subdirectory removed after exit
(`TestScrubbedEnv`). Commands are argv-only; no shell expansion
(`TestArgvOnlyNoShellExpansion`). Timeout and cancel kill the process
group (`TestTimeoutKillsProcessGroup`, `TestCancelKillsProcessGroup`).
Combined stdout+stderr default cap is 64 KiB
(`TestOutputCapKillsAndTruncates`). Cwd and argv0 must stay in the
workspace (`TestCwdAndArgvMustStayInWorkspace`).

Shared port suites live under `tools/porttest` and `testkit` (`MemFS`,
`ScriptedRunner`, `ScriptedApprover`).

## openaicompat assembler

When `NativeTools` is `supported` or `required`, the adapter sends
`tools` (`type=function`) and multi-role `messages`, and assembles
vendor `delta.tool_calls` by `index`. Engine emission is
`text_delta*` then complete `tool_call*` then `completed`, with
`FinishReason=tool_calls` (`TestStreamAssemblesSupportedToolCalls`,
`TestStreamSendsToolsAndMessages`). `required` plus empty
`ModelRequest.Tools` fail-closes before HTTP
(`TestStreamRequiredEmptyToolsFailClosed`). `unsupported` still maps
vendor `tool_calls` to `capability_mismatch` with empty `FinishReason`
(`TestRunTurnHTTPProfileTextOnlyToolCallsLeaveFinishReasonEmpty`).
Trailing partial, missing id+name, non-UTF-8, arguments over 32 KiB,
gapped or 1-based index, and conflicting or duplicate id are
`invalid_stream` (`TestStreamTrailingPartialCallInvalid`,
`TestStreamAssembledCallRejectsBounds`). `finish_reason=stop` plus
assembled tools is `invalid_stream`
(`TestStreamStopWithAssembledToolsInvalid`).

HTTP `RunTurn` e2e: tools-supported `read_file` then complete records
first usage `finishReason=tool_calls`; the second Stream sees assistant
`tool_calls` plus a tool message and text `Hello world`
(`TestRunTurnHTTPReadFileThenCompletes`). See the
[implemented provider-adapter contract](provider-adapter.md) for wire
mapping, classification, and keyless fixtures.

## Keyless default tests

Default `go test` uses `t.TempDir()`, `testkit.MemFS` /
`ScriptedRunner` / `ScriptedApprover` / `ScriptedModel`, and a scripted
`http.RoundTripper`. There is no live workspace, no live key, and no
`//go:build liveprovider` suite in this milestone.

## Formal adapters and executable evidence

- `openaicompat.Model` remains the production `engine.Model`.
- `workspacefs.FileSystem` and `localexec.Runner` are the production
  tool adapters.
- `testkit.ScriptedModel`, `MemFS`, `ScriptedRunner`, and
  `ScriptedApprover` remain the formal scripted adapters on the same
  ports.
- `MemoryEventStore` remains the EventStore v2 reference.
- Reusable suites `eventstoretest.Run`, `modeltest.Run`, and
  `enginescenariotest.Run` still pass on the scripted path.

Run the local evidence matrix from the repository root:

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test ./... -count=1
go test -race ./... -count=1
```

Focused packages that enforce this contract:

```bash
go test ./internal/harness/domain ./internal/harness/engine \
  ./internal/harness/policy ./internal/harness/tools \
  ./internal/harness/application \
  ./internal/harness/adapters/workspacefs \
  ./internal/harness/adapters/localexec \
  ./internal/harness/adapters/openaicompat \
  ./internal/harness/architecture -count=1
```

## Explicit exclusions

This implemented contract does not provide:

- an MCP client, MCP transport, or remote tool host (catalog `source=mcp`
  is a type hole only);
- Windows OS confinement (no bwrap/Seatbelt-equivalent backend exists
  there in this slice; `composition.Open` fails closed by default, with
  `AllowUnsandboxedExec` as the named escape hatch) or Linux Landlock
  (rejected: requires CGO for correctness on pre-ABI-V8 kernels). Linux
  bwrap and macOS Seatbelt confinement, and a Linux cgroup v2 memory
  quota, are implemented — see [exec sandboxing and resource
  quotas](../superpowers/specs/2026-08-30-exec-sandboxing-resource-quotas-design.md);
- ACP approval UI or TUI. Approval is an injected `Approver` port; the
  default denies;
- parallel tool batches, background exec, PTY, LSP, web fetch,
  apply-patch, grep/search as first-class tools, todo lists, Skills, or
  subagents;
- Runtime Host, process-crash continuation of a running Step loop,
  SQLite, or JSONL replica;
- a plugin kernel, Cordis, `next()` policy waterfalls, hook buses, or
  dynamic loading;
- Application retry of a model attempt. Provider `Retryable` remains
  advisory;
- expanding compact `Session` into a transcript or a Context Engine;
- folding tools, policy mode, or identity into `DigestRunTurnRequestV1`;
- EventStore v2 interface changes or a fifth Store method;
- vendor SDKs, OAuth, or model discovery;
- live-network or live-key CI.

These exclusions preserve dependency order. They do not weaken the
verified Step-loop path, and they prevent this milestone from being
presented as a GA harness.
