# DeepSeek Harness 对照与交付顺序

**状态：** 调研证据完成

**日期：** 2026-08-15

**范围：** 记录官方 DeepSeek Harness 一手来源、后续子系统门的采用/拒绝边界，以及 Engine 纵切之后的交付顺序结论。

本文是调研证据。它不改变 EventStore v2 行为，也不授权复制 DeepSeek Harness 的类型、插件或运行时。

英文版是规范性调研记录。本文是完整同步阅读版。

英文版本 [2026-08-15-deepseek-harness-and-roadmap.md](2026-08-15-deepseek-harness-and-roadmap.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。


## 问题

1. 在 Engine 纵切和生产 Runtime 设计之后，Open Code Harness 的产品目标是否仍然正确？
2. EventStore v2 是否仍应作为当前实现 Slice？
3. 官方 DeepSeek Harness 开源后，哪些对照来源是一手证据，哪些只是非权威上下文？
4. 后续 Architecture Gate 可以采纳 DeepSeek Harness 的哪些思想，哪些与章程冲突？
5. EventStore v2 Slice 1 之后，应继续做完剩余 Runtime Slice，还是回到产品能力？

## 产品目标结论

章程目标仍然正确：构建模型中立、界面中立、事件驱动的 Code Agent Engine；Session/Turn 可恢复；各子系统可独立验证；公开客户端边界是 ACP；每个已完成纵切都保持工业级质量。

Engine 纵切的准确定位仍是 Minimal Executable Turn Runner，不是带工具的 Agent Loop。EventStore v2 是对该纵切的合理合同修正：v1 把任何非空 Append Error 都当成确定未提交，并且 `Load` 拉回整条 Stream。这些假设对生产数据库过强或无界。

这次修正不替换产品路线图。章程中的下一产品能力仍是 Provider 与 Tool/Policy。Persistence Slice 1 的存在，是为了让后续 Adapter 不再继承含糊的 Store 合同。

## 必选对照集

后续子系统 Architecture Gate 必须重新核验当时仍公开、且与该 Slice 直接相关的官方来源：

| 来源 | 角色 | 一手入口 |
| --- | --- | --- |
| [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) | DeepSeek 官方 Harness；2026-08-13 之后的一手证据 | [architecture](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/architecture.md)、[session](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/subsystems/session.md)、[persistence](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/subsystems/persistence.md)、[tool pipeline](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/tool-execution-pipeline.md) |
| [Pi agent core](https://github.com/badlogic/pi-mono/tree/main/packages/agent) | 小型可注入循环与取消 | 既有 Engine / Runtime 门 |
| [Kimi Code](https://github.com/MoonshotAI/kimi-code) | 包拆分、Transcript 顺序、客户端/服务端分离 | 既有 Engine / Runtime 门 |
| [Grok Build](https://github.com/xai-org/grok-build) | Composition-root 拆分、ACP stdio、Headless 对等 | 既有 Runtime 门 |
| [OpenAI Codex](https://github.com/openai/codex) | 显式 Item 生命周期、有界队列、thread-store 权威 | 既有 Engine / Runtime 门 |
| [Maka](https://github.com/maka-agent/maka-agent) | 单一执行权威；事实与投影分离 | 既有 Engine / Runtime 门 |

[DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix) 仍是社区、非权威上下文，只用于 Provider 专用缓存与路由启发式。它不能替代官方 DeepSeek Harness。

未公开的实现细节记为 Unknown。产品营销页、非官方镜像和插件生态仓库不是一手证据。

## DeepSeek Harness 观察

2026-08-15 基于官方仓库
[deepseek-ai/deepseek-harness](https://github.com/deepseek-ai/deepseek-harness)
（developer preview，MIT，TypeScript/Cordis，“Everything is a Plugin”）。

| 已观察合同 | 后续采用 | 边界 |
| --- | --- | --- |
| Session Log 是模型可见上下文的来源。`deriveMessages()` 从日志投影历史。运行时不变量要求：任何进入模型请求的内容都必须能从日志重建。 | Context Engine 与 Provider 门：进模型即已入日志。上下文是投影，不是第二份可变 Transcript。 | 不把他们的内存 `Session` 对象当作本项目 Domain Aggregate。 |
| 事件分为 Surface 类型（`user/message`、`assistant/message`、`tool/result`）与 Log-only 事实。未识别且必选的类型 Fail Closed，除非标记 `ignorable`。 | 未知 Schema 继续 Fail Closed。区分模型可见事实与可回放的审计/运行时事实。 | 不复制他们的事件类型名或 `SessionEventMap` 插件合并。 |
| 冷启动遇到未关闭的 `turn/start` 时追加 `turn/end { reason: interrupted }`，不截断已持久化的 Step。活 Session 不会被合成中断。 | 与 `process_crash`、不确定时不静默重放 Model/Tool 副作用一致。 | 他们在同步内存 Append 之后异步合批 Flush，不能作为本项目提交权威。终端事实先提交，再投递。 |
| Step 是一次模型请求加上它调用的工具；Turn 是零个或多个 Step。 | 引入 Tool Runtime 时采用这套分层。当前 Engine 纵切仍是一次模型尝试。 | 不要把当前 Item/Turn 状态机硬塞成假的工具循环。 |
| `tool/call` 先入日志，再走 `pre-execute → approval → guards → execute → post-execute → tool/result`。 | Tool/Policy 门：内置工具与 MCP 工具走同一条可审计管线。 | 不把 Policy 做成 Cordis 瀑布里一串 `next()` 监听器。 |
| Capability Seam 包含 Service Definition、Provider 与 Consumer。替换 FS/Subprocess 时，Bash、PTY、LSP 一起走。 | 继续用消费方拥有的端口承载可替换 Adapter。 | “一切皆插件”不是 Go 核心架构。 |
| 每次模型请求记录 `request/header`（配置、System Prompt、Tool Schema），使一次请求成为 Log 的纯函数。 | Provider/Context 门：持久化该次 Attempt 实际使用的请求信封。 | 不记录原始秘密。脱敏导出是另一条命令。 |
| Persistence Backend 实现同一 `SessionPersistence` Seam。Format Unsupported 与 Corruption 分开。压缩替换 Surface 节点，不改写既有事实。 | 保持可重建投影、显式格式拒绝，以及压缩作为新事实。 | JSONL 不是第二个在线权威。已接受的 Runtime 设计仍以 SQLite 为唯一提交权威。 |
| 生成式 Persistence/Event Catalog、双语文档、包级运行时不变量。 | 继续机械文档与证据纪律。 | 不把他们的逐文件 100% 覆盖率规则，替代合同、竞态和故障注入证据。 |

## 拒绝的 DeepSeek Harness 形态

这些与章程或已接受设计冲突：

1. 以 Cordis 和“Everything is a Plugin”作为 Engine 内核。Adapter 可替换；Domain、Application 权威和 Store 合同不是可卸载插件。
2. 以 TypeScript/Node 作为 Engine 核心。核心保持纯 Go、无 CGO。
3. 以 Web UI 为一等产品面，把 ACP 降为 automation-only。公开客户端边界仍是 ACP v1；TUI 是 ACP Client。
4. 先内存 Append，再异步持久化。在线事实以提交为准。
5. JSONL 与 SQLite 作为平权的在线权威。
6. 自修改运行时、Claude/Codex Hook 桥，以及用通用事件总线拦截 Loop。
7. 复制 DeepSeek 类型名、插件 ID 或生态打包方式。

## 交付顺序

已接受的 Runtime 设计仍然列出六个持久化/客户端 Slice。Slice 1（EventStore v2）必须在 SQLite Adapter 之前完成。完成 Slice 1 并不强制立刻实现 Slice 2–6。

本次调研后的建议顺序：

1. 先收口 EventStore v2 Slice 1，包括 Unknown-Outcome Resolution 与删除全部 v1 Surface。不要让迁移停在半切开状态。
2. 不要只因为 Runtime 拆分里写着下一步，就立刻开始 SQLite、JSONL 审计、Runtime Host、ACP 或 TUI。
3. 下一份产品设计是 Provider 与最小 Tool/Policy Loop。这些门必须把官方 DeepSeek Harness 列入一手来源。
4. SQLite、恢复、ACP 和 TUI 在带工具的 Loop 合同稳定后再继续，或在后续 Gate 证明它们正在阻塞该 Loop 时再提前。

若不做这个停顿，仓库会变成一个很强的 Event Store，而不是能干活的 Code Agent。

## 结论

### F1. 产品目标不变

工业级、模型中立、协议对齐的 Harness。不是 Demo，不是厂商薄封装，也不是插件宿主。

### F2. EventStore v2 仍是当前打开的实现 Slice

它是已接受 Runtime 设计要求的 Breaking Contract Migration。它不是新的产品里程碑，也不是重写 Engine 纵切的理由。

### F3. 官方 DeepSeek Harness 替代 Reasonix 成为 DeepSeek 证据

Reasonix 只保留社区上下文。后续 Gate 引用 `deepseek-ai/deepseek-harness` 的文档与源码，而不是插件画廊。

### F4. 采用日志可重建、未知事件 Fail Closed、崩溃补终态、Step/Turn 分层和显式工具管线

它们会增强 Provider、Tool/Policy、Context 和后续 Persistence Slice，但不改变 Slice 1。

### F5. 拒绝插件内核、Web 优先、异步 Flush 权威和双在线存储

那些会推翻章程和已接受的 Runtime 设计。

### F6. Slice 1 之后回到 Provider 与 Tool/Policy

Runtime Slice 2–6 保持“已设计”，不自动成为下一步。

## 证据限制

- DeepSeek Harness 处于 developer preview，并声明会破兼容。后续 Gate 必须重读当时的文档。
- 公开文档和源码只说明可观察合同。未发布不变量记为 Unknown。
- 本门不实现 Provider、Tool、Context、SQLite、ACP 或 TUI。
- 参考项目不是依赖，也不捐赠类型名。
