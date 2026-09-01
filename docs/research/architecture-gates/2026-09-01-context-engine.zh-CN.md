# Context Engine 架构调研门

**状态：** 调研证据完成

**日期：** 2026-09-01

**范围：** `docs/README.md` 里程碑 8（Context Engine——模型可见上下文的选择、预算和压缩）
仍未设计。[2026-09-01 roadmap 门](2026-09-01-context-engine-evaluation-observability-tui.md)
把 Context Engine 和另外三个未设计的里程碑放在同一浅度调研了一遍，并且明确写道它"仍需要一份专属的、
重新核验当时最新一手资料的子系统架构调研门，才能进入正式设计"。这就是那份专属调研门：深入六个参考项目
的压缩机制本身——触发数学、边界选择算法、摘要生成机制、持久化/事务形状、失败语义、以及配置项——并对照
本项目自己代码里已经有的东西和还没有的东西。本文不做任何设计或实现。按文档规则第 1 条，本门之后的下一步
是一份正式设计文档（`docs/superpowers/specs/`），而不是直接进入实施计划。

English is normative. 本文件是同步的中文阅读副本，若有分歧以英文版
[2026-09-01-context-engine.md](2026-09-01-context-engine.md) 为准。

## 对照集与固定 commit

按文档规则第 8 条，这些是通过 `./scripts/fetch-reference.sh --list` 取得的、同一批已存在的 gitignored
`.reference/` 检出。按文档规则第 7 条：本门考虑过重新拉取，但判断没有必要——这些和同一天早些时候
roadmap 门读到的 commit 完全相同，而 roadmap 门自己也已经核实过它们和 2026-08-31 web-trajectory-ui
门拉取时的状态一致。同一天内为同样六个项目重新拉取不会带来新状态；本门把精力花在了把 roadmap 门只在
概览层面打开过的文件读得更深上。

| 项目 | 仓库 | Commit | 观察时间 | 为何被读 |
| --- | --- | --- | --- | --- |
| Pi（agent core 源码） | `badlogic/pi-mono` | `853a80d` | 2026-08-28 | `packages/agent/src/harness/compaction/` 和 `session/context.ts`——逐行读了触发、切点和持久化机制 |
| DeepSeek Harness | `deepseek-ai/deepseek-harness` | `0a53fb5` | 2026-08-30 | `packages/compaction/compaction-basic/src/{region,summarizer}.ts`——读了它完整的事务/锁/稳定性检查机制，不只是确认存在 |
| Kimi Code | `MoonshotAI/kimi-code` | `8f2c60b` | 2026-08-31 | `packages/agent-core-v2/src/agent/fullCompaction/`——读了触发比例、tool-pairing 切点逻辑、以及与 turn 的互斥处理 |
| Grok Build | `xai-org/grok-build` | `bc7f02e` | 2026-08-28 | `crates/common/xai-grok-compaction/src/{intra,inter,code}_compaction/`——读了精确触发公式、错误哲学，以及 `xai-grok-shell` 里的两遍预取缓存 |
| Codex | `openai/codex` | `a9519cb` | 2026-08-31 | `codex-rs/core/src/compact*.rs`——读了压缩前后钩子、token-budget 策略的实际机制、以及 remote-v2 的保留 token 预算 |
| Maka | `maka-agent/maka-agent` | `ef94235` | 2026-08-31 | `packages/runtime/src/{history-compaction,compaction-boundary,context-budget-policy}.ts`——读了它的三机制预算策略和显式的 fail-open 重放逻辑 |

## 本项目自己已经有什么

本项目今天完全没有压缩机制——没有 domain command，没有 domain event，没有预算概念——但也不是从零开始。
以下是任何未来设计都必须对照的具体基线。

- **发给模型的上下文今天就是无界的，而需要改动的确切位置是 `projectPriorTurns`。**
  `internal/harness/application/loop.go:66`
  （`func projectPriorTurns(records []domain.RecordedEvent, current domain.TurnID) []domain.ModelPromptMessage`）
  把**每一个**已提交的、来自更早 turn 的事件都折叠进模型的 prompt 消息里，没有任何预算、截断或摘要——
  它自己的文档注释（`loop.go:62-65`）说得很直白："compact 的 Session 聚合会丢弃已完成的 turn；事件日志
  才是权威。"它在 `runAfterAdmission`（`loop.go:191`：
  `owned.projection = newTurnProjectionWithPrefix(projectPriorTurns(records, result.TurnID), request.Input)`）
  里被调用，而后者又是从 `RunTurn`（`internal/harness/application/turn.go:28`）经 `runTurnOwned` →
  `runAfterAdmission` 到达的。压缩功能的自然插入点就在这里：要么让 `projectPriorTurns` 携带一个压缩水位线，
  从流的更靠后位置开始折叠；要么在 `newTurnProjectionWithPrefix` 消费它的输出之前，用一个新函数包一层。
- **本项目自己的代码里已经把"compact"用在了别的地方，设计必须避免和它冲突。** `loop.go:63` 注释里的说法——
  "compact 的 Session 聚合"——指的是一个真实的、完全不同的概念：`internal/harness/domain/compact_test.go`
  断言内存里的 `Session` 聚合在事件不断应用的过程中保持小而不增长（`compact_test.go:17,20`："compact state
  = ..."、"compact state unexpectedly grew"）。这是聚合快照意义上的紧凑（`Session` 结构体本身从不累积完整
  历史——事件日志才是权威），不是给模型用的上下文/token 压缩。Context Engine 设计需要一套自己的、不同的
  词汇——`Context` compaction，或者干脆避开"compact"这个词——避免这两个不相关的含义在代码、测试或文档里
  互相冲突。
- **token 窗口的分母已经端到端打通了；分子还没有。** `ContextWindowTokens`/`MaxOutputTokens` 从
  `-context-window`/`-max-output` 这两个 CLI flag（`internal/harness/composition/config.go:23-24`，
  在 `config.go:126-127` 校验非零）一路流经 `openaicompat.ProfileToolsSupported(...)`
  （`internal/harness/composition/assembly.go:122`）进入 `engine.Profile.ContextWindowTokens`/`MaxOutputTokens`
  （`internal/harness/engine/profile.go:25-26`），并单独进入 domain 自己的
  `ModelRequestSpec.ContextWindowTokens`/`MaxOutputTokens`（`internal/harness/domain/commands.go:105-106`，
  在 `internal/harness/domain/decide.go:74-75` 赋值，并且已经被持久化记录进
  `ModelAttemptStarted` 形状的事件 schema，`internal/harness/domain/events.go:178-179`）。真正缺的是
  每个参考项目触发数学都需要的那个分子：即将构建的 prompt 会消耗多少 token 的实际值或估计值，以及一个
  在 `RunTurn` 构建其 projection 之前会同时参考这两者的决策点。`ModelUsageRecorded`
  （`internal/harness/application/turn.go:529-543`，`modelUsageFromStats`）已经从供应商的 `stats.Usage`
  里捕获了真实的、事后的 `InputTokens`/`OutputTokens`/`CachedInputTokens`——这正是 Pi 自己的
  `estimateContextTokens`（见下文）在有真实用量时优先选用而不是字符估算的那种信号，而本项目已经有了它，
  只是还没有任何 turn 开始前的预算检查会去参考它。
- **事件存储是精确 append-only 的，没有删除或改写方法，这决定了任何压缩设计必须采取的形状。**
  `docs/architecture/eventstore-v2.md:31-36` 的 `EventStore` 接口正好是 `ReadStream`、`Append`、
  `ResolveAppend`、`FindCommandRequest`——没有 update，没有 delete，没有 rewrite。压缩不能移除或修改
  已有事件；它只能**追加一个新事件**，然后改变某个后续的 *projection*——也就是 `projectPriorTurns`
  自己——选择折叠进来的内容。这几乎和 Pi 自己的架构完全一致：Pi 的 session 日志是 append-only 的
  JSONL，它的 `compaction` entry 就是一条普通的追加日志条目
  （`CompactionEntry extends EntryBase`，`pi-mono/packages/agent/src/harness/session/types.ts:44-51`，
  和其它每一种 entry 共享同样的 `id`/`parentId`/`seq`/`timestamp` 形状），而
  `defaultContextEntryTransform`（`pi-mono/packages/agent/src/harness/session/context.ts:44-54`）是一个
  纯粹的**投影**：找到最新的 `compaction` entry，返回
  `[compaction, ...pathEntries.slice(compactionIndex + 1)]`——底层的 append-only entry 数组从未被改写
  或截断；变的只是读时投影折叠进模型消息里的内容。这是本门找到的最可迁移的先例：一个设计可以新增一个
  `domain.Event`（压缩事实），然后改动 `projectPriorTurns`，让它从最新这样一个事件往后开始折叠，完全
  不触碰本项目的 append-only Store 合同。
- **Secret 脱敏已经在模型可见与持久化文本的两个调用点运行，而压缩摘要恰好是从已脱敏材料派生出的新文本。**
  `redact.Text` 的调用点是 `internal/harness/application/pipeline.go:290`（工具失败消息）、
  `pipeline.go:307`（工具结果内容），以及 `internal/harness/application/loop.go:246,295`
  （最终 assistant 消息，成功路径和 terminal-unknown-resolution 路径都覆盖）——见
  `docs/architecture/secret-redaction.md:13`。压缩生成的摘要是基于已经过这些调用点一次的历史构建的，
  所以除非摘要模型被提示以某种方式复现了它从未见过的 secret 形状，脱敏在持久化前的这一层已经把摘要
  间接覆盖了。本门不判定这种间接覆盖是否足够严密到设计可以跳过对摘要本身的第三个脱敏调用点，还是应该
  两道保险都上——因为摘要 prompt 明确要求保留"精确的文件路径、函数名和错误信息"（DeepSeek Harness 自己
  的措辞，见下文），这和脱敏本身要移除 secret 形状文本的目标是直接冲突的。
- **章程点名了一个本项目还没建的 domain 实体。**
  `docs/superpowers/specs/2026-08-11-open-code-harness-architecture-design.md` §6.1 在 `Session`、
  `Turn`、`Item`、`ModelAttempt`、`Approval`、`Checkpoint`、`PolicyDecision` 之外，还列了
  `ContextSnapshot`（"发送给模型的上下文构成及裁剪依据"）作为目标 domain 实体。直接对
  `internal/harness/domain/` 做 grep 确认了 `ContextSnapshot` 类型不存在——该节点名的其它每一个实体都
  已经存在了。本门不判定压缩功能是否需要把 `ContextSnapshot` 实体化成自己的类型，还是可以把同样的想法
  表达成现有 `domain.Event` union 上的一个新变体；它只是确认了章程自己点名的这个缺口至今仍然开着。

## 各项目发现

### Pi——append-only 的压缩 entry、感知供应商用量的触发器、tool-call 安全的切点

- **触发数学**：`shouldCompact(contextTokens, contextWindow, settings)`（`compaction.ts:247-250`）是单一
  的预留边际检查：`contextTokens > contextWindow - settings.reserveTokens`。`DEFAULT_COMPACTION_SETTINGS`
  （`compaction.ts:158-162`）固定 `reserveTokens: 16384`、`keepRecentTokens: 20000`。
  `estimateContextTokens`（`compaction.ts:215-241`）在存在时优先使用最近一条 assistant 消息真实的供应商
  `usage` 区块，而不是字符估算（`getLastAssistantUsageInfo`，`compaction.ts:207-212`），只对该用量之后
  （trailing）的消息做字符估算——真实用量而非估算，是只要可得就优先的主信号。
- **边界选择**：`findValidCutPoints`（`compaction.ts:312-341`）枚举那些类型为消息且 role 属于
  `user`/`assistant`/`bashExecution`/`custom`/`branchSummary`/`compactionSummary` 的 entry，或者
  `branch_summary` entry——明确**排除** `toolResult` entry 作为有效切点（`compaction.ts:329-331`：
  `case "toolResult": break;`，不做 `cutPoints.push`）。`findCutPoint`（`compaction.ts:373-421`）从末尾
  往回累加 token 数直到达到 `keepRecentTokens`，向前吸附到最近的有效切点，然后额外再往回跳过任何非消息
  entry（`thinking_level_change`、`model_change` 等），确保切点最终精确落在一条消息或压缩边界上
  （`compaction.ts:406-412`）。当切点落在 turn 中段而非某条起始的 user 消息上时，它会报告
  `isSplitTurn`（`compaction.ts:419`）。
- **摘要生成机制**：`SUMMARIZATION_SYSTEM_PROMPT` 和 `SUMMARIZATION_PROMPT`（`compaction.ts:424-459`）
  是真实的、固定的 prompt 文本，带精确的 Markdown checkpoint 格式（`## Goal`、`## Constraints &
  Preferences`、带 `### Done`/`In Progress`/`Blocked` 的 `## Progress`、`## Key Decisions`、
  `## Next Steps`、`## Critical Context`）；`UPDATE_SUMMARIZATION_PROMPT`（`compaction.ts:461-497`）是
  一个独立的、用于**增量更新**已有摘要而非从头重新摘要的 prompt，每当已存在一条先前的压缩 entry 时使用
  （`prepareCompaction`，`compaction.ts:614-621`）。`generateSummaryWithUsage` 的 `maxTokens`
  （`compaction.ts:539-542`）是 `Math.min(0.8 * reserveTokens, model.maxTokens)`——摘要本身被限制在它
  本该腾出的预留预算的 80% 以内。Pi 没有单独的、非摘要式的重置策略；每一次压缩都是模型生成的。
- **持久化/事务形状**：`CompactionEntry` 是 `Entry` union 的一等成员
  （`pi-mono/packages/agent/src/harness/session/types.ts:44-51`），共享 `EntryBase` 的
  `id`/`parentId`/`seq`/`timestamp`——是一条普通的 append-only 日志条目，不是临时的内存态值。原始历史
  从不删除：`defaultContextEntryTransform`（`session/context.ts:44-54`）找到最新的 `compaction` entry，
  只返回 `[compaction, ...pathEntries.slice(compactionIndex + 1)]` 作为
  `buildContextEntries`/`buildSessionContext`（`session/context.ts:57-100`）的基础——是一次对未被改动的
  append-only entry 数组的纯读时投影。
- **失败/重试语义**：`generateSummaryWithUsage`（`compaction.ts:642-655`）在响应的 stop reason 为
  aborted（`"aborted"`）或出错（`"summarization_failed"`）时返回一个带类型的 `CompactionError`——是一个
  `Result` 类型，不是抛出异常，调用方必须显式处理。本门没有深挖调用方一侧的 fail-open/fail-closed 选择，
  只确认了 `prepareCompaction` 自己在无事可压缩时简单地返回 `undefined`（空操作，`compaction.ts:614-615`）。
- **配置**：`CompactionSettings` 只暴露 `enabled`、`reserveTokens`、`keepRecentTokens`
  （`compaction.ts:158-162`）作为可调项——是六个项目里配置面最小的。
- **工具调用/二进制内容处理**：`estimateTokens`（`compaction.ts:269-299`）对每个图片内容块按固定的
  `ESTIMATED_IMAGE_CHARS = 4800`（`compaction.ts:249`）计费，而不是读取真实图片大小——图片会按固定量
  推高 token 估算，但压缩本身不对它们做特殊排除或特殊处理。

### DeepSeek Harness——带锁、带稳定性检查、显式要求缩小的真实日志化事务

- **触发数学**：本门没有读到（在本门限定读取的 `compaction-basic` 包之外；roadmap 门也没有定位到）。
  本门深入确认的是触发决策之后的一切：从边界选择到提交。
- **边界选择**：`selectCompactableRange`（`region.ts:100-133`）从末尾往回累加一个 `retainTokens` 尾部，
  然后再把切点索引往回走一遍，具体是走到 `toolPairingBalancedBefore(session, ...)` 返回 true 为止
  （`region.ts:122-125`）——也就是说，边界安全性是在 token 预算这一遍选出临时切点**之后**、作为**第二个
  独立步骤**来检查的。`validateSurfaceRegion`（`region.ts:322-341`）在事务真正运行**之前**，会对选中区间
  的两端再检查**第二遍**（`toolPairingBalancedBefore`/`After`）——是针对"选中范围在选择和提交之间可能已
  过期"这种情况的纵深防御。
- **摘要生成机制**：`COMPACTION_INSTRUCTION`（`summarizer.ts:26-64`）是真实的、固定的 prompt 文本——一个
  `## Primary Request and Intent` / `## Key Technical Concepts` / `## Files and Code` /
  `## Errors and Fixes` / `## Pending Jobs` / `## Current Work` / `## Next Step` /
  `## Critical Context` 的 checkpoint 格式，作为**最后一条 user 消息**追加在被回放的对话之后发出，而不是
  作为单独的 system prompt——模块自己的文档注释（`summarizer.ts:20-25`）明确解释：把对话自己的 system
  prompt、工具和消息前缀留在指令之前，让这次摘要调用成为"上一次实际路由请求的真正前缀"，从而复用供应商的
  KV/prompt 缓存而不是使其失效。这条指令还明确处理了**迭代式**重新摘要的情况："如果对话里已经包含一个
  `<compacted-summary>` 区块，它就是一份先前的 checkpoint……把新信息合并进同一份结构化摘要"
  （`summarizer.ts:63`）。`summaryText`（`summarizer.ts:216-223`）会直接拒绝模型摘要输出里出现的任何图片
  内容（`throw new LlmError('compaction summary cannot contain image output', ...)`）——摘要本身必须是
  纯文本，但本门没有确认被摘要区间**内部**的图片，究竟是被包含进发给摘要调用的内容里，还是被剥离掉了。
- **持久化/事务形状**：`compactSurfaceRegion`（`region.ts:152-249`）是一次针对 session 自己的 append-only
  事件日志的真实两阶段事务：`session.append('compaction/start', lifecycle)` 在摘要开始**之前**提交
  （`region.ts:186`），随后无论哪个阶段失败，都会恰好追加一次 `session.append('compaction/end', ...)`——
  成功路径（`region.ts:214`）或被捕获的失败路径（`region.ts:225`，带上错误信息：
  `{ ...lifecycle, error: errorChain(error) }`）。`assertCompactionInactive`（`region.ts:284-296`）是一把
  持久化的锁：它检查事件日志里有没有未配对的 `compaction/start`，如果已有一个压缩事务开着，就拒绝开始
  新的——在开始前检查一次（`region.ts:168-170`），在一次异步策略决策之后，通过单独导出的
  `assertNoActiveCompaction` 再检查一次（`region.ts:299-306`）。自动压缩要求存在一个**打开的 turn**，
  手动压缩要求**没有**打开的 turn（`region.ts:174-183`）——在类型层面就实现了与 turn 生命周期的互斥
  （`owner: 'current-turn' | null`）。
- **失败/重试语义**：`summarizeCompaction`（`region.ts:367-386`）明确拒绝一份没有比它替换掉的内容更小的
  摘要：`if (framedSummaryTokenCount >= prepared.shadowedRouteTokenCount) throw ...`
  （`region.ts:384-386`，信息为"summary is not smaller than the shadowed content"）。当 session 的
  surface 在选择和提交之间发生变化时，会单独抛出一个 `SurfaceChangedError`（`region.ts:78`）——是一个
  乐观并发检查，与摘要器失败区分开来，方便手动调用方分别报告两种不同的原因（`throwManualFailure`，
  `region.ts:257-278`，映射到 `'busy'`/`'commit'`/`'changed'`/`'summary'`/`'persistence'` 这几种
  `ManualCompactionError`）。这是 fail-**closed** 的：`compaction/start` 之后的任何失败，仍然会带着
  错误信息关闭这个事务括号，但压缩本身不会生效，调用方拿到的是一个带类型的错误，而不是静默回退到完整历史。
- **配置**：本门没有深挖——本门限定读取的包（`compaction-basic`）实现的是机制本身，而不是每次部署可调的
  参数（保留 token 预算、触发阈值），那些大概率存在于调用方所在的包里。

### Kimi Code——双重触发/阻断比例、自适应的上下文大小估算、与 turn 的互斥

- **触发数学**：`DEFAULT_COMPACTION_CONFIG`（`strategy.ts:18-27`）设 `triggerRatio: 0.85`，`blockRatio`
  是计算得出而非独立配置——`config()`（`strategy.ts:90-99`）推导出 `blockRatio:
  Math.max(triggerRatio, DEFAULT_COMPACTION_CONFIG.blockRatio)`，所以即使调用方把 trigger 设得更低，
  block 也不会低于它。`shouldCompact`（`strategy.ts:112-118`）在 `usedSize >= maxSize *
  config.triggerRatio` **或** `shouldUseReservedContext`（`strategy.ts:126-129`：
  `reservedSize > 0 && reservedSize < maxSize && usedSize + reservedSize >= maxSize`）时触发——通过
  `reservedContextSize: 50_000`（`strategy.ts:20`）走的是第二条独立触发路径，和 Pi 自己的预留边际概念
  相呼应，但在这里是比例触发**之外**的**附加**触发，不是替代。`shouldBlock` 对 `blockRatio` 采用同样的
  双路径结构——一旦用量越过 block 线，请求会被直接拒绝，这和"仅仅触发一次压缩尝试"是不同的两件事。
- **边界选择**：`canSplitAfter`（`strategy.ts:242-250`）在候选索引处的消息是 `user` 消息、是带待处理
  `toolCalls` 的 `assistant` 消息、或者**下一条**消息是 `tool` 结果时，拒绝在此处切分——并且额外调用
  `prefixEndsWithOpenToolExchange`（`strategy.ts:252-261`）来检测并拒绝落在一次仍未结束的多段工具交换
  内部的边界。对于 `source === 'manual'` 的手动压缩（`strategy.ts:132-138`），它是从末尾往回走、通过
  `canSplitAfter` 寻找第一个合法切点，而不是走比例驱动的自动触发路径。
- **摘要生成机制**：`compaction-instruction.md` 是一个独立的 prompt 文件（不是内嵌在 TypeScript 里），
  被设定为第一人称的延续性笔记而不是第三方报告——原文写道："以你自己持续的思路来写这份笔记——第一人称，
  现在时……不要写一份关于别人工作的第三方报告。"它明确要求保留实际运行过的命令、精确的文件路径，以及
  实际返回的值（"实际返回的具体值、关键行或错误文本……因为重新运行去恢复它们可能很慢甚至不可能"），并
  要求用对话本身实际使用的语言书写，而不是仅仅因为这份指令本身是英文就默认切到英文。
- **持久化/事务形状**：本门没有确认它是否像 Pi 和 DeepSeek Harness 那样是 append-only 日志条目——本门
  没有在 `agent-core-v2` 里找到与之等价的、`CompactionEntry` 形状的持久化记录；
  `fullCompactionCompactionCountInTurnKey` 及其相邻的几个 `defineState<...>` 调用
  （`fullCompactionService.ts:107-121`）看起来更像是内存/session-state 计数器，而不是事件日志事实。这是
  本门自身覆盖上的一个真实缺口，不是一个已确认的反面结论。
- **失败/重试语义**：`CompactionTruncatedError`（`fullCompactionService.ts:101-105`）在压缩响应在产出
  完整摘要之前就被截断时抛出。`shouldRecoverFromContextOverflow`（`fullCompactionService.ts:298-309`）
  同时检查一个带类型码的 `CONTEXT_OVERFLOW` 错误和原始 HTTP 413，并对照当前**有效**最大上下文的
  `OVERFLOW_STATUS_RECOVERY_RATIO`。最有特点的是，`observeContextOverflow`
  （`fullCompactionService.ts:311-321`）在第一次观察到真实的溢出时，会**自适应地下调**自己对该模型
  上下文大小的估计——`this.observedMaxContextTokensByModel.set(modelAlias,
  Math.floor(estimatedRequestTokens * OVERFLOW_CONTEXT_SAFETY_RATIO))`——而不是永远相信一个静态声明的
  上下文窗口。`begin()`（`fullCompactionService.ts:335-345`）在请求手动压缩但循环无法获取静默态
  （quiescence）时抛出 `COMPACTION_UNABLE`（"无法在有 turn 活动或另一个上下文变更正在进行时压缩"）——
  与活跃 turn 的互斥是通过拒绝而不是排队来实现的。
- **配置**：`maxCompactionPerTurn`（默认 `Infinity`）、`maxOverflowCompactionAttempts: 3`、
  `minOverflowReductionRatio: 0.05`（一次把转录缩小不到 5% 的压缩尝试被算作失败尝试）、
  `maxRecentMessages: 4`、`maxRecentUserMessages: Infinity`、`maxRecentSizeRatio: 0.2`
  （`strategy.ts:18-27`）——是本门直接读到的六个项目里可调面最宽的。

### Grok Build——三种命名策略、默认关闭、明确表述的 fail-open 错误哲学、以及一个投机式预计算缓存

- **触发数学**：`should_compact`（`trigger.rs:117-149`）计算
  `threshold = context_window * trigger_threshold_percent / 100`（`trigger.rs:137`），在
  `last_prompt_tokens > threshold` 时触发；一个测试夹具（`trigger.rs:159-160`）把工作默认值定在
  `trigger_threshold_percent: 85`、`target_threshold_percent: 50`——这一遍会把上下文压缩到窗口的大约
  一半，而不只是压到 85% 触发线以下，是本门读到的六个项目里 trigger/target 差距最大的一个。Partial 模式
  额外要求 `current_step >= policy.min_steps_before_compact`（默认 3，`trigger.rs:131-133`）才会触发，
  跳过很短的对话；`FullReplace` 模式忽略这个下限（`trigger.rs:129-130`）。
  `IntraCompactionConfig::enabled`（`config.rs:90-91`）默认是**`false`**——和本门读到的其它每个项目不同，
  压缩在 Grok Build 里是选择性开启的，不是默认开的。
- **边界选择**：本门找到了这个概念（"no safe split point"，`trigger.rs:57`），但没有找到实现它的函数——
  `select_turns_to_compact` 和 `get_accumulated_turns_for_compaction` 在
  `IntraCompactionError::NothingToCompact` 自己的文档注释里被点名（`trigger.rs:53-58`），但没有在
  `xai-grok-compaction` 内部找到；这个 crate 关于 inter-compaction 的模块文档直接说明按 harness 划分的
  turn 收集和清洗"留在产品宿主里"（`inter_compaction/compact.rs:11-14`），所以真正的边界选择大概率活在
  `xai-grok-shell` 里，在本门的读取范围之外。
- **摘要生成机制**：三种真正独立的、有名字的策略，而不是一套算法配几个模式。Inter-compaction
  （`inter_compaction/compact.rs:1-14`）在 `Basic`（无界的分块预算，恰好一个分块）和
  `DivideAndConquer`（受 `dnc_chunk_token_limit` 限制，N 个分块）之间共享同一条分块流水线，二者只在
  单块预算上不同。Code-compaction（`code_compaction/compact.rs:1-16`）明确是保留尾部式压缩的反面："
  grok-build 不选择保留一个尾部；它摘要整个对话，从头重建一份新的历史"，流水线是
  `构建 prompt → 采样（重试+分类）→ 清理 → 组装`。
- **持久化/事务形状**：本门没有确认——本门的读取范围（`xai-grok-compaction`）自己的模块文档明确写道这是
  一层与传输无关的编排，"从不提交或持久化"（`code_compaction/compact.rs:16`）；持久化是产品宿主层面的
  关注点，本门没有读到。
- **失败/重试语义**：`IntraCompactionError`（`trigger.rs:47-80`）明确表示每一个变体在设计上都是非致命
  的——这个枚举自己的文档注释写道："所有错误都是非致命的——调用方应该记录日志并在不做压缩的情况下继续。
  最坏情况是下一次采样调用可能会以 400 失败，这和今天（完全没有压缩支持）是一样的"（`trigger.rs:47-50`）。
  这是本门读到的六个项目里表述得最明确的 fail-open 哲学。`InsufficientReduction { tokens_before,
  tokens_after }`（`trigger.rs:70-74`）是一个专门对应 DeepSeek Harness 那个"没有变小"检查的命名变体，
  通过 `max_reduction_ratio` 配置。
- **配置**：`IntraCompactionConfig`（`config.rs:82-101+`）是 `#[serde(default)]` 的——每个字段单独可选，
  都有文档化的默认值，可以从 YAML 或者一个"可能只暴露这些字段的一个*子集*"的远程 agent-config proto
  加载（`config.rs:76-81`）——本门在其它任何项目里都没看到这样明确区分"本地可配置"和"远程可配置"旋钮
  的做法。
- **工具调用/二进制内容处理**：本门没有确认——在 `xai-grok-compaction` 内部没有找到 `tool_call`/`ToolCall`
  的引用；很可能是在 `xai-grok-shell` 里按 harness 处理的 turn 收集步骤里处理的，在本门读取范围之外。
- **值得一提的相邻机制**：`xai-grok-shell` 的两遍"prefire"流程会在触发线之前投机式地预先计算一份压缩摘要，
  并按对话前缀的内容指纹缓存起来——`fingerprint_prefix`
  （`compaction_two_pass_prefire_helper_tests.rs:5-27`）经测试确认，只要前缀的内容或长度发生变化，就会
  使缓存的摘要失效，覆盖编辑或回退的情况。这是本门在其它五个项目里都没观察到的缓存优化，直接回答了
  "如何避免在触发那一刻同步付出压缩延迟"这个问题。

### Codex——一套共享生命周期覆盖三种策略、钩子可否决压缩、压缩直接接入遥测

- **触发数学**：本门没有读到——`compact.rs` 自己的 import（`compact.rs:1-48`）显示了
  `AutoCompactWindowIds` 状态和 `CodexCompactionEvent`/`CompactionTrigger` 带类型的遥测，但阈值计算函数
  本身没有在本门打开的文件里定位到。
- **边界选择**：本门没有读到——考虑到时间预算，一旦下面的共享生命周期和钩子发现被确立为 Codex 最独特的
  贡献，就把范围之外的部分放弃了。
- **摘要生成机制**：`compact_remote_v2.rs` 的 `RETAINED_MESSAGE_TOKEN_BUDGET: usize = 64_000`
  （`compact_remote_v2.rs:77`）是远程/服务器辅助摘要之后逐字保留的消息的固定 token 预算；
  `truncate_retained_messages_for_remote_compaction`（`compact_remote_v2.rs:582`）和
  `message_text_token_count`（`compact_remote_v2.rs:693`）实现了针对这个预算的截断。
  `compact_model_fallback::should_retry_with_current_model`（`compact_model_fallback.rs:8-19`）把
  `InvalidRequest`、`UnexpectedStatus`、`ContextWindowExceeded`、`UsageLimitReached`、
  `ServerOverloaded`、`InternalServerError`、`RetryLimit` 都当作可能是暂时性的、值得用当前模型重试的
  失败，而不是必须降级模型。
- **持久化/事务形状**：每一种策略——包括**完全不做摘要的 token-budget 压缩**——都被建模为同一个
  `TurnItem::ContextCompaction` 生命周期。`run_compact_task_inner`（`compact_token_budget.rs:66-91`）
  自己的文档注释（`compact_token_budget.rs:23-25,49-51`，在手动和自动两个入口点上一字不差）写得很直白：
  "Token-budget 压缩跳过了模型/服务器端摘要，转而安装一个全新的上下文窗口。它仍然被建模为压缩，这样
  压缩钩子和 `ContextCompaction` turn item 就能观察到和本地或远程压缩一样的生命周期。"函数本身：
  `run_pre_compact_hooks` → `sess.emit_turn_item_started(ContextCompaction)` →
  `sess.start_new_context_window(...)` → `sess.emit_turn_item_completed(...)` →
  `run_post_compact_hooks`（`compact_token_budget.rs:73-89`）——一次硬重置，却披着和真实摘要一模一样的
  可观测生命周期外衣。
- **失败/重试语义**：**压缩前钩子可以直接否决压缩。** `PreCompactHookOutcome::Stopped` 会在任何上下文
  变化发生之前中止整个 turn（`compact_token_budget.rs:74-76`：`return Err(CodexErr::TurnAborted)`）；
  `PostCompactHookOutcome::Stopped` 在压缩之后也是同样处理
  （`compact_token_budget.rs:86-88`）。本门读到的其它任何项目都没有暴露一个可以否决触发器已经决定要跑的
  压缩的扩展点。
- **配置**：`CompactionReason`（`compact_model_fallback.rs:29-34`）枚举了 `UserRequested`/
  `ContextLimit`/`ModelDownshift`/`CompHashChanged`——最后一个意味着压缩配置/prompt 本身的哈希发生了
  变化，即使没有新的上下文压力也会强制重新压缩；这和 Grok Build 的 `fingerprint_prefix`
  是同一类"按内容指纹让缓存失效"的想法，只是为了不同的目的（配置变更失效，而不是前缀编辑失效）独立得出的。
- **压缩直接接入了本项目自己在 Observability 上的缺口。** `record_model_fallback`
  （`compact_model_fallback.rs:21-27`）直接接收一个来自 `codex_otel` 的 `&SessionTelemetry` 参数——
  正是 2026-09-01 roadmap 门已经发现的那个专属 OTel crate（`otel/`、`otel_init.rs`）——并为每一次压缩
  fallback 事件记录带结构的 `reason_tag`/`implementation_tag` 字段。在本对照集里，Codex 是"压缩决策
  正是未来 Observability 集成会想追踪的那类事件"这一判断最清晰的证据，和 roadmap 门自己的综合结论一致。

### Maka——三种可组合的预算机制，以及显式的 fail-open 重放

- **触发数学**：`buildDefaultContextBudgetPolicy`（`context-budget-policy.ts:30-63`）从
  `defaultCompactReserveTokens(contextWindow)`（`context-budget-policy.ts:70-75`）推导出
  `reserveTokens`：上下文窗口的四分之一，上限封顶在经典值 `16_384`，窗口未知时回退到 `16_384`。它自己的
  注释解释了这个推导来自一次真实的 bug 修复，而不是随意选的："经典的 16384 预留假设的是大窗口模型；在
  一个 8K 窗口上它推出的历史预算只有 1 个 token，mid_turn 高水位线也只有 1 个 token——每一个多步 turn
  都会为了一个重放门永远无法接受的 checkpoint 去跑摘要器"（`context-budget-policy.ts:66-69`）——对本项目
  自己未来的默认值而言，这是一个具体的警示性先例，因为本项目已经支持任意小的 `-context-window` 取值，
  而一个预留常量对留给实际对话的空间没有任何下限。
- **边界选择**：本门没有深挖——鉴于下文 Maka 三机制预算拆分更具新意，把精力放在了那里。
- **摘要生成机制**：本门没有深挖；`history-compact-summarizer.ts`（319 行）和
  `history-compact-summary-validation.ts`（262 行）作为独立文件存在，但它们的 prompt 文本和校验规则
  没有打开读。
- **持久化/事务形状**：`CompactionBoundary`（`compaction-boundary.ts:52+`）是一条带类型的、持久化的记录，
  带 `kind: CompactionBoundaryKind`、`stage: CompactionStage`（`'priorReplay' | 'activeStep'`）、
  一个 `boundaryId`，以及一个把当前压缩和上一次压缩链起来的 `predecessorBoundaryId`——是一条链式
  checkpoint，不是单个可变指针。`applyRuntimeEventHistoryCompact`（`history-compaction.ts:495-547`）
  通过前缀匹配（`matchHistoryCompactCheckpointPrefix`）把最新的持久化 checkpoint 和当前事件账本重放
  对照，而不是假定这个 checkpoint 仍然适用——checkpoint 的有效性在每次读取时都会针对活的日志主动重新
  验证，而不是写下来之后就被信任。
- **失败/重试语义**：**明确的 fail-open，和本门读到的其它每个项目都不同。** 当
  `matchHistoryCompactCheckpointPrefix` 匹配失败（`history-compaction.ts:505-514`），或重放的
  checkpoint 装不进 token 预算（`evaluateHistoryCompactCheckpointReplay`，
  `history-compaction.ts:527-539`）时，`applyRuntimeEventHistoryCompact` 会返回**原始的、未压缩的
  `events` 数组，原封不动**，并打上 `decision: 'failedOpen'` 的诊断标记
  （`history-compaction.ts:513,539`），而不是抛出异常或阻塞 turn——只要对某个 checkpoint 是否仍然
  有效存疑，Maka 就宁可发送完整历史，也不愿冒着摘要不正确或过期的风险。`CompactionDecisionKind`
  （`compaction-boundary.ts:31`）把这种情况和 `'unchanged'`/`'replaced'` 一起类型化为一等结果，而
  不是一种异常情况。
- **配置——以及独树一帜地，把工具结果处理和历史摘要完全分开。** 默认策略
  （`context-budget-policy.ts:47-62`）组合了**三个独立**机制，而不是一个：`historyCompact`（摘要，
  `midTurn: { enabled: true, reserveTokens }`）、`staleToolResultPrune`（`enabled: true,
  maxResultEstimatedTokens: 2_048, minRecentTurnsFull: 2`——截断超过 2K 估算 token、且不在最近 2 个
  turn 内的旧工具结果）、以及 `activeToolResultPrune`（`enabled: true,
  maxCurrentResultEstimatedTokens: 2_048, minSupersededResultEstimatedTokens: 256,
  minStepNumber: 1`——截断**当前仍未结束的** turn 内已被取代的工具结果）。这直接回答了本对照集里其它
  任何项目都没有明确回答的一个问题：大型/二进制工具输出根本不会交给通用文本摘要一遍处理——它由一个独立
  的、更窄的、基于大小阈值的机制来剪裁，这个机制独立于（而且按 `activeToolResultPrune` 来看，甚至*早于*）
  全历史压缩被触发之前就在运行。

## 综合结论

- **append-only 日志加投影收敛的形状是真实存在的先例，不是本门自己发明的。** Pi 的 `CompactionEntry`
  加 `defaultContextEntryTransform`，以及 Maka 链式的 `CompactionBoundary` 配合读时重新验证的
  checkpoint，都是持久化的、事件日志原生的压缩记录，在投影时起作用，而不是改写历史的操作——和本项目自己
  的 append-only `EventStore`（不存在删除/改写方法）以及它现有的 `projectPriorTurns` 投影点直接兼容。
- **在读得深的实现里，触发器从来不是单个数字。** Pi 是最简单的（只有 `reserveTokens`）；其它每个项目
  都在此之上叠加了第二个信号——Kimi Code 独立的 `reservedContextSize` 路径和比例并存，Grok Build 的
  trigger/target 双阈值再加一个 `min_steps_before_compact` 下限，Maka 从窗口比例推导预留值、还带一段
  明确的小窗口 bug 修复历史。设计应该把"单一阈值"当成一种过度简化，哪怕是第一个切片也不例外。
- **对"是否会切断一次 tool-call/result 配对"的边界安全检查，凡是做得仔细的地方都不止检查一次。**
  DeepSeek Harness 检查了两次（选择时一次，事务真正提交前重新验证一次）；Kimi Code 的 `canSplitAfter`
  组合了三个独立条件（user 消息、待处理的 tool call、下一条消息是不是 tool），外加一次单独的"未结束工具
  交换"扫描。本项目自己的 Step loop 已经把一次 tool call 和它的结果当作紧密耦合的一对
  （`projectPriorTurns` 里的 `ToolCallStarted`/`ToolCallCompleted`/`ToolCallFailed`），所以一个等价的
  安全检查是很自然的延伸，而不是需要发明的新概念。
- **"压缩"不总是摘要，而这两个例外（Codex 的 token-budget 重置、Grok Build 的 `FullReplace`）仍然走的
  是和摘要式策略相同的生命周期**——Codex 的钩子/turn-item 配对无论是否发生任何模型调用都会同样触发。
  如果本项目未来想要不止一种压缩策略，Codex"一套生命周期、可插拔机制"的形状是本门读到的最干净的先例。
- **工具结果剪裁和历史摘要是可以分开的两个关注点，按 Maka 明确的三策略拆分来看**——一个大的工具输出不
  需要等待（也不需要由）全历史压缩来处理；它可以被独立地、更早地限定范围。这是本门找到的对"朴素的摘要
  流程会怎么处理不了大型/二进制内容"这个问题最直接、最具体的回答：在唯一明确处理了这个问题的项目里，
  答案是它根本不打算处理——有一个独立机制先接管。
- **Fail-open 和 fail-closed 都是真实的、刻意的选择，不是哪个项目"选错了"的疏忽。** DeepSeek Harness
  的手动路径是 fail-closed 的（带类型的错误，不静默回退）；Maka 的 checkpoint 重放是 fail-open 的
  （一旦存疑就静默回退到完整历史）；Grok Build 直接把哲学写明是 fail-open（"记录日志并继续……最坏情况
  是一次 400"）。设计必须针对每一种失败模式分别刻意选择，而不是假定某一个项目的选择就是显而易见的默认值。
- **压缩已经和本项目另外两个未设计里程碑的关注点挨在一起了。** Codex 的 `record_model_fallback` 直接
  喂给 `codex_otel::SessionTelemetry`，把 Context Engine 和 Observability 直接绑在了一起；Grok Build
  的 prefire 缓存和 Codex 的 `CompHashChanged` 原因都各自独立地引入了基于内容指纹的失效机制，这是一种
  未来无论最终选哪种具体触发数学，任何设计都可以复用的模式。

## 设计必须解答的开放问题

- **Domain 形状**：压缩是否会成为一对新的 `domain.Command`/`domain.Event`（一个 `CompactContext`
  命令产生一个 `ContextCompacted` 形状的事件，命名要考虑本项目自己的 `Session` 聚合已经把"compact"用在
  了别的意思上——见上文"本项目自己已经有什么"），而 `projectPriorTurns` 是否会改成参考最新这样一个事件
  作为水位线，效仿 Pi 的 `defaultContextEntryTransform`？还是压缩完全留在 domain 层之外，作为
  `projectPriorTurns` 自身输出之上的应用层变换，从不被持久化记录——这在持久化和重放语义上是实质性不同的
  两条路（设计必须决定：一个被压缩过的 turn 的上下文，是可以仅从事件日志重建出来，还是需要在读时重新
  跑一遍压缩）？
- **Token 分子的来源**：一次 turn 开始前的预算检查，用的是 `ModelUsageRecorded` 已经捕获到的、上一个
  turn 的 `InputTokens`（Pi 在有真实用量时优先选用的信号），还是在 session 第一个 turn、还没有任何真实
  用量之前用字符/字节估算（每个项目都有的兜底手段），还是像 Kimi Code 那样——一个**自适应修正**的上下文
  窗口估计，第一次观察到真实的 413/上下文溢出响应后就自我下调，而不是永远相信 CLI flag 声明的
  `ContextWindowTokens` 是精确的？
- **一种策略还是从一开始就多种**：只做保留尾部式摘要（Pi/DeepSeek Harness/Kimi Code/Maka 共享的形状），
  还是像 Codex 和 Grok Build 那样从第一天起就区分"不摘要的重置"和"摘要式的一遍"——如果是后者，两者是否
  共享同一个生命周期事件，就像 Codex 的 `TurnItem::ContextCompaction` 无论用哪种机制都一样？
- **工具结果处理**：第一个切片是把大型/二进制工具输出和其它一切一起塞进同一次摘要（Pi 的做法——对图片
  按固定值计费，没有特殊处理），还是效仿 Maka 的先例，用一个独立的、范围更窄的剪裁机制、在全历史压缩之前
  或与之并行运行？
- **按失败模式分别定语义，而不是一条统一策略**：一次失败或超时的摘要，是让 turn 失败（DeepSeek Harness
  的手动路径），还是静默回退到未压缩历史（Maka 的 `failedOpen`），还是记录日志、带着下一次调用可能失败
  的风险继续（Grok Build 表述的哲学）——本项目自己已有的 `application.Error` 分类体系
  （`loop.go` 里已经在用的 `CategoryModel`、`CategoryCanceled` 等）里，是否已经有一个天然适合放压缩
  专属失败码的位置，还是需要一个新分类？
- **与 turn 生命周期的并发关系**：压缩是否要求存在一个活跃 turn（DeepSeek Harness 的自动路径），还是
  禁止存在（它的手动路径，以及 Kimi Code 的 `COMPACTION_UNABLE` 拒绝），还是永远严格限定在本项目现有的
  `runStepLoop`/`RunTurn` 边界内，因为本项目已经通过它的执行租约机制
  （`internal/harness/application/turn.go`）按 session 序列化了 turn，使得一个独立的锁其实没有必要，
  因为现有的准入/租约机制已经防止了同一 session 上的并发 turn？
- **脱敏**：一份压缩摘要是否需要一个属于自己的第三个 `redact.Text` 调用点，因为一个明确要求保留"精确的
  文件路径、函数名和错误信息"（DeepSeek Harness 自己的措辞）的摘要 prompt，和脱敏本身要移除 secret 形状
  文本的目标是直接冲突的——还是现有的两个调用点（工具结果/失败、最终 assistant 消息）已经间接足够，
  因为摘要只基于已经过一次脱敏的材料构建？
- **客户端/导出可见性**：压缩是否需要自己的 ACP `session/update` 投影（这样一个实时客户端能像今天看到
  一次工具调用那样看到"上下文被压缩了"），或者在 `och export-session` 的 JSONL 里有自己的表示——还是说，
  一旦压缩作为一个 domain 事件存在，就已经被这两个界面对其它每一个 domain 事件都在用的那套通用
  事件到投影映射覆盖了？
- **资源上限**（文档规则第 4 条要求，且本项目代码里目前没有为这个功能命名过）：摘要的最大大小、每个
  turn 最多允许多少次压缩尝试（Kimi Code 的 `maxOverflowCompactionAttempts: 3` 和
  `minOverflowReductionRatio: 0.05` 是具体的先例）、以及摘要调用本身的超时——和本项目已经在 `application`
  里对 `MaxAssistantBytes`、`MaxToolResultBytes`、`MaxSteps` 等做的限定保持一致。

## 证据边界

- 以上每一条引用都追溯到对照表里那些固定的 commit，且都是在本次会话里直接打开读到的；没有任何一条是
  未经独立重新核验、直接照搬 2026-09-01 roadmap 门自己（更浅层）的表述。
- 本门不授权从任何参考项目原样照抄任何类型名、schema 形状、prompt 字符串或配置常量——只授权借鉴它们所
  代表的机制和架构选择，这和本项目此前每一份调研门对自己对照集所说明的规则一致。特别是，上文引用的任何
  prompt 文本（Pi 的 checkpoint 格式、DeepSeek Harness 的 `COMPACTION_INSTRUCTION`、Kimi Code 的延续性
  笔记框架）都没有被批准可以原样用于未来的设计或实现；这里引用它们只是作为机制的证据。
- 深度按项目做了明确披露、而非静默的不均衡：Pi 和 DeepSeek Harness 基本被端到端读完了整条
  触发→边界→摘要→提交→失败流水线；Kimi Code 和 Maka 针对各自最有特点的贡献读得比较深（自适应上下文
  大小修正；三机制预算拆分），其中一些部分（Kimi Code 的持久化形状、Maka 的边界选择和摘要 prompt）明确
  标注为未确认，而不是被默认成立；Grok Build 和 Codex 针对各自架构上最有特点的发现读得比较深（默认关闭
  加明确表述的 fail-open 哲学；钩子可否决压缩加一套生命周期覆盖三种策略），它们各自的触发/边界机制被
  明确标注为不在本门读取范围内。
- 几条具体线索被点了名但本门没有追到源头，这里明确标出而不是默默丢弃：Grok Build 的
  `select_turns_to_compact`/`get_accumulated_turns_for_compaction`（大概率在产品宿主
  `xai-grok-shell` 里，不在共享 crate 里）；被压缩区间*内部*的图片，究竟是被包含进 DeepSeek Harness
  摘要器实际发送的内容里，还是被剥离掉了（本门只确认了摘要**输出**本身会拒绝图片）；Maka 的边界选择和
  摘要 prompt 文件（`history-compact-summarizer.ts`、`history-compact-summary-validation.ts`）被确认
  存在且知道大小，但没有打开读；Codex 自己的触发和边界选择函数在本门的时间预算内没有定位到。
- 本门不对这六个项目任何一个的压缩实现的正确性、性能或安全性做审计——只针对放置位置、机制本身和先例
  价值。
- 这里的"当前状态"指 2026-09-01。本项目未来针对自己 Context Engine 的正式设计，必须权衡而不是照搬上面
  的发现；未来任何重新审视这六个项目的调研门，都必须按文档规则第 7 条重新拉取、重新阅读，而不是复用本
  文档的表述。
- 本门不选定任何设计。下一步是为 Context Engine 写一份正式设计，以上面的发现作为参考，而不是被它决定。
