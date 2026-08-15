# 已实现 Tool Runtime 合同

- 状态：已实现内部合同
- 稳定级别：v1.0 之前为 `experimental`
- 成熟度：pre-v0，尚非通用可用（GA）发布
- 范围：Application 拥有的 Step 循环、纯 Policy Decide 表、四个内置工作区
  工具（消费方拥有的端口）。不是插件内核、MCP 客户端、OS 沙箱、ACP 审批 UI
  或 Runtime Host。
- 英文规范设计：[Tool Runtime、Policy 与最小工作区工具](../superpowers/specs/2026-08-16-tool-runtime-policy-design.md)
- 完成证据：[Tool Runtime 证据台账](tool-runtime-evidence.md#中文证据台账)
- 英文已实现合同：[Implemented Tool Runtime Contract](tool-runtime.md)

本文是英文已实现合同的中文语义阅读版，记录当前代码和测试已经强制执行的行为。它是
内部 Go 合同，不是稳定公共协议；pre-v0 阶段若修改合同，设计、实现、测试和双语文档
必须同步变更。若本文与英文已实现合同冲突，以英文已实现合同为准。

## 已交付能力

Application 拥有 Step 循环。一次已准入的 `RunTurn` 可以跑
`model → tool* → model`，直到模型不再返回工具或触达上限。
`engine.TurnRunner` 仍是一次 `Stream` / 一次尝试。Domain 保持纯函数。
Policy 是 `policy.Engine.Decide(Input) Decision`——一张表，不是 `next()`
瀑布，也不写在工具体里。`ModeAllowWrites` 随 `policy` 包交付。
`NewService` / `DefaultConfig` 默认仍是 `ModeDefault`。

四个内置工具：`read_file`、`write_file`、`list_dir`、`exec`。
`list_dir` 的 `depth` 省略 ≡ 1，最大 2，整次 256 条。管线是
`tool.call.started` → 校验 → 词法 → `Resolve` → `Decide` → 审批 → 执行。
循环中途 append 使用 `step_append_*` 加 `ResolveAppend`。模型可见的工具
文案是冻结短句。工具 Item 仍在跑时取消或 Turn 失败走 `InterruptToolTurn` /
`FailToolTurn`。

`workspacefs` 是 realpath 前缀监狱。`localexec` 报告
`enforcement=partial`。`openaicompat` 在 `NativeTools` 为 `supported` 或
`required` 时发送 `tools` 并组装 `tool_call*`。EventStore v2 仍是四个
方法。调用唯一性在 call id。Step k≥2 记录后缀信封。流投影帽 4 MiB；带
工具的 HTTP `MaxRequestBytes` 至少 5 MiB。

未实现 MCP 客户端、OS Seatbelt/bwrap/Landlock、ACP 审批 UI、并行工具、
Runtime Host、插件内核和厂商 SDK。

## 包权威与依赖方向

```text
headless caller / composition（当前为测试）
                    |
                    v
internal/harness/application  -----> internal/harness/engine
  命令与 Step 循环                    Model 端口、TurnRunner、
                                     tool_call 流事件
                    |
        +-----------+-----------+
        v                       v
internal/harness/policy    internal/harness/tools
  只做 Decide()              ToolSpec 目录、schema 子集、
                             词法 scope、端口
        |
        v
internal/harness/domain
  生命周期 + log-only 事实

internal/harness/adapters/workspacefs  ----实现----> tools.FileSystem
internal/harness/adapters/localexec    ----实现----> tools.CommandRunner
internal/harness/adapters/openaicompat ----实现----> engine.Model
internal/harness/testkit               ----实现----> 全部端口（scripted）
internal/harness/adapters/memory       ----实现----> application.EventStore
```

[`dependencies_test.go`](../../internal/harness/architecture/dependencies_test.go)
用 `TestProductionDependencyBoundaries`、`TestForbiddenImport`、
`TestClassifyProductionDirectory`、`TestOsExecOnlyInLocalExec`、
`TestAllowAllProductionException` 强制这些方向：

- `domain` 与 `engine` 仍不能导入 `os`、`os/exec`、`net`、`net/http`，也不能
  导入路径段 `provider` / `providers`；
- `application` 可以导入 `policy` 和 `tools`，不能导入 `adapters/*`、
  `testkit`、`os`、`os/exec` 或 `net/http`；
- `policy` 可以导入 `domain` 和 stdlib 字符串/JSON 包，不能导入
  `application`、`engine`、`tools`、`adapters`、`os`、`os/exec`、`net`、
  `net/http`；
- `tools` 可以导入 `domain` 和词法 `path/filepath`，不能导入 `policy`、
  `application`、`engine`、`adapters`、`os`、`os/exec`、`net`、`net/http`；
- `workspacefs` 可以导入 `tools`、`domain`、`os`、`path/filepath`，不能导入
  `application`、`testkit`、`os/exec`、`net`、`net/http` 或其他
  `adapters/*`；
- `localexec` 是唯一可以导入 `os/exec` 的生产 owner，不能导入
  `application`、`testkit`、`net`、`net/http` 或其他 `adapters/*`；
- `openaicompat` 仍不能导入 `os/exec` 或 `tools`，它在 `ModelRequest` 上接收
  `[]domain.ToolSchema`；
- `policy.AllowAll` 是测试构造器，非测试生产文件不得调用；
- `go.mod` 保持模块 stdlib-only，没有厂商 SDK。

## Application 拥有的 Step 循环

空目录保持原先的一次尝试路径：`Messages==nil`、`Tools==nil`、一次 `Run`、
`CompleteAssistantTurn`（`TestEmptyCatalogKeepsNilMessagesAndTools`）。非空
目录要求 `RequestIdentity != nil` 且 `NativeTools ∈ {supported, required}`。
`required` 加空目录、`unsupported` 加目录、或目录缺少 `FileSystem` /
`CommandRunner`，都是 `CategoryPolicy` / `invalid_configuration`
（`TestNewServiceToolComposition`）。Engine 不看 profile，只把 `Messages`
和 `Tools` 当数据转发
（`TestTurnRunnerForwardsMessagesAndToolsWithoutConsultingProfile`）。

`DefaultConfig` 钉住 `PolicyMode=ModeDefault`、`MaxSteps=8`、
`MaxToolCallsPerStep=8`。`ModeAllowWrites` 是合法的 `NewService` 模式。
未知模式 `"yolo"` 是 `CategoryValidation` / `invalid_configuration`。未设置
的 `Approver` 变成 `tools.DenyApprover`。

目录启用时，`runStepLoop`（`internal/harness/application/loop.go`）：

1. 序列化后的 messages+tools 投影超过 4 MiB 则 `envelope_limit`，**零**
   次额外 `Stream`（`TestEnvelopeLimitFailsWithoutStream`）；
2. 本 Step 调用一次 `TurnRunner.Run`；
3. `len(tool_calls)==0` → `CompleteAssistantTurn`（Turn 结束）；
4. `1 ≤ n ≤ 8` → `CompleteAssistantMessage`（只结束 Item；Turn 保持
   running），然后按顺序跑管线；
5. `n > 8` → `invalid_stream`；**零** 次 `tool.call.started`；不执行前缀
   （`TestOversizedToolBatchFailsWithoutStarting`）；
6. 工具终态已提交后，若 `step == MaxSteps` 且模型仍返回工具 →
   `step_limit`（`TestMaxStepsFailsAfterDurableTools`）；
7. 否则为 Step k≥2 提交 `assistant.message.started` +
   `model.request.recorded`（后缀信封），再 `Stream` 一次
   （`TestTwoStepReadFileSuccess`）。

一个 Request ID 仍只准入一次。第二次调用只重建，永不打模型
（`TestRunTurnHTTPFindCommandRequestPreventsSecondStream`）。只有活所有者
在上一 Step 的每个工具都有持久终态后才能再 `Stream`。循环中途崩溃仍是
`reconciliation_required`。`DigestRunTurnRequestV1` 仍是 Session ID +
精确 UTF-8 Input。

两步 `read_file` 成功路径读取 fixture，把冻结的文件正文作为工具消息送进
第二次 Stream，然后完成（`TestTwoStepReadFileSuccess`、
`TestRunTurnHTTPReadFileThenCompletes`）。

## Policy Decide 表

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

`New` 接受 `ModeDefault`、`ModeReadOnly`、`ModeAllowWrites`、
`ModeDenyAll`（`TestNewAcceptsShippedModes`）。拒绝 `""`、`allow_all`、
`bypass`、`yolo` 和未知 token（`TestNewRejectsUnknownMode`）。
`PathLiteral` 只用于审计；Decide 不把它当路径解释
（`TestDecideDoesNotInterpretPathLiteral`）。

工作区内表格（`TestDecideDefaultTable`、`TestDecideAllowWritesEveryCell`）：

| 模式 | RiskRead | RiskWrite | RiskExec | RiskNetwork / Network |
| --- | --- | --- | --- | --- |
| `default` | allow | require_approval | require_approval | deny |
| `read_only` | allow | deny | deny | deny |
| `allow_writes` | allow | allow | require_approval | deny |
| `deny_all` | deny | deny | deny | deny |

工作区外（`WorkspaceIn=false`）在所有已交付模式都是 deny。空名或空白名
deny。未知 risk deny。网络 risk 或 `Network=true` 即使在
`ModeAllowWrites` 下也 deny。`AllowAll()` 无条件允许
（`TestAllowAllIsUnconditional`），测试之外禁用。

`ModeAllowWrites` 让 `write_file` 无需 Approver 即可执行
（`TestModeAllowWritesExecutesWrite`）。该模式下 `exec` 仍要审批。生产
组合仍是 `ModeDefault`。

## 四个内置工具

`tools.DefaultWorkspaceSpecs` 是四个名字唯一的 `domain.ToolSpec`
（`TestDefaultWorkspaceSpecsLockedContracts`）。已交付的 Source 是
`builtin`；`mcp` 在目录类型上合法，以便以后的适配器投影进来。没有 MCP
客户端。

| 工具 | 必填 | 可选 | Risk / Mutates | 成功时模型可见 |
| --- | --- | --- | --- | --- |
| `read_file` | `path` | — | read / false | UTF-8 文件；超过 64 KiB 保留前缀 + `\n[truncated]` |
| `write_file` | `path`、`content` | — | write / true | `wrote <n> bytes`（不含路径） |
| `list_dir` | `path` | `depth` 1–2；省略 ≡ 1 | read / false | 相对路径一行一个；256 条上限 |
| `exec` | `argv`（至少 1 项，无 shell） | `cwd`；省略 = 工作区根 | exec / true | 先 `exit <code>\n` 再合计 ≤ 64 KiB 的输出 |

`list_dir.depth` 为 0 或 3、小数或字符串都是 `invalid_args`
（`TestValidateArgsDefaultWorkspaceSpecs`）。`exec` 拒绝模型 `timeout`
字段和 shell `command` 字符串。写入 `content` 的 `maxLength` 是 32 KiB
（若跑到 `ValidateArgs` 则是 `invalid_args`）。32 KiB 的 `content` 加上
JSON 包装已经超过 Engine 的 32 KiB 参数帽，所以该 schema 格经
`TurnRunner` 到不了。目录重名和不受支持的 schema 关键字在 `NewCatalog`
时失败
（`TestNewCatalogRejectsDuplicateNames`、
`TestNewCatalogRejectsUnsupportedSchemaKeywords`）。允许的关键字是
`tools/schema.go` 里的封闭集合：`type`、`properties`、`required`、
`additionalProperties`、`enum`、`minLength`、`maxLength`、`minimum`、
`maximum`、`minItems`、`maxItems`、`items`。

词法 scope（`tools.CheckScopeLexical`）无 I/O。残留 `..`、NUL、非法
UTF-8、外盘 Windows volume、绝对路径前缀不匹配都会拒绝
（`TestCheckScopeLexicalDeniesEscapesWithoutIO`）。词法拒绝永不调用
`Resolve`，也不记录 `policy.decision.recorded`
（`TestToolDenialsContinueTurnWithFrozenText` / `lexical escape`）。

## 管线

章程顺序：在已提交 `tool.call.started` 之后
（`internal/harness/application/pipeline.go`）：

```text
schema 校验
  → 词法 scope（无 I/O）
  → Resolve 探针（有 I/O；不是执行）
  → policy.Decide(WorkspaceIn)
  → 可选审批
  → 只有 allow | granted 之后才 Read / Write / List / Run
```

`Resolve` 是 scope 探针，不得创建、截断或写入
（`TestResolveDoesNotCreateOrTruncate`）。符号链接 / realpath 逃逸：会
Resolve，会记录 `Decide(WorkspaceIn=false)`，不会 `Read`/`Write`/`Run`
（`TestResolveEscapeDecidesThenScopeDenied`、
`TestResolveSymlinkEscapeDoesNotReadOrWrite`）。含 `/` 或 `\` 的 `exec`
argv0 同样做 scope；指向工作区外的符号链接永不执行
（`TestExecArgvSymlinkOutNeverRuns`）。

策略 / 审批拒绝是工具级失败。Turn 继续，下一轮模型看到冻结短句
（`TestToolDenialsContinueTurnWithFrozenText`、
`TestApprovalTimeoutContinuesTurn`）：

| 情况 | 执行？ | 工具 code | 模型可见 `Text` |
| --- | --- | --- | --- |
| `allow` | 是 | （成功） | 规范化输出 |
| `deny` | 否 | `policy_denied` | `policy denied this tool` |
| `require_approval` + 批准 | 是 | （成功） | 规范化输出 |
| `require_approval` + 拒绝 | 否 | `approval_denied` | `approval denied this tool` |
| `require_approval` + 超时 | 否 | `approval_timeout` | `approval timed out` |
| 未接入 Approver | 否 | `approval_denied` | `approval denied this tool` |
| 未知工具名 | 否 | `unknown_tool` | `unknown tool` |
| schema 非法 | 否 | `invalid_args` | `invalid tool arguments` |
| 词法越权 | 否 | `scope_denied` | `path is outside the workspace` |
| Resolve 逃逸 | 否 | `scope_denied` | `path is outside the workspace` |
| exec 墙钟超时 | 否 | `exec_timeout` | `command timed out` |

测试比较这些精确 UTF-8 字符串。中途因体积杀掉的 `exec` 不是
`output_limit`。`localexec` 返回 `Truncated=true`；管线把该工具记为成功，
并带上前缀 + `\n[truncated]`（`TestOutputCapKillsAndTruncates`）。
`CodeToolOutputLimit` / `ToolTextOutputLimit` 未使用。截断标记是精确字符串
`\n[truncated]`。拒绝短句不含路径、参数或环境。

## 循环中途 `step_append_*` 与 ResolveAppend（表 A2）

`execution_registry` 增加 `step_append_in_flight` 和
`step_append_unknown`。`retainUnknown` 接受该未知相位。
`resumeAfterResolvedStepAppend` 复制准入 resume 合同（`retained=false`、
`ownerActive=true`）并回到 `running`。没有
`step_append_unknown → terminal_append_in_flight`。

`validExecutionTransition` 允许：

| 从 | 到 |
| --- | --- |
| `running` | `step_append_in_flight`、`terminal_append_in_flight`、`cancel_won` |
| `step_append_in_flight` | `running`、`step_append_unknown`、`cancel_won` |
| `step_append_unknown` | `running`（仅 resume）、`step_append_in_flight`（仅对保留 intent 精确重试）、`cancel_won` |

每个中途批次都对保留的 exact intent 做 `ResolveAppend`（沿用现有
`AppendResolutionTimeout` / `AppendResolutionMaxOperations`）。钉住的互锁
（`TestTableA2UnknownStartedDoesNotExecute`、
`TestTableA2UnknownToolTerminalDoesNotReexecute`、
`TestTableA2UnknownStepStartDoesNotStream`）：

1. 除非该 `ItemID` 的 `tool.call.started` 已是提交事实，否则永不
   `Read` / `Write` / `List` / `Run`；
2. 工具终态已提交或未知后，永不重做执行；
3. 本 Request ID 有未解析 append，或已是 `append_outcome_unknown` 时，永不
   `Stream`；
4. 预算耗尽 ⇒ `append_outcome_unknown`，零 `Stream`、零 execute。

EventStore v2 方法不变：`ReadStream`、`Append`、`ResolveAppend`、
`FindCommandRequest`。工具不是第二次准入。整个 Turn 共用一个
`CommandID`。每个 assistant Step 和每次工具调用分配新 `ItemID`。重建的
`ItemID` 仍是准入时的助手 Item。

## 冻结信封、唯一 call id 与边界

流语法是 `text_delta* tool_call* completed`。`completed` 事件的 `Text`
为空，`ToolCall` 为 nil。`RunResult` 可以同时带拼接文本和 `ToolCalls`。
唯一性在 **call id**，不在名字。两次 id 不同的 `read_file` 合法；重复 id
是 `invalid_stream`；工具之后再出 text delta 是 `invalid_stream`
（`TestTurnRunnerAcceptsTextThenOneToolCall`、
`TestTurnRunnerAcceptsTwoReadFileCallsWithDistinctIDs`、
`TestTurnRunnerRejectsDuplicateToolCallIDs`、
`TestTurnRunnerRejectsInterleavedDeltaAfterToolCall`）。失败和取消把
`ToolCalls` 和 `FinishReason` 清成 `""`
（`TestTurnRunnerClearsToolCallsAndFinishReasonOnFailAndCancel`）。成功
可拷贝 `FinishReason=tool_calls`。

Step 1 记录 `[{user, Input}]`（目录开启时加上 tools）。Step k≥2 只记录
后缀：该 Step 的助手消息及其工具结果（`decideStartAssistantStep` 使用
`projection.Suffix()`）。活所有者从本 CommandID 已提交事件重建 Stream
`Messages`。最后一条达到 logged-envelope 预算的 `model.request.recorded`
可以持久化；事件载荷仍低于 8 MiB
（`TestLoggedEnvelopePersistsAtBudget`）。Application 在 `Stream` **之前**
检查序列化投影，到 4 MiB 就 `envelope_limit`。刚好低于 4 MiB 的投影在
`MaxRequestBytes ≥ 5 MiB` 时被接受
（`TestStreamJustUnder4MiBProjectionAccepted`）。

其他钉住的边界：

| 边界 | 上限 | 超限 |
| ---: | ---: | --- |
| 每 Turn 的 Step | 8 | `step_limit`；不再 Stream |
| 每 Step 的工具调用 | 8 | `n > 8` ⇒ `invalid_stream`；零 started |
| 工具参数 JSON | 32 KiB UTF-8 | `invalid_stream`；零 `tool.call.started`；Turn 失败（`TestTurnRunnerRejectsInvalidEventsAndBoundsBeforeDelivery`） |
| 工具结果 / `read_file` / `exec` 输出 | 64 KiB | 工具成功；前缀 + `\n[truncated]`（`truncated=true`） |
| `write_file` content | 32 KiB schema `maxLength` | 若跑到 `ValidateArgs` 则是 `invalid_args`；JSON 包装超过 32 KiB 时经 Engine 到不了 |
| `list_dir` 条目 | 256 | `truncated=true` + 词序前 256 条 |
| `exec` 墙钟 | 30 s（`DefaultExecTimeout`；Application 始终传这个值） | `exec_timeout` |
| 审批等待 | 默认 30 s | `approval_timeout` |
| 助手 UTF-8 | 1 MiB | 现有 `output_limit` |
| 流投影 | 4 MiB | `envelope_limit`；零 Stream |
| 带工具的 HTTP 体 | ≥ 5 MiB | 超限则适配器 `provider_permanent` |

Turn 失败展示句包括 `turn exceeded the step limit` 和
`request envelope exceeded the size limit`。

## InterruptToolTurn / FailToolTurn

`decideInterruptTurn` / `decideFailTurn` / `decideCompleteTurn` 仍拒绝
正在跑的 Item（`TestDecideToolTurnCompositesAndBareTurnRejection`、
`TestTurnTerminalRejectsRunningItem`）。Application 不得在
`ActiveItem != nil` 时 `Decide(InterruptTurn)`。

```go
type InterruptToolTurn struct {
    SessionID, TurnID, ItemID  domain IDs
    Code, Message              string
    ApprovalID                 domain.ApprovalID // 零值 ⇒ 不带 approval.resolved
}
type FailToolTurn struct {
    SessionID, TurnID, ItemID  domain IDs
    Code, Message              string
}
```

`InterruptToolTurn` 按序发出：可选
`approval.resolved{decision=canceled}`（仅当设置了 `ApprovalID` 且
`approval.requested` 已 Apply）→ `tool.call.interrupted` →
`turn.interrupted`。`FailToolTurn` 发出 `tool.call.failed` →
`turn.failed`。执行中取消产生一次合法的 `InterruptToolTurn` Domain 批次
（`TestCancelDuringExecuteUsesInterruptToolTurn`）。Step 2 流取消中断活
着的助手 Item（`TestStepTwoStreamCancelInterruptsLiveAssistant`）。Step 2
`invalid_stream` 失败该助手 Item
（`TestStepTwoInvalidStreamFailsLiveAssistant`）。

`CompleteAssistantMessage` 只结束 Item；当 `ToolCalls != nil` 时 Turn
保持 running（`TestDecideCompleteAssistantMessageReturnsItemOnly`、
`TestApplyAssistantMessageCompletedWithToolCallsLeavesTurnRunning`）。
策略和审批事件是跑着的工具 Item 上的 version-only 事实
（`TestDecidePolicyAndApprovalAreVersionOnly`）。Compact `Session` 仍然
没有 transcript。崩溃留下的 running 工具 Item 会让**另一个** Request ID
失败为 `item_already_running` / `turn_already_running`
（`TestApplyCompactToolItemAndEligibility`、
`TestCheckStartAssistantTurnEligibilityRejectsRunningToolItem`）。

## 重建

`ReconstructRequestResult` 对 CommandID 子序列做 Apply 等价状态机：
`admit_turn` → `open_assistant` → `idle_in_turn` → `open_tool` →
`terminal`。非法顺序是 `store_corrupt`。旧的 2/3/4/5/6 事件形状仍合法
（`TestReconstructRequestResultAcceptsExactRequestShapes`）。额外接受的
形状：

- 只有 `assistant.message.completed`、没有 Turn 终态，仍是 **running**
  （`TestReconstructRequestResultAcceptsItemOnlyCompleteAsRunning`）；
- 两 Step 成功（`TestReconstructRequestResultAcceptsTwoStepSuccess`）；
- `InterruptToolTurn` 批次
  （`TestReconstructRequestResultAcceptsInterruptToolTurn`）；
- 工具之后空闲态的 `step_limit`
  （`TestReconstructRequestResultAcceptsStepLimitFromIdleAfterTools`）。

没有先前 item-only complete 就出现 `tool.call.started` 是损坏
（`TestReconstructRequestResultRejectsToolStartWithoutItemOnlyComplete`）。
`Text` 是最后一次空 `ToolCalls` 的助手完成。准入 `ItemID` 仍是稳定的
`RunTurnResult.ItemID`。

## workspacefs 与 localexec

`adapters/workspacefs` 用 realpath + 前缀监狱实现 `tools.FileSystem`。
测试只用 `t.TempDir()`。`Resolve` 是探针。`Read`/`Write` 再检查监狱
（`TestReadWriteJail`）。`List` 遵守 `depth` 1 与 2 以及 256 条上限
（`TestListDepthAndCap`），并跳过离开工作区的目录符号链接子项
（`TestListSkipsOutOfWorkspaceDirSymlinkChildren`）。文件根被拒绝
（`TestNewRejectsFileRoot`）。外工作区参数被拒绝
（`TestResolveRejectsForeignWorkspace`）。

`adapters/localexec` 实现 `tools.CommandRunner`。
`localexec.Enforcement == "partial"`（`TestEnforcementPartial`）。这不是
Seatbelt、bwrap 或 Landlock；挡不住 exec 里的 curl。子进程环境为空，只留
宿主 `PATH`、`HOME` = 工作区根、`TMPDIR` = 退出后删除的工作区子目录
（`TestScrubbedEnv`）。命令只走 argv，没有 shell 展开
（`TestArgvOnlyNoShellExpansion`）。超时和取消杀掉进程组
（`TestTimeoutKillsProcessGroup`、`TestCancelKillsProcessGroup`）。
stdout+stderr 默认合计 64 KiB（`TestOutputCapKillsAndTruncates`）。cwd
与 argv0 必须留在工作区内（`TestCwdAndArgvMustStayInWorkspace`）。

共享端口套件在 `tools/porttest` 和 `testkit`（`MemFS`、
`ScriptedRunner`、`ScriptedApprover`）。

## openaicompat 组装器

当 `NativeTools` 为 `supported` 或 `required` 时，适配器发送
`tools`（`type=function`）和多角色 `messages`，并按 `index` 组装厂商
`delta.tool_calls`。Engine 发出的顺序是 `text_delta*`，再完整
`tool_call*`，再 `completed`，`FinishReason=tool_calls`
（`TestStreamAssemblesSupportedToolCalls`、
`TestStreamSendsToolsAndMessages`）。`required` 加空
`ModelRequest.Tools` 在 HTTP 之前 fail-closed
（`TestStreamRequiredEmptyToolsFailClosed`）。`unsupported` 仍把厂商
`tool_calls` 映射为 `capability_mismatch`，`FinishReason` 为空
（`TestRunTurnHTTPProfileTextOnlyToolCallsLeaveFinishReasonEmpty`）。
尾部残缺、缺 id+name、非 UTF-8、参数超过 32 KiB、有缺口或从 1 起的
index、冲突或重复 id，都是 `invalid_stream`
（`TestStreamTrailingPartialCallInvalid`、
`TestStreamAssembledCallRejectsBounds`）。`finish_reason=stop` 同时带已
组装工具是 `invalid_stream`（`TestStreamStopWithAssembledToolsInvalid`）。

HTTP `RunTurn` 端到端：tools-supported 的 `read_file` 再完成，第一次
usage 的 `finishReason=tool_calls`；第二次 Stream 看到助手 `tool_calls`
加一条工具消息，文本为 `Hello world`
（`TestRunTurnHTTPReadFileThenCompletes`）。线路映射、分类和无密钥
fixture 见[已实现 Provider Adapter 合同](provider-adapter.md)。

## 默认无密钥测试

默认 `go test` 使用 `t.TempDir()`、`testkit.MemFS` / `ScriptedRunner` /
`ScriptedApprover` / `ScriptedModel` 以及 scripted `http.RoundTripper`。
本里程碑没有真实用户工作区、没有活密钥，也没有
`//go:build liveprovider` 套件。

## 正式适配器与可执行证据

- `openaicompat.Model` 仍是生产 `engine.Model`；
- `workspacefs.FileSystem` 与 `localexec.Runner` 是生产工具适配器；
- `testkit.ScriptedModel`、`MemFS`、`ScriptedRunner`、
  `ScriptedApprover` 仍是同一端口上的正式 scripted 适配器；
- `MemoryEventStore` 仍是 EventStore v2 参考实现；
- 可复用套件 `eventstoretest.Run`、`modeltest.Run`、
  `enginescenariotest.Run` 在 scripted 路径上仍然通过。

仓库根目录本地证据矩阵：

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test ./... -count=1
go test -race ./... -count=1
```

强制本合同时 focused packages：

```bash
go test ./internal/harness/domain ./internal/harness/engine \
  ./internal/harness/policy ./internal/harness/tools \
  ./internal/harness/application \
  ./internal/harness/adapters/workspacefs \
  ./internal/harness/adapters/localexec \
  ./internal/harness/adapters/openaicompat \
  ./internal/harness/architecture -count=1
```

## 明确排除项

本已实现合同不提供：

- MCP 客户端、MCP 传输或远程工具宿主（目录 `source=mcp` 只是类型孔）；
- OS 隔离后端（macOS Seatbelt、Linux bwrap/Landlock、Windows ACL）。
  `localexec` 是 `enforcement=partial`；
- ACP 审批 UI 或 TUI。审批是注入的 `Approver` 端口，默认拒绝；
- 并行工具批次、后台 exec、PTY、LSP、web fetch、apply-patch、作为一等
  工具的 grep/search、todo、Skills 或 subagent；
- Runtime Host、running Step 循环的进程崩溃续跑、SQLite 或 JSONL 副本；
- 插件内核、Cordis、`next()` 策略瀑布、hook 总线或动态加载；
- Application 对同一次模型尝试的重试。Provider `Retryable` 仍只是建议；
- 把 compact `Session` 扩成 transcript，或 Context Engine；
- 把 tools、策略模式或 identity 折进 `DigestRunTurnRequestV1`；
- EventStore v2 接口变更或第五个 Store 方法；
- 厂商 SDK、OAuth 或模型发现；
- 连网或活密钥 CI。

这些排除项用于保持依赖顺序，不降低已经验证的 Step 循环路径，也防止把本
里程碑误称为 GA harness。
