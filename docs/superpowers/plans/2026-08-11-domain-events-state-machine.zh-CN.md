# Harness 领域事件与状态机实施计划

> 注：英文计划是规范性的执行来源；本文档是与其同步的中文阅读副本。

> **面向智能体工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans，逐项任务实施本计划。步骤使用复选框（`- [ ]`）语法跟踪。

**目标：** 构建确定性的 Go 领域基础：已验证命令产生带类型事件，已记录事件以明确的终止语义重建会话/轮次状态。

**架构：** 包 `internal/harness/domain` 是纯领域模块，不依赖 ACP、MCP、模型 SDK、文件系统、时钟、随机性、日志或存储。决策函数返回未提交的带类型事件；应用函数消费携带元数据的已记录事件；回放是在有序事件流上的折叠。事件 JSON 用于夹具和未来持久化，但本里程碑不实现生产事件存储或可执行 Engine。

**技术栈：** Go 1.26、Go 标准库、`testing`、JSON Lines 夹具、GitHub Actions。

## 全局约束

- 模块路径为 `github.com/SongYii/open-code-harness`。
- 最低 Go 语言版本为 `1.26`，即计划制定时当前稳定主版本。
- 本里程碑只使用 Go 标准库；`go.mod` 不含第三方依赖。
- 所有领域代码位于 `internal/harness/domain`；它不是公共客户端 API。
- 领域决策是纯函数：不调用 `time.Now`，不生成 UUID/ULID，不访问文件系统，不读取环境变量，不进行网络调用，也没有全局可变状态。
- ACP v1 仍是未来的公共客户端协议，但本里程碑不导入 ACP 类型。
- MCP、Provider 适配器、TUI、工具、审批、items、持久化后端及 OpenTelemetry 不在本里程碑范围内。
- 会话状态严格为 `active` 和 `closed`。
- 轮次状态严格为 `running`、`completed`、`failed` 和 `interrupted`。
- 轮次终止事件相互排斥；终止轮次不能再转换。
- 一个会话最多只能有一个运行中轮次，且轮次运行时不能关闭。
- 已记录事件序号从 `1` 开始，且每个会话内必须连续。
- 已记录时间戳由调用方提供，归一化为 UTC，并以 RFC3339 纳秒精度编码。
- 测试断言稳定错误码，而非匹配偶发的错误措辞。
- 每项任务以 `gofmt`、聚焦测试、完整测试及一个小提交结束。

## 里程碑边界

本计划只实施架构设计第 13 节第 1 项：Harness 领域、事件模型以及会话/轮次状态机。同时提供内存中的确定性回放函数，以便验证领域行为。生产级只追加持久化和可执行 Engine 的纵向切片有意留给下一份规格和计划。

## 文件映射

| 路径 | 职责 |
|---|---|
| `go.mod` | 模块标识和最低 Go 版本 |
| `internal/harness/domain/doc.go` | 包契约和依赖边界 |
| `internal/harness/domain/errors.go` | 稳定的领域错误码和类型化错误 |
| `internal/harness/domain/ids.go` | 强类型会话、轮次、命令和事件标识符 |
| `internal/harness/domain/state.go` | 会话/轮次状态和不可变克隆辅助函数 |
| `internal/harness/domain/events.go` | 类型化领域事件和稳定事件名称 |
| `internal/harness/domain/record.go` | 事件元数据以及已记录/未提交信封 |
| `internal/harness/domain/commands.go` | 类型化领域命令和稳定命令名称 |
| `internal/harness/domain/decide.go` | 命令验证和事件决策 |
| `internal/harness/domain/apply.go` | 已记录事件验证和状态转换 |
| `internal/harness/domain/codec.go` | 已记录事件的带版本 JSON 编解码 |
| `internal/harness/domain/replay.go` | 确定性有序回放 |
| `internal/harness/domain/*_test.go` | 聚焦的行为测试 |
| `internal/harness/domain/test_helpers_test.go` | 测试共享的确定性状态/记录构建器 |
| `internal/harness/domain/testdata/session_lifecycle.jsonl` | 规范的跨版本回放夹具 |
| `docs/architecture/domain-events.md` | 状态机、不变量和事件目录 |
| `.github/workflows/ci.yml` | 格式化、vet、竞态和测试门禁 |

---

### 任务 1：引导 Go 模块、强类型 ID 和稳定错误

**文件：**
- 创建：`go.mod`
- 创建：`internal/harness/domain/doc.go`
- 创建：`internal/harness/domain/errors.go`
- 创建：`internal/harness/domain/ids.go`
- 测试：`internal/harness/domain/ids_test.go`

**接口：**
- 产出：`SessionID`、`TurnID`、`CommandID`、`EventID`
- 产出：`ParseSessionID(string) (SessionID, error)`、`ParseTurnID(string) (TurnID, error)`、`ParseCommandID(string) (CommandID, error)`、`ParseEventID(string) (EventID, error)`
- 产出：`ErrorCode`、`DomainError`、`IsCode(error, ErrorCode) bool`

- [ ] **步骤 1：编写失败的标识符和错误测试**

```go
package domain

import "testing"

func TestParseSessionID(t *testing.T) {
	t.Parallel()

	got, err := ParseSessionID("session-1")
	if err != nil {
		t.Fatalf("ParseSessionID() error = %v", err)
	}
	if got != SessionID("session-1") {
		t.Fatalf("ParseSessionID() = %q", got)
	}
}

func TestParseSessionIDRejectsBlankOrPaddedValues(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"", "   ", " session-1", "session-1 "} {
		_, err := ParseSessionID(input)
		if !IsCode(err, CodeInvalidID) {
			t.Fatalf("ParseSessionID(%q) error = %v, want code %q", input, err, CodeInvalidID)
		}
	}
}
```

- [ ] **步骤 2：运行聚焦测试并验证预期失败**

运行：`go test ./internal/harness/domain -run 'TestParseSessionID' -count=1`

预期：FAIL，因为 `ParseSessionID`、`SessionID`、`IsCode` 和 `CodeInvalidID` 尚不存在。

- [ ] **步骤 3：添加模块和包契约**

```go
// go.mod
module github.com/SongYii/open-code-harness

go 1.26
```

```go
// internal/harness/domain/doc.go
// Package domain contains the deterministic state and transition rules for
// Open Code Harness sessions and turns. It must remain independent of
// transports, storage, model providers, tools, clocks, and ID generators.
package domain
```

- [ ] **步骤 4：实现稳定错误和强类型 ID 解析器**

```go
// errors.go
package domain

import "errors"

type ErrorCode string

const (
	CodeInvalidID            ErrorCode = "invalid_id"
	CodeInvalidCommand       ErrorCode = "invalid_command"
	CodeInvalidEvent         ErrorCode = "invalid_event"
	CodeSessionAlreadyExists ErrorCode = "session_already_exists"
	CodeSessionNotFound      ErrorCode = "session_not_found"
	CodeSessionClosed        ErrorCode = "session_closed"
	CodeTurnAlreadyRunning   ErrorCode = "turn_already_running"
	CodeTurnNotRunning       ErrorCode = "turn_not_running"
	CodeTurnMismatch         ErrorCode = "turn_mismatch"
	CodeTurnAlreadyExists    ErrorCode = "turn_already_exists"
	CodeSequenceMismatch     ErrorCode = "sequence_mismatch"
)

type DomainError struct {
	Code    ErrorCode
	Message string
}

func (e *DomainError) Error() string { return string(e.Code) + ": " + e.Message }

func IsCode(err error, code ErrorCode) bool {
	var target *DomainError
	return errors.As(err, &target) && target.Code == code
}
```

将每种 ID 实现为独立字符串类型。四个解析器都使用同一个未导出辅助函数，拒绝空字符串和会被 `strings.TrimSpace` 改变的值；不要归一化已接受的 ID。

```go
type SessionID string
type TurnID string
type CommandID string
type EventID string

func ParseSessionID(value string) (SessionID, error) {
	if err := validateID(value); err != nil {
		return "", err
	}
	return SessionID(value), nil
}
```

- [ ] **步骤 5：格式化并运行聚焦和完整测试**

运行：`gofmt -w internal/harness/domain`

运行：`go test ./internal/harness/domain -run 'TestParseSessionID' -count=1`

预期：PASS。

运行：`go test ./... -count=1`

预期：PASS。

- [ ] **步骤 6：提交领域原语**

```bash
git add go.mod internal/harness/domain/doc.go internal/harness/domain/errors.go internal/harness/domain/ids.go internal/harness/domain/ids_test.go
git commit -m "feat(domain): add identifiers and stable errors"
```

---

### 任务 2：定义已记录事件并应用会话创建

**文件：**
- 创建：`internal/harness/domain/state.go`
- 创建：`internal/harness/domain/events.go`
- 创建：`internal/harness/domain/record.go`
- 创建：`internal/harness/domain/apply.go`
- 测试：`internal/harness/domain/apply_test.go`

**接口：**
- 消费：任务 1 的 ID 和错误类型
- 产出：`Session`、`Turn`、`SessionStatus`、`TurnStatus`
- 产出：`Event`、`SessionCreated`、`TurnStarted`、`TurnCompleted`、`TurnFailed`、`TurnInterrupted`、`SessionClosed`
- 产出：`UncommittedEvent`、`RecordedEvent`、`Apply(Session, RecordedEvent) (Session, error)`

- [ ] **步骤 1：编写失败的会话创建测试**

```go
func TestApplySessionCreated(t *testing.T) {
	t.Parallel()

	record := RecordedEvent{
		SchemaVersion: 1,
		ID:            EventID("event-1"),
		CommandID:     CommandID("command-1"),
		SessionID:     SessionID("session-1"),
		Sequence:      1,
		OccurredAt:    time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC),
		Event:         SessionCreated{WorkspaceRoot: "/workspace"},
	}

	got, err := Apply(Session{}, record)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if got.ID != SessionID("session-1") || got.Status != SessionStatusActive || got.Version != 1 {
		t.Fatalf("Apply() state = %#v", got)
	}
	if got.WorkspaceRoot != "/workspace" || len(got.Turns) != 0 {
		t.Fatalf("Apply() state = %#v", got)
	}
}

func TestApplyRejectsNonInitialSequence(t *testing.T) {
	t.Parallel()

	_, err := Apply(Session{}, RecordedEvent{
		SchemaVersion: 1,
		ID: EventID("event-2"), CommandID: CommandID("command-2"),
		SessionID: SessionID("session-1"), Sequence: 2,
		OccurredAt: time.Now(), Event: SessionCreated{WorkspaceRoot: "/workspace"},
	})
	if !IsCode(err, CodeSequenceMismatch) {
		t.Fatalf("Apply() error = %v, want code %q", err, CodeSequenceMismatch)
	}
}
```

测试可以使用 `time.Now` 创建输入；生产领域代码不得调用它。

- [ ] **步骤 2：运行聚焦测试并验证预期失败**

运行：`go test ./internal/harness/domain -run 'TestApply' -count=1`

预期：FAIL，因为状态、事件、记录和 `Apply` 类型尚不存在。

- [ ] **步骤 3：定义状态和事件类型**

```go
type SessionStatus string
const (
	SessionStatusActive SessionStatus = "active"
	SessionStatusClosed SessionStatus = "closed"
)

type TurnStatus string
const (
	TurnStatusRunning     TurnStatus = "running"
	TurnStatusCompleted   TurnStatus = "completed"
	TurnStatusFailed      TurnStatus = "failed"
	TurnStatusInterrupted TurnStatus = "interrupted"
)

type Turn struct {
	ID           TurnID
	Status       TurnStatus
	Input        string
	StartedAt    time.Time
	EndedAt      time.Time
	FailureCode  string
	FailureText  string
	InterruptWhy string
}

type Session struct {
	ID             SessionID
	Status         SessionStatus
	Version        uint64
	WorkspaceRoot  string
	ActiveTurnID   TurnID
	TurnOrder      []TurnID
	Turns          map[TurnID]Turn
}

func (s Session) Exists() bool { return s.ID != "" }
```

定义事件接口和稳定名称：

```go
type Event interface { EventType() string }

const (
	EventSessionCreated  = "session.created"
	EventTurnStarted     = "turn.started"
	EventTurnCompleted   = "turn.completed"
	EventTurnFailed      = "turn.failed"
	EventTurnInterrupted = "turn.interrupted"
	EventSessionClosed   = "session.closed"
)

type SessionCreated struct { WorkspaceRoot string `json:"workspaceRoot"` }
func (SessionCreated) EventType() string { return EventSessionCreated }
```

为其余每个事件提供具体负载和 `EventType` 方法。终止事件携带 `TurnID`；`TurnFailed` 还携带 `Code` 和 `Message`；`TurnInterrupted` 携带 `Reason`；`TurnStarted` 携带 `TurnID` 和 `Input`；`SessionClosed` 没有字段。

- [ ] **步骤 4：定义事件信封和最小应用逻辑**

```go
type UncommittedEvent struct { Event Event }

type RecordedEvent struct {
	SchemaVersion int
	ID            EventID
	CommandID     CommandID
	SessionID     SessionID
	Sequence      uint64
	OccurredAt    time.Time
	Event         Event
}
```

`Apply` 首先验证模式版本为 `1`、ID 非空、时间戳非零，将提供的时间戳归一化为 UTC，并要求 `Sequence == state.Version+1`。对于 `SessionCreated`，要求状态为空且工作区根目录非空，初始化映射/切片，设置 `Status=SessionStatusActive`，并设置 `Version=Sequence`。未知事件返回 `CodeInvalidEvent`。

- [ ] **步骤 5：格式化并运行测试**

运行：`gofmt -w internal/harness/domain`

运行：`go test ./internal/harness/domain -run 'TestApply' -count=1`

预期：PASS。

运行：`go test ./... -count=1`

预期：PASS。

- [ ] **步骤 6：提交已记录事件的应用逻辑**

```bash
git add internal/harness/domain/state.go internal/harness/domain/events.go internal/harness/domain/record.go internal/harness/domain/apply.go internal/harness/domain/apply_test.go
git commit -m "feat(domain): apply recorded session events"
```

---

### 任务 3：决策会话创建和轮次启动命令

**文件：**
- 创建：`internal/harness/domain/commands.go`
- 创建：`internal/harness/domain/decide.go`
- 创建：`internal/harness/domain/test_helpers_test.go`
- 修改：`internal/harness/domain/apply.go`
- 测试：`internal/harness/domain/decide_test.go`
- 测试：`internal/harness/domain/apply_test.go`

**接口：**
- 消费：`Session`、`Event` 和稳定错误
- 产出：`Command`、`CreateSession`、`StartTurn`、`CompleteTurn`、`FailTurn`、`InterruptTurn`、`CloseSession`
- 产出：`Decide(Session, Command) ([]UncommittedEvent, error)`

- [ ] **步骤 1：编写会话创建和轮次启动的失败决策测试**

```go
func TestDecideCreateSession(t *testing.T) {
	t.Parallel()

	events, err := Decide(Session{}, CreateSession{
		SessionID: SessionID("session-1"), WorkspaceRoot: "/workspace",
	})
	if err != nil { t.Fatalf("Decide() error = %v", err) }
	want := []UncommittedEvent{{Event: SessionCreated{WorkspaceRoot: "/workspace"}}}
	if !reflect.DeepEqual(events, want) { t.Fatalf("Decide() = %#v, want %#v", events, want) }
}

func TestDecideStartTurnRejectsBlankInput(t *testing.T) {
	t.Parallel()

	state := activeSessionForTest(t)
	_, err := Decide(state, StartTurn{
		SessionID: state.ID, TurnID: TurnID("turn-1"), Input: "  ",
	})
	if !IsCode(err, CodeInvalidCommand) {
		t.Fatalf("Decide() error = %v, want code %q", err, CodeInvalidCommand)
	}
}
```

添加表格用例，覆盖会话 ID 不匹配、重复创建、已关闭会话、重复轮次 ID 和第二个活动轮次。

- [ ] **步骤 2：运行决策测试并验证预期失败**

运行：`go test ./internal/harness/domain -run 'TestDecide(CreateSession|StartTurn)' -count=1`

预期：FAIL，因为命令和决策类型尚不存在。

- [ ] **步骤 3：定义命令和稳定名称**

```go
type Command interface {
	CommandType() string
	TargetSessionID() SessionID
}

const (
	CommandCreateSession = "session.create"
	CommandStartTurn     = "turn.start"
	CommandCompleteTurn  = "turn.complete"
	CommandFailTurn      = "turn.fail"
	CommandInterruptTurn = "turn.interrupt"
	CommandCloseSession  = "session.close"
)

type CreateSession struct { SessionID SessionID; WorkspaceRoot string }
type StartTurn struct { SessionID SessionID; TurnID TurnID; Input string }
type CompleteTurn struct { SessionID SessionID; TurnID TurnID }
type FailTurn struct { SessionID SessionID; TurnID TurnID; Code string; Message string }
type InterruptTurn struct { SessionID SessionID; TurnID TurnID; Reason string }
type CloseSession struct { SessionID SessionID }
```

在每个命令上实现这两个接口方法，不得更改用户输入文本。

- [ ] **步骤 4：实现创建/启动决策和轮次应用逻辑**

`Decide` 按具体命令类型分派。`CreateSession` 要求状态为空、ID 有效且工作区根目录非空白。`StartTurn` 要求存在活动会话、会话 ID 匹配、轮次 ID 有效且未使用、输入非空白，并且没有活动轮次。

`Apply` 处理 `TurnStarted` 时，会克隆 `Turns` 和 `TurnOrder`，插入一个 `StartedAt=record.OccurredAt` 的运行中轮次，设置 `ActiveTurnID`，将 ID 追加到 `TurnOrder`，并推进 `Version`。绝不改变输入状态的映射或切片。

- [ ] **步骤 5：添加确定性测试辅助函数**

```go
// test_helpers_test.go
package domain

import (
	"fmt"
	"testing"
	"time"
)

func recordedForTest(state Session, event Event) RecordedEvent {
	sequence := state.Version + 1
	return RecordedEvent{
		SchemaVersion: 1,
		ID:            EventID(fmt.Sprintf("event-%d", sequence)),
		CommandID:     CommandID(fmt.Sprintf("command-%d", sequence)),
		SessionID:     SessionID("session-1"),
		Sequence:      sequence,
		OccurredAt:    time.Date(2026, 8, 11, 0, 0, int(sequence), 0, time.UTC),
		Event:         event,
	}
}

func activeSessionForTest(t *testing.T) Session {
	t.Helper()
	state, err := Apply(Session{}, recordedForTest(Session{}, SessionCreated{WorkspaceRoot: "/workspace"}))
	if err != nil { t.Fatalf("create test session: %v", err) }
	return state
}

func runningTurnForTest(t *testing.T) Session {
	t.Helper()
	state := activeSessionForTest(t)
	state, err := Apply(state, recordedForTest(state, TurnStarted{TurnID: TurnID("turn-1"), Input: "inspect repository"}))
	if err != nil { t.Fatalf("start test turn: %v", err) }
	return state
}
```

- [ ] **步骤 6：添加不可变性回归测试**

```go
func TestApplyTurnStartedDoesNotMutateInputState(t *testing.T) {
	state := activeSessionForTest(t)
	before := state.Clone()

	_, err := Apply(state, recordedForTest(state, TurnStarted{
		TurnID: TurnID("turn-1"), Input: "inspect repository",
	}))
	if err != nil { t.Fatalf("Apply() error = %v", err) }
	if !reflect.DeepEqual(state, before) {
		t.Fatalf("Apply() mutated input: got %#v want %#v", state, before)
	}
}
```

公开 `Session.Clone() Session` 供测试和后续读模型使用；它必须同时复制映射和 `TurnOrder` 切片。

- [ ] **步骤 7：格式化并运行聚焦测试和完整测试**

运行：`gofmt -w internal/harness/domain`

运行：`go test ./internal/harness/domain -run 'TestDecide(CreateSession|StartTurn)|TestApplyTurnStarted' -count=1`

预期：PASS。

运行：`go test ./... -count=1`

预期：PASS。

- [ ] **步骤 8：提交创建/启动决策**

```bash
git add internal/harness/domain/commands.go internal/harness/domain/decide.go internal/harness/domain/decide_test.go internal/harness/domain/apply.go internal/harness/domain/apply_test.go internal/harness/domain/state.go internal/harness/domain/test_helpers_test.go
git commit -m "feat(domain): decide session and turn start"
```

---

### 任务 4：强制互斥的轮次终止状态

**文件：**
- 修改：`internal/harness/domain/decide.go`
- 修改：`internal/harness/domain/apply.go`
- 测试：`internal/harness/domain/decide_test.go`
- 测试：`internal/harness/domain/apply_test.go`

**接口：**
- 消费：任务 2–3 中定义的终止命令/事件
- 产出：具有稳定不变量的完成、失败和中断转换

- [ ] **步骤 1：编写失败的终止转换测试**

```go
func TestTerminalTurnTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cmd  Command
		want Event
	}{
		{"complete", CompleteTurn{SessionID: SessionID("session-1"), TurnID: TurnID("turn-1")}, TurnCompleted{TurnID: TurnID("turn-1")}},
		{"fail", FailTurn{SessionID: SessionID("session-1"), TurnID: TurnID("turn-1"), Code: "provider_rate_limit", Message: "retry budget exhausted"}, TurnFailed{TurnID: TurnID("turn-1"), Code: "provider_rate_limit", Message: "retry budget exhausted"}},
		{"interrupt", InterruptTurn{SessionID: SessionID("session-1"), TurnID: TurnID("turn-1"), Reason: "user_cancelled"}, TurnInterrupted{TurnID: TurnID("turn-1"), Reason: "user_cancelled"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := runningTurnForTest(t)
			events, err := Decide(state, tt.cmd)
			if err != nil { t.Fatalf("Decide() error = %v", err) }
			if len(events) != 1 || !reflect.DeepEqual(events[0].Event, tt.want) {
				t.Fatalf("Decide() = %#v, want %#v", events, tt.want)
			}
		})
	}
}
```

添加否定用例：错误的轮次 ID 返回 `CodeTurnMismatch`；没有运行中轮次时返回 `CodeTurnNotRunning`；空白失败代码/消息以及空白中断原因返回 `CodeInvalidCommand`。

- [ ] **步骤 2：运行聚焦测试并验证失败**

运行：`go test ./internal/harness/domain -run 'TestTerminalTurnTransitions|TestDecideTerminal' -count=1`

预期：FAIL，因为终止决策/应用逻辑尚不完整。

- [ ] **步骤 3：实现终止决策**

使用一个辅助函数：

```go
func requireRunningTurn(state Session, sessionID SessionID, turnID TurnID) (Turn, error)
```

它验证会话是否存在及其状态、会话是否匹配、是否存在活动轮次，以及活动轮次标识是否完全匹配。每个终止命令恰好返回一个对应事件。

- [ ] **步骤 4：实现终止事件应用逻辑**

对于每个终止事件，克隆状态，替换克隆映射中的活动轮次值，设置 `EndedAt=record.OccurredAt`，仅填充相关的失败/中断字段，清空 `ActiveTurnID`，并推进 `Version`。在没有匹配的运行中轮次时应用任何终止事件，都应返回稳定的领域错误且不得改变输入。

- [ ] **步骤 5：添加互斥性回放测试**

应用 `TurnCompleted`，然后在下一序号尝试为同一轮次应用 `TurnFailed` 和 `TurnInterrupted`。两者都必须返回 `CodeTurnNotRunning`，且已完成状态的版本和轮次状态必须保持不变。

- [ ] **步骤 6：格式化并运行测试**

运行：`gofmt -w internal/harness/domain`

运行：`go test ./internal/harness/domain -run 'TestTerminal|TestDecideTerminal|TestApplyTerminal' -count=1`

预期：PASS。

运行：`go test ./... -count=1`

预期：PASS。

- [ ] **步骤 7：提交终止状态**

```bash
git add internal/harness/domain/decide.go internal/harness/domain/apply.go internal/harness/domain/decide_test.go internal/harness/domain/apply_test.go
git commit -m "feat(domain): enforce turn terminal states"
```

---

### 任务 5：仅在安全边界关闭会话

**文件：**
- 修改：`internal/harness/domain/decide.go`
- 修改：`internal/harness/domain/apply.go`
- 测试：`internal/harness/domain/decide_test.go`
- 测试：`internal/harness/domain/apply_test.go`

**接口：**
- 消费：`CloseSession`、`SessionClosed`
- 产出：拒绝活动轮次和所有后续命令的关闭语义

- [ ] **步骤 1：编写失败的会话关闭测试**

覆盖以下确切用例：

```text
active session without running turn + CloseSession -> SessionClosed
active session with running turn + CloseSession -> turn_already_running
closed session + StartTurn -> session_closed
closed session + CloseSession -> session_closed
```

对于成功用例，应用事件，并断言 `Status=closed`、`ActiveTurnID` 为空且版本已递增。

- [ ] **步骤 2：运行聚焦测试并验证失败**

运行：`go test ./internal/harness/domain -run 'Test.*CloseSession' -count=1`

预期：FAIL，因为关闭行为尚不完整。

- [ ] **步骤 3：实现关闭决策和应用逻辑**

`Decide(CloseSession)` 检查是否存在匹配的活动会话，并拒绝非空的 `ActiveTurnID`，返回 `CodeTurnAlreadyRunning`。它返回一个 `SessionClosed` 事件。`Apply(SessionClosed)` 防御性地执行相同的不变量检查，设置状态并推进版本。

- [ ] **步骤 4：添加表格测试，证明每个非创建命令都会拒绝已关闭会话**

表格包含 `StartTurn`、`CompleteTurn`、`FailTurn`、`InterruptTurn` 和 `CloseSession`；每个结果都必须满足 `IsCode(err, CodeSessionClosed)`。

- [ ] **步骤 5：格式化并运行测试**

运行：`gofmt -w internal/harness/domain`

运行：`go test ./internal/harness/domain -run 'Test.*CloseSession|TestClosedSession' -count=1`

预期：PASS。

运行：`go test ./... -count=1`

预期：PASS。

- [ ] **步骤 6：提交关闭语义**

```bash
git add internal/harness/domain/decide.go internal/harness/domain/apply.go internal/harness/domain/decide_test.go internal/harness/domain/apply_test.go
git commit -m "feat(domain): close sessions at safe boundaries"
```

---

### 任务 6：添加带版本的已记录事件 JSON 编解码器

**文件：**
- 创建：`internal/harness/domain/codec.go`
- 创建：`internal/harness/domain/codec_test.go`
- 创建：`internal/harness/domain/testdata/session_lifecycle.jsonl`

**接口：**
- 消费：`RecordedEvent` 和所有事件负载
- 产出：`MarshalRecordedEvent(RecordedEvent) ([]byte, error)`
- 产出：`UnmarshalRecordedEvent([]byte) (RecordedEvent, error)`

- [ ] **步骤 1：编写失败的规范 JSON 测试**

```go
func TestRecordedEventJSONRoundTrip(t *testing.T) {
	t.Parallel()

	record := RecordedEvent{
		SchemaVersion: 1,
		ID: EventID("event-1"), CommandID: CommandID("command-1"),
		SessionID: SessionID("session-1"), Sequence: 1,
		OccurredAt: time.Date(2026, 8, 11, 1, 2, 3, 456000000, time.FixedZone("offset", 8*60*60)),
		Event: SessionCreated{WorkspaceRoot: "/workspace"},
	}

	encoded, err := MarshalRecordedEvent(record)
	if err != nil { t.Fatalf("MarshalRecordedEvent() error = %v", err) }
	want := `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-10T17:02:03.456Z","type":"session.created","data":{"workspaceRoot":"/workspace"}}`
	if string(encoded) != want { t.Fatalf("encoded = %s\nwant = %s", encoded, want) }

	decoded, err := UnmarshalRecordedEvent(encoded)
	if err != nil { t.Fatalf("UnmarshalRecordedEvent() error = %v", err) }
	record.OccurredAt = record.OccurredAt.UTC()
	if !reflect.DeepEqual(decoded, record) {
		t.Fatalf("decoded = %#v", decoded)
	}
}
```

- [ ] **步骤 2：运行编解码器测试并验证失败**

运行：`go test ./internal/harness/domain -run 'TestRecordedEventJSON' -count=1`

预期：FAIL，因为编解码器函数尚不存在。

- [ ] **步骤 3：实现显式线格式编码**

使用未导出的线格式信封，以便有意控制字段名称/顺序：

```go
type recordedEventWire struct {
	SchemaVersion int             `json:"schemaVersion"`
	ID            EventID         `json:"id"`
	CommandID     CommandID       `json:"commandId"`
	SessionID     SessionID       `json:"sessionId"`
	Sequence      uint64          `json:"sequence"`
	OccurredAt    string          `json:"occurredAt"`
	Type          string          `json:"type"`
	Data          json.RawMessage `json:"data"`
}
```

使用封闭的类型 switch 编组事件负载。解组时，对稳定事件名称使用封闭 switch，并对信封和负载都使用 `json.Decoder.DisallowUnknownFields()`。拒绝未知模式版本、未知事件名称、缺失 ID、序号 `0`、无效时间戳和尾随 JSON，并返回 `CodeInvalidEvent`。将时间戳归一化为 `UTC()`，并使用 `time.RFC3339Nano` 格式化。

- [ ] **步骤 4：添加无效线格式表格测试**

包括未知顶层字段、未知负载字段、模式版本 `2`、未知事件类型、零序号、带首尾空白的 ID、无效时间戳，以及信封后的第二个 JSON 值。每个用例都必须返回 `CodeInvalidEvent`。

- [ ] **步骤 5：添加规范的生命周期 JSONL 夹具**

创建六条单行记录，序号连续且使用固定 UTC 时间戳：

```text
session.created
turn.started
turn.completed
turn.started
turn.interrupted
session.closed
```

使用会话 `session-fixture`、轮次 `turn-1` 和 `turn-2`、一致编号的命令/事件、工作区 `/workspace`，以及用户中断原因 `user_cancelled`。

- [ ] **步骤 6：格式化并运行测试**

运行：`gofmt -w internal/harness/domain`

运行：`go test ./internal/harness/domain -run 'TestRecordedEventJSON|TestUnmarshalRecordedEvent' -count=1`

预期：PASS。

运行：`go test ./... -count=1`

预期：PASS。

- [ ] **步骤 7：提交事件编解码器和夹具**

```bash
git add internal/harness/domain/codec.go internal/harness/domain/codec_test.go internal/harness/domain/testdata/session_lifecycle.jsonl
git commit -m "feat(domain): add versioned event codec"
```

---

### 任务 7：证明确定性回放并拒绝损坏流

**文件：**
- 创建：`internal/harness/domain/replay.go`
- 创建：`internal/harness/domain/replay_test.go`
- 修改：仅在发现夹具缺陷时修改 `internal/harness/domain/testdata/session_lifecycle.jsonl`

**接口：**
- 消费：有序的 `[]RecordedEvent`、`UnmarshalRecordedEvent`、`Apply`
- 产出：`Replay([]RecordedEvent) (Session, error)`
- 产出：`DecodeJSONL(io.Reader) ([]RecordedEvent, error)`

- [ ] **步骤 1：编写失败的夹具回放测试**

```go
func TestReplayFixtureIsDeterministic(t *testing.T) {
	data, err := os.Open("testdata/session_lifecycle.jsonl")
	if err != nil { t.Fatal(err) }
	defer data.Close()

	records, err := DecodeJSONL(data)
	if err != nil { t.Fatalf("DecodeJSONL() error = %v", err) }
	first, err := Replay(records)
	if err != nil { t.Fatalf("Replay() error = %v", err) }
	second, err := Replay(records)
	if err != nil { t.Fatalf("Replay() second error = %v", err) }
	if !reflect.DeepEqual(first, second) { t.Fatalf("replays differ") }
	if first.Status != SessionStatusClosed || first.Version != 6 || len(first.TurnOrder) != 2 {
		t.Fatalf("Replay() = %#v", first)
	}
	if first.Turns[TurnID("turn-1")].Status != TurnStatusCompleted || first.Turns[TurnID("turn-2")].Status != TurnStatusInterrupted {
		t.Fatalf("Replay() turns = %#v", first.Turns)
	}
}
```

- [ ] **步骤 2：运行回放测试并验证失败**

运行：`go test ./internal/harness/domain -run 'TestReplayFixture' -count=1`

预期：FAIL，因为回放函数尚不存在。

- [ ] **步骤 3：实现 JSONL 解码和回放**

`DecodeJSONL` 使用 `bufio.Scanner`，将其缓冲区上限提高到 1 MiB，不跳过任何空白行，在错误中报告从 1 开始的行号，并对空流返回 `CodeInvalidEvent`。`Replay` 从 `Session{}` 开始，通过 `Apply` 折叠记录；它返回第一个错误并附带序号上下文。

不要对记录排序。乱序输入必须通过序号不变量失败，而不能被静默修复。

- [ ] **步骤 4：添加损坏流测试**

覆盖空流、空白 JSONL 行、缺失序号 `2`、重复序号 `1`、序号 `2` 处会话 ID 发生变化、`session.created` 之前出现事件、第二个 `session.created`，以及 `session.closed` 之后出现事件。断言稳定错误码，并确认发生错误时不返回部分状态。

- [ ] **步骤 5：添加竞态安全的并行回放测试**

启动 32 个 goroutine 回放同一个不可变记录切片。通过 channel 收集返回状态，并断言它们都等于第一个状态。实现不得改变记录或共享负载。

- [ ] **步骤 6：格式化并运行聚焦测试、完整测试和竞态测试**

运行：`gofmt -w internal/harness/domain`

运行：`go test ./internal/harness/domain -run 'TestReplay|TestDecodeJSONL' -count=1`

预期：PASS。

运行：`go test ./... -count=1`

预期：PASS。

运行：`go test -race ./... -count=1`

预期：PASS，且无竞态报告。

- [ ] **步骤 7：提交确定性回放**

```bash
git add internal/harness/domain/replay.go internal/harness/domain/replay_test.go internal/harness/domain/testdata/session_lifecycle.jsonl
git commit -m "feat(domain): add deterministic event replay"
```

---

### 任务 8：记录领域契约并在 CI 中强制执行

**文件：**
- 创建：`docs/architecture/domain-events.md`
- 创建：`.github/workflows/ci.yml`
- 修改：`README.md`

**接口：**
- 消费：所有已实现的事件名称、命令、状态规则和验证命令
- 产出：面向贡献者的契约和自动化合并门禁

- [ ] **步骤 1：编写领域契约文档**

记录以下确切状态机：

```text
nonexistent --session.created--> active --session.closed--> closed

absent --turn.started--> running --turn.completed----> completed
                              |--turn.failed---------> failed
                              `--turn.interrupted----> interrupted
```

列出六个事件名称、六个命令名称、稳定错误码、单活动轮次不变量、连续序号不变量、时间戳规则、不可变性规则和明确的里程碑排除项。声明内部事件不是 ACP 消息，且在 v1.0 之前不构成公共兼容性承诺。

- [ ] **步骤 2：添加 CI 工作流**

```yaml
name: ci

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

jobs:
  go:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: true
      - name: Check formatting
        run: test -z "$(gofmt -l .)"
      - name: Vet
        run: go vet ./...
      - name: Test with race detector
        run: go test -race ./... -count=1
```

- [ ] **步骤 3：添加 README 开发命令**

追加一个 `Development` 章节，链接 `docs/architecture/domain-events.md` 并列出：

```bash
gofmt -w .
go vet ./...
go test -race ./... -count=1
```

- [ ] **步骤 4：运行完整的本地质量门禁**

运行：`test -z "$(gofmt -l .)"`

预期：以 0 退出且无输出。

运行：`go vet ./...`

预期：以 0 退出且无输出。

运行：`go test -race ./... -count=1`

预期：PASS。

- [ ] **步骤 5：验证仓库范围**

运行：`git status --short`

预期：本任务中只有 `README.md`、`.github/workflows/ci.yml` 和 `docs/architecture/domain-events.md` 处于未提交状态。

运行：`rg -n 'ACP|MCP|provider|tui' internal/harness/domain`

预期：没有导入或实现依赖；若保留包边界注释，则仅允许在其中提及这些术语。

- [ ] **步骤 6：提交文档和 CI**

```bash
git add README.md docs/architecture/domain-events.md .github/workflows/ci.yml
git commit -m "ci: enforce domain quality gates"
```

## 最终验证

任务 8 完成后，从干净检出中运行所有命令：

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test ./... -count=1
go test -race ./... -count=1
git status --short
```

预期结果：

- 格式检查以 `0` 退出且不输出路径；
- `go vet` 以 `0` 退出；
- 常规和竞态测试套件通过；
- `git status --short` 不输出任何内容；
- 将 `testdata/session_lifecycle.jsonl` 回放两次会在版本 `6` 得到相等的关闭会话状态；
- 没有实现文件导入 ACP、MCP、模型 SDK、TUI 包、持久化后端或第三方模块。

## 完成证据

仅当交接包含以下内容时，里程碑才算完成：

- 最终提交哈希；
- 格式化、vet、常规测试和竞态测试的精确输出摘要；
- 生命周期夹具路径；
- 稳定命令/事件/错误目录路径；
- 确认 `go.mod` 不含第三方依赖；
- 任何有意偏离本计划的内容均记录在 ADR 或更新后的设计文档中。
