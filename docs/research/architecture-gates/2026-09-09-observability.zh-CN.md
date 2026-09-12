# 可观测性架构调研门

**状态：** 完整调研证据（中文阅读版）

本文与英文正本 [2026-09-09-observability.md](2026-09-09-observability.md) 完整同步；**两者若有出入，以英文正本为准。**

**日期：** 2026-09-09

**范围：** 里程碑 10 剩下的那一半。`docs/README.md` 写着「OpenTelemetry 仍未设计，且在 eval 契约之外」，而 [2026-09-01 合并调研门](2026-09-01-context-engine-evaluation-observability-tui.zh-CN.md)说过它涵盖的四个领域「各自都仍需要一份专属的子系统架构调研门，在规范设计之前重新核验彼时的一手来源」。Context Engine 和 Evaluation 都补上了，这份是 Observability 的。TUI 的仍然欠着。

本门读的是六个对比项目实际怎么给自己装仪表、这件事让它们付出了多少代价，以及一份设计必须回答哪些问题。**它不设计也不实现任何东西。**

## 钉住的来源

直接从被 gitignore 的 `.reference/` 检出读取，commit 如下。

| 项目 | Commit | 检出日期 |
| --- | --- | --- |
| codex | `67cc3c318d` | 2026-09-01 |
| grok-build | `bb7f39d` | 2026-08-31 |
| deepseek-harness | `dd6322d6` | 2026-08-31 |
| kimi-code | `ab565e081` | 2026-09-01 |
| pi | `853a80d26` | 2026-08-28 |
| maka-agent | `afbcabdc7` | 2026-09-01 |

## 一句话版本

| 项目 | 做法 | 用 OTel 吗 | 规模 |
| --- | --- | --- | --- |
| **codex** | 专门的 `otel` crate：追踪**和**指标都有，四种导出器选择，W3C trace context 由它自己的 HTTP 客户端注入 | 是 | 约 3,731 行 |
| **grok-build** | `xai-grok-telemetry` crate：OTel 层之外还有 fastrace、span profile、Sentry、进程指标，以及自己的统一日志 | 是 | **20,329 行** |
| **deepseek-harness** | 一个遥测**端口**，OTel 是挂在它后面的一个可加载**适配器** | 是，在接缝之后 | 528(端口) + 332(适配器) |
| **kimi-code** | 自己的遥测包 —— bootstrap、client、transport、sink，端点按区域解析 | 否 | 1,196 行 |
| **pi** | 自己的遥测包，带 `noop.ts` 和 `memory.ts` 两个实现 | 否 | 935 行 |
| **maka-agent** | 运行时遥测收窄到成本：定价、LLM 调用用量、工具调用记录 | 否 | 458 行 |

**六个里有三个接了 OpenTelemetry，不是两个。** 2026-09-01 那份门写的是「六个参考项目里只有两个(Codex、Grok Build)接了真正的分布式追踪」。DeepSeek Harness 发布了 `@deepseek-ai/dsh-session-telemetry-otel`，这个包自陈的职责是把「捕获到的会话记录交给 OTel JS SDK 的日志管线」。这条更正对排序有意义，因为**这第三个例子恰恰是形状最贴合本项目的那个**。

## codex —— 追踪和指标，工具输出也一起走

`otel` crate 两种信号都带。`OtelExporter` 提供 `None`、`Statsig`（只做指标，内部用）、`OtlpGrpc`、`OtlpHttp`，而且内置的 Statsig 默认值在 debug 构建里是刻意关掉的。trace context 以 W3C 头的形式由 codex 自己的 HTTP 客户端注入（`http-client/src/client.rs`），所以一个请求可以跨进程边界一直追到后端。

指标面是「agent 形状」的而不是通用的：`record_turn_ttft`、`record_turn_cost`、`record_startup_phase`，外加计数器、直方图和计时器。

**这里有一条设计必须回答的发现。** `emit_tool_result` 会把工具名、**调用参数**、MCP server 来源、耗时、是否成功，以及工具**输出的一段预览**送进遥测。`ToolResultLogConfig` 的 `max_bytes` 默认是 2 KiB —— 所以默认不是「不带输出」，而是「带前 2 KiB」。

对本仓库来说，这一行就是全部问题所在。本项目已经会扫描工具结果、工具失败消息和最终助手文本里的 secret 形状，并在持久化、审计复制或 ACP 投影**之前**脱敏（见[secret 脱敏](../../architecture/secret-redaction.md)），而 `SECURITY.md` 把「已强制」和「未强制」分得很清楚。一个把工具输出发往网络端点的遥测导出器，是一条全新的出口路径，上面那些决定每一条都要对着它重新论证一遍 —— 这不是因为 codex 不谨慎，而是因为 codex 的威胁模型和本项目的不是同一份文档。

## grok-build —— 规模警告

`xai-grok-telemetry` 有 **20,329 行**。它不只是 OTel：还带 fastrace、一条 OTLP HTTP 路径、span profile、Sentry、进程指标、会话指标、启动与子 agent 派生事件、内存遥测、采样日志、统一日志，以及 `redact_common.rs`。

OTel 层由一个环境变量过滤（`otel_layer/mod.rs:81`），带一个编译期默认过滤器；OTLP 客户端构建失败会降级成一条警告并关掉 span 导出，而不是让启动失败。

**这个数字本身就是发现。** 这正是那种「先加个 tracing 吧」的项目最后拥有一个比本仓库好几个包加起来还大的子系统的地方。这里的任何设计都应当在写代码之前先说明自己的规模上限，就像 MCP 那一刀在动手前先对着参考实现量过一样。

## deepseek-harness —— 形状最贴合本项目的那个

`SessionTelemetryBackend` 是一个抽象端口（含协调器共 528 行），`OpenTelemetrySessionBackend` 是挂在它后面的一个可加载适配器（332 行）。有三条性质值得拿过来：

**「关闭」是一种真实的模式。** `SessionTelemetryMode.DISABLED` 完全不构造任何 SDK 状态，并装一个监听器，在记录下来的反馈只停留在本地时发出警告。关闭不是「一个把数据丢掉的导出器」，而是一种**有明确后果的缺席**。

**共享状态是必须披露的强制属性。** 每个后端都必须实现 `readonly sharing: SessionTelemetrySharingStatus`，其描述是「部署方选择的会话共享策略，向确认界面披露，用于报告记录下来的反馈是否离开本进程」。只有在完全没有挂载遥测服务时，消费方才渲染「未配置」。这个词汇由接缝拥有，所以披露不会因后端而异。

这和本仓库自己那条「`CostStatusUnavailable` 不等于零」的规则是同一种直觉：**诚实的那个状态有自己的名字，任何实现都不许悄悄拿别的东西顶替它。**

**配置在加载时就失败，包括绕开一个真实的 SDK 缺陷。** 该适配器会拒绝缺失的或非 `http(s)` 的导出器 URL，也会拒绝非正数的 `maxExportBatchSize` —— 按它们自己的注释，SDK 会接受这个值，然后在关停排空时「拼接空批次却不消费队列」，于是 `dispose` 会在队列里还有记录的情况下永远挂住。

## kimi-code、pi、maka-agent —— 三种「不采用 OTel」的方式

**kimi-code**（1,196 行）有 bootstrap、client、transport、sink、崩溃与系统指标模块，端点按区域解析，而且解析发生在它的组合根而不是遥测包里。

**pi**（935 行）在真实实现之外还提供 `noop.ts` 和 `memory.ts`，所以空实现和内存记录器是一等公民，不是测试专用件。

**maka-agent**（458 行）最窄，对本项目也最有意思：它的运行时遥测就是 `pricing.ts`、`cost.ts`、`llm-call-usage.ts`、`record-llm-call.ts`、`record-tool-invocation.ts`。它回答的是「这花了多少钱、跑了些什么」，完全不引入追踪的词汇。

**这个答案本项目已经有了，而且更严格。** `ScorerUsage` 携带显式的 `CostStatus`，`PriceTable` 是冻结并按摘要绑定的，而不可得的价格绝不会被当作已计算的零发布（见[evaluation 契约](../../architecture/evaluation.zh-CN.md)）。

## 本项目今天有什么

没有 `internal/harness/telemetry` 包，也没有任何 OpenTelemetry 依赖 —— `grep -rn "OpenTelemetry\|otel" --include=*.go internal/ cmd/` 什么也搜不到。

存在的是规范事件存储、带导出器的 JSONL 审计复制品（带哈希链、可校验），以及 eval 系统里按 Attempt 的证据清单。宪章里「可观测」这条属性今天已经有一个能工作的实现，2026-09-01 那份门也是这么说的。

所以任何设计的诚实表述**不是**「加上可观测性」，而是「为一些已经被无损、本地记录下来的事实，再加一个有损的、会向网络出口的第二视图」—— 并且要说清楚这第二个视图买到了什么。

## 一份设计必须回答的开放问题

1. **工具内容到底出不出进程？** codex 的默认值会送出参数和 2 KiB 输出。本仓库在持久化前脱敏 secret，并把工作区关在牢笼里。遥测是只带元数据（名称、耗时、计数、判定、token 数），还是在脱敏之后携带内容？「在脱敏之后」意味着那个脱敏器要成为一条它当初并非为之设计的网络出口路径的安全边界。

2. **端口加适配器，还是直接依赖？** DeepSeek Harness 那个接缝是参考集里唯一和本项目架构门禁相容的形状 —— 门禁禁止适配器之间互相 import，并把第三方客户端限制在 `adapters/` 里。一个带 `noop`、`memory`（pi 的先例）和 `otel` 适配器的 `telemetry` 端口是合身的；在 `application` 里直接依赖 OTel 则不是。

3. **追踪、指标还是日志 —— 选哪一种信号，为什么是它？** codex 选了追踪和指标；DeepSeek Harness 刻意把会话记录送进 OTel 的**日志**管线。本项目的事实本来就是事件，这比 span 更接近日志的形状。

4. **规模上限是多少？** grok-build 的 20,329 行就是那个警告。MCP 那一刀在动手前对着参考实现量过尺寸；这里也该量，而且该把数字写进设计，而不是做到一半才发现。

5. **导出器不可达时会怎样？** grok-build 降级成一条警告并关掉导出。本仓库的宪章几乎处处 fail-closed，但一个因为收集器宕了就让 Turn 失败的遥测后端，会把可观测性变成负债。**这大概是 fail-open 才对的地方，因此必须被显式论证，而不是默认继承过来。**

6. **关闭遥测的构建还会不会带上这个依赖？** MCP 的实现期复核发现，仅仅 import 那个 SDK 就会拉进六个传递模块、其中包括 `golang.org/x/oauth2`，尽管那一刀只用 stdio，`SECURITY.md` 也因此不得不更正。OTel Go SDK 的足迹必须在设计定案之前用 `go list -deps` 实测，而不是估计。

7. **有消费者吗？** 方差机制是在没有任何东西能走到它的情况下休眠装船的，而那件事被如实记成了它本来的样子：一个异常。**谁来读这些追踪、以及那个读者今天是否存在**，属于设计的第一段，而不是它的风险表。

后续的[可观测性与 OpenTelemetry 设计就绪调研](2026-09-12-observability-design-readiness.zh-CN.md)
核验了当前官方 OTel 来源，实测 Go 依赖足迹，并对上述七个问题给出有证据的建议。
这些建议仍属于调研，不是已接受的规范设计。
