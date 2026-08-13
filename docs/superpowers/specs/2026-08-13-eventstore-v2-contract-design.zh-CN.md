# EventStore v2 Contract Migration

**状态：** 待审阅  
**日期：** 2026-08-13  
**父设计：** [生产 Runtime 持久化、恢复与客户端边界](2026-08-13-runtime-persistence-recovery-client-design.zh-CN.md)  
**证据：** [EventStore v2 Contract 架构门](../../research/architecture-gates/2026-08-13-eventstore-v2-contract.zh-CN.md)

## 1. 决策摘要

本 Slice 在编写任何 SQLite 代码之前，把当前“完整 Stream、Adapter 生成 Metadata”的 EventStore 替换为支持精确语义、分页与 Receipt 的 v2 Contract。Application 拥有稳定 Append/Event Identity、Event Timestamp、Schema Version 和 Caller Request Admission；Store 只拥有 Stream Sequence 与 Global Commit Position。

这次 Migration 有意采用 Breaking Change。所有 Application Use Case、Memory Adapter、Test Double、Fixture、Error Mapping 与 Shared Conformance Suite 在一个可审阅 Slice 中迁移。不得保留 v1 中“任何非 nil Append Error 都表示确定未提交”的含糊语义。

## 2. 目标

- 使用 `AppendID` 与完整 Append Digest 定义精确重试。
- 把 Lost Acknowledgement 建模为显式、可解析状态。
- 提供固定 Head 的分页 Stream Read。
- 将 Caller-Stable `RunTurnRequestID` Admission 与 Event 一起持久化。
- 阻止完全相同的重复 Request 启动第二次 Model Call。
- 把 Event ID、Schema 与 Occurrence-Time Ownership 移入 Application。
- 用 Compact Command Aggregate 替换无界写侧历史。
- 保持确定性 Decision 与历史 Turn/Item 唯一性。
- 提供 Adapter-Neutral Conformance Suite 与确定性 Fault Matrix。

## 3. 非目标

- SQLite Schema、Migration、PRAGMA、Backup 或 Driver 行为。
- JSONL Outbox、Segment、Manifest、Export 或 Import。
- Runtime Lease Acquisition、Heartbeat、Takeover 或 Crash Reconciliation。
- Transcript Projection、Snapshot 优化、ACP Adapter 或 TypeScript TUI。
- Model Retry、Tool Execution、Context Management 或 Remote Transport。

v2 Request 携带 Runtime Identity 与 Fencing Token，因为所有 Adapter 必须接受最终 Authorization Shape。本 Slice 测试这些字段及确定性 Memory Owner Check；Durable Lease Lifecycle 属于 Slice 2。

## 4. 规范决策

### EV2-01 — Identity Ownership

- Caller 拥有 `RunTurnRequestID`，一个 Logical Request 始终复用该 ID。
- Application 在第一次 Store Call 前，为每个 Atomic Append 分配 `CommandID`、`AppendID`，并为每个 Proposed Event 分配 `EventID`。
- Application 为每个 Atomic Decision Batch 捕获一次 UTC `OccurredAt`，Event Schema Version 为 `1`。
- Store 分配 Per-Session `Sequence` 与 Global `CommitPosition`。
- Runtime Composition 提供非零 `RuntimeID` 与 `FencingToken`。

`CommandID` 关联一次 `RunTurn` 的 Admission 与 Terminal Append，但不是 Idempotency Key。Exact Retry 必须复用 `AppendID`、Proposed Event ID、Timestamp、Schema Version、Payload、Admission 与 Expected Version。

### EV2-02 — 新 Domain Identifier

`domain.AppendID` 与 `domain.RunTurnRequestID` 是经过验证的不透明 UTF-8 String，使用现有 ID 的“非空、无首尾空白”边界。Runtime ID 属于 Application/Storage Identity，不是 Domain Aggregate Identity；`RuntimeID` 是使用相同边界验证的不透明 String，Fencing Token 必须大于零。

ID Generator 变为：

```go
type IDGenerator interface {
    NewSessionID() (domain.SessionID, error)
    NewTurnID() (domain.TurnID, error)
    NewItemID() (domain.ItemID, error)
    NewCommandID() (domain.CommandID, error)
    NewAppendID() (domain.AppendID, error)
    NewEventID() (domain.EventID, error)
}
```

### EV2-03 — EventStore Interface

```go
type EventStore interface {
    ReadStream(context.Context, ReadStreamRequest) (StreamPage, error)
    Append(context.Context, AppendRequest) (CommitReceipt, error)
    ResolveAppend(context.Context, ResolveAppendRequest) (AppendResolution, error)
    FindCommandRequest(context.Context, FindCommandRequestRequest) (CommandRequestLookup, error)
}

type ReadStreamRequest struct {
    SessionID     domain.SessionID
    AfterSequence uint64
    Limit         uint32
    HeadVersion   *uint64
}

type StreamPage struct {
    Records           []domain.RecordedEvent
    HeadVersion       uint64
    NextAfterSequence uint64
    End               bool
}

type WriterAuthority struct {
    RuntimeID    RuntimeID
    FencingToken uint64
}

type AppendRequest struct {
    AppendID        domain.AppendID
    SessionID       domain.SessionID
    ExpectedVersion uint64
    CommandID       domain.CommandID
    Authority       WriterAuthority
    Admission       *CommandAdmission
    Events          []ProposedEvent
}

type ProposedEvent struct {
    ID            domain.EventID
    SchemaVersion uint32
    OccurredAt    time.Time
    Event         domain.Event
}

type CommandAdmission struct {
    RunTurnRequestID domain.RunTurnRequestID
    RequestDigest    Digest
    TurnID           domain.TurnID
    ItemID           domain.ItemID
}

type CommitReceipt struct {
    AppendID       domain.AppendID
    CommitPosition uint64
    FirstSequence  uint64
    LastSequence   uint64
}
```

`Digest` 是可比较的 32-byte SHA-256 Value，具有严格小写 Hex Text Encoding。所有返回 Record、含 Reference 的 Receipt 与 Lookup Record 都使用 Defensive Value。Nil Context、Zero Limit、非法 ID、Zero Timestamp、非 UTC Timestamp、空 Batch、不支持的 Schema Version、未知 Event Type 与 Size Limit Violation 都必须在 Mutation 前拒绝。

### EV2-04 — Canonical Append Digest

`DigestAppendRequest(AppendRequest) (Digest, error)` 由 Application、Adapter 与 Conformance Test 共享。Format Version `1` 使用显式 Unsigned Big-Endian Length Framing，并按顺序包含：

```text
format-version
session-id
expected-version
command-id
admission-present
[request-id, request-digest, turn-id, item-id]
event-count
for each event: event-id, event-type, schema-version, RFC3339Nano UTC time,
                canonical event payload length and bytes
```

排除 `AppendID` 与 `WriterAuthority`：前者是 Receipt Key；后者授权新 Commit，但不改变 Immutable Request Identity。Canonical Payload Byte 复用严格 Domain Codec Rule。禁止无 Framing String 拼接或依赖 Map 顺序的 JSON Encoding。

### EV2-05 — Exact Append Semantics

对于新的 `AppendID`，Validation、Owner Check、Admission Uniqueness、Expected Version、Historical Identity Reservation、Event Recording 与 Receipt Creation 在每个 Adapter 中组成一次 Atomic Decision。

- 相同 `AppendID` 与 Digest 返回原 Receipt，即使 Stream 已推进且当前 Writer Ownership 已变化。
- 相同 `AppendID` 与不同 Digest 返回 `AppendIdentityMismatch`。
- 新 Append 要求精确 `ExpectedVersion`；Conflict 不重试。
- `EventID` 全局唯一；Batch 内重复或与任意已提交 Stream 重复，都会拒绝整个新 Append。
- Batch 要么完全可见，要么完全不存在。
- Commit Point 前观察到 Cancellation 表示没有 Mutation。
- Commit Point 后，Cancellation 不能把成功转变为“确定未提交”。

Slice 1 Memory Storage 分配单调递增 Global Commit Position，并在 Store 生命周期内永久保留 Append Receipt。
它以一个 Current `WriterAuthority` 构造；Exact Receipt Lookup 在 Authority Check 前执行，而来自其他 Runtime ID 或 Token 的任何新 Append 都返回 `WriterFenced`。改变确定性 Owner 时递增 Token，旧 Token 之后不能重新变为有效。

### EV2-06 — Error Algebra

Store 暴露稳定 Typed Code：

```text
invalid_read
invalid_append
version_conflict
append_identity_mismatch
command_request_conflict
command_identity_mismatch
domain_identity_conflict
writer_fenced
store_unavailable
commit_outcome_unknown
store_corrupt
```

每个 Error 都要说明本次 Append 是确定缺失还是可能已提交。`VersionConflict` 包含 Expected 与 Observed Version。Identity Error 只暴露 Request Session 与 Identity Kind；Lookup 不得泄露其他 Session 的 Record。Application 把这些 Code 映射到稳定 Category，但不得把 `CommitOutcomeUnknown` 折叠成 `append_failed`。
Typed Store Error 包含 `MayHaveCommitted`；只有 `commit_outcome_unknown` 为 true，其他 Code 一律为 false。

### EV2-07 — Receipt Resolution

```go
type ResolveAppendRequest struct {
    AppendID      domain.AppendID
    RequestDigest Digest
}

type AppendResolutionKind string

const (
    AppendResolutionCommitted        AppendResolutionKind = "committed"
    AppendResolutionNotFound         AppendResolutionKind = "not_found"
    AppendResolutionIdentityMismatch AppendResolutionKind = "identity_mismatch"
)
```

`ResolveAppend` 只读。只有 `Committed` 包含 Receipt。Storage Failure 是 Error，绝不表示 `NotFound`。Resolution 有意独立于当前 Writer Authority，使旧 Runtime 或继任 Runtime 能在不创建新 Commit 的情况下确定结果。

### EV2-08 — Pinned-Head Pagination

- `AfterSequence` 是 Exclusive。
- 第一次 Request 的 `HeadVersion` 为 nil；Store 捕获当前 Head。
- 后续 Page 重复完全相同的 Head，只返回 `AfterSequence < Sequence <= HeadVersion` 的 Record。
- `Limit` 范围是 `1..256`，返回数量绝不超过它。
- `NextAfterSequence` 是最后一个返回 Sequence；空 Page 时保持输入 Cursor。
- 当且仅当 `NextAfterSequence == HeadVersion` 时 `End=true`。
- 缺失 Stream 的 Head 为 `0`、Record 为空、Cursor 为 `0`、`End=true`。
- Cursor 大于 Head、指定 Head 大于当前 Stream Head，或指定 Head 小于 Cursor，返回 `InvalidRead`。

Page 之间不保留 Connection 或 Transaction。`ReadWholeStreamPinned` 是遵循该协议、具有 Caller Deadline 并增量 Replay 的 Application Helper，不属于 EventStore Method。

### EV2-09 — Durable Command Request Admission

```go
type FindCommandRequestRequest struct {
    RunTurnRequestID domain.RunTurnRequestID
    SessionID        domain.SessionID
    RequestDigest    Digest
}

type CommandRequestRecord struct {
    RunTurnRequestID  domain.RunTurnRequestID
    RequestDigest     Digest
    SessionID         domain.SessionID
    CommandID         domain.CommandID
    TurnID             domain.TurnID
    ItemID             domain.ItemID
    AdmissionAppendID domain.AppendID
}
```

Lookup Kind 只能是 `found`、`not_found` 与 `identity_mismatch`；只有 `found` 包含 Record。`RunTurnRequestID` 全局唯一。Admission Record 不可变，并且只能与对应 Turn/Item Start Event 一起提交。

`RunTurnRequest` 必须提供 `RequestID`。Version-1 Digest 覆盖 Session ID 与精确 UTF-8 Input。`RuntimeSink`、Deadline 与 Cancellation 属于 Delivery Concern，不进入 Digest。未来任何影响 Execution Semantics 的字段，必须先通过新 Digest Version 纳入后才能改变行为。

### EV2-10 — Duplicate 与 Unknown-Outcome Application Behavior

在分配 Command、Turn、Item、Append 或 Event ID 前，`RunTurn` 计算 Request Digest 并调用 `FindCommandRequest`。

- `not_found`：创建 Identity，尝试一次 Atomic Admission。
- `identity_mismatch`：返回稳定 Validation/Conflict Error；不调用 Model。
- `found` Terminal：读取 Pinned Stream、重建并返回 Durable Result；不调用 Model。
- `found` Running 且存在匹配 Live Execution：等待其 Terminal Result；本 Slice 的 Duplicate Caller 不接收历史 Delta。
- `found` Running 但没有 Live Execution：返回 `reconciliation_required`；不调用 Model。Startup Recovery 属于 Slice 4。

Live Registry 每个 Request ID 一条记录，Phase 为：

```text
admission_in_flight -> running -> terminal_append_in_flight
        |                |                 |
        v                v                 v
admission_unknown   cancel_won       terminal_unknown
        |                                  |
        `-------> terminal_committed <-----'
```

Unknown Admission 必须在任何 Model Call 前解析。已提交 Admission 只有在原 Live Owner 仍活动时才继续；否则 Append `request_abandoned`。Unknown Terminal Append 保留并解析精确 Terminal Intent，绝不再次调用 Model。Resolution 使用有界 Service Configuration：`AppendResolutionTimeout` 默认 5 秒，`AppendResolutionMaxOperations` 默认在初始 Unknown Result 后执行 4 次 Store Operation。每个 Cycle 先调用 `ResolveAppend`；`NotFound` 允许对保留 Request 执行一次 Exact `Append`，Unavailable 或 Unknown Result 消耗下一次 Operation。Caller Deadline、Resolution Timeout 或 Operation Count 中任一先耗尽，就结束本次尝试。预算耗尽后返回稳定 `append_outcome_unknown`；只要 Live Process 仍保留 Unresolved Intent，该 Session 的新 Admission 继续阻塞。

每个 Unresolved Registry Entry 只有一个 Resolver。之后使用同一 Request ID/Digest 的 Caller 可以在自己的 Context 内等待该 Entry，但不得启动另一个 Resolver、分配 Identity 或调用 Model。

Cancellation 只在 `running` Phase 获胜。一旦进入 `terminal_append_in_flight`，Cancellation 停止 Delivery，但不能用 Interruption 替换保留的 Completed/Failed Intent。CAS Loser Reload 并报告 Durable Winner。

Slice 1 新增 Domain Interruption Code `request_abandoned`，仅用于 Admission 已提交、但原 Live Owner 在任何 Model Effect 前取消的情况。`process_crash` 继续保留给 Slice 4 Startup Reconciliation。

### EV2-11 — Compact Command Aggregate

`domain.Session` 变为有界写状态，只包含 Session Identity、Workspace、Status、Version，以及最多一个 Active Turn 和最多一个 Active Item。Equivalence Test 存在后，移除 Completed Turn Collection、Item Collection、Transcript Text 与 Order Array。

`ApplyCompact`/`ReplayCompact` 消费相同 Immutable Recorded Event。Terminal Event 验证 Active Identity，随后从写状态丢弃 Completed Payload。Transcript Reconstruction 仍是 Read Concern。

Historical Turn/Item Reuse 由 EventStore 通过 Derived Identity Index 原子拒绝；Memory Adapter 维护等价 Set。`DomainIdentityConflict` 标识 `turn` 或 `item`；Application 将其映射到现有稳定 Domain Duplicate-ID Error。

Golden Equivalence Test 用 v1 与 Compact Logic Replay 每个当前 Fixture，并比较使用 Fresh ID 时所有可达 Decision。专用测试证明新 Store Identity Rule 保持 Historical Duplicate-ID Rejection。v1 Oracle 冻结在 Test-Only Code 中，不保留 Production Compatibility Path。只有这些测试通过后，才能移除旧无界 Field。

### EV2-12 — Resource Bound

Contract 默认值：

| 资源 | 上限 |
| --- | ---: |
| Canonical Encoded Event Payload | 8 MiB |
| 每次 Append 的 Event | 64 |
| Encoded Append Request | 16 MiB |
| Read Page | 256 Record |
| Assistant UTF-8 Output | 现有 1 MiB Application Limit |

Limit 必须在 Mutation 前验证；Canonical Fact 绝不截断。

## 5. Package 与 File Boundary

Implementation Plan 可以微调 Filename，但必须保持以下 Unit：

| Area | 职责 |
| --- | --- |
| `internal/harness/domain/ids.go` | Append 与 Caller Request ID Validation |
| `internal/harness/domain/state.go` | Compact Command Aggregate |
| `internal/harness/domain/apply.go` | Compact Deterministic Transition |
| `internal/harness/application/ports.go` | EventStore v2 与 Writer Authority |
| `internal/harness/application/digest.go` | Versioned Append 与 RunTurn Request Digest |
| `internal/harness/application/read_stream.go` | Pinned Pagination 与 Incremental Replay |
| `internal/harness/application/execution_registry.go` | Duplicate/Unknown Live Phase 与 Session Gate |
| `internal/harness/application/append.go` | Stable Proposed-Event Construction、Append、Resolution 与 Apply |
| `internal/harness/adapters/memory/event_store.go` | 确定性 v2 Reference Adapter 与 Fault Hook |
| `internal/harness/application/eventstoretest/` | Adapter-Neutral Conformance Suite |

Domain 继续独立于 Application 与 Adapter。Engine 不感知 EventStore、Request Receipt 或 Persistence Resolution。

## 6. 验证合同

### 6.1 Domain

- 确定性 Compact Replay 与 Terminal Irreversibility；
- 移除无界 Field 前的 v1 Fixture Decision Equivalence；
- Historical Identity Conflict Parity；
- ID、UTF-8、Timestamp 与 Codec Fuzz Test。

### 6.2 EventStore Conformance

- Exact CAS 与 All-or-None Multi-Event Batch；
- Same-ID/Same-Digest Receipt Replay 与 Different-Digest Rejection；
- Admission Identity Race 与保护隐私的 Mismatch；
- 并发下 Commit Position 与 Per-Stream Sequence Ordering；
- 另一 Goroutine Append 时的 Pinned Pagination；
- Defensive Copy、Context Cancellation、Owner Fencing、Limit 与 Corrupt State Fail-Closed；
- 注入 Pre-Commit Failure、Committed-but-Ack-Lost、Resolution Unavailable 与 Exact Retry。

### 6.3 Application Scenario

- Request Lookup 前不分配 ID；
- 两个同时发生的相同 Request 只产生一次 Admission 与一次 Model Call；
- 同一 Request ID 改变 Input 不调用 Model；
- Unknown Admission 在 Model 前解析；
- Unknown Terminal Append 绝不重复 Model；
- 每个 Live Phase 的 Cancellation 遵守 Winner Table；
- 没有 Live Execution 的 Running Admission 返回 Reconciliation Required；
- 现有 Session Create/Load/Close 及成功、失败、取消 Turn Scenario 在不削弱 Terminal Durability 的情况下迁移。

所有测试运行 `go test ./... -count=1` 与 `go test -race ./... -count=1`。Fuzz Smoke 与 Architecture Dependency Test 属于 Completion Gate。

## 7. 交付顺序

后续 Implementation Plan 必须使用 TDD，并为以下工作设置独立 Reviewer Gate：

1. Identifier、Digest Codec、Type 与 Error；
2. Pinned Read 与 Compact Aggregate Equivalence；
3. v2 Memory Adapter 与 Shared Conformance Suite；
4. Application Append Construction 与 Resolution；
5. Durable Request Admission 与 Duplicate Execution Registry；
6. Unknown-Outcome/Cancellation State Machine；
7. 所有 Use Case、Fixture、Architecture Test 与文档迁移。

每个 Task 都以小型 Commit 与独立 Verification 结束。只要任何 v1 EventStore Call、Adapter 或 Test Double 仍存在，Slice 2 就不能开始。

## 8. 接受标准

- 所有 EV2 Decision 已实施，并从 Documentation Index 链接。
- 没有 Production Package 调用 v1 `Load`，也没有代码期望 Append 返回 Record。
- 每个 EventStore Implementation 通过 Shared v2 Conformance Suite。
- 一个 Request ID/Digest 不会让 Application 产生第二次 Model Call。
- Unknown Commit Outcome 可观察、可解析，绝不映射为 Absence。
- Compact Replay 保持当前 Decision Behavior 与 Bounded Write State。
- Normal、Race、Fault、Fuzz-Smoke 与 Architecture Test 通过。
- Completion Evidence 列出 Task Commit、精确命令、Exclusion，以及剩余 SQLite/JSONL/Runtime/ACP/TUI Blocker。
