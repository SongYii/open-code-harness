# ACP 会话生命周期（切片 B）

- **日期：** 2026-08-27
- **状态：** Draft（设计已确认，等待本文档人工审阅）
- **稳定性：** `experimental` ACP v1 会话管理面
- **规范语言：** [英文原文](2026-08-27-acp-session-lifecycle-slice-b-design.md)

本文为英文规范的同步中文阅读副本；发生歧义时以英文为准。

---

## 1. 决策摘要

Slice B 为现有的持久化 Session 模型增加 capability-gated 的 ACP v1
`session/list`、`session/resume`、`session/close` 与 `session/delete`。原始
follow-on 仅列出 list/resume/delete；但 delete 只允许对 closed Session 执行，
因此 ACP 客户端必须有标准的 close 路径，故本 Slice 一并实现 `session/close`。

删除是不可逆的**逻辑删除**：追加新的 canonical 领域事实
`session.deleted`，绝不删除 `events` 行、审计链或 session transcript。已删除
Session 不出现在 ACP 列表中，并且普通 load、resume、prompt 都不可用。这样既能
清理用户可见历史，又不破坏 EventStore 和审计证据。

既有同步 `session_heads` 投影成为索引目录。迁移为每个 head 补齐 workspace root，
通过重放 canonical stream 回填并校验；每次 append 与 head 更新仍在同一事务，ACP
不会直接读 SQLite。

## 2. 目标与排除项

目标：实现并宣告四个 ACP v1 生命周期能力；仅列出 assembly workspace 中的会话；
让删除可持久化、可回放、可审计；保持 fencing、CAS、append identity、未知提交和
pinned read 的既有约束；明确 close/prompt/delete 竞态。

不在范围：ACP v2、版本协商、认证、mode/config/fork、批量删除、undelete、物理
清理；additionalDirectories、session MCP；`och` 管理子命令；从用户输入生成标题；
跨分页历史快照；以及对 Slice A 对话投影、audit JSONL 或 context compaction 的修改。

## 3. 状态和持久化事实

### 3.1 领域生命周期

新增 `SessionStatusDeleted = "deleted"`、`CommandDeleteSession =
"session.delete"`、`DeleteSession{SessionID}`、`EventSessionDeleted =
"session.deleted"` 和 `SessionDeleted{}`。

`domain.Decide` 仅在 Session 存在、目标 ID 匹配、状态为 `closed`、且没有活跃
Turn/Item 时接受 DeleteSession；它精确产生 `[SessionDeleted{}]`。`domain.Apply`
仅从同一 closed/idle 状态接受该事件并转为 `deleted`。已删除 Session 的其他命令均
拒绝，二次删除得到 `session_deleted`。codec、clone、compact replay、historical
oracle 与 audit serialization 全部认识此新事件。

`session.closed` 仍只表示正常结束，不隐藏历史；`session.deleted` 才是本格式内
不可撤销的终态。

### 3.2 Application 行为

Service 新增 ListSessions、ResumeSession、DeleteSession 用例。普通 `LoadSession`
对 deleted aggregate 返回 `session_not_found`；只有 DeleteSession 内部生命周期加载器
可重放其状态，从而令二次删除得到确定的领域错误。Transcript/audit export 继续直接读
authoritative stream，故仍可导出删除证据。

Resume 不写入也不发送 replay 通知，只接受当前 workspace 内 active 且 idle 的
Session。Delete 使用正常 `appendCompact` 与未知结果解析路径，绝不以特殊 SQL 修改
绕过 writer authority。

EventStore 增加 `ListSessionHeads` 目录端口；Service 固定以 workspace 和 50 条页长
调用它，过滤 deleted head，并把非法 cursor 映射为验证错误。所有 EventStore 实现和
conformance fixture 都必须实现它，ACP 不得直接查询 SQLite。

## 4. SQLite 目录投影和迁移

`session_heads` 是派生状态而非第二权威。migration 4 加入 `workspace_root TEXT NOT
NULL`，其列为：session ID、workspace root、`idle|running|closed|deleted` 状态、活跃
Turn/Item ID、最后更新时间对应的 commit position。

迁移在既有 writer migration transaction 内按稳定 session ID 顺序扫描
`event_streams`，解码并 compact-replay 每条 canonical stream。所得 workspace、状态、
活跃 ID 和最后 commit position 必须与已有 head 相同；不一致视为数据库损坏，不能静默
修复。缺失 head 会插入，验证通过的行补回 workspace root。新增复合索引：
`(workspace_root, status, updated_at_commit_position DESC, session_id DESC)`。

每次 append 的 `updateSessionHead` 从 `session.created` 得出 root、跨事件保留 root，
并在 `session.deleted` 转为 deleted，与 canonical append 同一事务提交。

ListSessionHeads 单个读事务中按 `(commit_position, session_id)` 降序查询非 deleted
head，并 join `event_appends` 得到 `committed_at_unix`。游标是最长 512 bytes、严格解析
的 base64url JSON `{"v":1,"p":123,"s":"session-id"}`，所有值均作为 SQL bound
parameter 使用。每页固定 50 条；保证单页 snapshot，不承诺跨页 pinned snapshot。
`sqlite.Reader` 仍仅能 ReadStream，不增加目录管理或写能力。

## 5. ACP v1 wire 合约

initialize 保留 `loadSession: true`，并返回：

```json
"sessionCapabilities": {"list": {}, "resume": {}, "close": {}, "delete": {}}
```

不宣告 additional directories、MCP、mode 或 config options。

| 方法 | 成功结果 | 拒绝 |
| --- | --- | --- |
| `session/list`（可选 `cwd`、`cursor`） | `{sessions:[{sessionId,cwd,updatedAt}],nextCursor?}` | foreign cwd 或坏 cursor：`-32602 invalid params` |
| `session/resume`（必填 `sessionId`、`cwd`） | `{}`，不发历史 update | 缺失、foreign、closed、running、deleted：统一 `-32602` |
| `session/close`（必填 `sessionId`） | `{}`，在 prompt settlement 与 durable close 后 | 缺失、foreign、closed、deleted：统一 `-32602` |
| `session/delete`（必填 `sessionId`） | `{}`，durable `session.deleted` 后 | 缺失、foreign、active、running、deleted：统一 `-32602` |

这四个方法的持久化/内部失败统一是 `-32603 session operation failed`，不泄露存在性、
workspace root、状态或存储细节。list 即使没有 cwd 也只列 assembly workspace，永不
返回 deleted，也不输出 title、additional directories 或 `_meta`。

`session/load` 保持现有兼容请求形状；如果带 cwd，必须匹配 assembly workspace；非空
`mcpServers` 或 `additionalDirectories` 一律拒绝。它和 `session/prompt` 都会在工作
开始或 replay 前拒绝 deleted Session。

ACP server 的 wire-session 状态为 `idle → running → idle` 与
`idle/running → closing → absent`。close 在 mutex 下标记 closing，取消 running prompt，
等待其 goroutine 写入最终结果，然后调用正常 `application.CloseSession`。等待和 append
期间不持锁；closing 时新的 prompt/resume/close/delete 均拒绝。delete 永不取消工作，
只能在 close 成功后进行。

## 6. 投影、导出与文档

`internal/harness/transcript.ProjectRecord` 新增 `session.deleted`、空 object payload；
其 catalog、golden、strict codec 测试与中英文 schema 文档同步更新。ACP 对话 replay
不会投影 deletion，因为它在 replay 前已被拒绝。

更新 `acp-v1`、`session-transcript`、`sqlite-eventstore` 的中英文 implemented contracts
和 conversation/transcript evidence ledger；新增内容仅说明实际已测试行为。

## 7. 验收与验证

必须覆盖：领域 delete 状态机和 codec；应用层 deleted load/prompt 拒绝与 close/delete
CAS winner；空/已填充 v3 SQLite 的迁移回填、head 不一致失败、排序/cursor/workspace/删除
过滤；四个 ACP RPC 的精确 JSON、resume 无 replay、外部或 deleted 无泄露、close 先取消
再 settlement；以及 deleted Session 仍能 export 的 transcript golden。

完成前执行：

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./internal/harness/domain/ ./internal/harness/application/ \
  ./internal/harness/adapters/acp/ ./internal/harness/adapters/sqlite/ \
  ./internal/harness/transcript/ ./internal/harness/composition/
GOOS=windows GOARCH=amd64 go test ./...
GOOS=darwin GOARCH=arm64 go test ./...
```

主要风险与对策：删除保留 append-only 证据；workspace filter 与统一错误避免枚举泄露；
closing 状态机避免 prompt 竞态；迁移重放校验避免目录静默漂移；索引 keyset query 避免
全流回放；并发分页仅承诺单页 snapshot；转录同 Slice 增加新事实避免 export 失效。
