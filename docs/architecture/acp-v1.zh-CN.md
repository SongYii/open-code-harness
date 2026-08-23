# ACP v1 Adapter — 已实现合同（中文阅读版）

**状态：** 已实现；非 GA

**权威：** [ACP v1 Adapter（里程碑 6）设计](../superpowers/specs/2026-08-22-acp-v1-adapter-design.md)

**证据：** [ACP v1 adapter 完成证据](acp-v1-evidence.md)

英文版本 [acp-v1.md](acp-v1.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。

## 范围

ACP v1 JSON-RPC 2.0，换行分隔 UTF-8。适配器把 initialize、session/new、
session/load、session/prompt、session/cancel 与 session/request_permission
翻译到已有 Application 服务，不含领域规则。

组合根暴露 `ServeACP`。`cmd/och -acp` 在 stdin/stdout 上服务，诊断只写
stderr。

## 映射

- `protocolVersion` 为 `1`，宣告 `loadSession`，无鉴权方法。
- `session/new` 在装配工作区创建 Session；非空且不等于该工作区的 `cwd`
  返回 `-32602`。
- `session/load` 在 RPC 结果之前把 `turn.started` 与
  `assistant.message.completed` 投影为 `session/update`。
- `session/prompt` 调用 `RunTurn`。带工具目录时，模型提示会带上事件日志里
  先前 turn 的消息。`completed → end_turn`；调用方取消 → `cancelled`；
  其余 `-32603` `session prompt failed`。
- 同会话并发 prompt 为 `-32600`。权限桥为 `tools.Slot`，仅 `allow-once`
  授予，其余拒绝。
