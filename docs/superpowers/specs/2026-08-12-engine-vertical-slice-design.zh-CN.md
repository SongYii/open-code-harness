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

本设计只采用官方仓库和一手技术文档中的实现证据。只有符合 Open Code Harness
章程的思想才会被采用；参考项目不是本项目依赖，也不直接决定本项目 API。

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

### 3.6 DeepSeek-Reasonix

Reasonix 有意针对 DeepSeek 优化：稳定提示前缀以提高缓存命中、模型专用工具调用
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

实现计划可以调整 Go 命名，但不能改变以下语义：按 `ExpectedVersion` 做 CAS；存储分配
连续序号；元数据来自注入 ID 与时钟；每次 append 只调用一次时钟，同一批记录共享
归一化后的 UTC 时间；一次 append 全成或全败；加载与返回记录做防御
复制；提交前取消不写入；冲突命令不自动重试；确定性故障注入可在提交前失败且无部分
状态。

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
turn.failed                         diagnostic
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

### 6.1 成功 Turn

```text
caller
  → application.RunTurn
  → EventStore.Load
  → domain.Replay
  → domain.Decide(StartTurn)
  → EventStore.Append(expectedVersion)          [turn.started]
  → EventStore.Append(expectedVersion + 1)      [assistant.message.started]
  → Model.Stream
  → RuntimeSink(model.stream.started)
  → RuntimeSink(model.text.delta)*
  → EventStore.Append atomically                [assistant.message.completed,
                                                 turn.completed]
  → RuntimeSink(model.stream.completed)
  → RunTurnResult
```

终态 Item 与 Turn 事件在同一个原子批次 append。Item 终态在前、Turn 终态在后，
二者共享 command ID 和 occurrence timestamp。调用方绝不能看到缺少最终 message
事实的 `turn.completed`。

### 6.2 模型启动失败

模型在产生 stream 前失败时，Engine 原子追加 `assistant.message.failed` 与
`turn.failed`。错误被归一为稳定 Engine 类别，原始 provider payload 不进入领域状态。

### 6.3 流中失败

此前发出的 delta 仍只是运行时观察。Engine 原子追加失败 Item 与 Turn 终态，不把
部分 assistant 文本表示成 completed message。

### 6.4 取消

每个不可逆边界前检查取消，并将 context 传入模型 stream 与 sink。Turn/Item 已启动
后，取消会尝试原子追加二者的 interrupted 事实。若中断提交成功，即使 provider 随后
返回普通 abort error，结果仍为 interrupted。

初始 Turn append 前取消不写入；终态提交后的取消不能替换终态。

### 6.5 Append 失败

初始 Turn append 失败时绝不调用模型。终态批量 append 失败时返回 persistence failure，
不得把模型成功报告为 Turn 成功。事件流可能停留在 running 边界，生产级 reconciliation
由未来持久化/恢复里程碑承担；当前失败必须显式且可测试，不能静默修复。

### 6.6 Runtime sink 失败

本阶段 sink 属于必需执行路径。终态提交前失败时，取消模型流并尝试以稳定 delivery
原因原子中断 Item/Turn。终态提交后失败时，持久成功仍是权威，返回结果携带与执行
状态分离的 delivery warning/error。任何 sink 失败都不能改写已提交终态。

## 7. 并发与事务语义

- EventStore CAS 是同一 Session 并发控制的权威；
- 两个调用方可加载同一版本，但只有一个初始 Turn append 能提交，失败方不调用模型；
- 不同 Session 可以并发执行；
- CAS 冲突后不自动重试，避免未来重复模型成本或外部工作；
- append 批次要么按序全部提交，要么一个也不提交；
- MemoryEventStore 必须在同 Session 冲突和独立 Session 并行下通过 `go test -race`；
- 并发测试使用 barrier/channel 建立顺序，不使用时间 sleep。

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

## 9. 确定性适配器与契约套件

### 9.1 ScriptedModel

模型测试适配器由 Engine 与未来适配器契约测试复用，支持精确请求断言、调用记录、
确定性阻塞/取消，并对脚本数据做防御复制。

### 9.2 MemoryEventStore

存储支持确定性元数据、CAS 冲突、原子批次、load/append 故障注入、防御复制断言和
并发访问。

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
- 非法 UTF-8；空成功输出的明确契约；恰好达到字节上限及超出一个字节；
- 非法模型 stream 事件顺序。

### 取消与 delivery

- 首次 append 前取消；Turn 启动后、模型前取消；流中取消；取消与完成竞争；
- 终态提交前后 sink 失败；每个已启动 Item/Turn 最多进入一个终态。

### 存储与并发

- load 失败；初始 append 失败阻止模型调用；终态批次失败不报告成功；
- 故障注入证明无部分批次；同 Session 并发一胜一冲突；
- 32 个独立 Session 无数据竞争地完成；加载/返回记录不能改变存储状态；
- `go test -race ./... -count=1` 通过。

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
MCP、Skills、memory、subagent 与多 Agent graph；OpenTelemetry 和完整场景评测平台。

排除这些能力是为了保持依赖顺序正确，不会降低当前 Engine、EventStore 契约、Item
生命周期或确定性适配器的质量要求。

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
