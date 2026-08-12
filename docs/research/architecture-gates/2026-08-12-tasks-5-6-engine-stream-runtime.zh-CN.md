# Task 5–6 架构门：Engine Stream 与 Runtime 边界

- 日期：2026-08-12
- 范围：Engine 计划 Task 5–6，并影响 Task 7–10
- 初始结论：**READY_WITH_AMENDMENTS**
- 修订落地后结论：**READY**
- 评审时实现状态：尚未开始

## 核心证据与取舍

| 项目 | 一手资料与事实 | 我们的决定 |
| --- | --- | --- |
| OpenAI Codex | [`app-server README`](https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md)、[`common.rs`](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/common.rs)、[`responses_websocket.rs`](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/endpoint/responses_websocket.rs)、[`compact.rs`](https://github.com/openai/codex/blob/main/codex-rs/core/src/compact.rs)：Item 生命周期是 `started -> zero or more deltas -> completed`，completed 是权威结果；transport 在 `response.completed` 前结束会报错，消费者遇到显式 completed 即停止。Codex 内部也使用 channel、transport task、retry 和 fallback。 | 采用显式完成与 premature EOF 失败；不能据此推导 Engine 应暴露 push/channel、detached work 或隐藏重试。 |
| Kimi Code | [`AGENTS.md`](https://github.com/MoonshotAI/kimi-code/blob/main/AGENTS.md)、[`transcript/AGENTS.md`](https://github.com/MoonshotAI/kimi-code/blob/main/packages/transcript/AGENTS.md)、[wire mode](https://moonshotai.github.io/kimi-cli/en/customization/wire-mode.html)：transcript 拥有自身合同和 cold rebuild 来源，记录操作保持顺序与 scope sequence，wire replay 只读且有序；中断时 `TurnEnd` 可以缺失，retry 可以替代 partial output。 | 采用合同所有权、scope 单调顺序、transcript/runtime 分离；严格 ModelStream grammar 拒绝其宽松中断结束与 retry 语义。 |
| Maka | [`ARCHITECTURE.md`](https://github.com/maka-agent/maka-agent/blob/main/ARCHITECTURE.md)：Runtime Host 是执行权威，Runtime Event Log 是消息、工具结果与终止事实的 canonical source，UI、recovery、context 是 projection。 | 采用唯一执行权威、持久事实与 transient delivery signal 分离；该来源未定义 pull、Close、UTF-8 或 byte limit。 |
| Pi | [`agent-loop.ts`](https://github.com/badlogic/pi-mono/blob/main/packages/agent/src/agent-loop.ts)、[`types.ts`](https://github.com/badlogic/pi-mono/blob/main/packages/agent/src/types.ts)、[`agent-session.ts`](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/src/core/agent-session.ts)：loop 注入 stream function、传递 AbortSignal、消费 async iterable，并等待内部 lifecycle delivery；公开 wrapper 启动 detached async work，AgentSession 还拥有 retry、compaction 以及等待规则不同的 listener。 | 采用依赖注入、显式取消、有序 lifecycle 与 awaited delivery；同步 Engine 边界拒绝 detached push wrapper 和宽泛 session policy。 |
| MiniMax Mini-Agent | [`Mini-Agent`](https://github.com/MiniMax-AI/Mini-Agent)、[`agent.py`](https://github.com/MiniMax-AI/Mini-Agent/blob/main/mini_agent/agent.py)：MiniMax 官方 demo 注入 LLM client 并运行 bounded step loop，但调用 provider-specific unary `generate`，取消只在 step boundary 检查。 | 只采用小型、可注入、有界 loop 的经验；它不提供 ModelStream、Close、UTF-8 或 delivery 合同。 |
| DeepSeek-Reasonix | [`ARCHITECTURE.md`](https://github.com/esengine/DeepSeek-Reasonix/blob/main/docs/ARCHITECTURE.md)：架构分离 immutable prefix、有序 append-only history 与 volatile scratch，并加入 provider-specific cache repair、retry 和 model escalation。 | 只把有序历史与 transient scratch 分离作为类比；Engine 拒绝 cache/repair/retry。它是 `esengine` 社区项目，不是 DeepSeek 官方仓库。 |
| Go 标准库 | [`io`](https://pkg.go.dev/io)、[`errors`](https://pkg.go.dev/errors)、[`context`](https://pkg.go.dev/context)、[`unicode/utf8`](https://pkg.go.dev/unicode/utf8)：只有协议允许时 EOF 才表示正常结束；`errors.Is` 遍历单链和 joined unwrap tree；Context 跨 API 传播取消；`utf8.ValidString` 校验完整字符串。 | structured stream 使用显式完成、full-tree error matching、传递取消与 per-delta UTF-8 校验。`io.Closer` 未规定首次调用后的行为，因此 exactly-once Close 由本项目自行定义。 |

上述项目的公开资料均**不能**同时证明本项目所需的 exactly-once Close
及其错误优先级、delivery 前 byte bound、Engine 无 detached work、typed-nil-safe
code lookup 或 failing-sink 记录语义。这些是本地合同，不能借外部项目名义臆推。

## 必须修订的合同

### 1. Emitter 独占 correlation 与 attempt order

调用者提交 `RuntimePayload`，而不是已填戳的 `RuntimeEvent`。Emitter 在创建
时校验不可变的 Session/Turn/Item/Command correlation，并为每次 sink 调用
填入该 correlation 与下一 ordinal。调用者填写 correlation/ordinal 必须被
拒绝，不能信任或与 Emitter state 静默混合。

ordinal 从 1 开始，只表示单个 command attempt 内的 delivery-attempt 顺序，
并在 sink attempt 前立即分配。sink 失败仍消耗 ordinal，后续 attempt 使用
`N+1`；payload 非法或 attempt 前已取消则不消耗。Emitter 只属于一次 run，
使用后不可复制，也不支持并发调用。ordinal 不是 durable sequence 或 global
clock。

### 2. 集中校验 RuntimePayload 与 RuntimeEvent

校验必须发生在填戳和调用 sink 之前：

- `started`、`completed`、`append.completed` 要求 Text、Code 均为空；
- `text_delta` 要求 Text 非空且是合法 UTF-8，Code 为空；
- `failed`、`interrupted` 要求 Text 为空，Code 是 1–64 ASCII byte 的稳定 token：
  首字符 `[a-z]`，其余仅 `[a-z0-9_]`；
- 若 Task 5–6 没有真实 consumer，`diagnostic` 应延期；若保留，则要求非空
  稳定 Code 与合法 UTF-8 Text；
- 未知 runtime type 是 caller contract error，不是 provider
  `invalid_stream`。

RuntimeSink 只能看到已完整校验、完整填戳的 RuntimeEvent。

### 3. 空 model delta 非法

`StreamEvent{Type: text_delta, Text: ""}` 返回 `invalid_stream`，不得 delivery、
累计或分配 runtime ordinal。provider keepalive 与空 transport chunk 由 adapter
过滤。空的最终 assistant answer 仍合法，用立即 `completed` 表示。

model grammar 精确为 `text_delta* -> completed`。completed 前 EOF、未知事件、
completed 携带非空 Text 均为 `invalid_stream`。Runner 遇到 completed 后立即停止
Next；它不能额外读取并宣称侦测到 completed 之后的潜在事件，因此 adapter
合同必须禁止此类事件。

### 4. 每个 acquired stream Close 恰好一次，并保持错误优先级

Model.Stream 返回的每个非 nil stream 都由 Runner 接管并 Close 恰好一次，
包括 `(stream, err)`、started delivery 失败、Next 失败、EOF、非法事件、取消、
output limit 和成功。nil stream 永不 Close。失败路径先 cancel 派生 context
再 Close；显式 completed 后先 Close 再 cancel，避免人为制造 cleanup failure。

若显式 completed 原本成功而只有 Close 失败，run 以 `model_stream` 失败，因为
成功事实尚未提交。若已有 primary failure，Close 不得替换其 stable code；以
一个外层 `engine.Error` 保留 primary Code，并令 Cause 为
`errors.Join(primaryCause, closeCause)`。不得 join 多个各自带 code 的 Engine
error，否则 `IsCode` 会出现多义性。

`Close() error` 没有 context，因此 adapter 合同必须要求 prompt、同步 teardown，
并 join 其拥有的 transport work。Engine 自身不启动 channel/goroutine，Close
返回后不得残留工作。

### 5. 定义所有 Model.Stream 与 ModelStream.Next 的 value/error 组合

| 调用结果 | 必须行为 |
| --- | --- |
| `Stream(non-nil, nil)` | acquisition 成功，立即建立 exactly-once Close ownership。 |
| `Stream(nil, nil)` | `invalid_stream`，不 Close。 |
| `Stream(nil, err)` | `model_startup`；若 caller context 已取消则为 `canceled`。 |
| `Stream(non-nil, err)` | event source 不可用，但 Runner 接管并 Close 一次；primary 为 `model_startup` 或 `canceled`。 |
| `Next(event, nil)` | 按 grammar 校验与处理 event。 |
| `Next(any value, err)` | 完全忽略 value，不 delivery、不累计；context cancellation 优先，EOF 映射 `invalid_stream`，其他错误映射 `model_stream`。 |

因此 poisoned `text_delta + error` 和 `completed + error` 都不能产生输出或完成。
在 Stream、每次 Next、event processing 与 sink delivery 前检查取消；依赖返回
错误且 caller context 已取消时，primary code 为 `canceled`。

### 6. 在任何副作用前执行 UTF-8 byte boundary

每个 model delta 严格按此顺序处理：非空检查、`utf8.ValidString`、剩余 byte
limit 检查、RuntimeSink delivery、builder 精确追加。超限 delta 既不 delivery
也不累计；恰好等于 limit 合法。已接受字符串必须 byte-for-byte 拼接，不得
trim、normalize、replacement 或 rechunk。

per-delta 校验意味着 adapter 不得跨两个 StreamEvent 拆分一个 UTF-8 code
point。size check 需避免溢出，例如先确保当前长度未超限，再判断
`len(delta) > limit-builder.Len()`。

### 7. IsCode 必须遍历完整 error tree，并对 typed nil 安全

`IsCode` 必须在 `errors.Join` 任意分支、任意深度找到匹配 Engine error。只调用
一次 `errors.As` 不够，因为前方不同 code 的 Engine error 会遮蔽后续 sibling。
优选 `errors.Is(err, &Error{Code: wanted})`，并实现 code-aware `Error.Is`，同时
保证 `Is`、`Unwrap`、`Error` 对 nil receiver 安全；也可实现等价显式 tree walk。

测试必须覆盖：匹配位于第二/中间 sibling、nested joins、首个 Engine error
code 不匹配而后续匹配、direct typed-nil `*Error`、`errors.Join` 内 typed nil、
普通 nil、非法 requested code。任何路径都不得调用会解引用 typed-nil receiver
的方法。

### 8. RecordingSink 分离 Attempts 与 Delivered

RecordingSink 在同一 mutex 内先把完整填戳 event 记录进 `Attempts`，再执行
确定性故障注入。失败调用存在于 Attempts、但不存在于 Delivered；成功调用
同时进入两者。两个 snapshot 都必须防御复制。

`FailOrdinal` 表示首次匹配 attempt 的 one-shot failure，并在同一 mutex 内
消费。非零 `FailOrdinal` 的 fixture 限定单个 Emitter 使用，因为多个 command attempt
都可能有 ordinal 1；禁用故障注入时可以共享。这样既保留 Engine 尝试过什么的证据，又不会把失败 delivery 伪装
成已送达。

### 9. 明确 concurrency boundary，并证明实际执行的并发场景

共享 Model 可以并发接收 Stream 调用。每个返回的 ModelStream 只有一个 consumer：
禁止并发 Next/Close、禁止跨 turn 复用、completed 后禁止 Next。ModelRequest 与
fixture 记录使用防御复制。共享 production RuntimeSink 必须支持不同 Emitter
并发调用；否则 adapter 必须提供显式 per-run factory。inline Emit 等待 sink，
由此形成 backpressure。

contract tests 覆盖并发独立 Stream acquisition、请求精确捕获、cancellation
barrier、single-consumer stream、Close 次数、Emitter 顺序、防御 snapshot 与
sink failure，并使用 Go race detector 执行。race detector 只能发现已执行路径
上的 race，不能替代书面 ownership 合同。

## Adopt / Reject / Defer

| 决策 | 内容 |
| --- | --- |
| Adopt | consumer-owned Model/ModelStream/RuntimeSink port；同步 pull；inline sink backpressure；显式 completed；premature EOF 失败；Emitter-owned correlation/ordinal；精确合法 UTF-8 bytes；delivery 前 bound；exactly-once cleanup；durable terminal fact 与 transient runtime delivery 分离；正式 adapter contract suite。 |
| Reject | push callback/channel 作为 Engine API；Engine-owned detached goroutine；caller 填戳 identity/order；空 delta；EOF 即成功；completed payload；校验/bound 前 delivery 或累计；忽略或重复 Close；Close 替换已有 primary code；持久化 token delta；global runtime ordinal；隐藏 retry/cache/repair/fallback；并发消费同一 stream。 |
| Defer | production provider adapter 及其内部有界 transport queue；retry/attempt policy；prefix cache 与 repair；tool/reasoning/usage event；持久 runtime log、catch-up cursor、global sequence；`Close(ctx)` 或 cleanup timeout；没有当前 consumer 的 diagnostic event。 |

## 最终 Task 5–6 合同

Task 5–6 只有在已接受设计和中英文计划写入以下修订后才可开始：

1. 用已校验 RuntimePayload 替代 caller-populated RuntimeEvent，并定义
   Emitter-owned correlation/ordinal。
2. 在 grammar 中明确空 delta 与全部 event-field 组合。
3. 定义 exactly-once Close ownership 与 primary-versus-cleanup precedence。
4. 规定 Stream/Next 的所有 value-plus-error 组合。
5. 在 delivery 和累计前执行合法 UTF-8 与 byte limit。
6. 使 IsCode 在 typed nil 存在时安全遍历完整 joined tree。
7. RecordingSink 分离 Attempts/Delivered，并定义 one-shot failure。
8. 明确 Model、ModelStream、Emitter、RuntimeSink 的 concurrency ownership，
   并用 race-enabled contract tests 覆盖实际执行路径。
9. runtime delta 保持 transient，所有 retry/cache/repair policy 排除在本 Engine
   milestone 之外。

上述修订落地后，本架构门结论为 **READY**。
