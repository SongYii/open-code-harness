# 最小 ACP 原生客户端设计（中文摘要）

- **日期：** 2026-08-30
- **状态：** 2026-08-30 已批准。复审时问到这次的几个"缩小范围"决定（拒绝能力、只覆盖部分 `sessionUpdate` 变体、自己写帧格式）会不会背离 ACP 这个事实行业标准——结论是都不会：拒绝能力和优雅处理不认识的变体正是协议自己设计出来的扩展机制，不是绕开它；自己写帧格式只是实现策略，跟线上字节兼不兼容无关。复审真正问出来的一个实在问题，本来就是这次设计的第一目标，不是新发现的缺口：现有 agent 侧适配器到今天为止只被本仓库自己的脚本化夹具跑过，从来没有真的对上过一个独立客户端，所以它到底合不合规范其实一直没验证过——这次接受把"验证这件事"本身当作本切片的验收标准（见第 7 节的真实集成测试），而不是继续放着不提。
- **稳定性：** 全新的面，不改动任何既有的 `experimental`/pre-GA 合同

英文版本 [2026-08-30-acp-native-client-design.md](2026-08-30-acp-native-client-design.md) 是规范文本；本文是摘要性的中文阅读版，不是逐字翻译。两者若有分歧，以英文为准。

**权威依据：** [客户端界面复用与安全加固顺序决策](../../research/architecture-gates/2026-08-30-client-surface-and-security-sequencing.zh-CN.md)（其顺序里的第 2 步）；[ACP 原生客户端架构调研门](../../research/architecture-gates/2026-08-30-acp-native-client.md)

## 决策摘要

本项目的 ACP v1 适配器（`internal/harness/adapters/acp`）到现在为止只被自己的脚本化测试夹具跑过，从来没有一个真正独立的**客户端**进程驱动过它——那个负责通过 stdio 拉起 agent 子进程、发 `session/prompt`、把收到的一串 `session/update` 渲染成人能看的东西的角色，这个仓库里完全不存在。

本设计新增这样一个角色：一个小的、独立的 Go 程序，讲 ACP v1，对着本项目自己的 agent（`och -acp`，或者任何其他 ACP v1 agent，因为协议本身跟具体项目无关）跑，在终端里实时渲染一次 prompt 的执行轨迹，并且交互式地回答 `session/request_permission`。它刻意做得很窄——是顺序决策第 2 步要的那个"最小"客户端，不是 `docs/README.md` 里已经单独列为里程碑 7、还没写设计的那个更完整的"TypeScript TUI client"。

四个架构决策，每一个都直接回答调研门留给这次设计的一个开放问题（文末有逐条对应）：

1. **自己实现线协议帧格式，不依赖外部 SDK。** 2026-08-22 那份调研门已经为 agent 侧做过这个决定（"帧格式这份合同小到可以自己拥有"）；这次为客户端侧做同样的决定，理由一样，也让本项目现在唯一的非测试依赖（`modernc.org/sqlite`）保持不变。
2. **只从实时 `session/update` 渲染；恢复会话时复用 `session/load`；不新增一条解析 `och export-session` JSONL 的历史通路。** 那份 JSONL 投影和这个实时客户端解决的是两个不同问题（持久审计/导出 vs. 交互式会话），而且本项目自己的 agent 已经通过 `session/load` 重放历史——客户端再自己解析一遍 JSONL 只是重复造一条本来就有的路。
3. **在 `initialize` 时彻底拒绝 `fs` 和 `terminal` 这两个客户端能力。** 本项目自己的 agent 已经把工作区文件系统和 `exec` 当作自己的、被限制住的工具（见 `tool-runtime.md`）——而且直接读了 `internal/harness/adapters/acp/protocol.go` 的 `initializeParams`，确认现在的 agent 根本不解析、也不使用客户端发来的任何能力声明，所以声明拒绝这两项既没有兼容性代价，现在也用不上。
4. **这一版只做纯文本、按行输出的终端界面，不引入 TUI 框架依赖。** 跟 `acp-go-sdk` 自己的示例客户端一个路数，把"要投入多少终端 UI"这个问题留给顺序决策自己说的"这个客户端做出来之后再说"（第 3 步），也让这次交付更像一个"协议对不对"的验证物，而不是一个 UI 作品。

## 范围

**做：** 一个新的 Go 包 `internal/client/acp`（线协议客户端：传输、会话状态、reducer）加一个新二进制 `cmd/acp-client`（flag 解析、终端 I/O、主循环），放在 `internal/harness/` 之外（它不是 harness 的适配器，也不实现任何 `tools.*` 端口）；`initialize` → 选 `session/new` 或 `session/load` → prompt 循环 → 渲染 `session/update` → 回答 `session/request_permission` → `session/cancel`/退出 的完整走线；一个按 `toolCallId` 归并状态的 reducer，只覆盖本项目 agent 实际会发的四种 `sessionUpdate`（`user_message_chunk`、`agent_message_chunk`、`tool_call`、`tool_call_update`），遇到不认识的变体降级成一行标注过的原始输出而不是崩溃或丢弃；权限请求按本项目 agent 的真实形状（永远两个选项 `allow-once`/`reject-once`）交互式地在终端问一句 y/n 来回答，同时留一个通用的"列出选项编号"兜底，不是把两选项写死成唯一能处理的形状。

**不做：** 里程碑 7 那个更完整的 TypeScript TUI（本设计交付的是一个更小的、Go 原生的东西，里程碑 7 可以参考它，也可以完全不理它）；对任意 ACP agent 的通用兼容性声明（这次是照着本项目自己 agent 的真实行为写、测的）；线协议级别的调试日志（类似 Zed 独立的 `acp_tools`，本项目自己的转录/审计面已经够用，以后要加也不影响这次的架构）；`fs`/`terminal` 客户端能力的任何实现；多会话、多 agent、分屏之类的 UI；消费 `och export-session` 的 JSONL（那是一个独立、已实现的离线/审计工具，这次不碰）；任何新的非测试依赖。

## 验证要点

reducer 的单元测试（四种变体单独和组合、不认识的变体走兜底路径）；线协议客户端对着一个进程内伪造的 agent（不是真子进程）做单元测试，覆盖设计里那张时序图，包括恢复会话时先重放 `session/update` 再收到 `session/load` 响应这个顺序；权限循环的单元测试，覆盖真实的两选项形状、通用兜底形状、以及"回答还没给就 EOF"时按拒绝处理（fail-closed，不是挂住）；一个真实的、按条件跳过的集成测试——真的拉起编译好的 `och -acp` 二进制，跑一次完整的新会话 prompt（包含一次真实需要审批的 `write_file`），断言 turn 能正常跑完——这是"agent 和一个真正独立的客户端能互通"这条核心目标的实际验收证据，不是纸面断言；`go vet`、`gofmt`、`CGO_ENABLED=0 go build ./...`、`go test -race ./...`，以及确认 `internal/harness/architecture` 现有的边界测试没有因为这次改动而报出新的导入关系。

## 主要风险

未来协议或 agent 侧变化（新的 `sessionUpdate` 变体、不一样的权限选项形状）悄悄弄坏渲染——靠上面说的两个兜底路径缓解，不是假设今天的形状永远不变；终端渲染假设（原地刷新状态行）在非 TTY（管道输出、CI）下表现不对——启动时只判断一次 TTY，非 TTY 走没有终端控制序列依赖的纯追加输出路径；这个客户端自己的 bug 被误判成 ACP v1 适配器的回归（反之亦然），因为两边都还年轻——靠伪造 agent 的单元测试把"线协议/reducer 对不对"和"真 agent 互通对不对"分成两个独立的验收点来隔离；实现过程中范围蔓延到里程碑 7 那个更完整的 TUI——不做的范围已经写死在设计里，接下来的实施计划会再钉一遍任务边界。

## 逐条对应调研门留下的开放问题

实时 vs. 回放 `och export-session` vs. 两者都要 → 只做实时，恢复靠 `session/load`；线协议调试日志要不要算进最小客户端范围 → 这次不做；`fs`/`terminal` 拒绝还是做薄代理 → 拒绝，已对着本项目 agent 的真实代码核实过；自己写传输层还是依赖 `coder/acp-go-sdk` → 自己写，跟 agent 侧当初的决定同一个理由；第一版要投入多少终端 UI → 纯文本按行输出，里程碑 7 继续独立存在。同一份调研门里后来补充的"DeepSeek Harness 前端能不能抠出来用"的结论，不影响这次任何一个决定——本设计从一开始就没打算用基于 DOM 的组件，跟那条结论无关地独立得出了同样的方向。
