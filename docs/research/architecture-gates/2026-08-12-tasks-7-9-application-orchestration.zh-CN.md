# Tasks 7–9 架构门禁：Application 编排

- 日期：2026-08-12
- 范围：Application 计划 Tasks 7–9；Session 用例与一次持久 `RunTurn`
- 证据规则：只把官方一手来源作为证据；明确标注的社区项目只能作为非权威上下文
- 评审时实现状态：Tasks 1–6 已实现，Tasks 7–9 尚未开始
- 结论：**READY**
- 英文规范报告：`2026-08-12-tasks-7-9-application-orchestration.md`

## 方法与证据边界

本文严格区分：

1. **观察证据**：官方来源明确写出的行为或合同；
2. **本项目推论**：Open Code Harness 基于证据作出的选择，不冒充参考项目事实；
3. **本地合同**：即使顶级项目没有公开相同保证，我们仍自行定义并自行证明的约束。

各项目对 Session、Turn、Item、transcript、event、retry 的含义并不相同，不能直接
等同，也没有一个可直接照搬的共同事务模型。

## 一手证据

| 项目 | 观察到的证据 | 对 Open Code Harness 的推论 |
| --- | --- | --- |
| OpenAI Codex | [app-server 合同](https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md)定义 `Thread -> Turn -> Item`、显式 `turn/started` 与终态 `turn/completed`，以及 `item/started -> delta* -> item/completed`；中断也以 `turn/completed(status=interrupted)` 收束，客户端应以该终态通知为准。`clientUserMessageId` 只被描述为回显字段，并未声明为幂等键。[rollout recorder](https://github.com/openai/codex/blob/main/codex-rs/rollout/src/recorder.rs)通过单 writer 排序 canonical items，并提供可等待的 flush；其 idempotent 描述针对 recorder materialization/retry，不等于 `turn/start` 恰好一次。 | 采用显式终态、单一生命周期权威、有序 canonical 记录，以及“持久后再发终态通知”。不得据此臆推 CAS、Turn 多事件原子提交或命令幂等。 |
| Kimi Code | [仓库包图](https://github.com/MoonshotAI/kimi-code/blob/main/AGENTS.md)分离 app/server/SDK、agent engine、provider、execution environment 与 transcript。[transcript 合同](https://github.com/MoonshotAI/kimi-code/blob/main/packages/transcript/AGENTS.md)拥有幂等 projection operation、per-session/agent 单调批序号，以及从持久 `wire.jsonl` 冷重建；同时明确部分 live-only 字段不能冷重建。 | 采用消费方拥有 projection 合同、scope 内序号和 live/rebuildable 证据边界。transcript operation 幂等不能证明领域命令或 EventStore 幂等。 |
| Pi | 当前 [agent loop](https://github.com/earendil-works/pi/blob/main/packages/agent/src/agent-loop.ts)等待生命周期事件发送、传递 abort signal 并产生终态 agent/turn 事件。[AgentSession](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/core/agent-session.ts)被 interactive/print/RPC 共用，在事件处理器中持久化 completed message，abort 后等待 idle，并拥有 auto-retry/compaction 策略。 | 采用所有界面共用一个应用入口、等待式取消和可注入执行；拒绝把持久化做成 UI/event listener 的偶然副作用，并把自动重试留在本里程碑之外。 |
| Maka | Maka 明确采用 [log-first、projection-driven](https://github.com/maka-agent/maka-agent/blob/main/ARCHITECTURE.md)，Runtime 保持执行权威，已提交事件保持事实权威。当前 [resume 架构](https://github.com/maka-agent/maka-agent/blob/main/docs/architecture/runtime-resume-architecture.md)区分 repair、continuation 与 retry；要求 terminal semantic fact 先于 terminal header；continuation 使用新执行身份；不确定时禁止盲目重放；exact retry 必须匹配 bytes 与 identity；外部副作用前后使用短事务，而非长事务包住外部工作。 | 采用副作用前后短提交、持久终态先于 projection/signal、显式 running/不确定状态和禁止盲重试。本阶段不引入 Maka 的恢复子系统。 |
| MiniMax | [MiniMax Code](https://github.com/MiniMax-AI/minimax-code) 明确只是桌面应用 issue 收集仓库，没有实现证据。官方 [Mini-Agent](https://github.com/MiniMax-AI/Mini-Agent) 自称 demo；其 [loop](https://github.com/MiniMax-AI/Mini-Agent/blob/main/mini_agent/agent.py) 有步数上限、在安全点检查取消、记录请求/结果、捕获 provider error 并返回用户错误字符串。 | 保留执行有界与可运行测试。不能从 MiniMax Code 或 Mini-Agent 推导事务、终态持久性、错误归一化或幂等合同。 |
| DeepSeek-Reasonix | 该社区项目的[架构](https://github.com/esengine/DeepSeek-Reasonix/blob/main/docs/ARCHITECTURE.md)分离 immutable prefix、ordered append-only log 与 volatile scratch；即使安全工具并行执行，tool result 仍按声明顺序 append；同时内置 DeepSeek 专用修复、升级与成本策略。 | 它可旁证“append-only 事实与 transient 数据分离”。provider retry/repair/escalation 不进入 Application。该项目不是 DeepSeek 官方仓库，也没有公开 CAS/terminal-commit 保证。 |
| KurrentDB | 官方 [append 文档](https://docs.kurrent.io/clients/python/v1.3/appending-events)定义原子 batch、stream-version consistency check；只有相同 consistency check 与相同 event IDs 标识同一 append 时，才支持安全幂等重试，并明确讨论“已提交但响应丢失”。 | 保留 exact CAS 与原子 batch。仅有 correlation `CommandID` 不是幂等协议。当前 in-process port 可排除模糊结果；未来 remote store 必须加入稳定 exact-retry identity 或显式 unknown-commit 结果。 |
| Go 标准库 | [`context.WithoutCancel`](https://pkg.go.dev/context#WithoutCancel)保留 value，但移除 cancellation、deadline、`Err` 与 `Cause`；[`WithTimeout`](https://pkg.go.dev/context#WithTimeout)建立新边界且要求调用 cancel。 | detached terminalization context 必须立即用 `TerminalCommitTimeout` 重新设界并在所有路径 cancel；它只服务持久 cleanup，不能让模型或普通 delivery 继续运行。 |

## 发现与必须修订项

### 1. Admission 必须是一个原子 batch

当前设计和计划把 `turn.started` 与 `assistant.message.started` 分两次 append；两次写
之间没有任何外部副作用，却人为制造了“Turn running、计划中的 assistant Item 尚未
开始”的持久状态，Task 9 还要为这个人工边界增加测试和清理分支。

增加一个领域复合命令 `StartAssistantTurn`（或等价命名），一次决定并按顺序产生：

```text
turn.started
assistant.message.started
```

Application 在调用模型前以一次 CAS 原子 append 提交二者；同批共享 command ID 与
occurred-at。append 失败或提交前取消时，两条都不可见且模型调用次数为零；成功后
version 增加 2。

这属于基于既有 atomic-batch 合同的本地推论，不冒充 Codex/Kimi/Pi 的实现事实；它与
Maka “先用短提交建立完整已知 pre-effect boundary，再执行外部工作”的原则一致。

计划应同步修改：Task 8 的两次 start append 合并；删除 Task 9 的“Turn 已开始但 Item
尚未开始时取消”分支；以 atomic admission failure 替换 Item-start append failure；
并在实现 `RunTurn` 前先增加复合领域命令及测试。

### 2. Admission 前完成所有可逆 preflight

精确顺序必须是：

```text
校验请求与 typed-nil 依赖
  -> Load 完整 stream
  -> Replay 并校验权威状态
  -> domain.CheckStartAssistantTurnEligibility
  -> 生成 TurnID、ItemID、CommandID
  -> 校验每个生成 ID
  -> 构造 Emitter
  -> Decide(StartAssistantTurn)
  -> Append 原子 admission batch
  -> 调用 TurnRunner
```

`NewEmitter` 必须发生在首次持久 append 前。IDGenerator 返回“nil error + 非法 ID”、
typed-nil sink 或 Emitter 构造失败，都不能遗留 running Turn。

请求校验先于 Load 和任何 ID 调用。Replay 后、生成 run IDs 前，Application 调用纯领域
`CheckStartAssistantTurnEligibility`；其有限范围是 Session 存在/active、完整结构合法且
没有 running Turn 或 Item，不检查 request input 或尚未生成的 IDs。
`Decide(StartAssistantTurn)` 在 command-field validation 前调用同一个 predicate，因此
Application 不复制 domain invariant。missing、closed、corrupt、already-running Session 不
消耗 ID；测试复用同一 eligibility table，并用 counting source 证明顺序。

错误归属精确定义为：

- caller input 或 `domain.Decide` 拒绝 -> `CategoryValidation`；
- 空 loaded stream -> `CategoryValidation/session_not_found`；
- 边界处 `ctx.Err() != nil` -> `CategoryCanceled/canceled`；
- 普通 Load/Append 依赖错误 -> `CategoryPersistence`；
- 任意 append（包括 terminal cleanup）的 `VersionConflictError` ->
  `CategoryConflict/version_conflict`；
- Store 提供的记录在 Replay/Apply 时失败 ->
  `CategoryInternal/store_contract_violation`，绝不是 caller validation；
- ID source error -> `CategoryInternal/id_generation_failed`；
- ID source 以 nil error 返回非法 ID ->
  `CategoryInternal/id_generator_contract_violation`。

如果依赖错误只是包裹了 `context.Canceled`，但传入 context 实际未取消，仍按依赖错误
归类，避免存储适配器意外改变错误所有权。

Task 7 Session 用例遵循相同 preflight 纪律：

- `CreateSession` 先校验 `WorkspaceRoot`，再生成并校验 Session/Command IDs，构造 pristine
  command 后以 version zero append；任何 source failure 都发生在持久化前；
- `LoadSession` 在 Load 前校验 Session ID；空 stream 映射 `session_not_found`，corrupt
  replay 映射 `store_contract_violation`，并返回深防御复制 state；
- `CloseSession` 先校验 Session ID、load/replay、decide close 是否合法，再在 append 前
  生成并校验 Command ID；running Turn 属于 domain rejection，不自动 retry。

### 3. 精确定义 Application 对 Append 返回值的验收

计划只检查数量和连续 metadata，不足以发现“数量/序号正确但 payload 错误”的坏 Store。

Application 在本地把 append 视为已提交前，必须验证：

1. 返回数量等于请求事件数；
2. sequence 精确为 `ExpectedVersion+1 .. ExpectedVersion+N` 且无溢出；
3. 每条 record 的 Session ID 与 Command ID 等于请求；
4. schema version、Event ID、timestamp 与 event shape 通过领域 record 校验；
5. 返回 event type、payload 与 order 精确等于请求；
6. 同一批共享同一个非零 UTC occurrence time；
7. 按序 apply 成功，且 final Version 等于 `ExpectedVersion + N`。

任何不一致均为 `CategoryInternal/store_contract_violation`；存在底层/apply cause 时保留
error chain；且不得启动模型。Application 无法回滚一个谎报 commit 的 Store，这个检查
用于检测 adapter 违约并 fail closed。

EventStore port 还必须声明本里程碑假设：

> `Append` 返回非 nil error 表示请求 batch 没有提交；一旦 batch 提交，即使 caller
> context 在 commit point 后并发取消，adapter 仍返回已提交 records。

MemoryEventStore 可以满足该规则，且下述 fallback 依赖它。未来 remote adapter 若可能
“commit 成功但 acknowledgement 丢失”，就不能诚实实现当前 shape，必须扩展 exact
idempotent retry identity 或显式 unknown-commit outcome。

### 4. 精确定义四个编排阶段与 context 所有权

| 阶段 | 持久边界 | Context | 必须结果 |
| --- | --- | --- | --- |
| Preflight | 无 | caller context | 失败无 record、无模型调用 |
| Admission | 原子 started Turn + Item | caller context | 成功后才允许调用模型 |
| Execution | 只有 runtime started/delta | caller context | Engine 负责 stream cancel 与 Close |
| Terminalization | 原子 terminal Item + Turn | success 使用 caller context；failure/interruption 使用有界 detached context | 精确一个 terminal batch，或显式 running persistence/conflict 结果 |

每个 admission 后的 Engine failure 都在同一调用栈创建：

```go
cleanupBase := context.WithoutCancel(ctx)
cleanupCtx, cancel := context.WithTimeout(cleanupBase, s.config.TerminalCommitTimeout)
defer cancel()
```

它只用于 failure/interruption append。RuntimeSink 继续使用原 caller context；取消可抑制
post-commit 尝试并转为 delivery warning。模型执行、retry 和普通 success 不得使用
detached context。

模型成功时，在 completed batch 前检查 caller cancellation，并用 caller context append：

- completed batch 返回 records -> 持久 completion 胜出，之后观察到取消也不能改写；
- completed append 失败且 `ctx.Err() != nil` -> 根据“error 表示未提交”，用 bounded
  cleanup context 尝试 interrupted pair；
- completed append 因其他 persistence/conflict 失败 -> 不制造第二终态，返回持久
  running boundary。

### 5. 持久终态事实授权 runtime 终态 signal

Application 继续是终态权威；Engine 只发 `model.stream.started` 与 delta。只有相应 terminal
batch 已被验收并 apply 后，Application 才能依次发送 `append.completed` 与一个
completed/failed/interrupted：

```text
success:     model.stream.started, delta*, append.completed, model.stream.completed
failure:     model.stream.started?, delta*, append.completed, model.stream.failed
interrupted: model.stream.started?, delta*, append.completed, model.stream.interrupted
```

terminal append 未成功时，不得发 `append.completed` 或 model terminal signal。终态 signal
delivery failure 不能改变持久状态：写入 `DeliveryWarning`；若没有更早执行错误，返回
Delivery category，否则保持早先 category 为主并 join delivery cause。

同一 Emitter 的 ordinal 覆盖整个调用；失败 sink attempt 消耗 ordinal，payload validation
或 pre-attempt cancellation 不消耗。`RunTurnResult` 不得让调用方把“取消后没有 runtime
terminal signal”误解为“没有持久终态事实”。

### 6. 发布完整 result/error 代数

`RunTurn` 有意返回 value + error；下表为规范：

| 结果 | Result status/text | `TerminalCommitted` | Error category |
| --- | --- | --- | --- |
| completed 且 delivery 成功 | completed / exact text | true | nil |
| completed，但终态 delivery 失败/被抑制 | completed / exact text，warning 已设 | true | delivery |
| model startup/stream/close failure 且终态已提交 | failed / empty | true | model |
| provider stream 非法且终态已提交 | failed / empty | true | model (`invalid_stream`) |
| output limit 且终态已提交 | failed / empty | true | output_limit |
| caller cancellation 且终态已提交 | interrupted / empty | true | canceled |
| pre-terminal sink failure 且 interruption 已提交 | interrupted / empty | true | delivery |
| admission/load/terminal persistence 或 conflict failure | absent 或 running / empty | false | persistence、conflict 或 internal |
| request validation failure | zero result | false | validation |

`Records` 是本调用已知已提交的所有 batch 的防御复制和有序拼接，包括错误路径；atomic
admission 后含两条 start records，terminalization 成功后再含两条 terminal records。
`Text` 精确等于 completed assistant output（允许空）；failed/interrupted 不把 partial delta
作为最终 text 返回或持久化。

错误优先级：

1. 阻止 terminalization 的 store-contract/conflict/persistence；
2. 原始 model/output/canceled/delivery 执行类别；
3. post-terminal delivery warning。

terminalization 失败时，用 `errors.Join` 保留原始执行 cause 与 terminal append cause，但
对外 category 采用 terminalization 类别，因为持久状态仍 running。provider prose 可供显式
unwrap 检查，但不能进入 stable error text 或 domain event。

Application `Error`、`IsCategory` 与 `VersionConflictError` 需要像 Engine 一样 nil-safe，
并遍历 joined error 的全部分支。测试 nested join、匹配项位于后续 sibling、direct typed
nil 和 join 中 typed nil。

### 7. 分离 correlation identity 与 idempotency

一个生成的 `CommandID` 可以继续关联 `RunTurn` 的两个 append batch 和全部 runtime events，
但必须明确：

- 它标识一次 application invocation/correlation lineage；
- 它不是 Store deduplication key；
- admission 与 terminal batch 复用它，意味着 `(SessionID, CommandID)` 不能唯一标识一个
  append request；
- Service 不自动 reload、re-decide、model retry 或 append retry；
- caller 收到不确定响应后不能盲重试 `RunTurn`。

KurrentDB 的一手合同说明 exact retry 依赖相同 expected revision 与 event IDs，而不只是
correlation；Maka 同样要求 exact bytes/identity，并为 continuation 创建新身份。

未来公共/API 幂等需要独立、caller-stable request/operation identity 与精确 batch identity，
还需定义 retry 是返回旧结果、从安全边界继续，还是创建新 Turn。本阶段延期该能力，但
当前命名不能暗示它已经存在。

### 8. 明确并发权威与 race 结果

atomic admission CAS 是同一 Session `RunTurn` 的线性化点：

- 两个调用可加载同一 version 并完成 preflight，但仅一个 admission batch 提交；loser 模型
  调用为零；
- 读取到 already-running Turn 的调用由共享纯领域 eligibility predicate 在生成 ID 前拒绝，
  模型调用为零；`Decide` 复用同一个 predicate；
- `CloseSession` 与 admission 竞争由 CAS 决定，只有一个合法 append 胜出且不自动 retry；
- 不同 Session 可通过同一 Service/TurnRunner 并发执行，遵守 Tasks 5–6 的 shared Model /
  RuntimeSink 合同；
- terminalization conflict 不 retry，也不能改写成 model failure；本调用返回 running/unknown
  于本地权威、`TerminalCommitted=false`，caller 必须 reload。

测试在 Load、admission commit、terminal entry、terminal return 周围使用 store barrier；
不得通过 sleep 推断 race 结果。

### 9. 把恢复缺口作为显式证据，不静默修复

以下情况可留下一个持久 running boundary：admission 后进程退出、terminal append 持久化
失败、terminal conflict 或 Store contract violation。

Tasks 7–9 不实现 startup repair/continuation，但必须让该边界可检查：返回 records/status
准确，replay 保留 running，绝不发送 success terminal signal，也不隐藏 retry 再调模型。
文档必须把生产 reconciliation 标为 GA 前阻断能力。

这采用 Maka 对 repair、continuation、retry 的区分，但不引入 Maka Resolver，也不声称本
阶段已交付 crash recovery。

## 对已接受设计与计划的精确修订

Task 7 编码前，英文规范、中文阅读版和双语计划必须同步：

1. 以一个原子 `StartAssistantTurn` admission batch 替换两次 start append；
2. 在 IDs 前增加共享纯领域 eligibility preflight，再在 admission 前校验 generated IDs 并
   构造 Emitter；
3. 分离 request/Decide rejection 与 corrupt Replay/Apply、adapter contract error；
4. 把 append-return 验收增强为 exact events/batch metadata、ordered Apply success 与 final
   Version `ExpectedVersion + N`，不虚构 independent expected-state oracle；
5. 加入“Append non-nil error 表示未提交”的里程碑 Store 规则和 future remote 限制；
6. bounded detached context 只用于 post-admission failure/interruption 持久终态；
   RuntimeSink 保持 caller context；
7. 用本文 phase/result matrix 替换现有取消和失败分支；
8. 完整定义所有返回路径的 `Records`、`Text`、`Status`、`TerminalCommitted`、warning、
   cause 与 category；
9. 定义 CommandID 是 correlation 而非 idempotency，并保持无自动 retry；
10. 增加 typed-nil/joined-error、generator-contract、exact-returned-event、atomic-admission、
    barrier-race 与 running-replay 测试。

## Adopt / Reject / Defer

| 决策 | 合同 |
| --- | --- |
| Adopt | 单一 Application 权威；validate/load/replay 先于 IDs；原子 admission batch；原子 terminal batch；admission 后才调模型；精确 append-return 验收；持久事实先于 runtime terminal signal；有界 detached terminal persistence；完整 result/error 代数；CAS 线性化；防御复制；全树 typed-nil-safe error。 |
| Reject | 无副作用之间拆分 start append；持久 start 后才构造 Emitter；把 replay corruption 当 caller validation；terminal commit 前发送 terminal signal；取消改写已提交成功；Service 内隐藏 Store/model retry；把 CommandID 宣称为幂等；UI/listener 回调持久化；sleep-based race；partial delta 作为最终持久文本。 |
| Defer | 公共 idempotency key 与结果缓存；remote ambiguous-commit protocol；生产 EventStore；startup repair/reconciliation；continuation/resume；runtime event persistence/catch-up；retry/rate-limit/provider policy；OpenTelemetry。 |

## 结论

总体职责划分仍然正确：Application 拥有 command 与 durability，Engine 拥有一次有界 stream，
Domain 拥有合法 transition，EventStore CAS 拥有并发权威。但修订前的 Tasks 7–9 **不能原样进入
实现**：split admission append、不完整的 append-return 检查、replay error 归属、取消阶段
边界与 idempotency 表述都存在可避免的歧义。

原十项与架构评审 round 1 已一致写入规范文档，本门禁现为 **READY**；开始生产实现前仍需
独立架构复审。

纳入记录（2026-08-12）：已修订规范英文设计
`docs/superpowers/specs/2026-08-12-engine-vertical-slice-design.md`、其中文阅读版，以及
`docs/superpowers/plans/` 下的中英文实施计划。四份文档现已共同定义 atomic admission、
精确 preflight/append 验收、Store 里程碑假设、四阶段 context 所有权、完整 result/error
代数、仅用于 correlation 的 CommandID、barrier 并发证据和显式 running-recovery gap。

评审 round 1 纳入记录（2026-08-12）：四份规范文档现已增加 IDs 前共享的纯领域
eligibility predicate；明确 Task 7 修改既有 `application/ports.go`、`ports_test.go`、
`application/eventstoretest/suite.go` 与 `adapters/memory/event_store_test.go`，以交付并通过
barrier 证明 no-ambiguous-error 合同；append acceptance 采用 option B（exact returned
records、ordered Apply success、final Version），不引入独立 state oracle；一手证据只采用
官方来源，DeepSeek-Reasonix 仅保留为明确标注的非权威社区上下文。
