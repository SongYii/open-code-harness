# ACP v1 Adapter — 已实现合同（中文阅读版）

**状态：** 已实现；非 GA

**权威：** [ACP v1 Adapter（里程碑 6）设计](../superpowers/specs/2026-08-22-acp-v1-adapter-design.md)

**证据：** [ACP v1 adapter 完成证据](acp-v1-evidence.md)；切片 A/A′ 映射见 [对话面与会话转录完成证据](conversation-and-transcript-evidence.md)

英文版本 [acp-v1.md](acp-v1.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。

**包：** `internal/harness/adapters/acp`

## 范围

ACP v1 JSON-RPC 2.0，换行分隔 UTF-8。适配器把 initialize、session/new、
session/load、session/prompt、session/cancel 与 session/request_permission
翻译到已有 Application 服务。映射在适配器纯函数
（`ProjectRuntimeEvent`、`ProjectRecordedEvent`）中，不含领域规则。

组合根暴露 `ServeACP`。`cmd/och -acp` 在 stdin/stdout 上服务，诊断只写
stderr。

对话面（用户 / 助手 / 工具卡片）属于本适配器。轨迹面（用量、步骤身份、
截断标志、墙钟）属于 [会话转录](session-transcript.md)。两面不共享编解码器，
也不得互相导入。

## Initialize 与会话 RPC

- `protocolVersion` 为 `1`，宣告 `loadSession`，无鉴权方法。适配器不协商
  客户端版本。
- `session/new` 在装配工作区创建 Session；非空且不等于该工作区的 `cwd`
  返回 `-32602`。
- `session/load` 与 `session/prompt` 仅在
  `filepath.Clean(loaded.WorkspaceRoot)` 等于装配工作区时接纳。不匹配或未知
  会话为 `-32602` `invalid params`，不发 `session/update`、不调用 `RunTurn`。
  报文不区分缺失与外来，不泄露外来路径。
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
| `tool.execution.failed` | `tool_call_update` `{status: failed}` | 绝不跳过。`Code` 不上线 |
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

## 裁剪界限

入站 RPC 帧仍为 `maxFrameBytes = 1 MiB`（超出 `-32700`）。在投影器内按
UTF-8 码点边界裁剪，绝不在领域层裁剪。

| 界限 | 上限 | 超出时 |
| --- | --- | --- |
| 出站 `agent_message_chunk` / `user_message_chunk` 文本 | 768 KiB | 裁剪；对话继续 |
| 出站工具 `content` 文本 | 16 KiB | 裁剪；若已裁且领域文本尚未以 `\n[truncated]` 结尾，则追加该标记 |
| 出站 `rawInput` | 16 KiB 紧凑 JSON | 在 UTF-8 边界裁剪编码字节；若结果不再是合法 JSON，则 **省略** `rawInput`。绝不给 `rawInput` 追加 `\n[truncated]` |

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

ACP v2、resume / list / delete、终端、斜杠命令、authenticate、感知 token
的压缩、protocolVersion 协商、取消时权限等待者清理、`RuntimeEvent`
增富，以及默认测试门中的子进程 stdio。
