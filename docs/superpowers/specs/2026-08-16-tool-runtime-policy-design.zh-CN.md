# Tool Runtime、Policy Engine 与最小工作区工具

- **状态：** 已评审设计（英文规范为权威）
- **日期：** 2026-08-16
- **规范原文：** [Tool Runtime, Policy Engine, and Minimal Workspace Tools](2026-08-16-tool-runtime-policy-design.md)
- **依赖：** 领域事件、Engine 纵切、EventStore v2、Provider + `openaicompat`（PR #7 / #12 / #13）
- **本 Slice 不做：** SQLite、JSONL、Runtime Host、ACP、TUI、完整 MCP 客户端、Seatbelt/bwrap、插件注册表、并行工具、Context Engine

英文稿是规范性来源。本文是完整同步阅读版：覆盖决策、行业对照、管线、工具合同和 PR Plan。codec 键列表、SSE 组装算法、重建状态机的逐转移表以英文稿为准。

---

## 1. 这段要做什么

Open Code Harness 已经能准入一次 Assistant Turn、打一次模型、把终态写进 EventStore v2。流语法仍是 `text_delta* → completed`。没有工具事件。一次 `RunTurn` 就是一次模型尝试。

本里程碑把一次已准入的 Turn 变成有界的 **Step 循环**（`model → tool* → model`），同时加上独立的 Policy Engine 和四个内置工作区工具。这不是插件宿主：Application 拥有循环和章程 §6.4 管线；Engine 仍是一次 `Stream`；Policy 是纯函数；工具走消费方拥有的端口。

---

## 2. 关键决策

| ID | 决策 | 理由 |
| --- | --- | --- |
| T-01 | Application 拥有 Step 循环。`TurnRunner` 仍是一次 `Stream` / 一次尝试 | EventStore v2「Application 不重试」；Engine 不能去 import policy / tools / Store |
| T-02 | Step = 一次模型 + 它产生的工具；Turn = 有上限的多个 Step | 采用官方 DeepSeek Harness 分层。Turn 在 Step 之间保持 `running` |
| T-03 | 一个 Request ID 只准入一次。只有活的 lease 所有者可以再 `Stream`，而且必须等上一 Step 的工具都有终态、并已提交新的 `model.request.recorded`。第二次调用永不打模型。崩溃中途是 `reconciliation_required` | 同一准入不能静默开始第二次模型调用，循环仍可从日志重建 |
| T-04 | EventStore v2 接口不变。工具/策略/审批是新的 schemaVersion-1 事件 | 不需要第五个 Store 方法 |
| T-05 | `tool.call.started` 先提交，再校验失败上报、等审批或执行。非法参数也是 started + failed | DSH：先记 `tool/call` 再执行。没有持久化意图就不能有副作用 |
| T-06 | Policy 是 `Decide(Input) Decision` 纯函数。不是 `next()` 瀑布，也不写在 `read_file` / `exec` 里 | 章程 §6.4；拒绝 DSH 的 listener 瀑布 |
| T-07 | 默认：工作区内 `read_file` / `list_dir` 允许；`write_file` 和 `exec` 要审批；越权路径和网络拒绝。没有 Approver ⇒ 拒绝。`ModeAllowWrites` **进 package**，生产默认仍是 `ModeDefault` | 最小权限。测试 / 无头 CI 可显式选 AllowWrites；exec 仍要审批 |
| T-08 | 第一批：`read_file`、`write_file`、`list_dir`、`exec`。`list_dir` 可选 `depth`（省略 ≡ 1，最大 2，整次 256 条） | 能看树、改文件、跑 `go test`。`depth` 1 就是普通 `ls` |
| T-09 | 工具按模型顺序串行 | Compact Session 最多一个 Active Item。并行要另开设计 |
| T-10 | 流语法 `text_delta* tool_call* completed`。`completed` **事件** 空 Text。`RunResult` 可以同时有文本和 `ToolCalls`。唯一性在 **call id**，不在名字 | Chat Completions 经常边说边调工具。同 Step 两次 `read_file` 合法 |
| T-11 | 非空目录要求 `RequestIdentity != nil` 且 `NativeTools` 为 `supported`/`required`。空目录：`Messages`/`Tools` 仍为 nil，现有 scripted 测试不变 | Engine 不看 profile。组合期才能知道 `unsupported` |
| T-12 | 执行器走端口（`FileSystem` / `CommandRunner` / `Approver`）。静态目录。没有插件注册表 | 「一切皆插件」只取可替换适配器，不取 Cordis 内核 |
| T-13 | Compact `Session` 仍然没有 transcript。活所有者在内存里投影本轮已提交事件 | 进模型即可从日志重建，但不复制 DSH 的内存 Session |
| T-14 | 第一版 exec 沙箱是 `enforcement=partial`：工作区 cwd、清洗环境、超时、输出帽、进程组杀掉。OS 隔离是以后同一端口上的适配器 | 能限制伤害，并诚实承认挡不住 exec 里的 curl |
| T-15 | 审批 **不是** 写侧 Item 种类。`approval.requested` / `resolved` 只加 Version。人机是注入的 `Approver`。默认拒绝 | 保持「最多一个 Active Item」 |
| T-16 | `DigestRunTurnRequestV1` 仍是 Session ID + Input。换目录/策略/模型要新 Request ID | 与 Provider 的 P-17 一致 |
| T-17 | 整个 Turn 共用一个 `CommandID`。每个 assistant Step 和每次工具调用新 `ItemID` | 重建仍是「这个 CommandID 的全部记录」 |
| T-18 | JSON Schema 用 stdlib 封闭子集。不引 schema 库 | `go.mod` 保持 stdlib-only |
| T-19 | 循环中途 append 用 `step_append_in_flight` / `step_append_unknown`，可以回到 `running`。未知 ⇒ 对保留的 exact intent 做 `ResolveAppend`。没有已提交的 `started` 就不执行；终态未知就不重做。预算耗尽 ⇒ `append_outcome_unknown`，零 `Stream`、零 execute | 今天的 lease 只能处理准入和一次 Turn 终态 |
| T-20 | 模型可见结果 / `read_file` / `exec` 输出 **64 KiB**。Step k≥2 记 **后缀** 信封。投影帽 **4 MiB**。带工具的 HTTP `MaxRequestBytes ≥ 5 MiB`。超投影 ⇒ `envelope_limit`，不打模型 | 否则会撑破 8 MiB 事件载荷和默认 1 MiB HTTP 体 |

取消/失败时工具 Item 仍在跑：一次 Domain 组合命令 `InterruptToolTurn` / `FailToolTurn`（工具终态 + Turn 终态，审批等待中取消还可带 `approval.resolved`）。裸 `InterruptTurn` 在 `ActiveItem != nil` 时仍非法。审批拒绝/超时：Turn 继续（`FailToolCall`）。ctx 取消：`InterruptToolTurn`，不是继续循环的 `tool.call.failed`。

---

## 3. 行业对照（采用 / 边界）

| 来源 | 采用 | 明确拒绝 |
| --- | --- | --- |
| **DeepSeek Harness** | 先记调用再执行；显式有序管线；Step/Turn 分层；没有审批 ⇒ 拒绝；沙箱是端口并报告 `full\|partial` | Cordis / `next()` 瀑布；TypeScript 插件内核；异步 flush 当提交权威；复制他们的事件名 |
| **Pi** | Step = 模型 + 工具；测试双体走同一端口；本 Slice 用更安全的串行 | 内存消息列表当权威；并行优先；hook 当 Policy |
| **Kimi Code** | 内置和以后的 MCP 同一执行合同；工作区/策略可否决工具体 | DI × Scope；本 Slice 不做并行调度 |
| **Grok Build** | deny 赢；工作区内读默认允许；策略决策 ≠ 沙箱执行 | 策略默认 sandbox=off；hook 总线；yolo / always-approve |
| **Codex** | 策略 ≠ sandbox；工具 Item 生命周期对齐助手 Item | 不实现 execpolicy DSL / PTY |
| **Maka** | 单一执行权威 = Application；事实 vs 投影；客户端不能执行工具 | 不实现 Runtime Host / Agent Graph |

DeepSeek-Reasonix 仍只是社区上下文。

---

## 4. 管线与工具合同

章程顺序（已锁）：

```text
schema 校验
  → 词法 scope（无 I/O；`..` / NUL / 绝对路径前缀不对 ⇒ scope_denied）
  → Resolve（I/O 探测；失败 ⇒ scope_denied）
  → policy.Decide(WorkspaceIn=已解析结果)
  → 审批
  → 只有 allow/granted 之后才 Read/Write/Run
```

`Resolve` 是 scope 探针，不是执行。词法失败永不调用 `Resolve`。符号链接逃逸会 `Resolve`，然后 `Decide(WorkspaceIn=false)`，永不 `Read`/`Write`。

### 四个内置工具

| | `read_file` | `write_file` | `list_dir` | `exec` |
| --- | --- | --- | --- | --- |
| 必填 | `path` | `path`, `content` | `path` | `argv`（最少 1 项，无 shell） |
| 可选 | — | — | `depth` 1–2，省略 ≡ 1 | `cwd`，省略 = 工作区根 |
| 成功可见 | UTF-8 文件；超 64 KiB 加 `\n[truncated]` | `wrote <n> bytes`（不含路径） | 相对路径一行一个；`depth` 1 只列本层，`2` 再下一层；整次 256 条 | 先 `exit <code>\n` 再 stdout/stderr，合计 ≤ 64 KiB |
| 模型不可设 | — | — | `depth` 0 或 >2 ⇒ `invalid_args` | timeout 是配置，不是模型字段 |

`exec` 环境：空环境 + 父进程 `PATH` + 工作区下的 `HOME`/`TMPDIR`。没有 `AWS_*` / token / proxy。

`n > 8` 次 tool call：整次模型尝试 `invalid_stream`，不执行前缀，不写 `tool.call.started`。

拒绝/失败给模型看的句子是冻结的安全短句，不含路径、参数、环境。截断标记是 `\n[truncated]`。

---

## 5. 重建与 lease

重建是对同一 `CommandID` 子序列做 **Apply 等价** 的状态机：`admit_turn` → `open_assistant` → `idle_in_turn` → `open_tool` → `terminal`。非法顺序 ⇒ `store_corrupt`。旧的 2/3/4/5/6 形状仍是合法特例。

中途 append：`step_append_in_flight` / `step_append_unknown`。解析成功后用 `resumeAfterResolvedStepAppend` 回到 `running`（和准入 resume 同一套 `retained` / `ownerActive`）。没有 `step_append_unknown → terminal_append_in_flight`。

没有已提交的 `tool.call.started` 就不调用 `FileSystem` / `CommandRunner`。工具终态已提交或未知时不重做。

---

## 6. PR Plan（中文）

每一 PR 都可独立评审合入。5b 之前 `main` 仍是一次模型尝试。

| PR | 标题 | 依赖 | 做什么 |
| --- | --- | --- | --- |
| **1** | 领域：工具/策略/审批事件；助手完成拆成 Item-only | 无 | `InterruptToolTurn` / `FailToolTurn`；codec 允许键/必填键拆分；`ItemKindToolCall`；`FinishReason=tool_calls`；memory `buildBatch` 认 `tool.call.started`。还没有 Application 循环 |
| **2** | Engine：`tool_call` 语法；`Messages`/`Tools` | PR 1 | `text_delta* tool_call* completed`；Application 仍忽略 `ToolCalls` |
| **3** | `policy` 纯 Decide + 默认表 | PR 1 | 含已交付的 `ModeAllowWrites`；`NewService` 默认仍是 `ModeDefault` |
| **4** | `tools` 目录、schema、词法 scope、端口 | PR 1（**不**依赖 PR 3） | 四个锁定 schema（含 `list_dir.depth`）；不 import `policy` / `os` |
| **5a** | 重建状态机 | PR 1 | 替换仅 2–6 的表；不打第二次模型、不执行工具 |
| **5b** | Step 循环 + 管线 + lease/unknown | 2 + 3 + 4 + 5a | 真正开始 `model → tool* → model` |
| **6** | `workspacefs` / `localexec` | PR 4 | `t.TempDir()`；`enforcement=partial` |
| **7** | `openaicompat` 组装 `tool_calls` | 适配器测试靠 PR 2；`RunTurn` e2e 靠 5b+6 | NativeTools supported 时发 tools；HTTP ≥ 5 MiB |
| **8** | 已实现合同 + 证据 | 5b–7 | `docs/architecture/tool-runtime.md` 及 zh-CN |

建议合入顺序：`1 → (2 ∥ 3 ∥ 5a) → 4 → 5b`；6 可与 5a/5b 并行；8 最后。

---

## 7. 已关闭的开放问题

1. **`ModeAllowWrites`：** 进 `policy` 包。生产默认 `ModeDefault`。测试和无头 CI 可显式打开。
2. **`exec` argv vs shell 字符串：** 锁死 argv。以后若要 shell，另做 `exec_shell`，不偷偷改 `exec`。
3. **`list_dir` 递归：** 本 Slice 做。`depth` 1–2，省略 = 1，整次 256 条。
4. **Digest 是否含 tools：** 不。换目录要新 Request ID。

---

## 8. 权威说明

- 规范性设计：英文稿 `2026-08-16-tool-runtime-policy-design.md`
- 本文：中文阅读版；若与英文稿冲突，以英文稿为准
- 行业对照证据：`docs/research/architecture-gates/2026-08-15-deepseek-harness-and-roadmap.zh-CN.md`
- 章程：`docs/superpowers/specs/2026-08-11-open-code-harness-architecture-design.md` §6.4
