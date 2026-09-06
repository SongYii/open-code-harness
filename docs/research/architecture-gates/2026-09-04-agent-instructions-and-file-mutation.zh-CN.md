# Agent 指令与安全文件修改架构门（中文阅读版）

**状态：** 调研证据已完成

**日期：** 2026-09-04

**规范源：** [英文架构门](2026-09-04-agent-instructions-and-file-mutation.md)。若中英文语义不一致，以英文为准。

## 范围与结论

本轮重新核验六个规定参考项目的 coding 指令与文件工具，并补读 Claude Code、OpenCode、PostgreSQL 乐观并发和 HTTP `If-Match`。固定版本为 Codex `89a4eec`、Kimi CLI `86f1364`、Grok Build `72a6125`、Pi `92d8e2d`、Maka `7f7843e`、DeepSeek Harness `b150a55`，观察日期均为 2026-09-04。

结论分成两个独立模块：先修已经上线的文件写入正确性，再做版本化 system prompt 与 `AGENTS.md`。本文是调研证据，不是实施计划，也不授权复制参考项目代码。

## 文件工具对照

主流 coding agent 的文件工具确实经过精心设计，但“精心设计”通常体现在小 schema、bounded read、精确替换、diff/审批、格式化/LSP 和可恢复错误，而不是数据库式跨 turn CAS。

| 项目 | 已采用的好机制 | 对 read 后被外部改写的保护 |
| --- | --- | --- |
| Codex | `apply_patch` 支持增删改移与上下文 hunk；不匹配即失败 | edit hunk 有隐式旧内容前置条件，但不是整文件版本 CAS；多文件 patch 可能在后续文件失败前已提交前面的文件 |
| Kimi CLI | Read/Write/StrReplace、精确替换、`replace_all`、diff/审批 | StrReplace 有内容前置条件；Write 仍是整文件覆盖语义 |
| Grok Build | `read_file`、`search_replace`、LSP 和工具过滤 | 未找到数据库式版本 token |
| Pi | 多个不重叠精确 edit、单文件进程内队列、保留 BOM/换行、diff | 能串行自身修改，但不会因为较早的 read 已过期而拒绝后来修改 |
| Maka | Runtime 拥有工具组合和策略 | 未找到更强的版本保护合同 |
| DeepSeek Harness | observation policy、opaque version、guarded atomic mutation、目标锁、结构化恢复错误 | structured tool 路径会把 observed-present 转为 replace-if-version，把 observed-absent 转为 create-if-absent；过期即拒绝 |

Open Code Harness 当前 `Read` 只返回 bytes/truncated，`Write` 没有前置条件；本地 adapter 直接 `O_TRUNC` 后写目标文件。这既可能暴露部分写入，也会让模型读取 A 后无声覆盖中间出现的 B。当前也没有 targeted edit，因此小改动必须整文件重写。

最适合我们的参考是 DeepSeek Harness 的四层拆分：model-facing tool、内部 observation policy、带可选 guard 的 filesystem port、执行 staged/fsynced publication 的 local provider。模型不看也不回传 version；成功读取由系统记下版本，write/edit 自动带 guard。`FS_NOT_OBSERVED` 与 `FS_STALE_VERSION` 只让模型重新读取后重试。

这不是“文件系统事务”。其版本来自 `dev:ino:size:mtimeNs:ctimeNs`，目标锁只覆盖本进程，不合作的外部 writer 不会被串行。数据库与 `If-Match` 真正可迁移的原则是：修改必须绑定 opaque observation，提交时不成立就拒绝并重试，而不是把时间戳交给模型。

本项目能承诺 structured file tools 之间不丢更新、写入以原子 publish 消除半文件，并在普通外部替换后拒绝 stale write；不能承诺所有外部进程的 kernel-atomic CAS。`exec` 是明确披露的不受此策略代理的写入通道。

## 指令与缓存对照

参考项目虽然文件名与层级不同，但都倾向于稳定核心 prompt，加上 workspace 自有指令。Codex 按 scope 发现 `AGENTS.md` 并让更近目录优先；Claude Code 使用层级 `CLAUDE.md`/rules，并公开说明 prompt cache 行为；DeepSeek Harness 稳定排序 system-prompt fragment，逐段记录 token/KV-cache 影响，并把 read/tool result 作为 append-only suffix。

Provider KV cache 只能复用不变前缀。每次 `AGENTS.md` 变化都重写最早的 system message，会让缓存从第一个差异 token 起失效；把 bounded change delta 追加到既有历史后面，则旧前缀保持 byte-stable。Context compaction 时必须把历史 delta rebase 成一个 durable effective snapshot，否则被替代的旧指令会永久占用窗口。

## 采用、拒绝与延期

采用：隐藏 observation state；literal `edit_file` 默认唯一匹配；同目录 staged atomic publish、保留 mode、进程内 per-target serialization；稳定 stale/not-observed/ambiguous/not-found 错误；固定版本 system prompt 加 append-only workspace instruction delta；Provider dispatch 前持久记录并在 compaction 时确定性 rebase。

拒绝：让模型提交 mtime/hash/version；未读既有文件就 unconditional overwrite；声称 mtime 是事务或 `exec` 也受 guard 保护；每 turn 改写第一条 system message；仅为这两个策略引入通用 plugin kernel。

延期：all-process workspace transaction/overlay filesystem；fuzzy edit、复杂 patch language、图片读取与任意指令文件名；Windows 特有 publish 行为；跨进程重启保存 observation，恢复后的 session 必须重读。

## 顺序决定

先实现 observed-state safe file mutation，因为它修复已有 write tool 的正确性缺口，也给后续安全发现 `AGENTS.md` 提供基础。再实现版本化 system prompt 与 append-only `AGENTS.md`。两份设计分开，避免第一步被 Provider request composition 变化拖住。完整一手来源链接和逐项目限制见英文规范源。
