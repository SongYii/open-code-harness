# ACP v1 Adapter — 已实现合同（中文阅读版）

**状态：** 已实现；非 GA

**权威：** [ACP v1 Adapter（里程碑 6）设计](../superpowers/specs/2026-08-22-acp-v1-adapter-design.md)

**证据：** [ACP v1 adapter 完成证据](acp-v1-evidence.md)；切片 A/A′ 映射见 [对话面与会话转录完成证据](conversation-and-transcript-evidence.md)；会话生命周期（list/resume/close/delete）见 [ACP 会话生命周期（切片 B）完成证据](acp-session-lifecycle-evidence.md)

英文版本 [acp-v1.md](acp-v1.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。

**包：** `internal/harness/adapters/acp`

## 范围

ACP v1 JSON-RPC 2.0，换行分隔 UTF-8。适配器把 initialize、session/new、
session/load、session/prompt、session/cancel、session/request_permission、
session/list、session/resume、session/close 与 session/delete 翻译到已有
Application 服务。映射在适配器纯函数（`ProjectRuntimeEvent`、
`ProjectRecordedEvent`）中，不含领域规则。

组合根暴露 `ServeACP`。`cmd/och -acp` 在 stdin/stdout 上服务，诊断只写
stderr。

对话面（用户 / 助手 / 工具卡片）属于本适配器。轨迹面（用量、步骤身份、
截断标志、墙钟）属于 [会话转录](session-transcript.md)。两面不共享编解码器，
也不得互相导入。

## Initialize 与会话 RPC

- `protocolVersion` 为 `1`，宣告 `loadSession`，同时宣告
  `sessionCapabilities: {list:{}, resume:{}, close:{}, delete:{}}`；无鉴权
  方法。适配器不协商客户端版本。
- `session/new` 在装配工作区创建 Session；非空且不等于该工作区的 `cwd`
  返回 `-32602`。
- `session/load` 与 `session/prompt` 仅在已加载 Session 的
  `WorkspaceRoot`（用 `application.CanonicalWorkspaceRoot` 规范化：绝对路径、
  `filepath.Clean`、不解析符号链接）等于同样规范化后的装配工作区时接纳。
  不匹配或未知会话为 `-32602` `invalid params`，不发 `session/update`、不
  调用 `RunTurn`。报文不区分缺失与外来，不泄露外来路径。在这个边界上，
  已删除的会话与未知会话不可区分。
- `session/prompt` 调用 `RunTurn`。带工具目录时，模型提示会带上事件日志里
  先前 turn 的消息。`completed → end_turn`；任何
  `TurnStatusInterrupted`、取消类别或已取消上下文 → `cancelled`；其余
  `-32603` `session prompt failed`。
- 同会话并发 prompt 为 `-32600`
  `a prompt is already in flight for this session`。
- `session/cancel` 取消进行中的 prompt 上下文；未知 ID 忽略。
- 权限桥为 `tools.Slot`：仅 `allow-once` 授予，其余（含传输失败）拒绝。
  `session/request_permission` 是反向 RPC，不是 `session/update`。其
  `toolCallId` 与工具卡片相同（`turnID + "/" + callID`）。标题为工具名；
  kind 见下表；status 为 `pending`。

## `toolCallId` 与 kind

`toolCallId = string(turnID) + "/" + callID`。领域 `CallID` 不保证跨 turn
在会话内唯一。标题为工具名。

| 工具名 | ACP `kind` |
| --- | --- |
| `read_file`、`list_dir` | `read` |
| `write_file` | `edit` |
| `exec` | `execute` |
| 其他 | `other` |

仅有 Code、`Text` 为空的 `tool.execution.failed` 的现场身份来自 prompt
sink 记住的 `LiveTool`，不是领域。顺序 `executeOneTool` 使同一时刻只有
一个未完成调用为真。

## 现场 `session/prompt` 映射

来源：`RunTurnRequest.Sink` 上的 `engine.RuntimeEvent`。客户端已有用户
提示；进行中的 turn 不发 `user_message_chunk`。

| Runtime 事件 | ACP `session/update` | 说明 |
| --- | --- | --- |
| 非空 `model.text.delta` | `agent_message_chunk` `{type:text, text}` | 按下方界限裁剪 |
| `model.tool_call` | `tool_call` `{toolCallId, title, kind, status: pending}` | 按最后一个 `:` 把 `Text` 解析为 `name:callID`。无 `rawInput` |
| `tool.execution.started` | `tool_call_update` `{toolCallId, status: in_progress}` | 包含校验 / 审批等待 |
| `approval.requested` / `resolved` | 无 | 权限 RPC 即 UX |
| `tool.execution.completed` | `tool_call_update` `{status: completed}` | 现场无 `content` / `rawOutput` |
| `tool.execution.failed` | `tool_call_update` `{status: failed}` | 可发送的帧绝不跳过。`Code` 不上线。若 `toolCallId` 本身放不下，与其他工具卡片一样省略 |
| `model.stream.*`、`append.completed` | 无 | 运行器 / 存储内部 |

prompt 路径上的 `session/update` 写失败被吞掉（`Emit` 返回 nil），以免
丢卡片变成 `CodeDelivery` 或改变 `stopReason`。prompt JSON-RPC **结果**
写入仍是尽力而为。`session/load` 期间 `session/update` 写失败则负载 RPC
以 `-32603` `session prompt failed` 失败。

## `session/load` 重放映射

来源：钉住的 `History.ReadStream`（页大小 256，第一页钉住头）。空文本跳过。
开放与进行中的会话都可重放；最后一张工具卡片可以仍是 `in_progress`。
load 不发 `session/request_permission`。

| 领域事件 | ACP `session/update` | 不投影 |
| --- | --- | --- |
| 非空 `Input` 的 `turn.started` | `user_message_chunk` | 空 Input |
| 非空 `Text` 的 `assistant.message.completed` | `agent_message_chunk` | `ToolCalls` 提议（卡片来自 `tool.call.*`） |
| 非空 `Message` 的 `assistant.message.failed` / `interrupted` | `agent_message_chunk` | 错误码 |
| `tool.call.started` | `tool_call` `{toolCallId, title, kind, status: in_progress, rawInput?}` | — |
| `tool.call.completed` | `tool_call_update` `{status: completed, content: [文本块]}` | 领域 `Truncated`（转录保留） |
| `tool.call.failed` | `tool_call_update` `{status: failed, content: [Message 文本块]}` | `Code` |
| `tool.call.interrupted` | `tool_call_update` `{status: failed}` | ACP v1 无 `interrupted` 状态 |
| `approval.*`、turn 终态、`session.*`、`assistant.message.started`、`model.request.recorded`、`model.usage.recorded`、`policy.decision.recorded` | 无 | — |

`rawInput`：若 `Arguments` 是 JSON 对象或数组，传紧凑 JSON 值；否则省略。
工具 `Content` 是字符串——使用
`content: [{type:"content", content:{type:"text", text: ...}}]`。不发明
`rawOutput`。

## 会话生命周期（list / resume / close / delete）

| 方法 | 请求 | 成功 | 拒绝 |
| --- | --- | --- | --- |
| `session/list` | 可选 `cwd`、可选不透明 `cursor` | `{sessions:[{sessionId,cwd,updatedAt}], nextCursor?}` | 非空的外来 `cwd` 或坏 cursor → `-32602` |
| `session/resume` | 必需 `sessionId`、必需 `cwd`；非空 `mcpServers` 或 `additionalDirectories` 被拒绝，空列表可容忍 | `{}`，不发 `session/update` | 缺失、外来、领域已关闭、正在运行或已删除 → `-32602` |
| `session/close` | 必需 `sessionId` | 取消/结算并 detach 该现场条目后返回 `{}`；无领域追加 | 未附着（该 id 没有 idle/running 现场条目）、该会话已在 closing/detached/deleting，或 durable 校验失败（缺失、外来工作区、领域已关闭或已删除）→ `-32602` |
| `session/delete` | 必需 `sessionId` | 持久 `session.deleted` 追加成功后返回 `{}`，或幂等空操作 | 同工作区条目处于 running、closing 或 deleting → `-32602`；缺失、外来或已删除 → 无变更地返回 `{}` |

这四个方法里任何内部（非校验）失败都是 `-32603`
`session operation failed`。`updatedAt` 为 RFC3339Nano UTC。即使省略
`cwd`，`session/list` 也总是列出装配工作区；它绝不返回已删除的会话，也不带
标题、`additionalDirectories` 或 `_meta`。

**ACP close 不是持久的 `session.closed` 事实。** close 只取消该 duplex
拥有的工作并 detach 现场条目；持久 Session 仍可恢复，绝不调用
`application.CloseSession`。close 确实会执行一次持久的*读*——
`LoadSession`——在接纳这次转换之前确认该会话在本工作区仍然存在：一个已被
外部删除或已被其他路径持久关闭的会话是 `-32602`，而不是静默地成功 detach。
这次读取不持锁，且运行在 dispatch goroutine 之外（这样一次缓慢的读取不会
阻塞其他会话的帧）；最终的接纳决定——重新查找现场条目、转到 `wireClosing`、
以及被实际使用的那对 `cancel`/`promptDone`——只在该读取返回之后、在锁内、
恰好做一次。这个决定里没有任何东西是在读取之前捕获的：如果读取进行期间某
个 prompt 结算了、或又有新 prompt 开始了，都会被正确地看到，因为 close
取消的永远是它真正提交 closing 那一刻实际在跑的 prompt，绝不是更早捕获的
那个。delete 是本切片新增的唯一持久生命周期事实：
它经由与其他命令相同的 CAS 保护追加路径写入 `session.deleted`，是逻辑删除
（不物理擦除任何行——见[会话转录](session-transcript.md)），并把缺失、
外来工作区与已删除会话都当作同一个不可区分的成功空操作，因而绝不会成为
存在性判定器。

### 现场会话状态机

一条 duplex 上每个已附着的会话都处于五种状态之一，只保存在适配器内存中
（`internal/harness/adapters/acp/server.go`）：

```text
new / load(活动) / resume ────────────────────────────────────> idle
idle ── prompt ──> running ── 终态响应结算 ────────────────────> idle
idle / running ── close ─> closing ── 取消 + 结算 + 释放 ──────> detached
detached ── load / resume ─────────────────────────────────────> idle
idle / detached / absent ── delete ─> deleting ── 追加/空操作 ─> absent
```

`session/new`、活动会话的 `session/load` 与 `session/resume` 都会附着一个
idle 条目；对一个已持久关闭的 Session 做 `session/load` 仍可重放其历史，
但不留下条目，因此它保持不可 prompt——也无法用 `session/resume` 重新附着，
因为 `ResumeSession` 本身会拒绝非 active 状态。`session/prompt` 要求已附着
的 idle 条目并把它转为 `running`。正常完成时，仍为 `running` 的条目会在
prompt 的终态 JSON-RPC 响应**发布之前**回到 `idle`（这样一个在收到响应后
立刻再次 prompt 的客户端绝不会读到仍处于 `running` 的陈旧状态）。若 close
已把条目改成 `closing`，prompt 结算会保留 `closing`；两条路径中，一个被阻塞
的 `session/close` 所等待的完成信号都只在终态响应写完**之后**才触发。因此
close 绝不会在被取消的 prompt 自己的终态帧上线之前就报告 `{}`。
`frameWriter` 会在这些 goroutine 之间串行写出每个完整帧，因此提前转为 idle
不会使 JSON 响应字节相互交错。
`session/prompt`、`session/resume`
与 `session/load` 在条目为 `running`、`closing` 或 `deleting` 时都被拒绝，
唯一例外是 `session/close` 本身可以接纳 `running`（这正是 close 取消一个
进行中 prompt 的方式）。`session/delete` 在调用 `DeleteSession` 之前，用与
其他每一次转换相同的互斥锁安装 `deleting`，因此不会有 prompt 在删除许可与
持久追加之间被接纳；除幂等的缺失/外来/已删除情形外，任何失败都会把条目
恢复到其确切的先前状态。close 与 delete 都在各自互斥锁保护的许可检查之后
把慢速工作（等待被取消的 prompt、调用 Application 层）交给一个 goroutine，
因此都不会阻塞同一 duplex 上其他会话的帧。

### 固定、不泄露的错误

每个生命周期校验失败都使用两个固定字符串之一（`-32602` 的
`invalid params`，或 `-32603` 的 `session operation failed`）；两者都不含
会话 ID、工作区根路径或生命周期状态名。

## 裁剪界限

入站 RPC 帧仍为 `maxFrameBytes = 1 MiB`。超长行使编解码器失败
（`token too long`）并拆掉 `Serve`，不会写成 `-32700` 帧。`-32700` 只用于
非法 JSON 或错误的 `jsonrpc` 版本。出站文本在投影器内按 UTF-8 码点边界
裁剪，绝不在领域层裁剪。JSON 编码之后（含换行与控制字符转义），
`session/update` NDJSON 帧含尾随换行不得超过 `maxFrameBytes`。投影器会
收缩文本与工具 `title` 直到编码后的帧放下。绝不裁剪 `toolCallId`：身份
必须在现场更新、load 回放与 `session/request_permission` 之间一致。若
身份字段本身放不下，投影器省略该更新（load 继续），而不是让 RPC 失败
或写出超限帧。

| 界限 | 上限 | 超出时 |
| --- | --- | --- |
| 出站 `session/update` 帧 | 编码后 1 MiB | 收缩文本/title 直到 marshal 后的帧放下；身份仍放不下则省略该更新 |
| 出站 `agent_message_chunk` / `user_message_chunk` 文本 | 原文 768 KiB，再按编码帧适配 | 裁剪；对话继续 |
| 出站工具 `content` 文本 | 原文 16 KiB，再按编码帧适配 | 裁剪；若裁剪后的前缀尚未以 `\n[truncated]` 结尾，则追加该标记 |
| 出站工具 `title`（及 permission `title`） | 收缩直到编码帧放下 | 按 UTF-8 边界裁剪；绝不追加 `\n[truncated]`。`kind` 仍按未裁剪的名称计算 |
| 出站 `toolCallId`（及 permission `toolCallId`） | 必须放进编码帧 | 绝不裁剪。省略该 `session/update`。跳过 permission RPC（fail-closed 拒绝） |
| 出站 `rawInput` | 16 KiB 紧凑 JSON | 在 UTF-8 边界裁剪编码字节；若结果不再是合法 JSON，则 **省略** `rawInput`。绝不给 `rawInput` 追加 `\n[truncated]`。若工具调用帧仍超过 1 MiB 则整段省略 |

## 现场保真缺口

`engine.RuntimeEvent` 未改。现场工具卡片只有 id、名称、kind 与状态。
参数与结果文本出现在 `session/load` 与转录上。从不 load 的客户端看不到
进行中 turn 的 `rawInput` 或输出内容。

## 从不投影到 ACP

用量 token、延迟、`finishReason`、`providerRequestID`；策略规则 ID；
`model.request.recorded`；审计摘要 / 提交位置；原始 provider SSE；领域
错误码（固定 JSON-RPC 报文保留）；子代理 origin、计划、思考、终端、
diff、ACP v2 字段；裁决。

## 排除项

ACP v2、终端、斜杠命令、authenticate、感知 token 的压缩、protocolVersion
协商、取消时权限等待者清理、`RuntimeEvent` 增富，以及默认测试门中的子进程
stdio。`session/set_mode`、`session/set_config_option`、会话分叉、批量删除、
撤销删除，以及对已删除会话的物理保留/垃圾回收。`session/load` /
`session/resume` 上的 `additionalDirectories` 与会话级 MCP 配置仅接受空值
且从不据此行动；不构造任何 MCP 客户端。不从提示词、搜索、标签或状态元数据
生成标题，只用 ACP 要求的 `SessionInfo` 字段。`session/list` 不保证多页
历史快照：每一页都是一次读事务,并发写入可以把某个会话移动到另一页。
