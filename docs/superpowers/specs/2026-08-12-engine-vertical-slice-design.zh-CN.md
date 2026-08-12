# 工业级 Engine 最小纵切设计

- 状态：用户已接受
- 日期：2026-08-12
- 仓库：`open-code-harness`
- 权威性：英文版为规范性文档
- 英文规范版：`2026-08-12-engine-vertical-slice-design.md`
- 依赖：`2026-08-11-open-code-harness-architecture-design.md`
- 基于：`docs/architecture/domain-events.md`

## 1. 定位

Open Code Harness 是面向工业应用的开源 code-agent harness。本里程碑刻意控制
能力范围，但它不是 Demo、教程、原型或一次性集成。“最小”只限制一次交付的能力
数量，不降低一致性、取消、并发、资源有界、错误处理、测试和可运维性要求。

仓库目前仍处于 pre-v0。工业级是工程目标和交付纪律，不代表当前仓库已经达到
通用可用（GA）的生产发布状态。

本里程碑交付第一个可执行的 application/Engine 纵切：通过既有领域状态机运行
一个由模型驱动的 Turn；原子记录持久事实；暴露具备背压的有界流式进度；显式终结
每一个已经开始的对象；并通过确定性重放证明结果。`ScriptedModel` 与
`MemoryEventStore` 是用于确定性验证的正式适配器，不是 Demo 专用旁路。

## 2. 目标与成功定义

当无界面调用方能够完成以下行为时，本里程碑才算完成：

1. 创建或加载 Session；
2. 通过乐观并发控制准确启动一个 Turn；
3. 执行一次与供应商无关的流式模型尝试；
4. 在背压约束下观察有序运行时进度；
5. 持久记录 assistant message 的终态事实；
6. 准确一次地完成、失败或中断 Turn；
7. 将已存储事件流重放为同一最终状态；
8. 无需网络、API Key、TUI、ACP 客户端、工具或墙上时钟，即可复现成功、失败、
   取消、冲突和输出边界场景。

它的准确名称是 **Minimal Executable Turn Runner（最小可执行 Turn Runner）**，
还不是完整的工具型 Agent Loop。在具备 model → tool → result → model 循环前，
命名必须保持诚实。

## 3. 行业项目实证评审

本设计只把官方仓库、官方文档与语言/运行时文档作为一手证据。社区项目只能作为明确
标注的非权威上下文，不能单独建立本项目合同。只有符合 Open Code Harness 章程的思想
才会被采用；参考项目不是本项目依赖，也不直接决定本项目 API。

### 3.1 Pi agent core

Pi 的核心循环小而可注入：接收流式函数、工具集、上下文转换钩子、取消信号、
事件接收器以及 steering/follow-up 队列；它能够顺序或并行执行工具，并发布细粒度
内存事件流。

采用：

- 小型、可注入模型行为的 Engine 核心；
- 显式传递取消；
- 流式生命周期事件；
- 以确定性测试注入替代供应商条件分支。

不照搬：

- 把内存消息列表当成持久领域权威；
- 在首个仅模型纵切中混入未来的 Tool/Policy 职责；
- 只靠事件发射器保证一致性或恢复。

证据：<https://github.com/badlogic/pi-mono/blob/main/packages/agent/src/agent-loop.ts>

### 3.2 MiniMax

公开的 `MiniMax-AI/minimax-code` 只是桌面产品的问题反馈仓库，没有暴露产品实现，
因此本文不据此推断 MiniMax Code 内部架构。

官方开源 Mini-Agent 集成完整模型/工具循环、持久笔记、上下文总结、Skills、MCP
以及请求/工具日志。它是有价值的可运行参考，也展示从模型流到工具执行的直接路径；
但其职责集合和偏 MiniMax 的 API 路径超出当前里程碑。

采用：端到端可运行验证、完整执行证据、用户路径与确定性测试使用同一边界。

不照搬：把 Engine 绑定到单一兼容 API；在内存、Skills、MCP、上下文压缩和工具
尚无契约及评测边界前同时引入它们。

证据：

- <https://github.com/MiniMax-AI/minimax-code>
- <https://github.com/MiniMax-AI/Mini-Agent>

### 3.3 Kimi Code

Kimi Code 在 TypeScript monorepo 中分离 CLI/TUI、server、client SDK、provider
抽象、执行环境、transcript、存储和 agent engines；当前还包含 DI × Scope Engine，
具有 App、Workspace、Session 和 Agent 生命周期，以及服务端和有序 transcript 操作。

采用：UI/服务端消费 Engine 契约而不拥有循环；transcript/运行时信号具有显式顺序
和生命周期；执行环境与 provider 抽象不进入领域层。

当前不照搬：通用 DI 容器、四级生命周期 scope、双代引擎、服务端端点、SDK facade
以及首个应用边界尚未证明前的 transcript 复制体系。

证据：<https://github.com/MoonshotAI/kimi-code/blob/main/AGENTS.md>

### 3.4 OpenAI Codex

Codex app-server 暴露 Thread → Turn → Item 原语和显式
`started → delta* → completed` 通知，并使用生成式协议 schema、有界队列、过载错误、
取消、审批和独立传输面。

采用：每个流式对象只有一个显式终态；最终 Item 事实与瞬时 delta 职责分离；有界
流控属于正确性要求；取消必须完成生命周期状态，而非仅停止输出。

当前不照搬：JSON-RPC、传输服务器、审批、分页、公共 schema；也不把 app-server
协议对象直接当作内部领域对象。

证据：<https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md>

### 3.5 Maka

Maka 把单一 Runtime Host 定义为执行权威。模型消息、工具调用、工具结果和终止事实
进入 Runtime Event Log；Session、上下文、UI 和恢复都是投影；评测对象也通过同一
Runtime 执行，而不是走 benchmark 专用捷径。

采用：单一 Session/Turn/模型生命周期执行权威；持久事实不因上下文裁剪或 UI 投影
而改写；确定性评测使用真实 Engine 边界；append 与 replay 是核心正确性路径。

当前不照搬：SQLite 运维存储、Runtime Host 进程、Graph 控制面、桌面组合或完整
评测平台，这些应在 Engine 契约稳定后分阶段建设。

证据：<https://github.com/maka-agent/maka-agent/blob/main/ARCHITECTURE.md>

### 3.6 DeepSeek-Reasonix（社区、非权威上下文）

DeepSeek-Reasonix 是社区项目，不是 DeepSeek 官方仓库，在本文仅作非权威上下文。
它有意针对 DeepSeek 优化：稳定提示前缀以提高缓存命中、模型专用工具调用
修复、flash/pro 成本路由、结果裁剪和并行安全标注。

以后采用：provider capability profile、由 trace 驱动的优化与 A/B 评测、显式的
成本/重试/缓存/并行安全证据。

不进入中立 Engine：provider 名称分支、DeepSeek 修复启发式，以及缺少 provider/
context 契约和基准证据的缓存布局、升级或压缩策略。

证据：<https://github.com/esengine/DeepSeek-Reasonix/blob/v1/docs/ARCHITECTURE.md>

### 3.7 评审结论

本阶段选择的组合是：

- Maka 的单一执行权威与事实/投影分离；
- Pi 的小型可注入循环和取消模型；
- Codex 的显式生命周期与有界流控纪律。

Kimi 的服务和生命周期拆分作为后续阶段参考；Reasonix 的模型专用优化归入未来
provider/context profile；Mini-Agent 是可运行集成参考，而非核心架构模板。

## 4. 架构决策

### 4.1 Application Service 是唯一命令权威

无界面测试、未来 CLI、ACP、TUI 和评测调用方都调用同一 Application Service。
任何适配器都不得自行调用模型后再独立制造领域事件。

首个应用面只有：

```text
CreateSession
RunTurn
CloseSession
```

`RunTurn` 把模型流式执行委托给 `TurnRunner`，但 application 层拥有加载、重放、
命令身份、乐观并发、持久 append 和最终结果映射。

### 4.2 接口归消费方所有

不创建通用 `ports` 包。Go 接口放在消费它的包内：

- application：`EventStore`、`Clock`、`IDGenerator`；
- engine：`Model`、`ModelStream`、`RuntimeSink`。

适配器实现这些接口，消费方不导入适配器类型，避免形成脱离真实用例的抽象目录。

### 4.3 ScriptedModel 是正式适配器

`ScriptedModel` 实现未来 provider 同样使用的 `engine.Model` 接口。生产代码不得出现
`if scripted`、测试模式、环境标志或另一条执行路径。

脚本可精确断言完整请求、发出有序文本 delta、正常结束、在流前或流中失败、阻塞至
取消，并以并发安全方式记录调用。

### 4.4 MemoryEventStore 是契约实现

内存存储受互斥锁保护，并实现未来文件或数据库存储必须满足的同一 append 契约：

```go
type EventStore interface {
    Load(ctx context.Context, sessionID domain.SessionID) ([]domain.RecordedEvent, error)
    Append(ctx context.Context, request AppendRequest) ([]domain.RecordedEvent, error)
}

type AppendRequest struct {
    SessionID       domain.SessionID
    ExpectedVersion uint64
    CommandID       domain.CommandID
    Events          []domain.Event
}
```

实现计划可以调整 Go 命名，但不能改变以下语义：按 `ExpectedVersion` 做 CAS，其中 version
精确定义为该 Session 权威、连续 recorded-event stream 的长度；存储分配连续序号；元数据来自注入 ID 与时钟；每次 append 只调用一次时钟，同一批记录共享
归一化后的 UTC 时间；一次 append 全成或全败；加载与返回记录做防御
复制；提交前取消不写入；冲突命令不自动重试；确定性故障注入可在提交前失败且无部分
状态。

本里程碑 `Load` 返回完整权威 stream；缺失 stream 返回空结果，由 Application 用例决定
是否映射为 `session_not_found`。`Append` 成功只返回本次新提交批次；成功追加 N 条会把
version 从 `ExpectedVersion` 推进到 `ExpectedVersion + N`。

snapshot、index、transcript model 和 UI state 都是未来可丢弃 projection，不能决定 CAS
是否接受、recorded sequence 或权威 version。Event ID 是不透明唯一标识：后期提交前失败
可消耗最终未入库的 ID，这不违反原子性；持久状态不变且已提交 record sequence 无空洞。
端口不承诺 ID/Clock 的事务性或无空洞。

本里程碑还规定：`Append` 返回非 nil error 表示请求批次没有提交；批次一旦提交，即使
caller cancellation 在 commit point 后并发发生，adapter 也必须返回已提交 records。
Application 只有在以下条件全部满足后才接受返回批次：数量准确；sequence 以无溢出的
方式严格等于 `ExpectedVersion+1..+N`；Session/Command ID 匹配；schema version、Event ID、
timestamp 与事件形状有效；event type、payload 和 order 与请求逐项相等；整批共享同一个
非零 UTC occurrence time。ordered Apply 必须成功，且最终 Version 必须等于
`ExpectedVersion + N`。任何 metadata/event mismatch、Apply failure 或 final-version mismatch
都是 `internal/store_contract_violation`，有 Apply cause 时保持可 unwrap。该检查会 fail closed，
但无法回滚一个已经提交后谎报结果的 Store。

进程内 `MemoryEventStore` 能满足该无模糊错误合同。未来 remote Store 若可能提交成功但
丢失 acknowledgement，就不能诚实实现当前端口；成为生产 adapter 前必须加入稳定的
exact-retry batch identity 或显式 unknown-commit outcome。

### 4.5 持久事实与运行时信号分离

领域事件重建权威状态；运行时信号服务实时消费者，可按适配器已记录策略合并或丢弃。

```text
持久事实                            运行时信号
───────────────────────────────     ─────────────────────────────
turn.started                        model.stream.started
assistant.message.started           model.text.delta
assistant.message.completed         model.stream.completed
assistant.message.failed            model.stream.failed
assistant.message.interrupted       model.stream.interrupted
turn.completed                      append.completed
turn.failed
turn.interrupted
```

本阶段不持久化文本 delta，终态 assistant message 保存最终准确文本。上下文裁剪、未来
压缩和 UI 渲染均不得改写这个事实。

### 4.6 最小 Item 生命周期扩展领域

```text
absent --assistant.message.started--> running
running --assistant.message.completed--> completed
        --assistant.message.failed-----> failed
        --assistant.message.interrupted-> interrupted
```

约束如下：Item 只属于一个 Turn；仅活跃 running Turn 可启动 Item；本阶段最多一个
assistant-message Item 运行；Item 恰有一个终态事件；Item 仍运行时 Turn 不得终结；
成功文本逐字节持久保存；通用 Item 只承载 identity/lifecycle，payload 使用 domain 内
封闭的 kind-specific 类型，不能演化成所有 kind 字段的扁平袋；失败/中断必须保存稳定
机器 code 和可选安全展示 message，而非 provider 原生错误；running/completed 不携带
terminal metadata，failed/interrupted 不持久化部分 assistant 文本。

每次 Item 或 Turn 终态转换前后，`ActiveItemID`、ItemOrder、Items map key、ownership、
payload kind、timestamp 与 status 必须互相一致；损坏前置状态返回 `invalid_event`，不得
静默修复。`caller_canceled` 和 `runtime_delivery_failed` 是首批稳定中断 code。

Application admission 使用一个领域复合命令
`StartAssistantTurn{SessionID, TurnID, ItemID, Input}`，校验完整的已知 pre-effect transition，
并在一个 decision batch 中准确返回 `TurnStarted`、`AssistantMessageStarted`。低层命令可为
兼容性保留为 domain building block，但 Application 绝不暴露拆分的 Turn-start/Item-start
持久分支。

`ModelAttempt`、usage、tool/reasoning/image Item 留待后续。这是范围控制，不允许把
provider 专用数据塞进 assistant-message Item。

记录事件的 `schemaVersion: 1` 版本化 envelope 与严格 payload 编码，并不冻结事件类型
目录。pre-v0 内部事件可在保持既有事件字节和 replay 语义兼容时继续加入 v1；现有
Session fixture 必须仍能解码、等价重放并逐记录按原字节重新编码。

### 4.7 核心同步执行并自然背压

`RunTurn` 同步执行，消费 pull-style `ModelStream`，并以内联方式调用 `RuntimeSink`。
核心不创建无界 channel 或脱离调用栈的 goroutine。

Sink 接收 `context.Context`，取消后必须及时返回。未来 ACP/server 适配器可以在
Engine 外建立有明确过载策略的有界队列；传输背压不隐藏在领域层。

### 4.8 输出必须有界

Runner 必须显式配置 `MaxAssistantBytes`。累计输出即将越界时拒绝或失败 Turn；默认值、
配置归属和稳定错误码在实现计划与测试中固定，任何路径不得无限累计输出。

字节限制在 delta 加入累加器前判断；模型边界要求有效 UTF-8；已接收文本不做归一化。

### 4.9 Runtime event 的所有权与校验

调用方只提交 payload，不提交 envelope。run-scoped `Emitter` 独占 correlation 和顺序：
每次 sink 尝试前填入完整关联字段与严格递增 ordinal。失败尝试也消耗 ordinal，不回滚、
不复用。Emitter 仅供单次运行、不可复制、不可并发调用；不同 Emitter 可以并发调用线程
安全的 sink。

Runtime payload 在分配 ordinal 前集中校验。started、completed、`append.completed` 不携带
Text/Code；text delta 必须是非空有效 UTF-8 且不携带 Code；failed/interrupted 必须携带
稳定非空 Code 且不携带 Text。稳定 runtime code 长度为 1–64 ASCII byte，首字符只能是
`a`–`z`，其余字符只能是 `a`–`z`、`0`–`9` 或 `_`。未知 type 属于调用方合同错误。
`diagnostic` 延后到有明确消费方与脱敏合同时再定义。

Emitter 的精确顺序是：校验 payload、检查 `ctx.Err()`、分配 ordinal、尝试 sink。校验失败
或 attempt 前取消不消耗 ordinal，取消返回 `canceled`；sink attempt 一旦发生就消耗序号。
sink 返回错误时若 context 已取消，主 code 为 `canceled`，否则为 `delivery`。

### 4.10 Model stream 所有权与清理

`Model.Stream` 返回的每个非 nil stream（包括 `(stream, error)`）都由 Engine 接管清理，
所有退出路径恰好调用一次 `Close`。`(nil, nil)` 是非法流，`(nil, error)` 是启动失败，
`(stream, error)` 是启动失败并附带受管清理。`Next` 同时返回 event/error 时忽略 event；
context 取消优先于并发 provider error；completed 前 `io.EOF` 非法；其他 Next error 为
model-stream failure；显式 completed 即终点，Runner 不再多调用一次 Next。

非成功路径先取消派生 stream context，再 Close；成功路径先观察 completed、Close，再取消。
Close 同步执行并必须及时汇合 provider 自有后台工作。仅 Close 失败映射 `model_stream`；
已有主错误时保持其稳定 code，并在一个 Engine error 内用 `errors.Join` 合并 cause。

每个 delta 的精确顺序为：拒绝空文本、校验 UTF-8、检查字节上限、发送 sink、追加 builder。
非法、未送达或越界 delta 不得累计；恰好达到上限有效；Engine 不 trim、normalize 或 rechunk。
provider adapter 不得跨事件拆分 UTF-8 code point。

## 5. 组件布局

```text
headless caller / future adapters
              │
              ▼
internal/harness/application
  Service · use cases · EventStore · Clock · IDGenerator
              │
       ┌──────┴──────────┐
       ▼                 ▼
internal/harness/engine  internal/harness/domain
  TurnRunner · Model       commands · events · replay
  ModelStream · RuntimeSink
       ▲
       │ implements
internal/harness/adapters/memory
  MemoryEventStore

internal/harness/testkit
  ScriptedModel · FixedClock · SequenceIDs · RecordingSink
```

实现计划可按 Go 习惯微调包名，但不能反转依赖方向。domain 不导入 application 或
engine；engine 不导入 memory adapter、ACP、TUI、provider SDK、持久化库或 testkit。

## 6. Turn 执行流程

`RunTurn` 有四个不可逆阶段：

| 阶段 | 持久边界 | Context | 必须结果 |
| --- | --- | --- | --- |
| Preflight | 无 | caller context | 失败无 record、无模型调用 |
| Admission | 原子 started Turn + Item | caller context | 成功后才允许调用模型 |
| Execution | 只有 runtime started/delta | caller context | Engine 负责 stream cancel 与 `Close` |
| Terminalization | 原子 terminal Item + Turn | success 使用 caller context；failure/interruption 使用有界 detached context | 精确一个 terminal batch，或显式 running persistence/conflict 结果 |

admission 前的精确顺序为：校验请求和 typed-nil 依赖；Load 完整 stream；Replay 并校验
权威状态；调用纯领域 `CheckStartAssistantTurnEligibility`；生成 Turn、Item、Command ID；
校验所有生成 ID；构造唯一 run-scoped Emitter；Decide `StartAssistantTurn`；原子 append
admission batch；最后调用 `TurnRunner`。eligibility predicate 的有限范围是 Session 存在、
active、完整结构合法且没有 running Turn 或 Item；它不校验尚未生成的 ID 或 request input。
领域 `Decide(StartAssistantTurn)` 在 command-field checks 前调用同一个 predicate，Application
绝不复制这些 invariant。因此 missing、closed、corrupt 或 already-running Session 不消耗
run ID；ID source 以 nil error 返回非法 ID 或 Emitter 构造失败也不会遗留 running Turn。

### 6.1 成功 Turn

```text
caller
  → application.RunTurn
  → EventStore.Load
  → domain.Replay
  → domain.CheckStartAssistantTurnEligibility
  → 校验生成的 Turn/Item/Command ID
  → 构造唯一 engine.Emitter
  → domain.Decide(StartAssistantTurn)
  → EventStore.Append(expectedVersion)          [turn.started,
                                                 assistant.message.started]
  → Model.Stream
  → RuntimeSink(model.stream.started)
  → RuntimeSink(model.text.delta)*
  → EventStore.Append atomically                [assistant.message.completed,
                                                 turn.completed]
  → RuntimeSink(append.completed)
  → RuntimeSink(model.stream.completed)
  → RunTurnResult
```

Admission 是一个原子批次：`StartAssistantTurn` 依次决定 `turn.started` 与
`assistant.message.started`；二者共享 command ID 和 occurrence timestamp，并将加载版本
推进 2。admission 失败或提交前取消时两条都不可见，模型调用次数为零。

终态 Item 与 Turn 事件同样在一个原子批次 append。Item 终态在前、Turn 终态在后，
二者共享 command ID 和 occurrence timestamp。调用方绝不能看到缺少最终 message
事实的 `turn.completed`。

### 6.2 模型启动失败

模型在产生 stream 前失败时，Engine 向 Application 返回稳定错误；Application 再原子
追加 `assistant.message.failed` 与 `turn.failed`。原始 provider payload 不进入领域状态。

### 6.3 流中失败

此前发出的 delta 仍只是运行时观察。Application 映射 Engine 结果并原子追加失败 Item
与 Turn 终态，不把部分 assistant 文本表示成 completed message。

### 6.4 取消

每个边界都检查取消，模型和 RuntimeSink 始终接收原 caller context。atomic admission
提交后，Engine failure、caller cancellation 或终态前 delivery failure 都在同一调用栈
创建有界 cleanup context：

```go
cleanupBase := context.WithoutCancel(ctx)
cleanupCtx, cancel := context.WithTimeout(cleanupBase, s.config.TerminalCommitTimeout)
defer cancel()
```

detached context 只用于 failure/interruption append，绝不用于模型、retry、普通 success
或 RuntimeSink delivery。admission 前取消不写入；终态已提交后观察到取消不能替换终态。

### 6.5 Append 失败

admission 失败时绝不调用模型。模型成功后先在 completed batch 前检查 caller
cancellation，并使用 caller context append；若返回 records，持久 completion 胜出。
若 append 失败且 `ctx.Err() != nil`，无模糊错误规则允许用 bounded cleanup context
尝试一次 interrupted pair；其他 persistence failure 或 conflict 不制造第二个终态。
任一 terminal append 失败都返回已知 admission records、running 状态且
`TerminalCommitted == false`；生产 reconciliation 是 GA 前阻断能力。

### 6.6 Runtime sink 失败

本阶段 sink 属于必需执行路径。终态提交前失败时，Engine 取消并关闭模型流，把稳定错误
返回 Application；Application 再尝试以稳定 delivery 原因原子中断 Item/Turn。终态提交
后的 delivery 失败或 caller cancellation 不能改写持久成功，返回结果只携带与执行状态
分离的 delivery warning/error。

### 6.7 Result 与 error 代数

`RunTurn` 有意同时返回 value 与 error。`Records` 是本调用已知提交的每个批次的防御复制
与有序拼接：admission 后两条，terminalization 后再加两条。`Text` 精确等于 completed
输出（空成功也有效）；failed/interrupted 的 Text 为空，partial delta 永不成为最终文本。

| 结果 | Result status/text | `TerminalCommitted` | Error category |
| --- | --- | --- | --- |
| completed 且 delivery 成功 | completed / exact text | true | nil |
| completed，但终态 delivery 失败/被抑制 | completed / exact text，warning | true | delivery |
| model startup/stream/close failure，终态已提交 | failed / empty | true | model |
| provider stream 非法，终态已提交 | failed / empty | true | model (`invalid_stream`) |
| output limit，终态已提交 | failed / empty | true | output_limit |
| caller cancellation，终态已提交 | interrupted / empty | true | canceled |
| 终态前 sink failure，中断已提交 | interrupted / empty | true | delivery |
| admission/load/terminal persistence 或 conflict failure | absent 或 running / empty | false | persistence、conflict 或 internal |
| request validation failure | zero result | false | validation |

accepted admission 前的任何 failure 都返回 zero `RunTurnResult`：IDs/status 为零值、
text/records 为空、terminal=false、warning=nil。accepted admission 后、terminal batch
验收前的任何返回都携带 Session/Turn/Item ID、running status、两条 admission records、
empty text、terminal=false、warning=nil。terminalization 验收后再追加两条 records，并按表
设置 failed/interrupted/completed、Text 和 terminal flag。`DeliveryWarning` 只在
post-terminal runtime delivery 失败或被抑制时非 nil，并保留该 cause；返回的 application
error 按优先级保留 primary execution 与 terminalization causes。

只有已验收并 Apply terminal batch 后，Application 才能发送 `append.completed`，随后准确
发送一个 `model.stream.completed`、`model.stream.failed` 或
`model.stream.interrupted`。没有 terminal commit 就没有这些 signal。后续 delivery failure
写入 `DeliveryWarning`；若不存在更早执行错误，它是返回 category，否则保持更早 category
为主并 join delivery cause。取消后缺少 runtime terminal signal 不能被解释为缺少持久终态。

```text
success:     model.stream.started, delta*, append.completed, model.stream.completed
failure:     model.stream.started?, delta*, append.completed, model.stream.failed
interrupted: model.stream.started?, delta*, append.completed, model.stream.interrupted
```

错误优先级是：阻止 terminalization 的 Store contract/conflict/persistence；原始
model/output/canceled/delivery execution error；post-terminal delivery warning。
terminalization 失败时，`errors.Join` 保留 execution 与 append cause，outer category 描述
持久 running 边界。

## 7. 并发与事务语义

- EventStore CAS 是同一 Session 并发控制的权威；
- atomic admission CAS 是同 Session `RunTurn` 的线性化点；两个调用方可加载同一版本并
  完成 preflight，但只有一个 admission 能提交，失败方不调用模型；
- 加载到 already-running Turn 的调用在 `domain.Decide` 前后不 append 且模型调用为零；
- `CloseSession` 与 admission 竞争只由 CAS 决定，只有一个合法 append 胜出且都不 retry；
- 不同 Session 可通过同一 Service/TurnRunner 并发执行；共享 Model 与 RuntimeSink 实现遵守
  Tasks 5–6 的并发合同；
- CAS 冲突后不自动重试，避免未来重复模型成本或外部工作；
- append 批次要么按序全部提交，要么一个也不提交；
- 冲突、已取消、非法请求或预提交注入故障不读取 Clock、不申请 Event ID；候选 append
  精确读取一次 Clock；后期校验失败可消耗未使用的不透明 Event ID，但不得推进 stream version；
- MemoryEventStore 必须在同 Session 冲突和独立 Session 并行下通过 `go test -race`；
- 并发测试使用 barrier/channel 建立顺序，不使用时间 sleep。
- terminal conflict 不 retry，也不改写为 model failure；结果是本调用本地权威下的
  running/unknown，`TerminalCommitted == false`，caller 必须 reload。

## 8. 错误模型

Application/Engine 错误必须结构化并保留 cause，至少区分：

| 类别 | 示例 | 重试责任 |
| --- | --- | --- |
| validation/domain | Session 已关闭、空输入 | 调用方修改请求 |
| conflict | expected version 失败 | 调用方显式 reload 后决策 |
| model | 启动或流式失败 | 未来策略；此处不自动重试 |
| canceled | 调用 context 取消 | 调用方决定是否新建 Turn |
| output_limit | assistant 输出越界 | 调用方/配置策略 |
| delivery | 必需 runtime sink 失败 | adapter/调用方 |
| persistence | load 或 append 失败 | 存储/恢复策略 |
| internal | 不变量或非法 stream 序列 | 运维/开发者 |

每个错误都必须说明持久终态是否已提交。错误文案不是兼容契约；稳定类别/代码和类型化
字段才是。

caller input 与 `domain.Decide` 拒绝属于 validation；空 Load 是
`validation/session_not_found`；ID source error 是 `internal/id_generation_failed`；
nil error 返回非法 ID 是 `internal/id_generator_contract_violation`。Store 提供 records 的
Replay/Apply failure 和任何 append-return mismatch 都是
`internal/store_contract_violation`，绝不是 caller validation。普通 Load/Append dependency
error 是 persistence；任意 `VersionConflictError`（含 cleanup）是 conflict。dependency
error 仅包裹 `context.Canceled` 不会变成 caller cancel，除非 supplied context 确实已取消。

Application `Error`、`IsCategory` 与 `VersionConflictError` 必须 nil-safe，并遍历完整
wrapped/joined error tree，包括 later sibling、direct typed nil 与 joined typed nil。

一个生成的 `CommandID` 关联同一 RunTurn 的两个 append batch 与全部 runtime event；它不是
idempotency 或 Store deduplication key。由于跨两个 append，`(SessionID, CommandID)` 不能
唯一标识一次 append request。Service 不自动 reload、re-decide、append retry 或 model
retry；caller 不得盲重试不确定响应。未来公共幂等需要独立 caller-stable operation identity、
exact batch identity 和明确的 return/resume/new-Turn 语义。

## 9. 确定性适配器与契约套件

### 9.1 ScriptedModel

模型测试适配器由 Engine 与未来适配器契约测试复用，支持精确请求断言、调用记录、
确定性阻塞/取消，并对脚本数据做防御复制。

RecordingSink 在应用一次性 ordinal 故障前先记录每次尝试。失败调用进入 `Attempts` 而不
进入 `Delivered`，成功调用同时进入二者；两种快照均为防御复制。只有禁用 ordinal
故障注入时，一个 sink 才可由不同 Emitter 并发共享；非零 run-local failure ordinal
限定单 Emitter。测试不得并发驱动同一个 Emitter。

### 9.2 MemoryEventStore

存储支持确定性元数据、CAS 冲突、原子批次、按 Session 一次性 load/append 故障注入、
防御复制断言和并发访问。契约测试覆盖缺失 stream、append 返回形状、load 故障的消费与
隔离、防御复制；adapter 专属测试使用计数/失败源证明 nil 依赖拒绝、成功一次 Clock
读取、候选构造前失败零 source 调用，以及 Event ID 或 Clock 生成失败时记录/version 不变。

### 9.3 共享契约套件

未来实现必须通过可复用套件，概念上为：

```text
eventstoretest.Run(factory)
modeltest.Run(factory)
enginescenariotest.Run(harness)
```

具体 Go 包由实现计划确定，但生产适配器不能只凭自己的定制测试宣称兼容。

## 10. 验证矩阵

实现计划至少覆盖以下用例。

### 正常行为

- create、run、complete、close；
- 一个 Session 中多个顺序 Turn；
- 多 delta UTF-8 输出逐字节保持；
- 通过 replay 重建最终状态和 assistant Item；
- 对注入 ID/时间标准化后，相同脚本输入得到等价 trace。

### 校验与模型失败

- 空或非法请求；模型启动失败；首个 delta 前失败；多个 delta 后失败；
- 非法 UTF-8；空 delta 非法且不发送、不累计；空成功输出有效；
- 恰好达到字节上限及超出一个字节；非法模型 stream 事件顺序；
- `(nil, nil)`、`(stream, error)`、`Next(event, error)` 组合；
- 所有非 nil stream 退出路径恰好 Close 一次，包括 sink failure；
- 仅 Close 失败和主错误叠加 Close 失败的优先级。

### 取消与 delivery

- atomic admission 前取消、admission 后立即取消、流中取消、取消与完成竞争；
- 终态提交前后 sink 失败；每个已启动 Item/Turn 最多进入一个终态。

### 存储与并发

- load 失败；atomic admission 失败阻止模型调用且无 partial start；终态批次失败不报告成功；
- 故障注入证明无部分批次；同 Session 并发一胜一冲突；
- 32 个独立 Session 无数据竞争地完成；加载/返回记录不能改变存储状态；
- `go test -race ./... -count=1` 通过。
- 稳定 Engine code 匹配遍历完整 joined error tree，并安全处理直接或 joined typed nil。
- generated-ID source error 与 nil-error invalid ID 都发生在 admission 前；
- exact append-return 拒绝覆盖数量、溢出、metadata、event type/payload/order、共享 UTC 时间、
  ordered Apply success 和 final Version；
- Application category/conflict 匹配覆盖 nested join、后续 matching sibling、direct/joined
  typed nil；
- 以 barrier 覆盖 Load、admission commit、terminal entry 与 terminal return；terminal
  persistence/conflict/Store-contract failure 后 running boundary 可重放且无隐藏 retry。

### 仓库边界

- domain 不导入 application、engine、adapter、provider、ACP、MCP 或 TUI；
- engine 不导入具体 adapter 或 provider SDK；
- 生产代码不对 ScriptedModel 分支；
- 无文档化决策和真实需求时不增加第三方依赖；
- 常规/聚焦/race 测试、格式化、vet 和 diff 检查都是 CI 门禁。

## 11. 可观测性与评测证据

本阶段不引入 OpenTelemetry，但 `RuntimeEvent` envelope 必须携带未来所需关联字段：

```text
session_id
turn_id
item_id（适用时）
command_id
RunTurn 内单调 event ordinal
runtime event type
```

默认不记录秘密模型输入/输出；测试用 RecordingSink 只接收显式测试数据；未来内容
telemetry 必须 opt-in 且默认脱敏。

完成证据包括：准确场景 trace fixture、故障矩阵、契约套件、常规与 race 测试输出、
依赖边界检查、replay 等价证据和明确延后的生产能力清单。

## 12. 安全与资源基线

- 不引入网络、文件系统、shell、MCP 或工具权限；
- context 取消到达模型和 sink；
- assistant 输出有显式字节上限；
- 核心不创建无界队列或 detached goroutine；
- ScriptedModel 与 MemoryEventStore 对并发测试安全；
- 错误不会自动暴露 provider 原始 payload 或秘密；
- ID、时间戳和文本遵循领域层严格校验规则。

## 13. 明确排除项

以下内容分别进入未来规格：真实模型 provider 契约与适配器；重试、限流、鉴权、能力、
usage 与成本策略；生产 JSONL/file/SQLite/remote EventStore；崩溃 reconciliation、
checkpoint、迁移、备份与恢复；工具调用、Tool Runtime、Policy、审批和工作区沙箱；
ACP、JSON-RPC server、TUI、IDE 和公共 SDK；Context Engine、提示构造、压缩和缓存；
MCP、Skills、memory、subagent 与多 Agent graph；OpenTelemetry 和完整场景评测平台；
在明确消费方和脱敏合同出现前的 diagnostic runtime event。

排除这些能力是为了保持依赖顺序正确，不会降低当前 Engine、EventStore 契约、Item
生命周期或确定性适配器的质量要求。

admission 后进程退出，以及 terminal persistence/conflict/contract failure，都可能留下
持久 running boundary。本里程碑不执行 startup repair、continuation 或 blind retry；返回
records 与 Replay 必须让边界可检查，并把生产 reconciliation 明确列为 GA 前阻断能力。

## 14. 被否决的替代方案

### 14.1 Demo 专用直接模型调用

否决：它绕过正式端口、持久输出事实、故障语义和可复用适配器测试，接入首个真实
provider 时就会迫使重写。

### 14.2 现在实现完整工具循环

否决：Tool/Policy/审批状态机尚未设计；提前加入会让 Turn Runner 与工具子系统故障
无法独立归因。

### 14.3 现在实现 Runtime Host 和公共协议

否决：server、transport、subscription、schema 和 persistence 会淹没首个 Engine
契约；应用边界已确保未来增加 Runtime Host 时无需迁移执行权威。

### 14.4 通用 DI/container 框架

否决：在多个真实组合证明必要前，构造函数和消费方拥有的接口已经足够。

### 14.5 持久化每个 token delta

否决：delta 是高频 delivery 信号，不是必要最终事实。终态 assistant message 持久化；
未来 transcript persistence 可另行定义合并或 journal 策略。

### 14.6 Engine 内 provider 专用优化

否决：cache、repair、retry 和 cost 行为随 provider/model capability 变化；采用前必须
有 provider 契约测试与 benchmark 证据。

## 15. 验收标准

只有以下事项均获接受后，设计才进入实现计划：

- 工业级定位与 pre-v0 成熟度表述；
- 单一执行权威和包依赖方向；
- 最小 Item 生命周期与持久/运行时分离；
- CAS 与原子 append 语义；
- 同步有界 streaming 与 sink failure 语义；
- 错误分类和不自动重试原则；
- 验证矩阵足够；
- 每个排除项都有明确未来里程碑。

实现只有在全部契约、场景、故障、并发、race、replay、边界和文档检查通过，工作树
干净，且独立评审无未解决 critical/important 缺陷时，才算完成。
