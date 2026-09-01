# Context Engine 架构门（中文阅读版）

**状态：** 完成的调研证据

**日期：** 2026-09-01

**规范源：** [2026-09-01-context-engine.md](2026-09-01-context-engine.md)。若中英文语义不一致，以英文规范源为准。

**范围：** 只覆盖里程碑 8 尚缺失的 Context Engine：模型可见历史投影、token 预算、安全压缩边界、摘要、checkpoint 持久化、手动压缩与 Provider 溢出恢复。已经实现的 EventStore、JSONL 审计副本、Runtime Host 和崩溃调和继续作为持久化基础，不在这里重做。Evaluation、OpenTelemetry、TUI、跨 Session 长期记忆与 MCP 都是独立里程碑。

## 为什么还需要专项架构门

较宽的 [Context Engine、Evaluation、Observability 与 TUI 架构门](2026-09-01-context-engine-evaluation-observability-tui.zh-CN.md) 已经确定 Context Engine 应当优先，但明确没有选择实现方案。文档规则 7、8 要求子系统规范前重新阅读直接相关的一手源码。

2026-09-01 再次确认并阅读了六个 `.reference/` pinned checkout；commit 与宽门一致，没有把任何参考项目源码复制进本仓库：

| 项目 | 仓库 | Commit | 本次复核重点 |
| --- | --- | --- | --- |
| Codex | [`openai/codex`](https://github.com/openai/codex) | `a9519cbcdd` | 本地摘要、token-budget reset、remote v2、模型 fallback 与测试 |
| Pi | [`badlogic/pi-mono`](https://github.com/badlogic/pi-mono) | `853a80d26` | 阈值、token 估算、安全 cut point、rolling summary 与回归测试 |
| DeepSeek Harness | [`deepseek-ai/deepseek-harness`](https://github.com/deepseek-ai/deepseek-harness) | `0a53fb55` | compaction transaction、region、summarizer、token meter、tool-result pruner |
| Kimi Code | [`MoonshotAI/kimi-code`](https://github.com/MoonshotAI/kimi-code) | `8f2c60b32` | 双水位、full compaction、handoff、overflow 上限与并发保护 |
| Grok Build | [`xai-org/grok-build`](https://github.com/xai-org/grok-build) | `bc7f02e` | intra/inter/code 策略、trigger/target、两遍预取与 chunking |
| Maka | [`maka-agent/maka-agent`](https://github.com/maka-agent/maka-agent) | `ef94235ba` | budget、checkpoint coverage/digest/lineage、ledger 恢复、边界与摘要校验 |

## 本仓库的实际现状

项目已经不是字面意义上的“完全没有历史上下文”，但仍没有独立 Context Engine：

- `internal/harness/application/loop.go` 的 `projectPriorTurns()` 能从事件重建历史消息，但只有非空 Tool Catalog 启用 Step loop 时才运行；model-only 路径仍只发送当前输入。
- 唯一上限是 4 MiB 的 `MaxProjectionBytes`，没有使用模型的 `ContextWindowTokens` 与 `MaxOutputTokens`。
- 没有压缩 command/event、checkpoint、摘要 prompt、安全切点、手动操作或 rolling 语义。
- admission 记录的首个 `ModelRequestRecorded` 发生在历史投影之前，后续 Step 又只记录 suffix；持久化事实不能独立等价于真正发给 Provider 的消息包络。
- EventStore 已有 pinned 分页读，Domain aggregate 已有有界状态，SQLite 已在 append transaction 中维护派生投影，Runtime Host 已有崩溃调和；Context Engine 应复用这些边界。
- composition 已拒绝为零的 ContextWindow/MaxOutput，因此预算可以 fail closed，不必猜未知容量。
- OpenAI-compatible Adapter 已把可信的 400/413/422 上下文超限响应归一化成 `context_overflow`，Application 不必重新解析厂商错误字符串。

## 六项目共同支持的不变量

### 预算来自当前路由模型

六个项目都让模型容量参与决策，但计算方式不同。可移植要求不是复制某个百分比，而是每次决定都明确容量来源、输出预留、安全余量、触发线、目标线和实际请求形状。

本项目以 composition-time `CapabilityProfile` 为容量权威；一个确定性、可替换的 meter 估算 provider-neutral wire envelope。Provider usage 是审计和校准证据，不直接替换当前请求的预算判断。

### 切点是协议边界，不是任意消息下标

安全边界必须保证：

1. 首选完整 Turn；
2. 超大历史 Turn 可在已经关闭的 assistant/tool Step 之间切；
3. 不能拆开 assistant tool call 与结果；
4. 当前 open assistant item 和不完整 tool pair 永远受保护；
5. model request、usage、policy、approval 和 compaction 事件是证据，不是对话来源。

### 压缩必须是持久化 transaction

六项目最接近共识的做法是 first-class compaction lifecycle。成功摘要不能只存在内存：

```text
started → completed(checkpoint) | failed
```

Checkpoint 覆盖有序源前缀；未闭合 started 是需要恢复的工作，不是可用 checkpoint。

### Event log 仍是真相，checkpoint 只是可丢弃投影

Maka 与本项目架构最贴合：checkpoint 保存 coverage、source digest、predecessor lineage 与当前预算重放结果；bounded latest-checkpoint projection 可从 canonical ledger 恢复。任何摘要都不能删除、改写或替代 EventStore/JSONL 的事实权威。

### 保留近期原文并滚动摘要

正常 successor 只输入“旧有效摘要 + 新进入压缩区间的 canonical units”，而不是每次重新总结 Session 全生命周期。模型、prompt version 或质量规则改变时，仍可从完整 EventStore 做 full rebuild。

### 摘要必须可用且确实缩小

非空字符串不够。必须验证固定 section、UTF-8、byte/token cap、截断、脱敏和净缩小；不合格输出关闭为 failed，不能落成 checkpoint。

## 本项目选择

### 同步 pre-provider 压缩

不采用会与继续增长的 Step loop 竞争的后台摘要。自动压缩只在干净边界同步执行：

- pre-turn：admission 前；
- mid-turn：现有 Turn owner 位于两个 Step 之间时；
- manual：拒绝 active Turn；
- overflow：只恢复尚未产出任何 delta 的启动期 overflow。

因此没有迟到 candidate，也没有“摘要生成期间历史是否变化”的竞态。

### 两遍 pinned scan

第一遍分页扫描只计算 digest、unit、token、边界，并保留一个有界 recent deque；若需要压缩，第二遍在同一 pinned head 下重读选中的 sequence range，按有界 chunk 送给 summarizer。低于 trigger 的请求最多持有一个模型请求预算，不能为了找 cut point 把整个 Session 读进内存。

### 一个摘要机制，加一个确定性 reset fallback

本次完整引擎有两个 checkpoint variant：

- `rolling_summary_v1`：正常的 LLM-assisted、保留尾部的滚动摘要；
- `source_tail_reset_v1`：只有 hard limit 或已确认 overflow 且无法得到可信摘要时才使用的确定性、无事实声明 fallback。

Reset 只声明“更早 canonical history 没有进入本次模型上下文”，不声称其内容。手动 compact 默认不静默 reset，必须显式选择。Provider-native opaque compact 需要未来以真实 Adapter 为前提另行设计。

### Tool Result prune 是相邻 rewrite，不是第二权威

超大 Tool Result 的模型投影保留 call/result 身份，正文改成 bounded head/tail excerpt、原始 byte count 与 digest；完整原文仍在 canonical event。每次真正发送的 projection 都会在 Provider dispatch 前被完整记录。Prune 不产生另一条 checkpoint 链，也不承诺本次实现 ArchiveRead 工具。

### 专用摘要模型必须显式配置

默认复用当前 agent model。只有显式配置独立 summary route 时，历史才可发往另一 Provider；其 credential 仍来自环境变量，只持久化非秘密 route identity。取消不允许 fallback。

## 必需失败语义

- 仍低于 hard budget 时，摘要失败记录 failed bracket，并继续使用旧有效 checkpoint 或完整 source-derived request。
- 已到 hard budget 或发生可信 startup overflow 时，可持久化 deterministic reset + 最新完整 raw tail；绝不捏造 summary。
- Candidate 只有 exact append 已提交或 unknown outcome 已解析为 committed 后才可使用。
- Version conflict 使 candidate 失效，重新从新 pinned head 规划，不能强写。
- Cancellation 在每个 Provider call 与 append 前获胜；迟到模型输出丢弃。
- Overflow retry 必须先发生可测量的缩小，并受 per-Turn 上限约束；已经发送 delta 的 mid-stream failure 不重试。
- 单个受保护 unit 即使经过 bounded tool projection 仍无法容纳时，返回稳定的 `context_unit_too_large`。
- 过期、畸形、source mismatch 或在当前模型预算下不合格的 checkpoint 必须拒绝并从 canonical events 重建或绕过。
- Runtime startup 把未匹配的 durable start 关闭为 failed，不能合成 completed。

## 后续规范必须落实

1. 纯 `contextengine` 包：meter、projector、planner、checkpoint validator、materializer；
2. Application-owned orchestration 与明确 command/event；
3. 精确预算公式和资源上限；
4. pre-turn、mid-turn、manual、overflow 四条流程；
5. rolling/reset checkpoint 的 coverage、digest 与 lineage；
6. memory/SQLite bounded latest-checkpoint projection 及 canonical rebuild；
7. 每次 Provider dispatch 前记录完整实际 request envelope；
8. conformance、fuzz、race、crash、mutation 与长 Session 证据。

## 明确排除

- vector retrieval、embedding、semantic memory、跨 Session memory；
- 删除或压缩 canonical EventStore/JSONL 数据；
- 没有真实 Provider 支持的 opaque native compaction；
- 后台/推测式 compaction；
- 非 context Provider 错误的通用 retry；
- MCP、ArchiveRead、TUI、OpenTelemetry、scenario evaluation；
- 复制参考项目的类型、prompt、schema 或常量。

## 证据限制

- 当前结论只对应表中 pinned commits 与 2026-09-01 的观察时间。
- 本门比较机制与放置方式，不替参考项目证明摘要质量或生产正确性。
- 通用 meter 无法对所有兼容 endpoint 承诺 tokenizer-exact；保守估算和闭合 overflow recovery 用来约束这种不确定性。
- Codex/Maka 的 provider-native state 只证明 checkpoint union 未来可能扩展，不授权今天伪造一个通用 remote compact endpoint。
