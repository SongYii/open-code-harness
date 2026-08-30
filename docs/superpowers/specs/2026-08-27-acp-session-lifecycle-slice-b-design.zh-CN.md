# ACP 会话生命周期（切片 B）

- **日期：** 2026-08-27
- **状态：** Draft（设计已确认，等待本文档人工审阅）
- **稳定性：** `experimental` ACP v1 会话管理面
- **规范语言：** [英文原文](2026-08-27-acp-session-lifecycle-slice-b-design.md)
- **本文性质：** 与英文关键决策同步的中文摘要，不逐字段翻译 API 声明

发生歧义时以英文规范为准。

---

## 1. 决策摘要

Slice B 为现有持久化 Session 模型增加 capability-gated 的 ACP v1
`session/list`、`session/resume`、`session/close` 与 `session/delete`。

ACP close 与既有持久化事实 `session.closed` 是两个不同操作：

- ACP `session/close` 取消当前 duplex 中的工作、等待终态响应并释放运行资源，
  但保留可 resume 的持久化 Session；
- 既有 Application `CloseSession` 才是结束对话并追加 `session.closed` 的领域命令。

删除是不可逆的**逻辑删除**：追加新的 canonical 领域事实
`session.deleted`，绝不删除 `events` 行、审计链或 session transcript。已删除
Session 不出现在 ACP 列表中，并且普通 load、resume、prompt 都不可用；ACP 重复删除
或删除不存在的 Session 幂等成功。

既有同步 `session_heads` 投影成为索引目录。migration 4 通过重放 canonical stream
校验并重建最终表，为每个 head 补齐规范化 workspace root；append、audit import、
rebuild 和 recovery 使用同一套 head 状态映射。ACP 不直接读取 SQLite。

## 2. 目标与排除项

目标：实现并宣告四个 ACP v1 生命周期能力；仅列出 assembly workspace 中的会话；
让删除可持久化、可回放、可审计且 wire 层幂等；保持 fencing、CAS、append identity、
未知提交和 pinned read 约束；明确 close/prompt/delete 竞态与 wire-session attachment。

不在范围：ACP v2、版本协商、认证、mode/config/fork、批量删除、undelete、物理清理；
additionalDirectories、session MCP；`och` 管理子命令；从用户输入生成标题；跨分页历史
快照；以及对 Slice A 对话投影、audit JSONL 或 context compaction 的修改。

## 3. 状态和持久化事实

### 3.1 领域生命周期

新增 `SessionStatusDeleted = "deleted"`、`CommandDeleteSession =
"session.delete"`、`DeleteSession{SessionID}`、`EventSessionDeleted =
"session.deleted"` 和 `SessionDeleted{}`。

`domain.Decide` 仅在 Session 存在、目标 ID 匹配、状态为 `active` 或 `closed`，且没有
活跃 Turn/Item 时接受 DeleteSession；它精确产生 `[SessionDeleted{}]`。`domain.Apply`
仅从同一 idle active-or-closed 状态接受该事件并转为 `deleted`。running Session 不可
删除。二次领域删除仍得到确定的 `session_deleted`；ACP adapter 将它和 Session 不存在
映射为幂等成功。codec、clone、compact replay、historical oracle 与 audit
serialization 全部认识新事件。

`session.closed` 仍是既有 Application close 产生的持久化对话终态，不隐藏历史；
ACP `session/close` 不追加该事实。`session.deleted` 才是本格式中不可撤销且不可普通
使用的终态。

### 3.2 Application 与 workspace 行为

Service 新增 ListSessions、ResumeSession、DeleteSession 用例；DeleteSession request
同时携带 SessionID 与 WorkspaceRoot。普通 `LoadSession` 对 deleted aggregate 返回
`session_not_found`；只有 DeleteSession 的内部生命周期加载器可重放 deleted 状态。
DeleteSession 的公开边界把不存在、foreign workspace 和已删除统一为
`session_not_found`，供 ACP 实现不泄露存在性的幂等结果。Transcript/audit export
继续直接读取 authoritative stream，因此仍可导出删除证据。

Resume 不写入也不发送 replay 通知，只接受当前 workspace 内 active 且 idle 的
Session。Delete 通过正常 `appendCompact`、CAS 与未知结果解析路径追加一个
`session.deleted`，绝不以特殊 SQL 绕过 writer authority。

workspace root 只有一种词法表示：共享 helper 要求绝对路径并执行 `filepath.Clean`，
但不解析 symlink。CreateSession 把规范值写入 `session.created`；list、resume、delete、
load、prompt 和 assembly root 都使用同一 helper。迁移只从已记录的
`session.created` 校验并 clean 得到目录 root，绝不从 ACP request 制造它；因此历史
`/repo/.` 与 `/repo` 的 list/load admission 一致。

EventStore 增加 `ListSessionHeads` 目录端口。端口使用独立的
`SessionHeadStatus = idle | running | closed`，不复用 `domain.SessionStatus`；它在分页
前就排除 deleted head。Service 固定使用 canonical workspace 和 50 条页长。所有
EventStore 实现及 conformance fixture 都必须实现该端口。

## 4. SQLite 目录投影和迁移

最终 `session_heads` 列为：session ID、`workspace_root TEXT NOT NULL`、
`idle|running|closed|deleted` 状态、活跃 Turn/Item ID 和最后 commit position。

非空 SQLite 表不能直接增加无默认值的 NOT NULL 列，因此 migration 4 在既有 writer
migration transaction 内建立带最终约束的 `session_heads_v4` 影子表。它按稳定 session
ID 顺序扫描 `event_streams`，解码并 compact-replay 每条 canonical stream，再把派生
结果写入影子表。

状态映射固定如下：

- domain active 且无 Turn → `idle`；
- domain active 且有 Turn → `running`；
- domain closed → `closed`；
- domain deleted → `deleted`。

校验 v3 head 时，旧存储值 `active` 只等价于新 `running`；`idle` 与 `closed` 必须
精确相等，活跃 ID 与 `event_streams.last_append_commit_position` 也必须一致。其他
不一致或 orphan head 视为数据库损坏；缺失的派生 head 可以重建。全部验证后删除旧表、
rename 影子表，并创建只覆盖可见会话的 partial index：

```sql
CREATE INDEX session_heads_visible_by_workspace
ON session_heads (
    workspace_root,
    updated_at_commit_position DESC,
    session_id DESC
)
WHERE status <> 'deleted';
```

`updateSessionHead` 与 audit-import head builder 都从 `session.created` 得出 canonical
root、跨事件保留 root，并在 `session.deleted` 转为 deleted。
`RebuildAndVerifySessionHeads` 校验 root、状态、活跃 ID 和 commit position；runtime
recovery 枚举 `running`，不再查询旧 `active`。这些路径都有 focused tests。

ListSessionHeads 在一个读事务中先以 SQL 排除 deleted，再按
`(commit_position, session_id)` 降序取 `Limit + 1` 条，并 join `event_appends` 得到
`committed_at_unix`。时间转为 UTC，ACP `updatedAt` 使用 RFC 3339 Nano。游标是最长
512 bytes、严格解析的 base64url JSON `{"v":1,"p":123,"s":"session-id"}`，所有值均作为
SQL bound parameter。额外一行只用于决定 next cursor；cursor 来自实际返回的最后一条
可见记录。每页保证单个 SQLite snapshot，不承诺跨页 pinned snapshot。
`sqlite.Reader` 仍只具有 ReadStream 能力。

## 5. ACP v1 wire 合约

initialize 保留 `loadSession: true`，并返回：

```json
"sessionCapabilities": {"list": {}, "resume": {}, "close": {}, "delete": {}}
```

不宣告 additional directories、MCP、mode 或 config options。

| 方法 | 成功结果 | 拒绝 |
| --- | --- | --- |
| `session/list`（可选 `cwd`、`cursor`） | `{sessions:[{sessionId,cwd,updatedAt}],nextCursor?}` | foreign cwd 或坏 cursor：`-32602 invalid params` |
| `session/resume`（必填 `sessionId`、`cwd`） | `{}`，不发历史 update，并 attach | 缺失、foreign、closed、running、deleted：`-32602` |
| `session/close`（必填 `sessionId`） | prompt settlement 与 duplex 资源释放后返回 `{}`；无领域 append | unattached、foreign、domain-closed、deleted：`-32602` |
| `session/delete`（必填 `sessionId`） | durable `session.deleted` 后返回 `{}`，或幂等 no-op | 同 workspace 的 running/closing/deleting：`-32602`；不存在、foreign、deleted：不写入并返回 `{}` |

持久化/内部失败统一为 `-32603 session operation failed`。验证错误不包含 session ID、
workspace root、生命周期状态或存储细节；delete 对不存在、foreign、deleted 返回同一
成功结果，不成为存在性 oracle。list 即使没有 cwd 也只列 assembly workspace，永不
返回 deleted，也不输出 title、additional directories 或 `_meta`。

`session/load` 保持兼容请求形状；若带 cwd，必须匹配 canonical assembly workspace；
非空 `mcpServers` 或 `additionalDirectories` 一律拒绝。load 在 replay 前拒绝 deleted。
`session/prompt` 做同样 admission，并要求 attached idle wire entry。new、active load、
resume 会 attach；durable domain-closed Session 可以 load/replay 历史，但不可 prompt。

wire-session 状态机为：

```text
new / load / resume ───────────────────────────────────────> idle
idle ── prompt ──> running ── terminal response ──────────> idle
idle ── close ─> closing ───────────── release resources ─> detached
running ── close ─> closing ─> cancel + terminal response ─> detached
detached ── load / resume ─────────────────────────────────> idle
idle / detached / absent ─> deleting ─> append or no-op ──> absent
```

close 在 mutex 下标记 closing；running 时先 cancel，再等待 prompt goroutine 发布终态
响应，释放 duplex 资源并记录 detached。它不调用 `application.CloseSession`，也不追加
`session.closed`；等待期间不持锁。detached Session 必须 load/resume 后才能再次 prompt。

delete 永不取消工作。它在 mutex 下拒绝 running/closing/deleting，并在 load 或 append
前把 idle、detached 或不存在的 entry 改成 deleting，从而阻止 prompt 插入 admission 与 CAS
之间。deleting 时 prompt/resume/load/close/delete 均拒绝。提交成功或幂等
absent/foreign/deleted 后移除 entry；内部失败则恢复先前的 idle/detached/absent 状态。
跨 duplex 或 runtime 的竞态仍由 EventStore CAS 最终裁决。

## 6. 投影、导出与文档

`internal/harness/transcript.ProjectRecord` 新增 `session.deleted`、空 object payload；
其 catalog、golden、strict codec 测试与中英文 schema 文档同步更新。ACP 对话 replay
不会投影 deletion，因为 deleted Session 在 replay 前已被拒绝。

更新 `acp-v1`、`session-transcript`、`sqlite-eventstore` 的中英文 implemented
contracts 和 conversation/transcript evidence ledger；新增内容只说明实际已测试行为。

## 7. 验收与验证

必须覆盖：

- 领域层 active-idle/closed 可 delete、running 不可 delete、deleted 终态和 codec；
- 应用层 canonical workspace admission、deleted load/prompt 拒绝、export 保留、
  二次 delete 内部结果及 RunTurn/delete 单一 CAS winner；
- 空/已填充 v3 SQLite 的影子表迁移、旧 `active` 到 `running` 映射、精确回填、
  malformed/orphan/mismatched head 失败、legacy root 规范化、audit import、rebuild、
  recovery、排序、`Limit + 1` cursor、workspace 和 deleted 过滤；
- 四个 ACP RPC 的精确 JSON、resume 无 replay、absent/foreign/deleted delete 幂等成功、
  close 取消后 settlement 且无领域 append、detached 必须 load/resume、deleting 阻止
  prompt 进入；
- deleted Session 仍可 export 的 transcript golden。

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

主要风险与对策：删除保留 append-only 证据；canonical workspace filter 与 delete 的
幂等成功避免枚举泄露；wire close 不产生 durable close；closing/deleting 状态封住
prompt 竞态；迁移、append、audit import、rebuild、recovery 共用并校验 head 映射；
partial index 与 SQL 预过滤保证 keyset pagination；并发分页只承诺单页 snapshot；
转录同 Slice 增加新事实，避免 export 失效。
