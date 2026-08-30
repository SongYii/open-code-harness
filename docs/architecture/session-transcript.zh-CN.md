# 会话转录 — 已实现合同（中文阅读版）

**状态：** 已实现；非 GA

**稳定性：** `experimental`

**证据：** [对话面与会话转录完成证据](conversation-and-transcript-evidence.md)；`session.deleted` 事实见 [ACP 会话生命周期（切片 B）完成证据](acp-session-lifecycle-evidence.md)

英文版本 [session-transcript.md](session-transcript.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。

**包：** `internal/harness/transcript`

**装配：** `composition.ExportSession`；`cmd/och export-session`

## 范围

把一个 EventStore 会话投影为面向会话的实验性 JSONL 导出。模式名
`och.session.transcript`，`formatVersion` 为 1。每行一条投影事实。
EventStore 仍是唯一在线提交权威；转录是投影，不是副本、不是提交点、
不可写回存储。

这不是 Slice 3 审计副本（每原子追加一行、带摘要链），也不是 ACP 对话面。
`adapters/acp` 不得导入本包；本包不得导入 ACP 或任何适配器。

## 信封

每行一个 UTF-8 JSON 对象。不得嵌入原始换行。编码行上限 **2 MiB**
（`line_limit`；失败封闭，不静默跳过）。

三种线上结构。事实行冻结键序
`formatVersion, schema, sessionId, eventId, commandId, sequence, occurredAt, type, payload`。
完整性行（`transcript.snapshot`、`transcript.complete`）省略
`eventId`、`commandId` 与 `sequence`。

`sequence` 是 EventStore 每会话序号，事实行从不省略，也不是稠密计数。
被省略的领域类型表现为缺口。`occurredAt` 为 RFC3339Nano UTC
（`2006-01-02T15:04:05.000000000Z07:00`）。相同时间戳表示同一原子批次，
不是并发。

成功导出的第一行是 `transcript.snapshot`，最后一行是
`transcript.complete`。缺少任一条、`complete.headSequence` 与快照不一致、
中间事实行数与 `complete.factLines` 不一致、或 complete 换行之后还有字节
的流或文件，消费者必须整份拒绝。序号缺口不能代替 trailer。

| 完整性 `type` | 载荷 |
| --- | --- |
| `transcript.snapshot` | `headSequence`、`open`、`running`、`stability`（`experimental`） |
| `transcript.complete` | `headSequence`、`factLines`、`open`、`running` |

`open` 为 `session.Status == active`。`running` 为 `ActiveTurn != nil`。
空闲的活动会话是 `open: true`、`running: false`。逻辑删除的会话
（`session.Status == deleted`）是 `open: false`、`running: false`，且仍可
完整导出：删除只追加、绝不删除任何行，所以包含末尾 `session.deleted`
事实在内的整条流仍能正常导出，并带有正常的 complete trailer。快照与
complete 的 `occurredAt` 共用导出器时钟，不是领域事件。

## 事实目录

`ProjectRecord` 发出事实行、省略、或失败封闭。

| `type` | 载荷字段 | 来源 |
| --- | --- | --- |
| `session.created` | `workspaceRoot` | `session.created` |
| `session.closed` | `{}` | `session.closed` |
| `session.deleted` | `{}` | `session.deleted` |
| `turn.started` | `turnID`、`input` | `turn.started` |
| `turn.completed` | `turnID` | `turn.completed` |
| `turn.failed` | `turnID`、`code`、`message` | `turn.failed` |
| `turn.interrupted` | `turnID`、`reason` | `turn.interrupted` |
| `assistant.message.started` | `turnID`、`itemID`、`stepIndex`、`stepRef` | started + 投影器计数 |
| `assistant.message.completed` | `turnID`、`itemID`、`stepIndex`、`stepRef`、`text`、`toolCalls?` | `assistant.message.completed` |
| `assistant.message.failed` | `turnID`、`itemID`、`stepIndex`、`stepRef`、`code`、`message` | `assistant.message.failed` |
| `assistant.message.interrupted` | `turnID`、`itemID`、`stepIndex`、`stepRef`、`code`、`message` | `assistant.message.interrupted` |
| `model.usage.recorded` | `turnID`、`itemID`、`inputTokens`、`outputTokens`、`cachedInputTokens`、`latencyMs`、`finishReason`、`providerRequestID` | `model.usage.recorded` |
| `tool.call.started` | `turnID`、`itemID`、`callID`、`stepIndex`、`stepRef`、`name`、`arguments` | `tool.call.started` |
| `tool.call.completed` | `turnID`、`itemID`、`callID`、`stepIndex`、`stepRef`、`content`、`truncated` | `tool.call.completed` |
| `tool.call.failed` | `turnID`、`itemID`、`callID`、`stepIndex`、`stepRef`、`code`、`message` | `tool.call.failed` |
| `tool.call.interrupted` | `turnID`、`itemID`、`callID`、`stepIndex`、`stepRef`、`code`、`message` | `tool.call.interrupted` |
| `approval.requested` | `turnID`、`itemID`、`approvalID`、`callID`、`name`、`reason` | `approval.requested` |
| `approval.resolved` | `turnID`、`itemID`、`approvalID`、`decision` | `approval.resolved` |

**省略（`sequence` 缺口，不是错误）：** `model.request.recorded`、
`policy.decision.recorded`。

**诚实的用量省略：** 若从未追加 `model.usage.recorded`，则没有用量行。
不发零 token。

**无 `origin` 字段。** v0 没有子代理。

任何其他规范领域类型是 `unsupported_event_type`（失败封闭）。未知领域
`schemaVersion` 是 `unsupported_schema_version`。跳过未知仅适用于
*外部* 转录事实 `type`（`DecodeSkipsUnknown`）；snapshot/complete 从不跳过，
EventStore 记录也从不跳过。

助手完成行上的 `toolCalls`（若存在）是领域 `[]ToolCallOffer`
（`id`、`name`、`arguments`）。

## `stepRef`

`stepRef` 为 `turnID + "/" + decimal(stepIndex)`，无空格。

- 助手事件：投影器按 `turnID` 对 `assistant.message.started` 从 1 计数，
  并把该计数写成 `stepIndex`。
- `tool.call.started`：复制 `ToolCallStarted.StepIndex`。不发明第二张表。
- 工具终态：使用当前 `steps[turnID]`（至此的 `assistant.message.started`
  计数）。不改写不一致的流。

## `WriteSession`

`WriteSession(ctx, StreamReader, sessionID, now, writer) (Result, error)`
以 256 条记录分页 `ReadStream`，在第一页钉住头。

1. 非法会话 id → `invalid_session_id`；不写。
2. 第一页为空且 `HeadVersion == 0` → `session_not_found`；不写。
3. 双次钉住读取，同一 `HeadVersion`，内存 O(页)：
   第一遍 `domain.Apply` 得到 `open` / `running` 与事实计数；
   第二遍写 snapshot、事实、complete。
4. snapshot 上线之后的失败（取消、`line_limit`、存储损坏、不可读规范载荷）
   **不**写 `transcript.complete`。仅在 trailer 写完后返回 `Result`。
5. 钉住之后的追加不可见。

`StreamReader` 没有 `Append`。使用内存 EventStore 的测试只在 `_test.go`。

## 导出路径

`composition.ExportSession(ctx, databasePath, sessionID, out) (transcript.Result, error)`
打开 [`sqlite.OpenReader`](sqlite-eventstore.md)（不取 runtime 租约、不迁移、
不要 provider 凭证），以 `time.Now().UTC()` 调用 `WriteSession`，然后关闭
reader。组合根不打印。

```text
och export-session -database PATH -session SESSION_ID [-output FILE]
```

这是子命令，不是 serve 模式标志。它不调用 `composition.Open`。`cmd/och`
不导入 `transcript` 或 `adapters/sqlite`。成功时 stderr 来自 `Result` 的
一行：

```text
och: exported session SESSION facts=N head=M open=bool running=bool
```

stdout 模式直接写 JSONL。`-output PATH` 在同目录创建临时文件，写入、
`Sync`、关闭，再 `Rename` 到 `PATH`。错误或取消删除临时文件并保持
`PATH` 不动。

## 所有权

`internal/harness/architecture` 拥有 `ownerTranscript`：

- transcript 可导入 `domain`、`application`（`ReadStream` 类型）以及除
  `os` / `os/exec` / `net` / `net/http` 外的标准库
- transcript 不得导入 `engine`、`policy`、`tools`、`runtime`、`testkit`
  或任何适配器
- composition 可导入 transcript；domain、engine、application、policy、
  tools、acp、sqlite 与 runtime 不得导入

## 压缩与并行工具

在压缩作为领域事件存在之前，转录不得发出合成的 `context.compacted`，
Application 也不得原地改写既有 `RecordedEvent` 载荷。Step 循环是顺序的；
墙钟保真是起止事件的 `OccurredAt`。

## 排除项

迷宫 UI 与裁决；ACP v2；session resume / list / delete；写入
`transcript_entries` / `snapshots`；改动审计 JSONL 编解码器；脱敏导出；
子代理 `origin`；`RuntimeEvent` 增富；复制外部磁盘布局或事件名；把转录
JSONL 导入 EventStore。
