# 基于已观察状态的安全文件修改设计（中文阅读版）

**状态：** 已于 2026-09-05 接受

**日期：** 2026-09-04

**稳定性：** v1.0 前新增 Go 类型、port、错误和工具均为 `internal`。

**规范源：** [英文设计](2026-09-04-observed-file-mutation-design.md)。若中英文语义不一致，以英文为准。

**调研依据：** [Agent 指令与安全文件修改架构门](../../research/architecture-gates/2026-09-04-agent-instructions-and-file-mutation.zh-CN.md)

## 问题与目标

当前 `read_file` 不返回内部 revision，`write_file` 直接 truncate，且没有 targeted edit。模型读到 A 后，外部 actor 写入 B，稍后的工具调用会无声覆盖 B；写到一半失败还可能留下半文件。审批只回答“准不准写”，不能证明修改仍适用于模型读过的版本。

本设计要让同一 active session 的 structured filesystem tools 防止 lost update，检测常见外部变化，新增 bounded literal `edit_file`，并用 atomic publish 替代原地 truncate。版本永远不进入模型参数。它不承诺 transactional filesystem，不代理 `exec`/外部软件写入，不跨重启保存 observation，不做 fuzzy/regex/patch language，也不做 Windows 专项工作。

## 三层所有权

1. `tools.FileSystem` 拥有 opaque observation 与 guarded mutation 类型；
2. Application 按 active session 保存 observation，并把 model call 转换为 guard；
3. `workspacefs` 拥有 canonical target identity、version、per-target lock、literal edit 和 staged atomic publication。

不引入通用 plugin/event kernel。模型仍只传路径和内容，不传 timestamp/hash/version。

## Observation 合同

状态键为 `(SessionID, canonical target identity)`，值为 `unseen`、`absent` 或 `present(version)`。

| 操作 | 先前状态 | 行为 |
| --- | --- | --- |
| 成功 read | 任意 | 记录 `present(version)` |
| 明确 missing read | 任意 | 记录 `absent`，照常返回 not-found |
| write | unseen/absent | `create_if_absent`，已有目标时绝不覆盖 |
| write | present(v) | `replace_if_version(v)` |
| edit | unseen | `FS_NOT_OBSERVED` |
| edit | absent | `FS_NOT_FOUND` |
| edit | present(v) | `replace_if_version(v)` |

这样新文件无需先做一次注定失败的 read，但并发 creator 仍受保护；覆盖既有文件则必须先读。失败 mutation 不更新 observation；stale 只能靠重新 read 修复。runtime 丢弃或 session resume 后状态回到 unseen，默认 fail closed。

Windowed/truncated read 也记录整目标 version。Freshness 只表示“还是同一 revision”，不表示模型看过全部字节；partial read 后 whole-file overwrite 仍须审批，prompt guidance 会要求小修改优先 edit。

## Port 与 `edit_file`

Port 语义固定为：`Read` 返回 bytes/truncated/version；`Write` 和 `Edit` 接收 Application 自动生成的 `create_if_absent` 或 `replace_if_version` guard，并返回新 version 与 create/update 类型。local adapter 的 opaque token 至少结合 device、inode、size、mtime、ctime 的高精度值；外层和测试不得解析。

`edit_file` 参数为 `path`、非空 `old_string`、`new_string`、可选 `replace_all`。默认要求 literal 恰好匹配一次：零次是 `FS_EDIT_NOT_FOUND`，多次是 `FS_AMBIGUOUS_EDIT`；`replace_all=true` 修改所有非重叠匹配。Adapter 先检查 version，再匹配文字，因此 stale edit 不会针对新内容误报 match error。仅支持 bounded UTF-8 text，并保留主导 LF/CRLF 与原 regular file mode。

## Atomic publication

在同一 `workspacefs` 内，write/edit 按 canonical target 加锁，然后重新 jail/识别目标、验证 guard、计算 edit，在目标同目录的私有 staging directory 中 exclusive 创建 temp，完整写入并 fsync、应用 mode、关闭，最后用 no-replace create 或 atomic rename replace 发布。发布前失败保持旧目标 byte-identical；发布成功即 commit point，之后 cleanup 失败只留下可清理 residue。

必须诚实保留一个限制：portable filesystem 没有“仅当目标仍是某 metadata version 才 rename”的通用 syscall。所有本进程 structured writers 由锁串行，普通外部替换会在 guard check 被发现，但不合作的外部 writer 仍可能卡在最终 check-to-rename 窗口。要获得 all-process 强保证，未来需要 overlay/versioned workspace，而不是夸大本合同。

## 错误与验证

稳定错误包括 `FS_NOT_OBSERVED`、`FS_STALE_VERSION`、`FS_EDIT_NOT_FOUND`、`FS_AMBIGUOUS_EDIT`、`FS_NOT_REGULAR_FILE`、`FS_NOT_TEXT`、`FS_TOO_LARGE`。展示文字给出 read/retry 或增加上下文的恢复动作，但不泄露 opaque version。Policy、Approver、scope、cancel 与 adapter error 仍分开分类。

验收必须覆盖：A→外部 B→拒绝 stale→重读成功；两个同 observation 的并发 writer 只允许一个提交；并发 create 不覆盖；全部 edit 匹配情形；UTF-8/目录/符号链接 jail/bounds/cancel/mode/换行；publish 前 fault 保持目标不变；审批先于 effect；restart 后强制重读；race test；并明确证明 `exec` 在保证范围之外。

Observation 不增加 prompt token；固定 `edit_file` schema 仅在 catalog 变化时影响 KV cache。完成还需要同步 implemented contract 与 evidence ledger，不需要 live model/API key。
