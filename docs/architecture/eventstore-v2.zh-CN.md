# 已实现 EventStore v2 合同

- 状态：已实现内部合同
- 稳定级别：v1.0 之前为 `experimental`
- 成熟度：pre-v0，尚非通用可用（GA）发布
- 范围：仅 Slice 1 合同迁移。内存适配器是一致性参考实现，不是可持久化的生产存储。
- 英文规范设计：[EventStore v2 contract migration](../superpowers/specs/2026-08-13-eventstore-v2-contract-design.md)
- 已实施计划：[EventStore v2 contract migration implementation plan](../superpowers/plans/2026-08-13-eventstore-v2-contract.md)
- 完成证据：[EventStore v2 证据台账](eventstore-v2-evidence.md)
- 英文已实现合同：[Implemented EventStore v2 Contract](eventstore-v2.md)

本文记录当前代码和测试已经强制执行的行为。它是内部 Go 合同，不是稳定公共协议；
pre-v0 阶段若修改合同，设计、实现、测试和本文必须同步变更。

## 已交付能力

Application 拥有 Append Identity、Event Metadata、Request Admission 和
Unknown-Outcome Resolution。Store 只分配 Per-Session Sequence 与 Global Commit
Position。`RunTurn` 对调用方稳定的 Request 精确准入一次；丢失确认时解析
Receipt，不启动第二次模型调用；已保留的 Completed/Failed Intent 胜过迟到的取消。

尚未实现 SQLite 持久化、JSONL 副本/导入、Runtime Host/恢复、ACP 和 TUI。

## Store 接口

```go
type EventStore interface {
    ReadStream(context.Context, ReadStreamRequest) (StreamPage, error)
    Append(context.Context, AppendRequest) (CommitReceipt, error)
    ResolveAppend(context.Context, ResolveAppendRequest) (AppendResolution, error)
    FindCommandRequest(context.Context, FindCommandRequestRequest) (CommandRequestLookup, error)
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
```

`Load` 和 `Append(...) ([]domain.RecordedEvent, error)` 已不存在。临时名称
`EventStoreV2` 与 `AppendRequestV2` 已删除。架构 AST 门拒绝这些生产表面。

## Identity 所有权

| Identity | 拥有者 | 分配时机 |
| --- | --- | --- |
| `RunTurnRequestID` | 调用方 | 第一次 Store 调用之前 |
| `AppendID`、`CommandID`、`EventID` | Application | 第一次 Store 调用之前 |
| Event Schema Version 与 UTC `OccurredAt` | Application | 每个原子 Batch 一次 |
| Stream `Sequence` 与全局 `CommitPosition` | Store | 提交时 |
| `RuntimeID` 与非零 `FencingToken` | 组合层 / Writer Authority | 每次变更请求 |

用不同 Digest 重用 `AppendID` 是 `append_identity_mismatch`。Event ID 仍然必要且
全局唯一，但不能代替 Batch Receipt。

## 规范 Digest

`DigestAppendRequest` 对 Version-1 Framed Encoding 做 SHA-256。覆盖字段顺序为：

```text
format-version
session-id
expected-version
command-id
admission-present
[request-id, request-digest, turn-id, item-id]
event-count
for each event: event-id, event-type, schema-version, RFC3339Nano UTC time,
                canonical payload length and bytes
```

`AppendID` 与 `WriterAuthority` 会校验，但不进入 Digest。
`DigestRunTurnRequestV1` 只覆盖 Session ID 与精确 UTF-8 Input。

## 错误代数

| Code | 含义 |
| --- | --- |
| `invalid_read` / `invalid_append` | 变更前拒绝 |
| `version_conflict` | Expected Version 与 Stream Head 不符 |
| `append_identity_mismatch` | 同一 `AppendID`、不同 Digest |
| `command_request_conflict` | 同一 Request ID 已被准入 |
| `command_identity_mismatch` | Request ID 被不同 Digest 或 Session 重用 |
| `domain_identity_conflict` | 历史 Turn/Item 唯一性 |
| `writer_fenced` | Authority Token 被拒绝 |
| `store_unavailable` | 确定未提交 |
| `commit_outcome_unknown` | 提交可能已成功；只有此 Code 可设 `MayHaveCommitted` |
| `store_corrupt` | Fail Closed |

Application 把 Append 后的未知结果映射为 `append_outcome_unknown`，绝不把未知
翻译成缺失。

## 分页

第一页捕获 `HeadVersion`。后续页重复该值，只返回不晚于它的 Record。页与页之间
不保留 Read Transaction。Head 变化、游标倒置或不前进的页都是合同破坏。

| AfterSequence | Limit | HeadVersion | 结果 |
| --- | ---: | --- | --- |
| 0 | 1–256 | nil 或当前 | 从 Sequence 1 开始的第一页 |
| 上次返回的 `NextAfterSequence` | 1–256 | 钉住的第一页 Head | 下一不可变页 |
| `HeadVersion` | 任意 | 同一钉住 Head | 空的终止页，`End=true` |
| 大于钉住 Head | 任意 | 钉住 | `invalid_read` |

## Admission 与 Live Execution

`RunTurn` 要求 `RequestID`。在分配 Command、Turn、Item、Append 或 Event ID 之前，
先计算 Request Digest 并调用 `FindCommandRequest`。

| Lookup | 行为 |
| --- | --- |
| `not_found` | 选举一个 Live Owner，尝试一次原子 Admission |
| `identity_mismatch` | Conflict；不调用模型 |
| `found` 终态 | 重建持久结果；不调用模型 |
| `found` running 且本地有 Owner | 等待该 Owner 的终态 |
| `found` running 且本地无 Owner | `reconciliation_required`；不调用模型 |

每个 Request ID 只有一个 Registry Entry。Waiter 不启动第二个 Resolver、不分配
Identity、不调用模型。Session 上若保留未解析的 Unknown Append，则拒绝另一个新
Admission。

## Compact 写侧状态

生产 `domain.Session` 只保留 Identity、Workspace、Status、Version，以及至多一个
Active Turn 和其上至多一个 Active Item。已完成 Transcript 不是写侧状态。历史
Turn/Item 唯一性由 Store Integrity Index 保证。完整历史 Aggregate 只作为测试
Oracle 存在。

## Unknown-Outcome Resolution

默认：`AppendResolutionTimeout = 5s`，初始 Unknown 之后最多
`AppendResolutionMaxOperations = 4` 次 Store 操作。每轮调用 `ResolveAppend`。
`committed` 返回经校验 Receipt；`not_found` 允许一次完全相同的 Exact `Append`；
`identity_mismatch` Fail Closed。耗尽返回 `append_outcome_unknown`，并保留未解析
Entry。

Admission Unknown 必须在任何模型调用前解析。若 Admission 已提交且调用方已取消，
Application 追加 `request_abandoned`，不调用模型。Terminal Unknown 保留并解析
原来的 Completed/Failed Intent。

## Cancellation Winner

| Phase | 取消效果 |
| --- | --- |
| `running` | 可以追加 `caller_canceled` |
| `terminal_append_in_flight` | 只停止投递 |
| 已保留的 Completed/Failed Intent | 胜过迟到的取消 |
| CAS Loser | 若存在持久 Winner 则重载并报告 |

`process_crash` 留给 Slice 4。

## 资源上限

| 资源 | 上限 |
| --- | ---: |
| 规范 Event Payload | 8 MiB |
| 每次 Append 的 Event 数 | 64 |
| 编码后的 Append Digest | 16 MiB |
| Read Page | 256 条 Record |
| Assistant UTF-8 输出 | 1 MiB |
| Unknown 之后的 Resolution 操作 | 4 |
| Resolution 超时 | 5 s |

规范事实只能拒绝，不能截断。

## 排除项

本已实现合同不提供 SQLite、JSONL 导出/导入、持久 Runtime Lease 或崩溃恢复、ACP、
TUI、工具、Provider 或上下文管理。内存适配器是共享 Conformance Suite 使用的确定性
参考实现，不是生产持久化。
