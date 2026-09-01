# Context Engine、Evaluation、Observability 与 TUI 架构调研门

**状态：** 调研证据完成

**日期：** 2026-09-01

**范围：** `docs/README.md` 里程碑状态里还有四项没有设计：里程碑 7（TypeScript TUI
客户端——目前只有一个 Go 版 ACP 原生终端客户端和一个网页轨迹 UI 作为垫脚石，更完整的
TUI 本身"仍未规格化"）、里程碑 8（Context Engine——持久化和恢复已经实现，但模型可见
上下文的选择、预算和压缩本身还没设计）、以及里程碑 10（场景评测、基准测试和
OpenTelemetry——"尚未设计"）。里程碑 9（MCP 客户端适配器）已经有自己的调研门
（[2026-08-30-mcp-client-adapter.md](2026-08-30-mcp-client-adapter.md)），不在本文范围内。

本文读的是项目自己官方对照集六个项目（`docs/superpowers/specs/2026-08-11-open-code-harness-architecture-design.md`
第 12 节：Codex、Pi、DeepSeek Harness、Kimi Code、Grok Build、Maka）各自怎么实现——或者刻意不实现——
这四块，并记录阅读过程中顺手发现的、本项目里程碑表根本没提到的能力缺口。本文不做任何设计或实现。
按文档规则第 1 条，Context Engine、Evaluation、Observability、TUI 这四块各自仍然需要一份专属的、
重新核验当时最新一手资料的架构调研门，才能进入正式设计——本文建立的是比较方向和排序建议，就像
[2026-08-30 客户端界面与安全加固顺序决策](2026-08-30-client-surface-and-security-sequencing.md)
对 exec 沙箱和 ACP 原生客户端所做的那样，不能替代那些未来的调研门。

英文版本 [2026-09-01-context-engine-evaluation-observability-tui.md](2026-09-01-context-engine-evaluation-observability-tui.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。

## 对照集与钉住的 commit

按文档规则第 8 条，这些都是同一批 gitignored 的 `.reference/` 检出，通过
`./scripts/fetch-reference.sh --list` 直接读取。按第 7 条，本次不需要重新抓取：自
[2026-08-31 网页轨迹 UI 调研门](2026-08-31-web-trajectory-ui.md)上次在同一状态重新核验过这同一批六个项目以来，什么都没变过。

| 项目 | 仓库 | Commit | 观察日期 | 为什么读它 |
| --- | --- | --- | --- | --- |
| Codex | `openai/codex` | `a9519cb` | 2026-08-31 | Rust；有专门的 `otel` crate、`turn_diff_tracker.rs`、走子代理路由的审批、三种明确区分的压缩策略（`compact.rs`、`compact_token_budget.rs`、`compact_remote_v2.rs`） |
| Pi | `earendil-works/pi` | `853a80d` | 2026-08-28 | TypeScript；跟下面 `pi-mono` 的 HEAD 完全相同（原因未查，跟本文无关，MCP 调研门之前也记过一笔）；有真实的行为评测 harness，从零手搓的终端工具包 |
| Pi（agent core 源码） | `badlogic/pi-mono` | `853a80d` | 2026-08-28 | 本文实际读的 `packages/agent`/`packages/tui`/`packages/evals`/`packages/telemetry` 源码都在这个仓库，不在 `earendil-works/pi` 那个镜像里 |
| DeepSeek Harness | `deepseek-ai/deepseek-harness` | `0a53fb5` | 2026-08-30 | TypeScript/Cordis；六个里压缩包拆得最细，有真正的 OTel 日志导出，没有终端 TUI，基准测试只是个外部约定 |
| Kimi Code | `MoonshotAI/kimi-code` | `8f2c60b` | 2026-08-31 | TypeScript；直接 vendor 了 Pi 的终端工具包（`packages/pi-tui`），压缩用的是两级 trigger/block 比例 |
| Grok Build | `xai-org/grok-build` | `bc7f02e` | 2026-08-28 | Rust；六个里压缩实现最深（intra/inter/code 三种、Basic 与 DivideAndConquer 两种分块策略、两遍预取），用 `fastrace` 包了一层 OTLP tracing，TUI 基于 `ratatui` |
| Maka | `maka-agent/maka-agent` | `ef94235` | 2026-08-31 | TypeScript；既有真实的评测 harness（`packages/eval` + Python `harbor/`），也有真实的长期记忆层；桌面端 React 的 `packages/ui` 和基于 `pi-tui` 的终端 `packages/cli` 是两套完全独立的界面 |

## Context Engine（上下文压缩）

六个项目全部实现了压缩；本项目一个都没实现（`internal/harness` 里没有任何压缩相关的
command、event 或预算概念）。

- **Pi**（`pi-mono/packages/agent/src/harness/compaction/compaction.ts`）：
  `DEFAULT_COMPACTION_SETTINGS`（第 158–162 行）固定 `reserveTokens: 16384`、
  `keepRecentTokens: 20000`；`shouldCompact(contextTokens, contextWindow,
  settings)`（第 247 行）在 `contextTokens > contextWindow - settings.reserveTokens`
  时触发。`estimateContextTokens`（第 216 行）优先用最近一次真实 provider `usage`
  块，没有才退回字符数估算。`findValidCutPoints`/`findCutPoint`（第 312、374 行）
  从末尾往回走累计 `keepRecentTokens`，再吸附到最近的一个合法切点——一个 `message`
  条目、角色是 `user`/`assistant`/工具调用边界，绝不切在一对进行中的工具调用/结果
  中间——并报告这个切点是否切在了一个进行中的 turn 里面（`CutPointResult.isSplitTurn`）。
  另外，`.../session/context.ts` 的 `buildSessionContext`/`buildContextEntries`
  （第 45–100 行）永远把历史折叠到最近一个 `compaction` 条目开始的那段：压缩本身是
  session 日志里一个真实的领域事件，发给模型的上下文是它的一个纯投影，不是第二份
  可变的记录。
- **DeepSeek Harness**（`packages/compaction/compaction-basic/src/region.ts`、
  `summarizer.ts`）：`selectCompactableRange`（第 100 行）从末尾往回走累计
  `retainTokens` 尾部，并拒绝选一个会切开工具调用/结果对的边界
  （`toolPairingBalancedBefore`/`After`）。`compactSurfaceRegion`（第 154 行）是一个
  真正的事务：在开始摘要之前先追加 `compaction/start`，无论哪个阶段失败都保证恰好
  追加一次 `compaction/end`（失败时带错误载荷），并且拒绝一份摘要如果它的 token
  开销并不比它替换掉的内容更小（`summarizeCompaction`，第 367 行，
  `framedSummaryTokenCount >= prepared.shadowedRouteTokenCount`）。
  `buildSummarizationInput`（第 508 行）把 session 自己最近一次请求头（系统提示词、
  工具 schema）作为压缩指令之前的真实前缀重放一遍，就是为了让摘要调用能复用
  provider 的 prompt 缓存。`summarizer.ts` 的 `COMPACTION_INSTRUCTION`（第 31–66 行）
  是一段手写的、结构固定的 Markdown checkpoint 格式（"Primary Request and Intent"、
  "Files and Code"、"Pending Jobs" 等），以*最后一条用户消息*的形式发送而不是单独的
  系统提示词，同样是为了复用缓存。
- **Kimi Code**（`packages/agent-core-v2/src/agent/fullCompaction/strategy.ts`）：
  `DEFAULT_COMPACTION_CONFIG`（第 18–27 行）跟 Pi 单一保留 token 阈值的形状完全不同——
  两个独立比例，`triggerRatio: 0.85`（何时开始）和 `blockRatio: 0.85`（何时强制阻塞
  后续请求），外加 `reservedContextSize: 50_000`、`maxOverflowCompactionAttempts: 3`、
  `minOverflowReductionRatio: 0.05`（一次压缩如果没能把记录缩小至少 5%，就算作一次
  失败的尝试）。`fullCompactionService.ts` 和 `compactionOps.ts` 实现具体的执行；
  `compaction-instruction.md` 是一个独立的提示词文件，不是内联字符串。
- **Grok Build**（`crates/common/xai-grok-compaction/src/{intra,inter,code}_compaction/`）：
  六个里实现最深。`intra_compaction/trigger.rs`（第 117–160 行）计算
  `threshold = context_window * trigger_threshold_percent / 100`（默认 85%），
  外加一个独立的 `target_threshold_percent`（默认 50%）——压缩要把占用压到这个目标
  以下，不只是超过阈值才触发。`inter_compaction/compact.rs` 的头部注释（第 1–10 行）
  说明这是一条被两种策略共用的分块管线——`Basic`（无界分块预算，正好一块）和
  `DivideAndConquer`（有界 `dnc_chunk_token_limit`，N 块）——两者唯一的区别就是每块
  的 token 预算。`code_compaction/compact.rs` 的头部注释（第 1–16 行）明确这是*第三种*
  独立策略："grok-build 不选一个尾部保留下来；它把整段对话摘要一遍，从头重建一份
  全新的历史"，管线是 `build prompt → sample（重试+分类）→ clean → assemble`。产品
  宿主里还有一条独立的两遍"预取"流程
  （`crates/codegen/xai-grok-shell/src/session/compaction_two_pass_prefire_helper_tests.rs`），
  印证了这个 crate 自己声明的"按 harness 的触发和传输逻辑留在共享 crate 之外"。
- **Codex**（`codex-rs/core/src/compact*.rs`）：三种明确并列的独立策略，而不是一个
  算法带几种模式。`compact_token_budget.rs` 的 `run_manual_compact_task`/
  `run_inline_auto_compact_task`（第 26–56 行）文档注释写得很直白："Token 预算压缩
  跳过模型/服务端摘要，直接装一个全新的上下文窗口。它仍然被建模成压缩，所以压缩钩子
  和 `ContextCompaction` turn item 能观察到跟本地或远程压缩一样的生命周期"——也就是说，
  一个不摘要、直接重置的策略是一等公民压缩策略，不是挂在摘要类策略上的兜底路径。
  `compact_remote_v2.rs`（第 1–20 行的 import）是模型/服务端辅助摘要的路径，接到
  `compact_model_fallback::should_retry_with_current_model`；`codex_analytics::CompactionTrigger`
  在类型层面就明确区分了 `Manual` 和自动触发。
- **Maka**（`packages/runtime/src/history-compact*.ts`、`compaction-boundary.ts`）：
  六个里第二深，除核心的 `history-compaction.ts`（573 行）外，还有专门的 checkpoint
  （771 行）、ledger、summarizer、summary-validation、checkpoint-coordinator 文件。
  `compaction-boundary.ts`（第 24–30 行）定义了一个 `CompactionBoundaryKind` 分类
  （`historyCompact` / `staleToolResultPrune` / `activeToolResultPrune`），以及一个
  `CompactionDecisionKind`：`unchanged` / `replaced` / `failedOpen`——对一个没能
  完成的压缩决策明确采用 fail-*open* 语义，这在本文读到的其他项目里都没有专门命名过。

**小结：** 每个实现都共享同样的三个动作——从 token 估算和模型上下文窗口对比决定要不要
压缩、找一个工具调用安全的切点、把被切掉的区域替换成一条模型生成的摘要消息——但除此之外
就没有更多共识了。触发的算法从单一保留 token 余量（Pi）到独立的 trigger/block 双比例
（Kimi Code）到 trigger 加 target 两个数（Grok Build）各不相同。有两个项目（Codex、
Grok Build）把"不摘要直接重置"和"整段摘要重来"当成跟保留尾部式压缩并列的独立命名策略，
而不是同一个谱系的两端。"压缩即一次被记录的事务"（DeepSeek Harness 的
`compaction/start`/`compaction/end` 括号、Pi 的 `compaction` session 条目、Grok
Build 的 `ContextCompaction` turn item）是这六个项目里最接近共识的一点，也正好是本项目
自己事件溯源的 Domain 层天然适合的形状。

## Evaluation（评测）

分化明显：六个里两个真的有一套面向场景/行为的评测 harness；三个完全没有评测/基准
包（已确认，不是没搜到）；一个只有外部约定。

- **Pi**（`packages/evals`）：`README.md`（第 1–4 行）说这个包把一个真实的
  `AgentSession` 适配给第三方库 `vitest-evals`，并且"在隔离的临时项目和 agent 目录
  里运行"。`src/pi-harness.ts` 的 `createPiCodingAgentHarness`（第 246 行）是每个
  测试套件绑定一个的 harness 构造器。每次运行会写一个 gitignore 掉的 `.eval/`
  产物目录；`runs.jsonl` 把已完成的运行连同它们在 `sessions/` 下的原生 Pi session
  JSONL 一起索引（README 第 32–34 行）——harness 自己真实的 session 格式就是评测
  产物本身，不是专门为评测发明的合成转录格式。
- **Maka**（`packages/eval` + `harbor/`）：`README.md`（第 20–24 行）说这个包
  "拥有实验语义。它自己不执行 Maka，也不构造 Runtime 对象"，并画出
  `Experiment → Cells → Attempts → Results` 的图，由一个独立的 `Runtime Host`
  执行真正的 agent——评测编排跟它测量的 harness runtime 是刻意解耦的。
  `packages/eval/harbor/` 是一个并行的 Python 层（`eval_framework.py`、
  `run_trial.py`、`egress_filter.py`、一个出网代理的 Docker compose 文件），实现
  了带网络出口控制的沙箱化试跑。`packages/eval/experiments/` 里有真实的、已提交的
  对比运行记录，包括
  `terminal-bench-2.1-deepseek-v4-flash-maka-vs-deepseek-harness.json`——一次跟
  本项目自己六个参考项目之一的公开正面对比。
- **DeepSeek Harness**（已确认）：仓库根目录的 `BENCHMARK.md` 只有三行，指向手动
  跑 Python SDK 的 `jsonrpc-agent` 变体、用独立的 workspace 和 session ID——没有
  仓库内的 harness、runner 或打分包。
- **Codex、Kimi Code、Grok Build**（已确认的负面发现）：三个里都没有面向场景/质量
  的评测包。三个各自有但不是这类东西的：Codex 的
  `codex-rs/cli/e2e_benches/codex_help.rs`（一个 20 行的 `divan` 微基准，测的是
  `codex --help` 进程启动耗时）、Kimi Code 的 `packages/minidb/bench`、Grok Build
  的好几个 `crates/codegen/*/benches` 目录——都是特定子系统的 Rust/JS 性能微基准，
  不是 agent 质量或场景正确性评测。本项目未来的评测里程碑更接近 Pi 和 Maka 在做的
  事，而不是这三个项目所说的"bench"。

**小结：** Pi 的模式——一个真实的 harness 对象驱动真实 session，通过一个外部库做
模型评判，产物按 harness 自己原生的 session 格式索引——是跟本项目自己章程最贴合的
先例（§6.9："Eval Runner 直接驱动应用层或 headless Engine，不依赖 TUI"）。Maka 明确
的 Experiment/Runtime-Host 分离是第二个可用先例，专门用来说明如何把评测编排跟
`internal/harness/application` 解耦。

## Observability（可观测性）

真正分化：六个里两个接了真实的 OpenTelemetry tracing；其余的要么只把 OTel 用在日志、
要么自己搞一套 typed span schema、要么做的是产品分析而不是 tracing、要么记的是
成本/用量领域事实而不是 tracing。

- **Codex**（`codex-rs/otel/`）：一个专门的 crate。`Cargo.toml:379–383,478` 钉死了
  精确版本 `opentelemetry = "0.31.0"`、`opentelemetry-otlp`、`opentelemetry_sdk`、
  `opentelemetry-semantic-conventions`，以及 `tracing-opentelemetry = "0.32.0"`——
  跟本项目自己对依赖版本的钉死纪律是一样的。`otel/README.md`（第 1–9 行）描述了
  给日志/trace/指标导出器做的 provider wiring，外加"通过
  `codex_otel::SessionTelemetry` 做 session 级业务事件发射"。
  `core/src/otel_init.rs` 把 `build_provider`（第 16 行）、`record_process_start`
  （第 97 行）、`install_sqlite_telemetry`（第 104 行）暴露为组合时的接入点。
- **Grok Build**（`crates/common/xai-tracing/src/fastrace.rs`）：包的是更轻的
  `fastrace` crate，不是直接用 `opentelemetry` crate。`init_fastrace`（第 12 行）
  构造了一个 `fastrace_opentelemetry::OpenTelemetryReporter`（第 27 行），依然是
  走真实的 OTLP 导出。`current_trace_id`（第 38 行）和
  `enter_span_with_traceparent`（第 46 行）编码/解码 W3C `traceparent` 头，
  `TraceparentMiddleware`（第 69 行）在一个 HTTP 客户端上传播它们——是真正的跨进程
  trace 上下文传播，不只是本地 span。
- **DeepSeek Harness**（`packages/session/session-telemetry-otel/src/index.ts`）：
  只把 OTel JS SDK 用在**日志**上——`LoggerProvider` + `BatchLogRecordProcessor` +
  一个 OTLP/HTTP 日志导出器（模块文档注释，第 4–6 行；import，第 30–32 行）——不是
  trace 或 span。一个 `SessionTelemetryMode` 枚举（第 45–47 行）：`FULL` /
  `FEEDBACK_ONLY` / `DISABLED`，默认 `DISABLED`（第 51 行，
  `DEFAULT_TELEMETRY_MODE`），控制到底有没有东西离开这台机器；一条禁用态的警告
  字符串（第 53 行）明确写着什么都不会分享出去，反馈留在本地。
- **Pi**（`packages/telemetry`，`@earendil-works/pi-telemetry`）：自己的一套
  typed 事件/span schema（`src/index.ts` 的 `AttributeValue` 联合类型，第 1 行；
  `src/memory.ts` 的 `RecordedTelemetryEvent`/`RecordedTelemetrySpan`，第 11–20
  行），参考实现是一个内存记录器。这是 span 形状的（名字、属性、事件），但不是
  OpenTelemetry——没找到 OTLP 导出器，也没找到对 `opentelemetry` crate/包的依赖。
- **Kimi Code**（`packages/telemetry/src/client.ts`）：一个带队列的产品分析事件
  客户端，不是 tracing。`TelemetryClient`（第 29 行）带 `deviceId`/`sessionId`
  （第 33–34 行），通过一个注入的 `EventSink`（第 31 行）发送——跟产品分析 SDK
  （Amplitude/Segment 那种）是同一种形状，确认是真正 span/trace tracing 的一个
  负面发现。
- **Maka**（`packages/runtime/src/provider-request-telemetry.ts`）：名字带
  telemetry，实际是成本/用量核算，不是 tracing。`ProviderRequestUsage`/
  `ProviderRequestAttemptRecord`（第 44–92 行）和 `ResolvedModelCallCost`（第
  125–131 行）记录的是一次 `ModelCallAttempt` 的用量和折算成美元的成本——在这个
  对照集里，结构上最接近本项目自己的 `PolicyDecisionRecorded` 领域事件
  （`internal/harness/domain/apply.go:94`、`codec.go:210–297`）：一条结构化的、
  持久的、关于某次决策或调用的事实，记在跟其他一切一样的同一个事件日志里，而不是
  一条带外的 trace。

**小结：** 本项目章程已经把"Observable"列为必需的质量属性（§3.2："模型调用、工具
调用、审批、压缩、重试和错误均有结构化 trace"），而且今天已经通过持久的、可回放的
领域事件（`ModelAttempt`、`ToolExecution`、`PolicyDecisionRecorded`）实现了它，
而不是靠一个外部 tracing 系统——结构上更接近 Maka 和 DeepSeek Harness 的
`session-telemetry-otel` 在做的事（领域事实，可选导出），而不是 Codex 或 Grok
Build 那种实时 span tracing。未来接入 OTel 到底是一个真正独立的需求（跨进程 trace
关联、延迟分位数、一个标准化的可视化面板），还是本项目现有的审计轨迹已经基本满足了，
是一个留给设计阶段回答的开放问题，本文不做结论。

## TUI

- **Pi**（`packages/tui`）：从零手搓。`package.json:47–54` 只列了两个运行时依赖——
  `get-east-asian-width` 和 `marked`——外加两个仅开发用的依赖（`@xterm/headless`、
  `chalk`）；整个代码树里没有 `ink`、`blessed` 或任何终端 UI 框架。
  `src/components/`（17 个文件：`box.ts`、`select-list.ts`、`markdown.ts`、
  `image.ts`、`editor.ts`、`scroll-view.ts` 等）是一个手写的组件库；`src/` 本身
  还带了一个有撤销栈的编辑器（`undo-stack.ts`）、自动补全、备用屏幕处理。审批/确认
  UI 在 `packages/coding-agent/src/modes/interactive/interactive-mode.ts`
  （`ui.confirm`/`showExtensionConfirm`，第 2437–2571 行）——一个很大（6000+ 行）
  的文件，是本项目自己 `cmd/acp-client` 的 `PermissionPrompter` 最接近的功能对照物，
  只不过是给一个本地运行的 agent 循环用的，不是给一个 ACP 客户端用的。
- **Kimi Code**（`packages/pi-tui`）：确认是一份 vendor 的 fork，不是独立重新实现。
  它自己的 `AGENTS.md`（第 3 行）直接写着："`packages/pi-tui` 是 upstream pi-mono
  项目里 pi-tui 的一份 vendor 拷贝（基线：upstream 0.80.2，见 commit
  `7859b0af`）……不再通过 pnpm patches 打补丁——所有本地修复都直接改在源码上。"
  `package.json` 把它命名为 `@moonshot-ai/pi-tui`；`CHANGELOG.md`（第 21、27 行）
  记录了定期对 upstream `@earendil-works/pi-tui` 发布版本重新拉基线，同时明确保留
  一份具名的本地补丁清单（窄终端加固、粘贴突发回退、多根 `@` 补全）。
- **Grok Build**：采用了 `ratatui` 生态，不是手搓也不是 fork。
  `crates/codegen/xai-ratatui-textarea/Cargo.toml:9–10` 直接依赖
  `ratatui`/`ratatui-core`；`crates/codegen/xai-grok-pager/Cargo.toml:27,88–89`
  依赖 `ratatui` 加上本项目自己的 `xai-ratatui-textarea` 和 `xai-ratatui-inline`——
  一个搭在被采用的库上分层构建的 pager，而不是一个单体 TUI crate。
- **DeepSeek Harness**（已确认的负面发现）：`apps/cli/src/` 一共六个文件、841 行；
  `bin.ts`（50 行）解析 argv，分发给 `profile-boot.ts` 或 `dump-config.ts`——没有
  渲染循环，没有按键处理，没有任何组件库。这印证了之前的发现：DeepSeek Harness
  主要的交互界面是基于浏览器的，不是终端 TUI。
- **Maka**：两套完全独立的界面，比"以包拆分的形式存在"更精确。`packages/ui` 自己
  的 `README.md`（第 20 行）写着这是"Maka **桌面应用**的共享 UI 层……被
  `apps/desktop` 的 renderer 消费"——一个 React 19（`package.json:32–33`）的 GUI
  组件库（`stories/` 里有 `session-list-panel.stories.tsx`、
  `model-picker.stories.tsx`、`sandbox-boundary-prompt.stories.tsx`），根本不是
  终端渲染器。真正的终端客户端在 `packages/cli` 里，而且它跟 Kimi Code 用的是*同一个*
  上游工具包：`packages/cli/package.json:24` 直接依赖
  `"@earendil-works/pi-tui": "0.84.2"`（发布的包本身，不是 vendor 拷贝），通过一批
  命名为 `pi-tui-runner.ts`、`pi-tui-layout.ts`、`pi-tui-transcript-viewer.ts`、
  `pi-tui-turn.ts`、`pi-tui-pickers.ts`、`pi-tui-mcp-status.ts` 的文件消费。

**小结：** 六个里有三个（Pi 自己、Kimi Code、Maka）把终端客户端建在同一个工具包
血统上——Pi 自己的 `@earendil-works/pi-tui`——要么是它的源头，要么是一份维护中的
fork，要么是直接依赖；这是一个真实的收敛，不是三个独立的选择。Grok Build 是六个里
唯一采用了一个既有终端 UI *框架*（`ratatui`）、而不是 Pi 血统内某种形式的项目。
DeepSeek Harness 完全没有终端 TUI。这个对照集里没有一个项目是用 Go——本项目自己的
语言——手搓终端 UI 的，所以不管未来的设计选"搭"还是"建"，六个里都没有一个同语言的
现成参照。

## 顺手发现的、里程碑表没提过的缺口

- **子代理/任务委托**——六个里四个有，Pi 没有（已确认：`packages/coding-agent/src`
  里搜不到任何 `subagent`/`sub-agent` 命中）。Codex 的实现比名字听起来窄：
  `codex-rs/protocol/src/config_types.rs`（第 177–200 行）把*审批决策*（不是一般的
  任务执行）路由给一个 `auto_review` reviewer，描述为"一个经过精心设计提示词的
  subagent"，还保留了一个 `guardian_subagent` 的旧别名做兼容——这是一个专门用于
  策略审核的子代理，不是一般的工作委托。Kimi Code 的
  `packages/protocol/src/task.ts`（第 5 行）把 `taskKindSchema` 定义成
  `z.enum(['subagent', 'bash', 'tool'])`——一个任务本身就可以作为三种执行类型之一
  被路由给一个子代理。DeepSeek Harness 的面最大：一个 `packages/subagent/` 组，
  十个包（`subagent`、`subagent-in-process-driver`、`subagent-fork-in-process`、
  `subagent-spawn-in-process`、`subagent-acp`、`subagent-codex`、
  `subagent-claude-code`、`subagent-dsh-sdk`、`tool-subagent`、
  `tool-subagent-control`、`tool-subagent-report`）——分别对应进程内、fork、spawn
  三种委托驱动，外加桥接到直接把 Codex 或 Claude Code 本身当子代理跑。Grok Build
  有一个专门的 `crates/codegen/xai-grok-subagent-resolution` crate（本文没有深入
  读它的内容）。
- **超越事件回放的长期/跨 session 记忆**——只有 Maka。
  `packages/storage/src/sqlite-long-term-memory-store.ts` 和
  `long-term-memory-store.ts` 实现了一个跟 session/runtime 事件日志完全独立的
  持久化层；`packages/core/src/long-term-memory.ts` 和
  `packages/runtime/src/session-recap.ts` 是把它重新投影回一个 session 的消费层。
  这跟本项目自己事件溯源的回放是完全不同的概念：回放重建的是*一个* session 自己的
  历史；Maka 的长期记忆持久化的是*跨* session 的事实。
- **结构化的按 turn diff 追踪**——只有 Codex。
  `codex-rs/core/src/turn_diff_tracker.rs`（模块级常量在第 8–19 行，比如
  `DIFF_TIMEOUT: Duration = Duration::from_millis(100)`，带一个记录在案的病态输入
  兜底路径）按 turn 追踪对工作区文件的改动，结构化程度足以重建一份真正 git 风格的
  diff，而不是依赖一个通用 exec/patch 工具的原始输出。

## 非目标交叉核查

对照 `docs/superpowers/specs/2026-08-11-open-code-harness-architecture-design.md`
第 4 节读过一遍：以上发现没有一条跟已声明的非目标冲突。子代理/任务委托，就本文在
那四个有它的项目里读到的实现而言，是本地、进程内的（一个 agent 自己的循环调用它
自身的另一个实例，或者同一个 runtime 里的一个兄弟 agent）——不是被排除的"A2A、远程
Agent daemon 和分布式多 Agent 协作"（第 24 行：A2A、远程 agent daemon 和分布式
多 Agent 协作明确推迟到 v0 之后，不过第 24 行同时也写了架构不能阻止以后以 adapter
形式加进来）。本地子代理委托和远程 A2A 是两个不同的问题；本文没有把它们混为一谈，
未来的设计也不应该。超越回放的长期记忆不是"续 personal-harness"，也不是 Obsidian
插件/知识库产品（第 4 节)——它是一个 harness 内部的持久化问题，不是产品方向的
重新定位。上面提到的其他发现都没有碰到任何一条其他非目标（没有 v0 云端控制平面/
团队/计费的诉求，没有把 TUI 行为当成 Engine 正确性唯一验证方式的诉求，没有提出
任何一个尚无消费者的投机性扩展点）。

## 排序建议

这是给未来的调研门和设计阶段参考的建议，不是承诺。

**Context Engine 优先。** 六个参考项目全部实现了它，而且比本文覆盖的其他任何一块
都做得更深（Grok Build、Maka、DeepSeek Harness 尤其如此），并且它是其他三块的结构性
前提：Evaluation 需要有点东西可供回归测试，而不只是测现有功能；Observability 最有
意思的 span 恰恰就是压缩决策本身；未来的 TUI 需要把压缩/摘要事件渲染进轨迹里。
**Evaluation 紧随其后**——Pi 的模式（一个真实的 harness 对象驱动真实 session、隔离
的 fixture 目录、按 harness 自己原生 session 格式索引的产物）是跟本项目自己章程和
现有 `composition` 层 fixture 驱动 provider 先例（`README.md`："跑一轮工具调用对话，
针对一个真实数据库和一个 fixture 驱动的 provider——没有网络也没有凭据"）最贴合的
先例，先把它建好，才能让 Context Engine 以及之后的一切被回归测试，而不是靠肉眼看。
**Observability 优先级较低**——六个参考项目里只有两个（Codex、Grok Build）接了真正
的分布式 tracing，另外四个里的三个用的是结构上更接近本项目自己现有审计事件路线的
东西，而且本项目章程的"Observable"属性今天已经有一个不依赖 OpenTelemetry 的可用
实现。**TUI 是最不紧迫的**——按 2026-08-30 的排序决策，它已经排在两级垫脚石客户端
后面了，而且跟 Context Engine 或 MCP 不一样,即便在这个参考集里也没有一个收敛的
"就抄这个"的答案（三个收敛到 Pi 的工具包血统，一个采用 `ratatui`，一个干脆没有）——
早做只会花力气而没有清晰的参照可抄,而且依然解锁不了 Context Engine、Evaluation
或 Observability。

## 设计阶段必须回答的开放问题

- **Context Engine**：压缩要不要像本项目其他决策一样走一个独立的领域 command 和
  event（对应 DeepSeek Harness 的 `compaction/start`/`compaction/end` 括号和 Pi
  的 `compaction` session 条目），还是内联在现有 Step 循环里作为上下文组装的副作用？
  token 预算的真源是什么，考虑到 `engine.Model` 的能力档案已经带着
  `ContextWindow`/`MaxOutput`（`internal/harness/composition/config.go:23`、
  `assembly.go:122`）？本项目要不要从一开始就只采用一种压缩策略（保留尾部式摘要，
  最接近 Pi/DeepSeek Harness/Kimi Code/Maka），还是像 Codex 和 Grok Build 那样，
  从一开始就有多种具名策略并存？
- **Evaluation**：真实模型驱动的评测（Pi 的路线）跟本项目自己现有 composition
  层 fixture 驱动 provider 的先例——是互补的两层，还是对第一个切片而言一个能取代
  另一个？评测编排要不要完全放在 `internal/harness/application` 之外，效仿 Maka
  明确的 Experiment/Runtime-Host 分离？
- **Observability**：本项目现有的结构化审计事件集合（`ModelAttempt`、
  `ToolExecution`、`PolicyDecisionRecorded`）是不是已经满足了章程"Observable"
  这条质量属性，还是说 OpenTelemetry 是一个真正独立的需求（比如跨进程 trace
  关联——本项目今天没有分布式部署拓扑，所以还没有这个用例）？如果要接，是直接包
  原生 `opentelemetry` Go SDK（Codex 的路线），还是一层更轻的中间层（Grok Build
  的 `fastrace`）？
- **TUI**：用 Go 从零手搓（这个参考集里没有一个同语言的先例可参照，不管往哪个方向）,
  还是采用一个现成的 Go 终端 UI 库,考虑到参考集本身就在手搓（Pi，以及通过 Pi 工具包
  延伸出的 Kimi Code 和 Maka）和采用现成框架（Grok Build 的 `ratatui`）之间分裂,
  六个项目并没有形成共识？

## 证据局限

- 上面每一条引用都可以追溯到对照表里钉住的 commit；没有一条结论是凭记忆、单靠一个
  项目 README 的宣传性语言，或者不经重新核验就照搬某份更早调研门的说法得出的。
- 本文不授权照抄任何参考项目的类型名、schema 形状、提示词字符串或配置常量——只是
  这些机制和架构选择本身值得参考，跟本项目之前每一份调研门对自己对照集声明的规则
  一样。
- 本文没有审计这六个项目 Context Engine、Evaluation、Observability 或 TUI 实现的
  正确性、性能或安全性——只看了摆放位置、机制和参考价值。
- 各个话题读的深度是刻意不均的：Context Engine 和 TUI 对每个项目读了多个文件、
  给出行级引用；Evaluation 和 Observability 主要读了入口点和包级结构,一旦一个项目
  的整体形状清楚了就没有再深挖——这跟本文的路线图性质范围相符，不是像 MCP 客户端
  适配器调研门那样的单一子系统深挖。
- 有几条线索点到了但没有深入读，这里明确记一笔而不是悄悄丢掉：Grok Build 的
  `xai-grok-subagent-resolution` crate 具体内容；DeepSeek Harness 十个
  `packages/subagent/*` 包除了名字之外的内容；有没有任何参考项目对压缩或 MCP
  服务器子进程做了操作系统级沙箱。
- "当前状态"在本文里指 2026-09-01。未来任何一份重新审视这些项目、或者把这四个
  话题之一收窄成自己正式设计的调研门，都必须按文档规则第 7 条重新抓取、重新阅读，
  不能直接沿用本文的表述。
- 本文不为这四个话题里的任何一个选定设计。不管先启动哪一个，下一步都是它自己的
  正式设计,参考上面的发现,而不是被它们决定。
