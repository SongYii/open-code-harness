# 已实现的 Engine 最小纵切

- 状态：已实现内部合同
- 成熟度：pre-v0，尚非通用可用（GA）发布
- 范围：一次同步、供应商中立、仅模型 Turn
- 英文规范设计：[工业级 Engine 最小纵切设计](../superpowers/specs/2026-08-12-engine-vertical-slice-design.md)
- 已实施计划：[Engine 纵切实施计划](../superpowers/plans/2026-08-12-engine-vertical-slice.md)
- 完成证据：[Engine 纵切完成证据台账](engine-vertical-slice-evidence.md#中文证据台账)
- 英文已实现合同：[Implemented Engine Vertical Slice](engine-vertical-slice.md)

本文是英文已实现合同的中文语义阅读版，记录当前代码和测试已经强制执行的行为。它是
内部 Go 合同，不是稳定公共协议；pre-v0 阶段若修改合同，设计、实现、测试和双语文档
必须同步变更。

## 已交付能力

当前真实路径可以创建、加载 Session；原子准入一个 assistant Turn；消费一次有界模型流；
同步发送运行时进度；原子持久化 assistant Item 与 Turn 终态；再通过 Replay 重建相同状态。
成功、模型失败、取消、sink 故障、输出边界、持久化故障和乐观并发冲突都经过同一个
Application/Engine 路径。

准确定位仍是 Minimal Executable Turn Runner，而不是完整工具型 Agent Loop。

## 包权威与依赖方向

```text
headless caller / future protocol adapters
                    |
                    v
internal/harness/application  -----> internal/harness/engine
  命令与持久化权威                    有界流执行权威
                    |
                    v
            internal/harness/domain
               生命周期事实权威

internal/harness/adapters/memory ----实现----> application.EventStore
internal/harness/testkit          ----实现----> application/engine ports
```

- `domain` 不导入 Application、Engine、adapter、testkit、provider、ACP、MCP 或 TUI；
- `engine` 只向内依赖 Domain，不导入 Application、具体 adapter、testkit、provider、ACP、
  MCP 或 TUI；
- `application` 依赖 Engine 与 Domain，但不依赖具体 adapter 或 testkit；
- adapter/testkit 向内实现由消费方拥有的接口；
- 只有 Application 可以围绕模型执行制造持久生命周期命令。

[`dependencies_test.go`](../../internal/harness/architecture/dependencies_test.go)
使用 Go 标准库 AST 自动检查上述方向、生产代码不存在 `ScriptedModel` 分支/类型断言，
以及 Application/Engine/Memory 的宿主和网络 import 边界。

## 已导出的内部接口

Application 持久化边界是 [已实现 EventStore v2 合同](eventstore-v2.zh-CN.md)：

```go
type EventStore interface {
    ReadStream(context.Context, ReadStreamRequest) (StreamPage, error)
    Append(context.Context, AppendRequest) (CommitReceipt, error)
    ResolveAppend(context.Context, ResolveAppendRequest) (AppendResolution, error)
    FindCommandRequest(context.Context, FindCommandRequestRequest) (CommandRequestLookup, error)
}

type Clock interface { Now() time.Time }

type IDGenerator interface {
    NewSessionID() (domain.SessionID, error)
    NewTurnID() (domain.TurnID, error)
    NewItemID() (domain.ItemID, error)
    NewCommandID() (domain.CommandID, error)
    NewAppendID() (domain.AppendID, error)
    NewEventID() (domain.EventID, error)
}
```

`Load` 以及返回 `[]domain.RecordedEvent` 的 v1 `Append` 已不存在。

Application Service：

```go
type Config struct {
    MaxAssistantBytes             int
    TerminalCommitTimeout         time.Duration
    AppendResolutionTimeout       time.Duration
    AppendResolutionMaxOperations uint32
}

func DefaultConfig() Config
func NewService(EventStore, IDGenerator, Clock, *engine.TurnRunner, WriterAuthority, Config) (*Service, error)

func (*Service) CreateSession(context.Context, CreateSessionRequest) (CreateSessionResult, error)
func (*Service) LoadSession(context.Context, domain.SessionID) (domain.Session, error)
func (*Service) CloseSession(context.Context, CloseSessionRequest) (CloseSessionResult, error)
func (*Service) RunTurn(context.Context, RunTurnRequest) (RunTurnResult, error)
```

当前 request/result 精确结构为：

```go
type CreateSessionRequest struct { WorkspaceRoot string }
type CreateSessionResult struct {
    SessionID domain.SessionID
    Records   []domain.RecordedEvent
}

type CloseSessionRequest struct { SessionID domain.SessionID }
type CloseSessionResult struct {
    Session domain.Session
    Records []domain.RecordedEvent
}

type RunTurnRequest struct {
    SessionID domain.SessionID
    RequestID domain.RunTurnRequestID
    Input     string
    Sink      engine.RuntimeSink
}

type RunTurnResult struct {
    SessionID         domain.SessionID
    TurnID            domain.TurnID
    ItemID            domain.ItemID
    Status            domain.TurnStatus
    Text              string
    TerminalCommitted bool
    DeliveryWarning   error
    Records           []domain.RecordedEvent
}
```

Records 是本调用已知提交的全部批次的防御副本。

Engine 模型边界：

```go
type Model interface {
    Stream(context.Context, ModelRequest) (ModelStream, error)
}

type ModelStream interface {
    Next(context.Context) (StreamEvent, error)
    Close() error
}

type RuntimeSink interface {
    Emit(context.Context, RuntimeEvent) error
}

type ModelRequest struct {
    SessionID domain.SessionID
    TurnID    domain.TurnID
    ItemID    domain.ItemID
    Input     string
}

type StreamEvent struct {
    Type StreamEventType
    Text string
}

type Correlation struct {
    SessionID domain.SessionID
    TurnID    domain.TurnID
    ItemID    domain.ItemID
    CommandID domain.CommandID
}

type RuntimePayload struct {
    Type RuntimeEventType
    Text string
    Code string
}

type RuntimeEvent struct {
    Correlation
    Ordinal uint64
    Type    RuntimeEventType
    Text    string
    Code    string
}

type RunRequest struct {
    ModelRequest
    MaxAssistantBytes int
}

type RunResult struct { Text string }

func NewTurnRunner(Model) (*TurnRunner, error)
func (*TurnRunner) Run(context.Context, RunRequest, *Emitter) (RunResult, error)
func NewEmitter(RuntimeSink, Correlation) (*Emitter, error)
func (*Emitter) Emit(context.Context, RuntimePayload) error
```

`ModelRequest` 包含 Session/Turn/Item ID 与准确 input；流语法固定为
`text_delta* -> completed`。调用方提交的 `RuntimePayload` 只有 Type/Text/Code；单次
Run scoped Emitter 独占 Session/Turn/Item/Command 关联字段和从 1 开始的尝试序号。

stream event 只有 `text_delta` 和 `completed`；已实现 runtime type 为
`model.stream.started`、`model.text.delta`、`model.stream.completed`、
`model.stream.failed`、`model.stream.interrupted` 与 `append.completed`。

## 生命周期与执行流程

assistant Item：

```text
absent --assistant.message.started--> running
running --assistant.message.completed--> completed
        --assistant.message.failed-----> failed
        --assistant.message.interrupted-> interrupted
```

Turn：

```text
absent --turn.started--> running
running --turn.completed--> completed
        --turn.failed-----> failed
        --turn.interrupted-> interrupted
```

准入和终态必须成对原子提交：

```text
atomic admission:       turn.started
                        assistant.message.started

atomic success:         assistant.message.completed
                        turn.completed

atomic failure:         assistant.message.failed
                        turn.failed

atomic interruption:    assistant.message.interrupted
                        turn.interrupted
```

完整成功顺序：

```text
校验请求/依赖
  -> Load 完整 Session stream
  -> Replay 并检查领域 eligibility
  -> 生成并校验 Turn/Item/Command ID
  -> 构造唯一 Emitter
  -> atomic admission CAS
  -> Model.Stream 与同步 Next loop
  -> atomic completed Item/Turn CAS
  -> runtime append.completed
  -> runtime model.stream.completed
```

准入批次未被验收前绝不调用模型；终态持久批次未被验收并 Apply 前绝不发送终态
runtime signal。

## CAS 与原子 append

- Session version 精确等于其连续权威 recorded-event stream 长度；
- `ExpectedVersion` 必须准确相等；成功批次含 N 条事件时 version 增加 N，且只返回这 N 条；
- 一个 append 有序且全成全败，不存在 partial admission 或 partial terminal pair；
- 同一批每条记录拥有不同 Event ID、连续 sequence、同一 Command ID 和同一个非零 UTC 时间；
- Load/Append 返回防御副本；Application 只有在 metadata、事件内容/顺序、Apply 和最终版本
  全部精确匹配后才接受返回值；
- 同 Session 并发在线性化点 admission CAS 上决胜；loser 返回 conflict，模型调用为零；
  Application 和 Store 均不隐藏 reload/retry；
- `Append` 非 nil error 表示批次未提交；提交后发生 caller cancellation，当前端口仍要求
  返回已提交记录；
- 一个 RunTurn Command ID 只表示两个 append 批次与 runtime events 的关联谱系，不是
  idempotency 或 Store 去重键。

未来 remote store 若存在“已提交但 acknowledgement 丢失”，不能静默弱化当前端口；必须
增加 exact retry identity 或显式 unknown-commit result。

## 边界、流所有权与清理

```go
const DefaultMaxAssistantBytes = 1 << 20
const DefaultTerminalCommitTimeout = 5 * time.Second
```

- 两项配置均必须大于零；
- 每个 delta 必须非空且为合法 UTF-8；接收字节不 trim、normalize、replacement 或 rechunk；
- 在 sink delivery 与累计前检查 byte limit；恰好达到上限有效，多一个 byte 即
  `output_limit`；
- Engine 同步消费，不创建 goroutine 或 channel；
- 每个非 nil acquired stream（包括 `(stream, error)`）在所有退出路径恰好 Close 一次；
  nil stream 不 Close；
- failure 先取消派生 context 再 Close；显式 completed 先 Close 再取消；Close error 不替换
  既有稳定主 code，并保留在 error tree 中；
- RuntimeSink 同步内联形成背压；生产 shared sink 必须允许不同 Emitter 并发调用。

## 持久事实与瞬时运行时信号

| 持久、可 Replay 事实 | 瞬时 runtime signal |
| --- | --- |
| `turn.started` | `model.stream.started` |
| `assistant.message.started` | `model.text.delta` |
| `assistant.message.completed` | `append.completed` |
| `assistant.message.failed` | `model.stream.completed` |
| `assistant.message.interrupted` | `model.stream.failed` |
| `turn.completed`、`turn.failed`、`turn.interrupted` | `model.stream.interrupted` |

文本 delta 不持久化；completed assistant Item 保存准确最终文本。failed/interrupted Item
只保存稳定终态 code/message，绝不把 partial model output 写成成功答案。

Runtime ordinal 是单次 RunTurn 内从 1 开始的 sink 尝试顺序。sink 失败消耗 ordinal；payload
非法或 attempt 前取消不消耗。它不是 durable sequence，也不是全局时钟。

## 取消边界与终态权威

| 阶段 | 持久边界 | Context 与结果 |
| --- | --- | --- |
| Preflight | 无 | caller context；取消不写记录、不调模型 |
| Admission | started Turn + Item | caller context；一个原子 CAS 是模型调用边界 |
| Execution | runtime started/deltas | caller context 传递到 Model 和 RuntimeSink |
| Terminalization: success | completed Item + Turn | caller context；已验收 commit 胜过之后取消 |
| Terminalization: failure/interruption | failed/interrupted Item + Turn | `context.WithoutCancel` 立即加配置 timeout，仍在同一调用栈 |

有界 cleanup context 只用于持久化 admission 后的失败/中断，不用于模型、普通 success、
runtime delivery、retry 或 detached background work。终态后的 delivery failure 只设置
`DeliveryWarning`，不能改写 durable completion。

## 错误合同

Application error 暴露稳定 Category、Code 与 `TerminalCommitted`；原始 cause 只供显式
unwrap，不进入稳定错误文案。

| Category | 代表 code/结果 | 重试责任 |
| --- | --- | --- |
| `validation` | `invalid_request`、`session_not_found`、`domain_rejected` | caller 修改请求 |
| `conflict` | `version_conflict`，未提交终态 | caller 显式 reload 后重新决策 |
| `model` | `model_startup`、`model_stream`、`invalid_stream` | 未来策略；当前不 retry |
| `canceled` | `canceled`，admission 后持久 interrupted pair | caller 决定是否新建 Turn |
| `output_limit` | `output_limit`，持久 failed pair | caller/配置策略 |
| `delivery` | `runtime_delivery_failed`；终态前 interrupted，终态后 warning | adapter/caller |
| `persistence` | `load_failed`、`append_failed`；可能保留 running boundary | 存储/恢复策略 |
| `internal` | Store/ID/Engine 合同违约 | operator/developer |

Engine 稳定 code 为 `invalid_request`、`model_startup`、`model_stream`、`canceled`、
`output_limit`、`delivery`、`invalid_stream`。Engine/Application matcher 均遍历完整
wrap/join error tree，并安全处理 typed nil。稳定错误字符串不拼接 provider/sink 原始文案。

## 正式适配器与可执行证据

- `MemoryEventStore` 是 mutex 保护、具备 CAS/原子性与确定性一次性预提交故障的正式
  EventStore 合同实现；
- `ScriptedModel` 实现准确 Model 端口、请求断言、独立 stream、确定性 barrier、
  startup/stream/close 故障和并发调用记录；
- `FixedClock` 与 `SequenceIDs` 提供确定性、并发安全 metadata；
- `RecordingSink` 在一次性 ordinal 故障前记录 Attempts，成功后才记录 Delivered，快照均
  为防御副本；
- 可复用套件是 `eventstoretest.Run`、`modeltest.Run`、`enginescenariotest.Run`；
- `enginescenariotest` 自己定义与 adapter 无关的 `Step`、`ModelBehavior`，以及准确的
  Application error、durable terminal、runtime event 期望；Factory 负责把这些行为翻译为
  具体模型 fixture。Harness 分开暴露 `RuntimeAttempts`（包含 sink 拒绝的每一次调用）和
  `RuntimeDelivered`（仅 sink 接受的调用）；套件精确检查 ordinal、correlation、Type、
  Text、Code、外层 Application Category/Code/TerminalCommitted，以及 Replay 后的终态
  Code/Message；
- [成功 JSONL trace](../../internal/harness/application/testdata/run_turn_success.jsonl)
  通过真实 Service 和 `domain.MarshalRecordedEvent` 生成；测试逐记录与 live run 精确比较并
  Replay；
- barrier 测试证明同 Session 并发 admission 一胜一 conflict，以及 32 个独立 Session 通过
  同一 Store、ID source、runner 和 shared sink 全部完成。

完整本地证据命令：

```bash
gofmt -w .
test -z "$(gofmt -l .)"
go vet ./...
go test ./... -count=1
go test -race ./... -count=1
git diff --check
```

已完成计划还记录了本地 Markdown link check。

## 已知 running-boundary 限制

外部模型工作之前已经持久提交 admission，因此 admission 后进程退出，或 terminal
persistence/conflict/Store-contract failure，可能留下可合法 Replay 的 running Turn 与 Item。
当前结果会准确返回 `Status == running`、`TerminalCommitted == false` 和已验收 admission
records；不会发送虚假 success signal，也不会盲目重调模型或 append。

startup reconciliation、continuation 和 recovery 尚未实现。生产 reconciliation 的设计与
适配器是 GA 前阻断能力。

## 明确延后的能力

本里程碑未实现：

- 真实 provider 合同/adapter；鉴权、限流、retry、cache repair、usage、cost、fallback；
- tools、Tool Runtime、Policy、approval、workspace sandbox；
- ACP/JSON-RPC、TUI、IDE、公有 SDK 或公共协议兼容；
- 生产 file/SQLite/remote persistence、ambiguous-commit protocol、snapshot、checkpoint、
  migration、backup、reconciliation、recovery；
- Context Engine、prompt、compaction、memory、Skills、MCP、subagent、多 Agent graph；
- 持久 runtime log、catch-up、OpenTelemetry 与完整 evaluation infrastructure。

这些排除项用于保持依赖顺序正确，不降低当前 pre-v0 纵切已经验证的合同，也防止把本
里程碑误称为 GA harness。
