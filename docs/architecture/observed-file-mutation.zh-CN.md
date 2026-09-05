# 已实现观察式文件修改合同

- 状态：已实现内部合同；pre-v0，不是 GA
- 英文规范源：[Observed-state safe file mutation](../superpowers/specs/2026-09-04-observed-file-mutation-design.md)
- 实施计划：[六任务计划](../superpowers/plans/2026-09-05-observed-file-mutation.md)
- 完成证据：[证据台账](observed-file-mutation-evidence.md)
- 英文已实现合同：[Implemented Observed File Mutation Contract](observed-file-mutation.md)

本文是英文已实现合同的同步中文语义阅读版；冲突时英文为准。所有类型、端口、错误码和
schema 在 v1 前都只属于内部合同。

## 端口和值

`tools.FileSystem` 是由消费方拥有的工作区 I/O 端口，精确方法为：

```go
Resolve(ctx context.Context, workspace, requested string) (abs string, err error)
Read(ctx context.Context, abs string, limit int) (FileRead, error)
Write(ctx context.Context, abs string, data []byte, guard MutationGuard) (MutationResult, error)
Edit(ctx context.Context, abs string, old, replacement []byte, replaceAll bool, guard MutationGuard) (MutationResult, error)
List(ctx context.Context, abs string, depth, limit int) (names []string, truncated bool, err error)
```

`FileVersion string` 可比较但对调用方不透明，不能解析。`FileRead{Data,
Truncated, Version}` 即使数据截断也报告整个目标的版本。`MutationGuard` 只可为
`{Kind: GuardCreateIfAbsent, Version: ""}` 或
`{Kind: GuardReplaceIfVersion, Version: non-empty}`；其他形状是 invalid arguments。
`MutationResult` 返回下一版本和 `MutationCreate` / `MutationUpdate`。
`MaxEditFileBytes` 精确为 `1 << 20`（1 MiB）。

`workspacefs` 在 Linux/Darwin 从目标身份和高精度元数据推导版本；其他平台实现仅为
编译。它在执行时重新 jail，并在同一 adapter 实例内按规范目标串行化。

## 五个关闭的内置 schema

原有四个顺序不变，随后追加 `edit_file`：

| 工具 | 必需 / 可选字段 | 风险与结果 |
| --- | --- | --- |
| `read_file` | `path` 字符串，1–4096 bytes | read；UTF-8 内容，64 KiB 上限和既有截断标记 |
| `write_file` | `path` 1–4096；`content` string ≤32768 bytes | write；`wrote <n> bytes`；已有文件必须已观察且版本匹配 |
| `list_dir` | `path` 1–4096；可选整数 `depth` 1–2，默认 1 | read；最多 256 条 |
| `exec` | `argv` 为 1–64 个、每项 ≤4096 的字符串；可选 `cwd` 1–4096 | exec；由单独 CommandRunner 合同处理 |
| `edit_file` | `path` 1–4096、非空 `old_string` ≤32768、`new_string` ≤32768；可选 JSON boolean `replace_all`，默认 false | write；`edited file` 或 `replaced all occurrences` |

所有 schema 都是 `additionalProperties:false` 的对象；模型参数不能提供版本或 guard。
boolean 只可作叶子，因此根 boolean schema 仍非法。`old_string` 不得等于
`new_string`；匹配是字面、非重叠匹配：零处或未指定 `replace_all` 时多处均失败。
adapter 负责 1 MiB 整文件编辑上限、UTF-8、主导 LF/CRLF 换行与已有普通文件 mode。

## 观察与授权

Application 维护同步、进程本地的表，键是 Session ID 加已解析的规范目标。状态是
`unseen`、`absent` 或 `present(version)`：

| 事件或调用 | 状态 / 结果 |
| --- | --- |
| 成功 `read_file` | 记录 `present(返回版本)` |
| 缺失的 `read_file` | 记录 `absent`；返回普通 not-found 行为 |
| unseen/absent 后 `write_file` | 用 `create_if_absent` guard |
| present(v) 后 `write_file` | 用 `replace_if_version(v)` guard |
| unseen/absent 后 `edit_file` | `fs_not_observed` |
| present(v) 后 `edit_file` | 用 `replace_if_version(v)` guard |
| 成功 write/edit | 用返回版本更新状态 |
| 失败 mutation | 保留先前状态 |

`LoadSession` 与普通后续 Turn 保留这个运行时状态。成功的 `ResumeSession`、
`CloseSession`、`DeleteSession` 清除该 Session 的状态；失败或未解决的生命周期操作不清。
新建 Service 即使面对同一 durable Session 也从 unseen 开始；观察绝不持久化或重建。

新鲜度不是授权。固定顺序为 schema validation → lexical scope（无 I/O）→ `Resolve`
probe → `policy.Engine.Decide` → 必需的 `Approver` grant → observation guard 和文件修改。
所以策略/审批拒绝不会有 edit/write 效果；`ModeAllowWrites` 可授权写，但不能取消 guard。

## 稳定恢复和隐私

完整的新 code/message 对照固定，wire 为小写：

| Code | 精确模型可见消息 |
| --- | --- |
| `fs_not_observed` | `read the file before changing it` |
| `fs_stale_version` | `file changed since it was read; re-read it and retry` |
| `edit_no_match` | `literal was not found` |
| `edit_ambiguous` | `literal appears more than once; include more context or use replace_all` |
| `fs_is_directory` | `target is not a regular file` |
| `fs_not_text` | `file is not valid UTF-8 text` |
| `fs_too_large` | `file exceeds the edit size limit` |

映射只输出这些有界字面量与 code；路径、内容、不透明版本和 adapter cause 不会进入工具结果、
records、runtime events、model messages 或 approval requests。

## 发布与范围边界

guarded mutation 在 target lock 下重新 jail/识别并验证 guard；edit 随后读取有界 UTF-8，
并在匹配前检查 guard、计算替换内容。它在目标同目录的私有 0700 staging 目录中创建
独占 0600 文件，写全、sync、设 mode、close、再重新 jail/revalidate；create 用不替换的
link 发布，replace 用原子 rename。之后尽力 sync parent 并清 staging。不会原地截断目标；
发布前失败保留旧内容。即使发布后清理/metadata 报错，发布仍是 commit point。

这不是文件系统事务：它只串行化共享同一 `workspacefs` 实例的 structured writes，并在
guarded check 侦测修改；没有 kernel/external-process CAS，也不保证 external final
check-to-rename race。`exec` 不受 guard 中介；exec 改动只会在后续结构化 read/edit 的版本
不同时时被发现。观察不持久化。此 Linux 证据主机只做 Windows/Darwin compile-only，绝不
声称 Windows runtime。保留 workspace jail，但不把它升级为 hostile-parent-path 安全声明。

本切片不需要也不声称 live model、provider API 调用或 API key；证据均来自本地、脚本化或
文件系统 fixture。
