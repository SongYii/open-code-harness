# Context Engine：有预算的历史与持久压缩 — 已实现合同（中文阅读版）

**状态：** 已实现；非 GA

**权威：** [Context Engine：有预算的历史、持久压缩与恢复 设计](../superpowers/specs/2026-09-01-context-engine-design.md)

**已实现计划：** [Context Engine 实施计划](../superpowers/plans/2026-09-01-context-engine.md)

**完成证据：** [Context Engine 证据台账](context-engine-evidence.md)

**包：** `internal/harness/contextengine`（纯）、`internal/harness/application`（编排）、`internal/harness/adapters/{sqlite,memory}`（检查点持久化与恢复）、`internal/harness/composition`（组装接线）

英文版本 [context-engine.md](context-engine.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。

## 范围

唯一的 Context Engine 从规范 Session 事件为纯模型对话与带工具的 Turn
构造模型可见的请求：它按所选路由声明的容量度量完整的、与提供方无关
的信封，选择一个对工具配对安全的历史前缀，并在 Provider 消费之前用
一个持久检查点替换该前缀。规范事件与 JSONL 审计副本永不被压缩或改
写——检查点是有损、可丢弃的投影，携带精确的源覆盖范围、覆盖范围之上
的 SHA-256 摘要、格式版本、token 证据与前驱血统。

`internal/harness/contextengine` 是纯的：它只导入 `internal/harness/domain`
与标准库（CE-01）。它拥有投影、度量、规划与检查点校验，但从不发起
Provider 调用、Store 追加或取消处理——这些属于 Application（CE-02）。
Composition 在每次组装中无条件构造 Context Engine（`composition.Config.Context`
只调参数；没有单独的启用开关——见下文[组装接线](#组装接线)）。

## 预算与度量合同

对于路由声明的上下文窗口 `W` 与最大输出 `O`：

```text
safety = max(512, ceil(W * 0.02))
hardInput = W - O - safety
trigger = floor(hardInput * TriggerPercent / 100)
target = floor(hardInput * TargetPercent / 100)
protectedTail = floor(hardInput * TailPercent / 100)
summaryOutputCap = min(O, max(128, floor(hardInput * 0.10)))
```

`contextengine.ComputeBudget` 计算这些值；当 `O + safety >= W` 时返回
`ErrBudgetInvalid`，因为此时不存在正的 `hardInput`。Composition 在构造
任何资源之前就校验这一点（见下文），Application 在 `Context.Enabled`
时于 `NewService` 独立再次校验 `Budget.HardInput > 0`。

默认值（设计 §8 自己的表格，均可通过 `composition.Config.Context` 配置，
均在组装前做范围校验）：

| 设置 | 默认值 | 有效范围/不变式 |
| --- | --- | --- |
| `TriggerPercent` | 80 | 60–95 |
| `TargetPercent` | 55 | 30–80 且 `< TriggerPercent` |
| `TailPercent` | 25 | 10–50 且 `< TargetPercent` |
| `MaxSummaryChunks` | 8 | 1–16（已接受并校验；**尚未被消费**——见[已知局限](#已知局限)） |
| `MaxOverflowCompactionsPerTurn` | 2 | 1–3 |
| `CompactionTimeout` | 2 分钟 | 5 秒–10 分钟 |
| `MaxPrunedToolResultsPerRequest` | 64 | 1–64（已接受并校验；**尚未被消费**——见[已知局限](#已知局限)） |

默认确定性度量器 `och_wire_estimate_v1`（`contextengine.WireEstimateMeter`）：
文本/JSON 载荷 `ceil(UTF-8 字节数 / 3)`；每条消息固定 8 token 的成帧
开销；每个 Tool Call 或 Tool Result 额外 16 token；每个 Tool Schema
16 token 加 `ceil(规范化 JSON 字节数 / 3)`。这有意对典型 ASCII 散文
偏保守估价，而不是冒低估的风险。

`contextengine.EvaluateUsageAnchor` 实现了设计 §8 的非降低型
provider-usage 锚点（`budgetEstimate = max(wireEstimate, anchoredEstimate?)`），
作为纯的、独立测试过的逻辑存在——但它**尚未被 Application 调用**：今天
每一次真实的派发决策都只使用确定性的 wire 估算。见[已知局限](#已知局限)。

## 投影、规划与检查点流水线

规范事件按序列顺序投影为对话单元（`contextengine.ProjectSourceEvents`，
设计 §9.1）：`TurnStarted` → 用户消息，`AssistantMessageCompleted` →
助手消息（+ Tool Calls），`ToolCallStarted`/`Completed`/`Failed`/`Interrupted`
→ 工具结果。Session、模型请求/用量、策略、审批与 context 事件从不直
接产生消息。只有当每一个提供的 Tool Call 都恰好有一个终态结果时，边
界才算平衡；重复、未知或缺失的 ID 会闭合失败。

`contextengine.Scan` 执行钉住头版本、逐页的读取（第一遍）：第一页固定
`HeadVersion`；此后每一页都对同一值发出请求，一旦不一致就闭合失败
（`ErrHeadMismatch`）。`contextengine.SelectCutPoint`（第二遍）从头部向
后走，直到满足 `protectedTail`，然后把更早的覆盖范围吸附到最近的安
全 Turn 边界——覆盖范围总是完整的 Turn，Turn 从不会在覆盖与保留之间
被切开。`contextengine.Materialize` 把一个可选检查点自身的消息、保留
的尾部与当前输入合并为一个 `PreparedContext`，其 `EstimatedTotalTokens`
就是 Application 在派发前与 `Budget.HardInput` 比较的数值。

检查点（`contextengine.ContextCheckpoint` / `domain.ContextCheckpointRecord`）
有两种：

- `rolling_summary_v1` ——一个 LLM 生成的结构化摘要，在被接受之前先
  经过形态、脱敏、截断与可测量收缩的校验（`contextengine.ValidateSummary`）；
- `source_tail_reset_v1` ——一个不含任何历史性断言的确定性标记，只在
  硬容量限制无法等待一个可信摘要时使用。

每个检查点都携带一条 SHA-256 摘要链（`contextengine.ExtendSourceDigest`，
以 `D0 = SHA256("och-context-source-v1\n")` 为种子），覆盖它所声称覆盖
的、经 `contextengine.IsSourceEvent` 过滤后的源事件（与投影语法折叠为
一条消息的同样六种事件类型）。后继检查点的摘要从前驱自身的摘要开始，
只对新覆盖的范围做延伸；覆盖范围不变的重写要求摘要完全相同。这条链
在三个位置被独立地重新校验——绝不信任一个声称的值：SQLite 写时钩子
（`updateContextCheckpointHead`，位于提交完成事件的同一事务内）、
SQLite/内存的读路径（`LoadLatestContextCheckpoint`），以及 SQLite 的
冷重建路径（`RebuildAndVerifyContextCheckpointHeads`）。任何不一致都
是 `store_corrupt`，绝不会悄悄覆盖一条已存在的行——只有当一个会话确
实**缺失**这一行、且能从规范事件独立推导出有效检查点时，重建才会修复
（写入）它。

## 四种触发方式

| 触发 | 时机 | 所需 Turn 状态 |
| --- | --- | --- |
| `pre_turn` | 一个 Turn 的准入批次提交之前，当未压缩估算超过 `Budget.Trigger` 时 | 无活动 Turn |
| `mid_turn` | 工具 Step 之间，同样的条件 | 有活动 Turn |
| `manual` | 运维者手动触发（`Service.CompactSession`），通过 `PlanInput.Force` 绕过 Trigger 比较 | 无活动 Turn |
| `overflow_retry` | Provider 启动时的拒绝被归类为上下文溢出，按 `MaxOverflowCompactionsPerTurn` 限制每个 Turn 的次数 | 有活动 Turn |

Domain 在 `Decide` 与 `Apply` 两处都强制这种 Turn 状态配对
（`decideStartContextCompaction`/`applyContextCompactionStarted`）：
`pre_turn`/`manual` 要求无活动 Turn；`mid_turn`/`overflow_retry` 要求
有一个。每个 Session 任一时刻至多有一个活动的压缩（`Session.ContextCompaction`）。

手动压缩（`Service.CompactSession`）刻意比自动路径更窄：只尝试一种策
略（`summary`，默认，或显式的 `reset`），**没有阶梯式回退**——失败的
手动摘要直接返回自己的失败，符合设计 §16 的"手动摘要直接返回其失败"
规则，而不会跌落到运维者并未要求的确定性重置。自动路径确会跌落：在
`hardInput` 以下失败的摘要尝试会未压缩地继续（记录日志）；在
`hardInput` 或以上，若符合资格则尝试确定性重置——除非调用者自身的取
消在失败摘要之后被立即检查并整体短路这条阶梯（取消绝不会变成重置，
全局约束，已在 `context_concurrency_test.go` 中用真实 goroutine 竞争
验证）。

溢出恢复在两次尝试之间至少缩减 10%，并受 `MaxOverflowCompactionsPerTurn`
限制；由于 `SelectCutPoint` 自身的裁剪已经是一次性最大化的，实践中每
个 Turn 至多只能有一次真正成功的恢复——第二次尝试在真正触及配置的上
限之前，结构上就已经找不到更多可覆盖的内容了（已披露，不是缺陷）。

## 恢复

设计 §14 的三个部分：

1. **SQLite 投影**（迁移 5，`context_checkpoint_heads`）：每个会话一
   行有索引的记录（`checkpoint_event_sequence`、`checkpoint_event_id`、
   `checkpoint_id`、`covered_through_sequence`、`source_digest`、
   `updated_at_commit_position`），只有在同一追加事务内完成独立哈希
   链校验之后才会更新。`LoadLatestContextCheckpoint` 是 O(1)：一次有
   索引的行读取加一次按主键的规范事件连接，而不是全流扫描（已做基准
   测试——见证据台账）。
2. **投影恢复**：`RebuildAndVerifyContextCheckpointHeads` 只从规范事
   件独立重新推导每个会话最远的有效检查点，以有界分页方式进行（从不
   一次性把整个会话历史都放进内存），并把存储的行与之核对。它被接入
   JSONL 导入作为专门的一层，因为导入从不像 session_heads 那样增量
   写入这个投影。
3. **Runtime Host 调和**：启动时发现的未匹配 `ContextCompactionStarted`
   会变成 `ContextCompactionFailed{Code: "runtime_recovered"}`。当一
   个 `mid_turn`/`overflow_retry` 压缩在一个正在运行的 Turn 内部崩溃
   时，它的失败被排在该 Turn 自身的终态事件**之前**。`manual`/`pre_turn`
   压缩没有外围的 Turn，因此由 `Store.SessionsWithActiveCompaction`
   （一个自身流头部是未匹配 `context.compaction.started` 的会话——这
   是可靠的，因为压缩活动期间没有其他命令能够扩展该流，无需专门的派
   生状态表）提供 `session_heads.status` 本身无法呈现的候选。

内存适配器的 `LoadLatestContextCheckpoint` 做了一个不同的、已披露的
取舍：不存在单独的写时钩子，因此每次读取都独立地从 `D0` 重新计算完
整摘要链——每次读取是 O(history)，而非 SQLite 的 O(1)，但提供完全相
同的校验保证，与该适配器一贯"简单优先于性能"的既有先例一致。

## 失败代数

| 代码 | 类别 | 可重试 | 含义 |
| --- | --- | --- | --- |
| `context_budget_invalid` | 校验/配置 | 否 | 路由容量无法产生正的硬输入预算。 |
| `context_projection_invalid` | 内部 | 否 | 规范事件无法形成有效的消息/工具单元。 |
| `context_unit_too_large` | context/model | 否 | 一个受保护的投影单元超过硬输入。 |
| `context_compaction_busy` | 冲突 | 是，待所有者完成后 | 另一个持久压缩占用了该 Session。 |
| `context_nothing_to_compact` | 对手动而言是校验；内部则是空操作 | 否 | 不存在安全的源前缀。 |
| `context_summary_failed` | model | 是，由后续命令 | 活动路由的摘要调用或流失败。 |
| `context_summary_invalid` | model | 是，路由/提示词版本变更后 | 输出未通过结构、截断、脱敏或收缩校验。 |
| `context_checkpoint_invalid` | 内部/store | 否 | 候选或已加载的检查点违反覆盖/血统/schema。 |
| `context_compaction_limit` | model | 否，本 Turn 内 | 溢出恢复上限已耗尽。 |

每一个返回未知提交结果的压缩追加（Start/Complete/Fail）都通过本项目
其他每一个追加所用的同一套 `ResolveAppendIntent` 模式来解决——绝不
会永久悬而不决。

## 并发与取消

已在 `go test -race` 下验证（`context_concurrency_test.go`）：同一
Session 上并发的手动/手动压缩（链完整性不变式——一条严格前进、正确
链接的链中没有重复或重叠覆盖，而不是天真的"恰好一个成功"断言，因为
合法的顺序成功是可能的）；手动压缩与 `RunTurn` 并发（互斥，通过遍历
持久日志检查压缩区间与 Turn 区间是否重叠来验证）；`RunTurn` 与
Session 关闭并发（同样的模式）；溢出恢复与调用者取消并发（两道独立
的防线：`runCompactionBracket` 在失败的摘要之后立即检查
`contextError(ctx)` 并整体跳过重置阶梯，`ResetEligibility.CallerCanceled`
作为第二道独立检查被贯穿传入）；一个压缩追加落入未知结果状态与取消
并发（解析器规则获胜）。

## 组装接线

`composition.Config.Context` 是一个调参结构体，不是启用开关——一个能
正常工作的 Context Engine 是本里程碑的基线组装行为。`Open` 的构造顺
序在会话 Provider/runner 与工作区工具/目录之间新增了"Context 度量
器/引擎 + 摘要器"：摘要器（`application.EngineContextSummarizer`）包
装的正是对话路径所用的**同一个** runner/model（设计 §18 的"不引入第
二个 Provider"），而已经构造好的 SQLite store（自迁移 5 起就是一个
`ContextCheckpointStore`）被直接传下去。`Context` 中的每一条范围/关
系都会被校验，路由能否产生正的预算也会通过 `contextengine.ComputeBudget`
本身来检查，这一切都发生在 `Open` 构造任何资源之前。

## 适配器与协议投影

- **ACP**（`adapters/acp/project.go`）：一个检查点或 context 准备决
  策绝不替换或补充可见的对话历史——全部四种新事件类型都投影为空
  （`ProjectRecordedEvent` 的 `default` 分支），与
  `ModelRequestRecorded`/`PolicyDecisionRecorded` 一贯的做法完全一致。
  有一个显式的"不投影"测试证明这一点，而不仅仅是它在正向投影表中的
  缺席。
- **Transcript**（`transcript/codec.go`）：严格的事实编解码器现在接
  受全部四种新事件类型，包括检查点摘要文本本身——transcript 是一个
  显式的、承载内容的导出，不同于 ACP 的仅规范投影。在本任务之前，一
  个无法识别的事件类型会让整个导出闭合失败（`CodeUnsupportedEventType`）；
  任何曾经运行过压缩的会话都会让 `och export-session` 在碰到该事件
  的那一刻报错。这在本里程碑中已被修复，而不是留作一个潜伏的回归。

## 资源边界

- Application 派发的任何请求都不会超过 `Budget.HardInput` token 或
  `MaxProjectionBytes`（4 MiB）——由 `BenchmarkMaterialize`
  （100/1,000/10,000-Turn 流）确认：一旦 `SelectCutPoint` 已经决定了
  要保留什么，物化后的信封及其估算 token 数就与 Turn 数量无关，保持
  平坦。
- `LoadLatestContextCheckpoint`（SQLite）不随历史长度增长而变化——
  `BenchmarkLoadLatestContextCheckpoint` 在 100、1,000 与 10,000 个
  Turn 下都测得约 190µs/23KB。
- `RebuildAndVerifyContextCheckpointHeads` 在时间上是 O(history)（对
  一个冷门/罕见的恢复路径而言，这是一个可接受的、已披露的代价），但
  是分页的、堆内存有界的——它从不会把一个会话的整个规范历史一次性放
  进一个切片；`TestRebuildContextCheckpointHeadsSpansMultiplePages`
  专门证明了跨页边界时的正确性。

## 已知局限

在此处、在证据台账中、以及在代码注释的确切位置都做了披露——不留给
一个基准测试数字或一个缺失的测试来自行说明。以下几点都不损害正确
性或安全性；它们都是本里程碑选择不再投入更多时间的性能、完整性或
便利性上的取舍。

1. **`Scan` 与 `SelectCutPoint` 自身的前置估算在每一次调用中，无论
   时间还是瞬时内存都是 O(history)——而不是相对配置好的预算窗口的
   O(1)。** 每次 `PrepareContext` 运行时，`Scan` 都会从流的最开始重
   新读取并持有每一条规范源记录；不存在"从上一个检查点继续扫描"的
   模式，即便一个检查点已经覆盖了除受保护尾部之外的一切。
   `BenchmarkScan`/`BenchmarkSelectCutPoint`
   （`internal/harness/contextengine/bench_test.go`）直接测量了这一
   点：两者的分配量都随 Turn 数量线性增长（100 → 1,000 个 Turn 大致
   是字节数/次操作的 10 倍跳跃）。这意味着一个长期会话中低于触发阈
   值的 Turn 准入，付出的代价与整个会话的历史成正比，而不是与配置
   的预算成正比——这是本里程碑自身基准测试工作发现的最重要的一处缺
   口。它不违反正确性（`BenchmarkMaterialize` 确认实际派发的信封确
   实保持有界），但确实意味着设计 §22.4 要求基准测试套件检查的"存
   活堆由预算而非 Turn 数量决定上界"这条属性,在今天的规划路径上**并
   未**兑现。修复它需要让 `Scan` 支持一种增量的、从检查点续扫的模
   式——这是对一个纯的、经过大量变异测试的包的一次真正架构性改动，
   计划作为后续工作而不是在本里程碑收尾时仓促完成。
2. **非降低型 provider-usage 锚点（`EvaluateUsageAnchor`）已作为纯逻
   辑实现并独立测试，但没有被 Application 调用。** 今天每一次真实的
   派发决策都只使用确定性的 wire 估算；该锚点利用观测用量收紧这一估
   算的潜力目前处于闲置状态。这是安全的（wire 估算始终是一个有效的、
   即便保守的上界），但相对设计 §8 而言并不完整。
3. **`MaxSummaryChunks` 与 `MaxPrunedToolResultsPerRequest` 已被
   `composition.Config.Context` 接受并做范围校验，与设计的字面合同
   一致，但尚未改变任何行为。** 摘要器仍是单次调用式的
   （`buildSummaryCheckpointWithFocus` 对超出一次调用能力的源材料是
   直接拒绝，而不是分块）；Tool Result 裁剪
   （`contextengine.ProjectToolResult`）从未被 `Materialize` 的流水
   线调用过。
4. **专门针对"当一个 `pre_turn` 自动压缩正在进行时,重复的 `RunTurnRequestID`
   合并"这一场景**（设计 §22.2 自身点名的场景）没有专门的测试；与之
   相邻的"手动压缩与 RunTurn 互斥"用例
   （`TestConcurrentManualCompactionAndRunTurnAreMutuallyExclusive`）
   已被覆盖，但这一更窄的自动触发变体没有。

`och compact-session`（设计 CE-14 自身的 CLI 命令）现已构建——`cmd/och`
自己的 `compact-session` 子命令打开正常的组装根，运行
`Service.CompactSession`，并向 stdout 打印一个稳定的 JSON 对象（见
[Getting Started](../getting-started.md#manual-compaction)）。第一次针
对一个真实的、已接入组装的 `ContextCheckpointStore` 构建它（此前每一
个 reset 策略测试都要么使用从不做任何校验的 `fakeCheckpointStore`，要
么只依赖 `ValidateSuccessor` 自身的纯结构性检查，两者都不会从规范内
容重新计算摘要），发现并修复了一个真实的缺陷：`buildResetCheckpoint`
的 `Coverage.SourceDigest` 被留在了它的种子值上，从未真正对新覆盖的
规范记录做延伸，即便 `ThroughSequence` 正确地前进了。这意味着此前构
建过的**每一个**确定性重置检查点——无论是手动 `-strategy reset` 路径
还是共享同一函数的自动溢出恢复重置路径——一旦被一个真正做校验的存储
读回，都会在那一刻被拒绝。修复方式是像滚动摘要路径早已做的那样,对新
覆盖的范围延伸摘要;已通过回归测试验证，并附带其自身的变异检查。

## 排除项

把设计 §4 自身的非目标重申为已交付的排除项，而不是悄悄的缺席：

- 不包含向量搜索、embedding、RAG、语义检索或跨会话记忆。
- 不包含对规范事件的删除、改写、压平或存储层压缩——每个检查点都是有
  损且可丢弃的；规范日志与 JSONL 审计副本原封不动。
- 不包含没有具体支持性 Adapter 的、provider 原生的不透明压缩。
- 不包含后台压缩工作者或投机性摘要生成——压缩始终是同步的、由调用者
  驱动的。
- 不包含针对身份验证、配额、速率限制、瞬态或任意永久性 Provider 失败
  的通用重试——只有确认的启动期上下文溢出会重试，且只在测量到缩减之
  后。
- 不包含面向模型的、用于读取被裁剪 Tool Result 的归档读取工具。
- 不包含 context 编辑、回退、分支或用户编写的检查点修改。
- 不包含 MCP、TUI、OpenTelemetry 或里程碑 10 的评测运行器表面。
- 不包含用于手动或实时压缩的 ACP 方法或非标准 `session/update` 变体。
- 不包含对每一个说 OpenAI 兼容协议的端点的精确分词器对齐——
  `och_wire_estimate_v1` 是一个刻意保守的、与模型无关的估算。

## GA 阻碍项

本里程碑至少在以下条件满足之前保持**非 GA**：存在对滚动摘要的真实模
型质量评测（本实现自身的测试始终使用脚本化/固件式的摘要器，从未使
用真实模型的实际摘要质量）；比本里程碑所使用的 OpenAI 兼容适配器更
广泛的 provider 覆盖；上文所述 `Scan`/`SelectCutPoint` 的 O(history)
规划路径成本被解决或针对生产规模被明确接受；以及在本里程碑的确定性
时间与脚本化结果测试证据之外，对恢复路径进行墙钟、多进程的 soak 测
试。
