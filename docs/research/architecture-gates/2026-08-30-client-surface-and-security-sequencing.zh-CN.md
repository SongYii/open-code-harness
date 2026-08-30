# 客户端界面复用与切片 B 之后的交付顺序决策

**状态：** 调研证据完成

**日期：** 2026-08-30

**范围：** 记录"是否复用外部产品的 Web UI 作为本项目 ACP 客户端"这一决策，以及在 ACP 会话生命周期（切片 B）之后被采纳的交付顺序。起因是讨论 DeepSeek Harness 的 Web UI 与其 agent 轨迹展示。

英文版本 [2026-08-30-client-surface-and-security-sequencing.md](2026-08-30-client-surface-and-security-sequencing.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。

本文是一次对话中达成的顺序决策，不是新的一手来源对照：只引用了既有的 2026-08-15 DeepSeek Harness 调研门，本文写作过程中没有重新抓取或核验任何外部仓库。本文不实现任何东西，不授权复制 DeepSeek Harness 的 UI 代码，也不是实现 exec 沙箱、资源配额或 TUI/Web ACP 客户端之前所需要的那份 architecture gate 本身——按 `docs/README.md` 文档规则第 7 条，这几个子系统仍然是"已接受但未设计"的边界，各自都需要一份重新核验当时最新一手来源的门文档，才能进入实施计划。

## 问题

考虑到 DeepSeek Harness 的 agent 轨迹展示效果很好，Open Code Harness 是否应该直接复用它的 Web UI 作为客户端界面，以省去自己做 UI 的精力？

## 结论：这重复了一个已经被拒绝过的形态

2026-08-15 的 DeepSeek Harness 调研门
（[`2026-08-15-deepseek-harness-and-roadmap.md`](2026-08-15-deepseek-harness-and-roadmap.md)）
已经评估并明确拒绝过这个形态，即"Rejected shape 3"：

> Web UI 作为主产品面，ACP 被降级为仅自动化接口。ACP v1 仍然是公开客户端边界；TUI 是一个 ACP 客户端。

据那次调研的观察，DeepSeek Harness 自己的架构就是"Web UI 是主面，ACP 只是次要的自动化接口"——这和本项目章程恰好相反：我们把 ACP 定死为唯一的公开客户端边界，正是为了保持 harness 的模型中立、界面中立（见 `docs/README.md` 里程碑 6 与 ACP v1 设计）。如果直接复用 DeepSeek Harness 的 Web 前端，只有两条路：

1. 让 harness 在这个 UI 背后去说 DeepSeek Harness 自己的协议——这正好重新落回被拒绝的那个形态；
2. Fork 并改写 DeepSeek Harness 的前端，把它的数据层换成消费 ACP JSON-RPC——这是真实的、持续的集成工程，而且要跟随一个 DeepSeek Harness 自己都标注为"developer preview、不保证兼容性"的上游，不是省事，是换了一种开销。

这两条路都不是"用他们的 UI 而不是自己做"，而是需要新建并长期维护的集成面。

## 决策

1. 不集成也不 Fork DeepSeek Harness 的 Web UI。本项目做的任何客户端都必须是 ACP 客户端——这是重申 2026-08-15 的既有决策，不是重新打开这个讨论。
2. DeepSeek Harness 轨迹视图之所以好看，靠的是"log 可重建、按 turn/step 分层、显式工具管线"这些性质——这些在 2026-08-15 那次调研的 Adopt 一栏里已经原则性采纳过，并且已经体现在本项目自己的转录与实时投影设计里：`session-transcript` JSONL 投影、ACP 的 `session/update` 通知、`policy.decision.recorded` 审计事实。未来的客户端应该基于这些已有的数据面渲染轨迹，而不是照搬外部的数据模型。
3. 切片 B 之后的优先级是**可用且安全**。这说的是紧迫性，不是实施顺序：应该先把当前明确"未强制"的安全边界补上，再去投入一个会扩大接触面的客户端。具体说：`SECURITY.md` 里列为"Not enforced"的 exec 沙箱和资源配额，排在最小 ACP 客户端之前；客户端排在更完整的 UI 打磨或第二个客户端界面之前。
4. 截至本决策，安全加固子系统和最小 ACP 客户端都仍是"已接受但未设计"的边界（见 `docs/README.md` 里程碑 6–7）。两者各自都需要独立的 architecture gate 才能进入实施计划：沙箱那一项要重新核验 Codex、Grok Build、Kimi Code 等参考项目**现在**（不是 2026-08 的旧快照）是怎么约束工具执行的；客户端那一项要先确认公开 ACP 生态里是否已经存在真正说 ACP 协议、值得参考的客户端实现，因为 ACP 是一个已发布的协议、有自己的生态，不等于 DeepSeek Harness 的 Web App。本文不能替代这两份门文档中的任何一份。

## 交付顺序

1. Architecture gate、设计、实现：exec 沙箱与资源配额。扩展 `internal/harness/adapters/localexec`；`SECURITY.md` 里的"Not enforced"清单就是这项工作要关闭或明确收窄的验收标准。
2. Architecture gate、设计、实现：一个最小的、原生说 ACP 协议的客户端，足以发送 prompt，并能从 `session/update` 通知和 `och export-session` 的输出渲染出轨迹视图。
3. 更完整的 UI 投入、MCP 客户端适配器、评测/基准测试，以及 `docs/README.md` 里所有仍标"未设计"的里程碑，都排在第 1、2 步之后，除非后续某份 gate 证明其中某一项正在阻塞已经在推进的工作。

## 证据边界

- 这是一次对话中记录下来的顺序决策，不是一手来源对照；它本身不授权直接开始实现 exec 沙箱或 ACP 客户端。
- 这里说的"轨迹"，指的是本项目自己的转录与实时投影设计已经能从规范事件日志投影出的 turn/step/工具调用时间线，不是 DeepSeek Harness 的任何 UI 组件。
- 按 2026-08-15 那份调研门的说法，DeepSeek Harness 仍是 developer preview；未来若有 gate 重新评估它，必须重新读取其当时的最新状态，而不是沿用本文的描述。
