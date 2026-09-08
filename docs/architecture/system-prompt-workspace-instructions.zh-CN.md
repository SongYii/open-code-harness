# 版本化 System Prompt 与 Workspace Instructions

**状态：** 已实现合同（中文阅读版）。英文规范源是
[system-prompt-workspace-instructions.md](system-prompt-workspace-instructions.md)；
若两者有出入，以英文文件为准。接受的设计、实施计划和证据分别见
[设计](../superpowers/specs/2026-09-04-system-prompt-workspace-instructions-design.md)、
[计划](../superpowers/plans/2026-09-08-system-prompt-workspace-instructions.md)和
[证据台账](system-prompt-workspace-instructions-evidence.md)。

## 一句话结论

启用 Context Engine 的对话请求固定以一条 harness 自有、带版本的
system prompt 开头。仓库的 `AGENTS.md` 以层级方式发现，变更通过
append-only 的 `set/replace/remove` 事件在模型请求前持久化；没有变化时，
旧消息字节不变，只在真正变化时追加一个 suffix。压缩后使用一个经过校验的
最终有效集合快照，而不是让 summarizer 改写仓库指令。

## 固定 prompt

资产位于
`internal/harness/agentinstructions/prompts/och_coding_agent_v1.md`，并由
`PromptID`、`PromptVersion`、`PromptDigest` 绑定。它不插入 session、工作区、
provider、日期或凭证等动态内容。旧的、未启用 Context Engine 的调用保持原有
请求合同；summarizer 使用独立的摘要 prompt。

prompt-change procedure 要求一次评审同时更新 prompt 资产、语义版本、精确字节
SHA-256、`internal/harness/agentinstructions/prompt_test.go` 的 golden 断言、
英文实现合同和证据台账。只改其中一项是不完整变更。

## 发现、顺序与上限

只识别 `AGENTS.md`。根候选总会检查；成功的 `read_file`、`write_file`、
`edit_file`、`list_dir` 会让系统在下一次模型请求前检查触达目录的祖先链。
`exec` 与 MCP 不驱动发现。

有效来源按 scope 深度从宽到窄、再按路径排序，所以更具体的文件排在后面。
最多发现 256 个路径；单来源最多 1 MiB；单条渲染消息最多 64 KiB。空间不足时
优先舍弃较宽来源，并用 diagnostics 明示省略或截断。

## 文件版本与内容身份

每个 session 有一份进程内 probe 表。`tools.FileVersion` 只是快速提示；版本变化
或进程重启后必须重新读取，以内容 SHA-256 作为最终身份。相同 digest 的重写不
产生事件。确认不存在可以产生 remove；瞬时读取失败则保留最后一次成功状态，
并把同一失败 episode 去重记录。

重启时从 durable events 重放指令状态，但不会持久化或信任旧 probe；新进程会
重新检查磁盘。

## 事件、缓存形状与压缩

`workspace.instructions.recorded` 记录 prompt 身份、epoch、发现范围、typed
changes、diagnostics、确定性渲染消息和有效集合 digest。事件必须先提交，使用它的
provider request 才能提交并发送。

请求顺序为：固定 system prompt；可选 summary/reset marker；可选指令快照；
保留的对话 tail 与之后的指令 delta；当前用户输入。没有变化时既有前缀逐字节
不变；发生变化时只追加新 suffix。这保证“可缓存形状稳定”，不承诺 provider
一定命中缓存。

summary 与 reset 都在 covered-through sequence 对最终有效集合做快照。快照记录
prompt 身份、epoch、sequence、排序后的来源、精确 rendered message 和 aggregate
digest。summarizer 只看对话单元，绝不看仓库指令正文或 framing。固定前缀和快照
都计入 fit、non-shrinking 与 hard-input 检查。

Domain 校验路径顺序、每个内容 digest、aggregate digest 与 sequence；Application
还会重新渲染并核对当前 prompt 身份。Memory 返回深拷贝；SQLite 从 canonical
completion event 恢复和重建，所以不需要 schema migration。

## 安全边界与排除项

仓库文本被 JSON 转义并明确标成非授权数据，不能授予 approval、扩大 workspace、
改变 sandbox、credential 或工具风险规则。它按设计存在于 canonical event 和
content-bearing transcript 中，但不会进入 summarizer。

非法 UTF-8、路径、大小、数量、digest chain、快照或 prompt 身份都会 fail closed。
Windows 专属运行时保证、由 `exec`/MCP 驱动的发现、provider 缓存命中率保证、
真实模型的抗 prompt-injection 结论均不在本合同内。

