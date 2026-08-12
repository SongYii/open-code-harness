# 工业级 Engine 最小纵切实施计划

> 说明：英文计划 `2026-08-12-engine-vertical-slice.md` 是规范性执行来源，包含逐测试代码、精确失败预期和提交命令；本文是与其任务、接口和质量门同步的中文执行阅读版。如有歧义，以英文规范版为准。

> **面向智能体工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans，逐项实施。使用复选框（`- [ ]`）跟踪；每个任务由新鲜上下文执行并独立评审。

**目标：** 构建第一个工业级、可执行的 Go Turn 路径：确定性模型流通过唯一 Application 权威进入事件溯源领域，生成持久 assistant message，并以有界、可重放、并发安全的语义终结。

**架构：** `internal/harness/application` 负责命令、重放、乐观并发和持久 append；`internal/harness/engine` 负责 provider-neutral 的有界流消费；`internal/harness/domain` 仍是纯生命周期事实来源。`MemoryEventStore`、`ScriptedModel`、固定时钟、序列 ID 与 RecordingSink 都实现未来生产适配器使用的正式端口，离线测试不走 Demo 旁路。

**技术栈：** Go 1.26、仅 Go 标准库、`testing`、JSON Lines fixture、GitHub Actions race detector。

## 全局约束

- 模块路径保持 `github.com/SongYii/open-code-harness`，最低 Go 版本保持 `1.26`。
- 本里程碑不增加第三方依赖。
- 当前是 pre-v0 工业级质量纵切，不是教程、原型，也不宣称已经 GA。
- 依赖方向固定为 `adapters/testkit → application → engine + domain`；domain 不导入 application/engine，engine 不导入具体 adapter、provider SDK、ACP、TUI 或 testkit。
- Application Service 是唯一命令权威，adapter 不得调用模型后自行制造持久生命周期事件。
- `RunTurn` 同步执行；核心不创建无界 channel 或 detached goroutine。
- `DefaultMaxAssistantBytes` 精确为 `1 << 20`（1 MiB），配置必须大于零，delta 在接收前检查边界。
- `DefaultTerminalCommitTimeout` 精确为 `5 * time.Second`；取消清理使用 `context.WithoutCancel` 加该 timeout，仍在原调用栈内完成。
- 模型文本必须是有效 UTF-8；接受的字节不得 trim、normalize 或重写。空成功输出明确允许。
- runtime delta 是瞬时信号；只持久化最终成功文本、稳定失败/中断数据和 Turn/Item 生命周期事实。
- 一个 assistant Item 只属于一个 Turn；当前一个 Session 最多一个 running Turn、一个 running Item。
- Item/Turn 终态事件在同一 EventStore append 批次中原子提交。
- EventStore CAS 是并发权威；冲突返回调用方，永不自动重试。
- append 批次全成或全败；提交前取消和故障注入不写入任何部分状态。
- `ScriptedModel` 与 `MemoryEventStore` 实现正式端口；生产代码不存在 scripted/test-mode 分支。
- 并发测试只使用 barrier/channel，不使用时间 sleep。
- 测试断言稳定类型化 code/category，不依赖错误文案。
- 生产代码不自动记录模型输入/输出、provider 原始 payload 或秘密。
- 每项任务以格式化、聚焦测试、完整测试和一个小提交结束。

## 里程碑边界

本计划只实施已批准 Engine 规格：assistant-message Item 生命周期、EventStore/Model/RuntimeSink 正式端口、确定性适配器、Application Service、有界模型 Turn、失败/取消语义、重放与验证证据。不实施真实 provider、自动重试、生产持久化、崩溃恢复、tool、Policy、approval、ACP、TUI、MCP、Context Engine、OpenTelemetry 或 subagent。

## 文件布局

| 区域 | 文件与职责 |
| --- | --- |
| Domain | `ids.go`、`state.go`、`events.go`、`commands.go`、`decide.go`、`apply.go`、`codec.go`、`record.go`：Item ID、状态、事件、命令、严格编解码、深复制 |
| Application | `ports.go`、`errors.go`、`append.go`、`service.go`、`session.go`、`turn.go`：正式端口、错误分类、唯一命令权威和用例 |
| Engine | `model.go`、`runtime.go`、`errors.go`、`runner.go`：模型流、运行时信号、稳定错误与同步有界 runner |
| Adapter | `adapters/memory/event_store.go`：CAS、原子批次、防御复制、故障注入 |
| Testkit | `clock.go`、`ids.go`、`scripted_model.go`、`recording_sink.go`：确定性正式适配器 |
| Contract suites | `eventstoretest/suite.go`、`modeltest/suite.go`、`enginescenariotest/suite.go` |
| Evidence | assistant/domain 与 RunTurn JSONL fixtures、并发/race/依赖边界测试、架构文档 |

---

### 任务 1：增加 Assistant Item 的已记录生命周期

**修改文件：** `domain/ids.go`、`state.go`、`events.go`、`record.go`、`apply.go`、`codec.go` 及对应测试。

**产出接口：**

```go
type ItemID string
func ParseItemID(string) (ItemID, error)

type ItemKind string
const ItemKindAssistantMessage ItemKind = "assistant_message"

type ItemStatus string
const (
	ItemStatusRunning ItemStatus = "running"
	ItemStatusCompleted ItemStatus = "completed"
	ItemStatusFailed ItemStatus = "failed"
	ItemStatusInterrupted ItemStatus = "interrupted"
)
```

`Turn` 增加 `ActiveItemID`、`ItemOrder`、`Items`；`Turn.Clone` 与 `Session.Clone` 深复制嵌套容器。新增四个稳定事件：

```text
assistant.message.started
assistant.message.completed
assistant.message.failed
assistant.message.interrupted
```

- [ ] 先写 Item ID、apply、不可变性和严格 JSON 的失败测试。
- [ ] 验证测试因缺少 Item 类型失败。
- [ ] 实现状态、事件及唯一 running Item 约束。
- [ ] 实现 `CloneEvent` 与 `CloneRecordedEvents`；未知事件拒绝共享。
- [ ] schema v1 严格支持新 payload；拒绝非法 UTF-8、ID、字段和类型。
- [ ] 运行 `gofmt -w internal/harness/domain`、聚焦测试及 `go test ./... -count=1`。
- [ ] 提交：`feat(domain): add assistant item lifecycle`。

### 任务 2：增加原子 Item/Turn 命令与重放证据

**修改文件：** `domain/errors.go`、`commands.go`、`decide.go`、`apply.go`、相关测试、`testdata/assistant_lifecycle.jsonl`、`docs/architecture/domain-events.md`。

**命令：**

```go
type StartAssistantMessage struct { SessionID SessionID; TurnID TurnID; ItemID ItemID }
type CompleteAssistantTurn struct { SessionID SessionID; TurnID TurnID; ItemID ItemID; Text string }
type FailAssistantTurn struct { SessionID SessionID; TurnID TurnID; ItemID ItemID; Code string; Message string }
type InterruptAssistantTurn struct { SessionID SessionID; TurnID TurnID; ItemID ItemID; Reason string }
```

后三个命令分别返回“Item 终态在前、Turn 终态在后”的两个事件。新增稳定错误码 `item_already_running`、`item_not_running`、`item_mismatch`、`item_already_exists`。

- [ ] 先测试完成/失败/中断均返回两个事件的原子决策批次。
- [ ] 测试错误 Item、重复 Item、第二个 Item、跨 Turn Item 和二次终态。
- [ ] existing Turn terminal command/apply 在 Item running 时必须失败。
- [ ] 建立 8 条事件的 assistant fixture，序号严格 `1..8`，完成 pair 共用 command ID。
- [ ] 重放必须得到关闭 Session、准确 Unicode 文本及第二个 interrupted Turn。
- [ ] 更新领域契约后运行 domain/full tests。
- [ ] 提交：`feat(domain): decide atomic assistant turns`。

### 任务 3：定义 Application 端口、类型化错误与确定性源

**创建文件：** `application/doc.go`、`ports.go`、`errors.go`、测试；`testkit/clock.go`、`ids.go`、测试。

**正式端口：**

```go
type EventStore interface {
	Load(context.Context, domain.SessionID) ([]domain.RecordedEvent, error)
	Append(context.Context, AppendRequest) ([]domain.RecordedEvent, error)
}
type AppendRequest struct {
	SessionID domain.SessionID
	ExpectedVersion uint64
	CommandID domain.CommandID
	Events []domain.Event
}
type Clock interface { Now() time.Time }
type IDGenerator interface {
	NewSessionID() (domain.SessionID, error)
	NewTurnID() (domain.TurnID, error)
	NewItemID() (domain.ItemID, error)
	NewCommandID() (domain.CommandID, error)
	NewEventID() (domain.EventID, error)
}
```

错误 category 固定为 `validation`、`conflict`、`model`、`canceled`、`output_limit`、`delivery`、`persistence`、`internal`；`application.Error` 携带 `Code`、`TerminalCommitted` 和 `Cause`。`Error()` 只呈现稳定 category/code/commit 状态，不拼接原始 cause 文本；`Unwrap()` 只供显式程序化检查。`VersionConflictError` 携带 session/expected/actual version。

- [ ] 测试固定时钟、各类型序列 ID、32 goroutine 唯一性与错误链。
- [ ] 实现正式端口和 CAS/原子/防御复制文档契约。
- [ ] 实现 UTC `FixedClock` 和互斥保护的五组独立 ID counter。
- [ ] 运行 application/testkit race tests 和全量测试。
- [ ] 提交：`feat(application): define runtime ports`。

### 任务 4：实现原子 MemoryEventStore 与契约套件

**创建文件：** `application/eventstoretest/suite.go`、`adapters/memory/event_store.go` 及测试。

**产出：**

```go
func NewEventStore(application.Clock, application.IDGenerator) (*EventStore, error)
func (*EventStore) FailNextLoad(domain.SessionID, error)
func (*EventStore) FailNextAppend(domain.SessionID, error)
func eventstoretest.Run(*testing.T, eventstoretest.Factory)
```

- [ ] 契约套件先覆盖连续 metadata、CAS、注入失败原子性、取消和防御复制。
- [ ] store 在 mutex 内依次验证 context/ID/非空事件、CAS、故障、克隆、ID/时间、Replay，再一次性赋值提交。
- [ ] 任一步骤失败时，持久切片与版本均不变化。
- [ ] 同 Session 两个 append 一胜一冲突；32 个独立 Session 并发成功，无 sleep。
- [ ] 运行 memory 聚焦 race 和全仓 race。
- [ ] 提交：`feat(memory): add atomic event store`。

### 任务 5：定义 Engine 流契约与正式测试适配器

**创建文件：** `engine/doc.go`、`model.go`、`runtime.go`、`errors.go`、`modeltest/suite.go`；`testkit/scripted_model.go`、`recording_sink.go` 及测试。

**模型接口：**

```go
type Model interface { Stream(context.Context, ModelRequest) (ModelStream, error) }
type ModelStream interface {
	Next(context.Context) (StreamEvent, error)
	Close() error
}
```

Stream 只允许 `text_delta* → completed`；completed 带文本、completed 前 EOF、未知事件均非法。Runtime envelope 必须携带 Session/Turn/Item/Command ID、单调 `Ordinal`、Type、Text、Code。`RuntimeSink.Emit` 同步内联。

稳定 Engine code：`invalid_request`、`model_startup`、`model_stream`、`canceled`、`output_limit`、`delivery`、`invalid_stream`。Engine `Error()` 同样不得自动拼接 provider cause 文本。

`ScriptedStep` 从一开始包含 `Event`、`Err`、`WaitForCancel`、`Entered`、`Release`，以支持无 sleep 的取消/竞态测试。

- [ ] 先写 Model contract、请求精确匹配、流顺序、取消与并发调用记录测试。
- [ ] 写 RecordingSink 顺序、防御复制和指定 ordinal 失败测试。
- [ ] 实现 Engine 接口、单调 Emitter 和稳定错误。
- [ ] 实现并发安全 ScriptedModel/RecordingSink，不读取环境测试开关。
- [ ] 运行 engine/testkit race 与全量测试。
- [ ] 提交：`feat(engine): define streaming contracts`。

### 任务 6：实现同步有界 TurnRunner

**创建文件：** `engine/runner.go`、`runner_test.go`。

```go
type RunRequest struct {
	ModelRequest
	MaxAssistantBytes int
}
type RunResult struct { Text string }
func NewTurnRunner(Model) (*TurnRunner, error)
func (*TurnRunner) Run(context.Context, RunRequest, *Emitter) (RunResult, error)
```

- [ ] 成功测试证明多 delta Unicode 逐字节保持，runtime 为 started、delta...，runner 不提前发 terminal runtime event。
- [ ] 完整失败矩阵：nil/非法请求、limit、startup/midstream、EOF、未知事件、completed 文本、UTF-8、恰好上限/超一字节、空成功、取消、sink、Close。
- [ ] 执行顺序固定为 validate → Model.Stream → emit started → pull/validate/bound/emit/accumulate → explicit completed → Close → return。
- [ ] 边界检查使用 `len(delta) > limit-builder.Len()`，越界 delta 不发送、不累计。
- [ ] 取消测试使用 Entered/Release barrier，不 sleep、不产生泄漏。
- [ ] 运行 runner 聚焦 race 与全仓 race。
- [ ] 提交：`feat(engine): run bounded model streams`。

### 任务 7：构建 Application Service 与 Session 用例

**创建文件：** `application/service.go`、`append.go`、`session.go` 及测试。

```go
const DefaultMaxAssistantBytes = 1 << 20
const DefaultTerminalCommitTimeout = 5 * time.Second
func NewService(EventStore, IDGenerator, *engine.TurnRunner, Config) (*Service, error)
```

- [ ] 先测试 create/load/close、domain 拒绝、load/append/ID 错误和取消。
- [ ] 构造器拒绝 nil 依赖和非正配置。
- [ ] 唯一 decide/append/apply helper 负责 `Decide → Append(expectedVersion) → Apply returned records`，不 reload、不 retry。
- [ ] 错误映射固定：domain→validation，VersionConflict→conflict，context→canceled，其余存储→persistence，坏 store 返回→internal。
- [ ] `CreateSession` 在 version 0 append；`LoadSession` 对空流返回 session_not_found；`CloseSession` 通过同一 helper。
- [ ] 返回状态和记录均防御复制。
- [ ] 运行 session 聚焦 race 与全仓 race。
- [ ] 提交：`feat(application): add session service`。

### 任务 8：编排成功 RunTurn 持久路径

**创建文件：** `application/turn.go`、`turn_success_test.go`。

```go
type RunTurnRequest struct {
	SessionID domain.SessionID
	Input string
	Sink engine.RuntimeSink
}
type RunTurnResult struct {
	SessionID domain.SessionID
	TurnID domain.TurnID
	ItemID domain.ItemID
	Status domain.TurnStatus
	Text string
	TerminalCommitted bool
	DeliveryWarning error
	Records []domain.RecordedEvent
}
```

成功顺序必须是：Load/Replay；生成 Turn/Item/Command ID；append turn.started；append assistant.started；创建一个 Emitter；Runner.Run；原子 append assistant.completed + turn.completed；emit append.completed；emit stream.completed。

- [ ] 端到端测试使用真实 Service、MemoryEventStore、ScriptedModel 和 RecordingSink。
- [ ] 断言持久事件顺序、终态 pair 共用 command ID、runtime ordinal/correlation 和 exact text。
- [ ] 同 Session 顺序执行两个 Turn，重放状态与返回结果一致。
- [ ] 同一 RunTurn 的所有 append 使用同一 command ID；冲突方不调用模型。
- [ ] 终态提交后 sink 失败不改写成功：返回 completed result 与 terminal=true 的 delivery error/warning。
- [ ] 运行 RunTurn 聚焦 race 与全仓 race。
- [ ] 提交：`feat(application): persist successful turns`。

### 任务 9：补齐失败、取消、Delivery 与 Persistence 语义

**修改/创建文件：** `application/turn.go`、`turn_failure_test.go`、ScriptedModel barrier 测试。

持久映射固定为：

```text
model_startup  → model_startup / "model failed before streaming"
model_stream   → model_stream / "model stream failed"
output_limit   → output_limit / "assistant output exceeded limit"
invalid_stream → invalid_stream / "model stream violated contract"
caller cancel  → interrupted / "caller_canceled"
sink failure   → interrupted / "runtime_delivery_failed"
```

- [ ] 测试 startup、首 delta 前/后、invalid stream/UTF-8、limit、空成功和 Close failure。
- [ ] 测试取消发生在任何 append 前、turn.start 后、stream 中、completion 竞争中和终态后。
- [ ] 测试 sink started/delta 失败，initial/item/terminal append 失败，cleanup append 失败。
- [ ] partial delta 永不持久化；raw provider error 永不进入领域。
- [ ] 取消清理使用 `context.WithoutCancel` + 5 秒 timeout，同步尝试 Turn 或 Item/Turn interrupt。
- [ ] terminal append 失败返回 persistence 且 terminal=false；终态提交后的 delivery 失败 terminal=true。
- [ ] 原 primary model/canceled category 不被后续 terminal runtime delivery warning 替换。
- [ ] 运行 application/testkit race 与全仓 race。
- [ ] 提交：`feat(application): make turn failures durable`。

### 任务 10：增加可复用场景、并发门禁、Fixture 与已实现文档

**创建/修改文件：** `enginescenariotest/suite.go`、`scenario_test.go`、`concurrency_test.go`、`testdata/run_turn_success.jsonl`、`architecture/dependencies_test.go`、`docs/architecture/engine-vertical-slice.md`、README 和 docs index。

- [ ] `enginescenariotest.Run` 的 Scenario 显式携带 `CancelDuringStream` 与 `SinkFailOrdinal`，统一覆盖 success、startup/midstream、cancel、limit 边界和 delivery failure，并通过真实 Service、正式 Sink 与 barrier 重放结果。
- [ ] 同 Session 用 Load barrier 让两次调用读取同一 version，断言一胜一 conflict 且模型总调用一次。
- [ ] 32 个独立 Session 并发完成并通过 race detector。
- [ ] 成功 JSONL fixture 必须由 `domain.MarshalRecordedEvent` 生成；live trace 与 fixture 只归一化注入的 ID/时钟。
- [ ] 使用 `go/parser`、`go/token`、`filepath.WalkDir` 自动检查 domain/engine/application 依赖方向、无 ScriptedModel 生产分支，并禁止 application/engine/memory 生产代码导入 `os`、`os/exec`、`net` 或 `net/http`。
- [ ] Engine 架构文档记录接口、生命周期、CAS/原子语义、常量、错误、runtime/durable 分离、取消边界、running-boundary 限制和延后能力。
- [ ] 只有全部门禁通过后，README/docs index 才把 Engine 标记为 implemented；继续保留 pre-v0/not-GA 以及 provider/tools/ACP/TUI/persistence 未实现声明。
- [ ] 完整执行：

```bash
gofmt -w .
test -z "$(gofmt -l .)"
go vet ./...
go test ./... -count=1
go test -race ./... -count=1
git diff --check
```

- [ ] 独立评审必须检查规格覆盖、唯一命令权威、依赖方向、原子性、取消竞态、防御复制、错误分类、资源边界与 scope exclusion；清零 critical/important finding 后重跑全部门禁。
- [ ] 提交：`test: verify industrial engine vertical slice`；提交后再次确认工作树干净和 race PASS。

## 完成门

只有十个任务提交全部存在、每个复选框都有命令证据、EventStore/Model contract suite 与 Engine scenario suite 全部通过、race/重放/fixture/依赖边界均通过、文档仍准确声明 pre-v0/not-GA，且独立评审没有未解决 critical/important 缺陷时，实施分支才可以集成。仅 happy path 通过不算完成。
