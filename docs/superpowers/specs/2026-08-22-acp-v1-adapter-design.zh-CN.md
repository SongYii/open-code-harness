# ACP v1 Adapter（里程碑 6）

**状态：** 已接受设计

**日期：** 2026-08-22

本文档是英文正本
[2026-08-22-acp-v1-adapter-design.md](2026-08-22-acp-v1-adapter-design.md)
的中文同步阅读版；两份副本如有分歧，以英文正本为准。

**父文档：** [基础架构](2026-08-11-open-code-harness-architecture-design.md)

**证据：** [ACP v1 adapter 架构门](../../research/architecture-gates/2026-08-22-acp-v1-adapter.md)

## 1. 决策摘要

1. **只面向 ACP v1。** 服务 `protocolVersion: 1`。v2 保持可增量，本切片不实现。
2. **`adapters/acp` 是传输包。** 把 JSON-RPC 翻译成已有 Application 命令，不做策略、重试或会话记忆。唯一生产引用者是组合根。
3. **Approver 注入是 `tools.Slot`，不是新 Application 端口。** Service 仍在构造时接收 `tools.Approver`。组合根始终安装一个互斥槽位，初始为 `DenyApprover`。ACP 服务器在 `Serve` 期间把自己 `Set` 进去，拆解时设回拒绝。
4. **停止原因是已实现 turn 代数上的全函数。** `completed → end_turn`；`interrupted` 且 `caller_canceled → cancelled`；其余终态或错误一律 JSON-RPC `-32603`，固定文案 `session prompt failed`。适配器永不编造 `refusal`。
5. **自持最小 NDJSON JSON-RPC codec。** 不引入社区 Go SDK。
6. **`session/load` 投影事件日志；`session/prompt` 仍然失忆。** 多轮记忆属于里程碑 8。
7. **默认验证是内存 NDJSON 对着真实装配。** 无网络、无凭据、无子进程。

## 2. 目标与非目标

目标：initialize、session/new、load、prompt、cancel、request_permission；组合根暴露 `ServeACP`；同会话并发 prompt 本地 `-32600` 拒绝；权限 fail-closed；无密钥测试覆盖上述方法。

非目标：v2、resume、终端、elicitation、slash、session 模式、从 `session/new` 接入 MCP、authenticate、Context Engine、把 refusal 加进领域终态、`-acp` 时往 stdout 打横幅。

## 3. 映射

| ACP | Application |
| --- | --- |
| `initialize` | 协商版本 1，宣告 `loadSession`，无鉴权方法 |
| `session/new` | `CreateSession`，工作区为装配根；`cwd` 若出现必须与之相同 |
| `session/load` | `LoadSession` + `ReadStream`，把 `turn.started` / `assistant.message.completed` 投影为 `session/update` |
| `session/prompt` | 拼接文本块后 `RunTurn`；`model.text.delta` 变成 `agent_message_chunk` |
| `session/cancel` | 取消进行中的 prompt 上下文；未知 session 吞掉 |
| `session/request_permission` | `Approver.Decide` 的反向 RPC；仅 `allow-once` 授予，其余拒绝 |

prompt 在独立 goroutine 中运行，以便单行读取循环仍能收到 cancel。
