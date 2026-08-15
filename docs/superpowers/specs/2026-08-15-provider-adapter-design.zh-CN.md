# Provider 合同与第一个真实 Provider Adapter

- **状态：** 已评审设计（英文规范为权威）
- **日期：** 2026-08-15
- **规范原文：** [Provider Contract and First Real Provider Adapter](2026-08-15-provider-adapter-design.md)
- **依赖：** EventStore v2 Slice 1（已合入 PR #6）、Engine 纵切、领域事件
- **下一步不在本 Slice：** Tool Runtime、Policy、MCP、审批、SQLite、JSONL、Runtime Host、ACP、TUI

英文稿是规范性来源。本文是完整同步阅读版：覆盖决策、行业对照、为何自研薄适配器，以及五步 PR Plan。SSE 分类表、codec 键列表、重建形状等实现细节以英文稿为准。

---

## 1. 这段要做什么

Open Code Harness 已经能通过 `engine.Model` 跑完一次有界 Assistant Turn。今天唯一实现是 `testkit.ScriptedModel`。Application 是唯一命令权威；EventStore v2 负责准入、提交和不确定结果解析。

本里程碑补上**第一个真实 HTTP 适配器**，让同一条 Engine 端口能打到 OpenAI 兼容的 Chat Completions SSE。DeepSeek / Kimi / MiniMax 兼容端点共用这一套实现，只在 composition 时换 Capability Profile 和 identity。Application 和 Engine 里仍然不能出现 `if provider == "deepseek"`、HTTP、API key 或厂商 SDK。

EventStore v2 接口不变。新增的是两条 log-only 领域事件：`model.request.recorded`（进模型的信封）和可选的 `model.usage.recorded`（用量 / finish / 厂商 request id / 延迟）。

---

## 2. 要不要自己实现 Provider？

**要自研，但只自研一层很薄的 Adapter，不是再造一个 LLM 运行时，也不是再造 DeepSeek Harness / Codex / Kimi。**

章程 §6.3 已经把职责写死：Provider Adapter 负责请求映射、流式响应、错误归一化、token usage 和取消。Capability Profile 描述能力。通用 loop 禁止按供应商名分支。

### 2.1 我们实现的是什么

大约一个 `internal/harness/adapters/openaicompat` 包：

- `net/http` + SSE 解析，实现已有端口 `engine.Model` / `ModelStream`
- 把 vendor 流映射成现有语法 `text_delta* → completed`
- 把 401 / 429 / 空完成 / 协议违规分类成稳定的 durable code
- 把本次请求信封和用量写成领域事实
- 默认 `go test` 走 scripted `RoundTripper`，不需要真密钥、不访问网络

我们**不**实现：多厂商路由、成本优化、prompt cache 布局、DeepSeek 专用 tool-call 修复、厂商 SDK、OAuth、模型目录、Application 静默重试。

### 2.2 为什么不能“直接用别人的”

| 选项 | 为什么不采用 |
| --- | --- |
| 把 DeepSeek Harness / Pi / Kimi / Codex / Grok Build 当库用 | 它们是 TypeScript 或 Rust 的完整 Agent 产品，不是可嵌入的 Go 端口。引入等于换内核。 |
| 引入 `go-openai` / `openai-go` / LangChainGo | 会把重试、自有流类型、自有错误树带进 `go.mod`。章程要求核心 stdlib-only；EventStore v2 承诺一个 Request ID 只打一次模型。SDK 默认重试会破坏这个合同。 |
| 继续只用 `ScriptedModel` | Engine 端口从未对真实线路证明过。没有 HTTP，就没有真实 Agent。 |
| 第一适配器做成 DeepSeek 或 OpenAI 官方 SDK | 把第一个真实路径锁死在一家厂商，Kimi / MiniMax 立刻要第二套适配器，并鼓励名称分支。 |
| 第一适配器做成 Codex 现在的 Responses API | DeepSeek / Kimi / MiniMax 兼容端点说的是 Chat Completions，不是 Responses。Codex 去掉 `wire_api=chat` 是他们的产品选择，不是行业定律。 |
| 在 Adapter 里重试（Codex `request_max_retries`、DSH `dsh-llm-retry`） | 一次已准入 Turn 下的第二次 HTTP 调用是第二次模型副作用。DSH 自己也要求 Adapter 关掉库级重试。 |

### 2.3 行业实现我们参考了什么

对照是**合同级采用/拒绝**，不是抄代码、也不是把对方仓库当依赖。2026-08-15 对照集与 [DeepSeek Harness 对照与交付顺序](../../research/architecture-gates/2026-08-15-deepseek-harness-and-roadmap.zh-CN.md) 一致。DeepSeek-Reasonix 只是社区上下文。

| 来源 | 采用 | 明确拒绝 |
| --- | --- | --- |
| **DeepSeek Harness** | 进模型即已入日志；请求信封可从日志重建；Adapter 只分类不重试；用量是事实；未知本地方案类型 fail-closed；能力是 seam 不是 vendor `if` | Cordis / 一切皆插件当内核；TypeScript 核心；DeepSeek 专用 loop 路由；异步 flush 当提交权威；复制 `EpochHeader` / `StreamChunk` 名称 |
| **Pi** | 多家厂商共用 `openai-completions` 线路；测试双体走同一端口；注入式鉴权；显式取消；很小的 wire hint（`include_usage`、字段名） | 他们的巨大兼容矩阵、成本账本、OAuth、目录、SDK 后端；本 Slice 不发 thinking/tool 事件 |
| **Kimi Code（kosong）** | 线路层与 loop 分离；Capability 对象；先看 HTTP 状态再分类；取消优先于可重试映射；配额耗尽 ≠ 429 限流 | 复制 `kosong` 类型名；视频/音频；Application 重试环；用正文正则当**主**分类器 |
| **Grok Build** | Composition-root 提供 endpoint + env key + headers；未知**配置**键可忽略；不把 Harness session 凭证泄漏到外来 BaseURL | 构造期“配置坏了就跳过继续跑”；复制 xAI sampling 类型；本 Slice 不做 ACP/TUI 切模型 UI |
| **OpenAI Codex** | Provider info 是 composition 数据；idle / header map；环境变量取密钥；429 分类而不是自动重试；Item 生命周期已与我们的 Engine 对齐 | 第一适配器改成 Responses；客户端内重试当 Application 行为；用 app-server 协议对象当领域事件 |
| **Maka** | Adapter 隔离线路；取消压过迟到的 `completed`；用量在 Adapter 归一化；执行权威仍是 Application | 用 AI-SDK `streamText` 当内核；像 Maka 现在那样推迟请求信封可重建性 |

一句话：**思想对齐行业，端口和事实模型是我们自己的。** 自研成本在 SSE 映射和分类器，不在再写一套 Agent loop。

---

## 3. 关键决策

| ID | 决策 | 理由 |
| --- | --- | --- |
| P-01 | `engine.Model` 仍是唯一消费端口；HTTP 适配器直接实现它 | 与 `ScriptedModel` 同路。生产代码没有 `if scripted` / `if openai` |
| P-02 | 第一个适配器是 OpenAI 兼容 Chat Completions SSE，不是厂商 SDK，也不是 Responses | DeepSeek / Kimi / MiniMax 兼容端点共用这条线 |
| P-03 | Capability Profile 是数据；厂商差异只进 profile / hint / identity | 章程 §6.3；禁止 Application/Engine 按供应商名分支 |
| P-04 | 流语法仍是 `text_delta* → completed`；`Usage` 只挂在 `completed`；`RunResult.Stats` 每次退出都带 | 不改 `TurnRunner` / `modeltest`。Tool/reasoning 常量本 Slice 不加 |
| P-05 | 未知 vendor JSON 忽略；协议违规 / 空完成 / 越权 tool call fail-closed；reasoning 不进 assistant 文本 | 混进 `text` 会污染可持久化的助手事实 |
| P-06 | Provider 分类可重试性；Application **不重试**；一次 `RunTurn` = 一次模型尝试 | 对齐 DSH“一次 Adapter 调用一次尝试”和 EventStore v2“一个 Request ID 不打第二次模型” |
| P-07 | `model.request.recorded`（配置了 identity 时必有）和可选 `model.usage.recorded` 是 schemaVersion-1 的 log-only 事件。Store 接口不变 | 进模型即可重建。Compact `Session` 仍然只加 Version |
| P-08 | `RequestIdentity` 可选。Scripted 测试保持 2-event 准入。HTTP composition 必须设置 identity | 真实路径强制可重建，不重写全部 scripted 测试 |
| P-09 | 密钥只来自 env/config；永不进事件、日志、metrics、`Error()` | 章程安全基线 |
| P-10 | 默认测试用 scripted `RoundTripper` + 录制 SSE；`go.mod` 保持 stdlib-only | 没有活密钥也能做工业级测试 |
| P-11 | 包在 `internal/harness/adapters/openaicompat`；可导入 `net/http` 和 `os`；不可导入 `os/exec`、Application、testkit、其他 adapter | `EnvAPIKey` 需要 `os.Getenv` |
| P-12 | 分类码在 `engine.ProviderFailure`；`durableFailure` 用 `errors.As` 解开；Application 不 import adapter | 否则 401 仍会落成 `model_startup` |
| P-13 | `RunResult.Stats` 是 usage / finish / request-id / latency 的唯一管道；失败和取消也要带上次观察到的 stats | 今日 `TurnRunner.fail` 返回空 `RunResult`，必须改 |
| P-14 | 本里程碑进模型的消息就是 `[{role:user,text:Input}]`；`Decide` 要求与 `Input` 字节相等 | Compact `Session` 没有历史。Context Engine 以后再扩展 `Messages` |
| P-15 | `engine.Model` 不加 `Identity()`；只有具体适配器有；HTTP composition 必须拷贝到 `Config.RequestIdentity` | 端口加方法会强迫 ScriptedModel 伪造 identity |
| P-16 | 默认 idle timeout **60 秒**；composition 可按 endpoint 提高 | 已锁定。无界等待比一次分类过的 transient 更差 |
| P-17 | 更换 model / adapter / profile / endpoint 必须使用**新 Request ID** | 已锁定。一个 Request ID 一次尝试 |
| P-18 | 仅当 hint 打开时发送 `include_usage`；服务器 400 **不**在 Adapter 内重试 | 已锁定。自动探测会变成第二次副作用 |
| P-19 | `User-Agent` 就是字面量 `open-code-harness`，暂不加版本 | 已锁定。有 release tag 后再加版本，不是 schema 变更 |

开放问题已全部关闭，见 P-16–P-19。

---

## 4. PR Plan（中文）

增量、可独立评审；每一 PR 都不需要活密钥即可合入。英文规范里的文件列表和测试名仍是实施时的权威清单。

### PR 1 — 领域：请求信封与用量事实

- **标题：** `domain: add model.request.recorded and model.usage.recorded facts`
- **主要文件：** `internal/harness/domain/events.go`、`commands.go`、`decide.go`、`apply.go`、`codec.go`、`record.go`（`CloneEvent`）、`historical_oracle_test.go`（`HistoricalApply` / `HistoricalDecide`）；`internal/harness/application/request_result.go` 及重建测试
- **依赖：** 无（main 已有 EventStore v2）
- **做什么：**
  - 增加 schemaVersion-1 的 log-only 事件
  - `StartAssistantTurn` 增加可选 `*ModelRequestSpec`：一次 `Decide` 产出 2 或 3 个事件，不做 preview Apply
  - `Request != nil` 时，必须恰好一条 `user` 消息，且 `text` 与 `Input` 字节相等
  - `Apply` / `CloneEvent` / `HistoricalApply` 只加 Version
  - `HistoricalDecide` 必须与 `Decide` 对齐（含 `RecordModelUsage`）
  - 重建实现精确的 2/3/4/5/6 事件形状；多余、错位、未知同 CommandID 伴生事件 fail-closed
  - **不**改 `resolveTerminalUnknown` / `durableFailure` / `allowedFailureCode`
  - 无 HTTP；`RequestIdentity` 本 PR 还不接线

### PR 2 — Engine：profile、AttemptStats、ProviderFailure，以及 Application 接线

- **标题：** `engine: add capability profile, attempt stats, and provider failure`
- **主要文件：** `internal/harness/engine/profile.go`、`model.go`、`errors.go`、`runner.go`、`modeltest/suite.go`；`internal/harness/application/service.go`、`turn.go` 及成功/失败测试
- **依赖：** PR 1
- **做什么：**
  - `StreamEvent.Usage` 仅出现在 `completed`；`RunResult.Stats` + `AttemptObserver`
  - 退出顺序：先 Snapshot，再 cancel，再 Close。失败/取消返回带 Stats、空 Text 的 `RunResult`
  - `FinishReason` 只在成功 `completed` 后为 `stop|length|unknown`；失败/取消为 `""`
  - **改 `Next` 错误映射：** 若 `errors.As` 找到带合法 Code 的 `*engine.Error`，保留 Code 和 Cause；不要对这棵树走 `isEOF`；未分类的 `io.EOF` 仍是 `CodeInvalidStream`
  - `engine.ProviderFailure` 是分类 Cause；`durableFailure` unwrap 后持久化 `failure.Code`
  - 终端事件按**类型**查找，不再假设 `Events[0]` 是 item terminal
  - 无 HTTP。未落地 P-12 / P-13 / Next remap 测试前不要合入

### PR 3 — 第一个 HTTP 适配器与架构门禁

- **标题：** `adapters/openaicompat: first HTTP Model adapter with scripted transport`
- **主要文件：** `internal/harness/adapters/openaicompat/*`、`testdata/sse/*`、`internal/harness/architecture/dependencies_test.go`
- **依赖：** PR 2
- **做什么：**
  - `New` + `Stream` 实现 `engine.Model` 和 `AttemptObserver`
  - `Identity()` 填 profile 和 wire hints
  - 封闭分类器：取消 → 状态表 → 4 KiB 正文 token 列表
  - 封闭 usage JSON 映射：先 `prompt_tokens`/`completion_tokens`，再 `input_tokens`/`output_tokens`；缓存先 `prompt_tokens_details.cached_tokens`，再 `prompt_cache_hit_tokens`
  - 默认 idle 60 秒（P-16）；`User-Agent: open-code-harness` 无版本（P-19）；仅 hint 打开时发 `include_usage`，400 不重试（P-18）
  - 非 SSE 的 200、非 200 的 2xx fail-closed；`CheckRedirect` 只返回 `http.ErrUseLastResponse`
  - 无 keyless sentinel；`http://` 仅 loopback 且显式 `AllowInsecureLoopback`
  - AST 门禁：`openaicompat` 可导入 `net/http` 和 `os`，不可导入 `os/exec` / Application / testkit / 其他 adapter
  - `go test` 默认无密钥

### PR 4 — 用真实适配器路径跑通 `RunTurn`

- **标题：** `application: RunTurn through openaicompat fixtures`
- **主要文件：** Application 场景测试（或适配器集成测试），使用 `MustComposeHTTP` + `ProfileTextOnly` / 内联 profile；memory store + fixture transport
- **依赖：** PR 3
- **做什么：** 一条成功、一条 401（durable `provider_auth`）、一条 429 配额 vs 限流、一条取消、一条空完成、一条 reasoning 隔离、一条密钥脱敏。证明准入含 `model.request.recorded`，终态在观察到用量时含 usage，且 `FindCommandRequest` 仍阻止第二次 `Stream`。

### PR 5 — 落地后的已实现合同文档

- **标题：** `docs: implemented provider adapter contract and evidence`
- **主要文件：** `docs/architecture/provider-adapter.md`、`docs/architecture/provider-adapter-evidence.md`，以及对应 zh-CN 阅读版
- **依赖：** PR 4
- **做什么：** 记录测试强制执行的行为，体例对齐 `engine-vertical-slice.md` 和 `eventstore-v2.md`。设计阶段不写这篇；代码落地时才写。

---

## 5. 回滚与范围

- 回滚：停止构造 `openaicompat.New`。Compact Apply 只加 Version，旧 scripted 路径不受影响。
- 本 Slice 不做 Tool、Policy、MCP、SQLite、ACP、TUI。
- 本 Slice 不把任何厂商 SDK 加入 `go.mod`。

---

## 6. 权威说明

- 规范性设计：英文稿 `2026-08-15-provider-adapter-design.md`
- 本文：中文阅读版；若与英文稿冲突，以英文稿为准
- 行业对照证据：`docs/research/architecture-gates/2026-08-15-deepseek-harness-and-roadmap.zh-CN.md`
- 章程：`docs/superpowers/specs/2026-08-11-open-code-harness-architecture-design.md` §6.3
