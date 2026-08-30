# 已实现 ACP 原生客户端合同

**状态：** 已实现；尚非 GA

**稳定级别：** v1.0 之前为 `experimental`

**成熟度：** pre-v0，尚非通用可用（GA）发布

**英文规范：** [Minimal ACP-native client design](../superpowers/specs/2026-08-30-acp-native-client-design.md)

**完成证据：** [ACP 原生客户端完成证据](acp-native-client-evidence.md)

**包：** `internal/client/acp`（线协议传输、会话生命周期、执行轨迹 reducer、权限提问逻辑）；`cmd/acp-client`（二进制：flag 解析、子进程拉起、真实终端 I/O、主循环）

本文记录当前代码和测试已经强制执行的行为。它是内部 Go 合同，不是稳定的公共协议。若本文与英文版冲突，以英文 [acp-native-client.md](acp-native-client.md) 为准。

## 范围

这是 ACP v1 的**客户端**这一端：负责通过 stdio 拉起一个 agent 子进程、发 `session/prompt`、把收到的 `session/update` 渲染成实时执行轨迹、并回答 `session/request_permission`。它是[已实现 ACP v1 Adapter 合同](acp-v1.zh-CN.md)（实现的是**agent**这一端）的镜像；两者不共享代码，也不互相导入（`TestClientPackagesAreIsolatedFromInternalHarness`，`internal/harness/architecture`）。

`internal/client/acp` 不是 harness 的适配器：它不实现任何 `tools.*` 端口，完全放在 `internal/harness/` 之外——跟 `cmd/och` 自己作为组合根的消费者、而不是组合根本身的一部分是同一个道理。它也不是通用客户端：是照着本仓库自己 agent 的真实、已观察到的行为写的、测的（那个 agent 实际会发的四种 `sessionUpdate` 变体、它发送的两选项权限形状），不是从 ACP 规范里抽象推导出来的——但代码本身并没有刻意跟本仓库自己的 agent 绑死，`-agent` flag 可以指向任何 ACP v1 agent 二进制。

## 线协议传输

`internal/client/acp/wire.go` 和 `connection.go` 实现了 NDJSON 分帧的 JSON-RPC 2.0：每行一个 JSON 值、有界的帧大小上限（`TestFrameWriterRejectsAnOversizedPayload`），以及一个 `Connection`——一个后台读循环 goroutine，把入站通知和唯一会收到的入站调用（`session/request_permission`）分发给一个 `Handler` 接口（`TestConnectionDeliversSessionUpdateToHandler`、`TestConnectionAnswersRequestPermissionThroughHandler`）。遇到不认识的入站方法会立刻回一个 method-not-found，不会让 agent 一直等一个永远不会来的回应（`TestConnectionAnswersAnUnknownInboundMethodWithMethodNotFound`）。`Connection.Close` 关闭底层 reader 来唤醒卡住的读操作，并让所有未完成的调用都收到一个具名错误（`TestConnectionCloseUnblocksAPendingCall`）；它是幂等的（`TestConnectionCloseIsIdempotent`）。`Call` 的 context 取消会立刻返回，不等 agent（`TestConnectionCallReturnsPromptlyOnContextCancellation`）。这是同一套线协议格式的第二份、独立的实现——`internal/harness/adapters/acp/codec.go` 是 agent 那一侧的实现——刻意不共享代码，跟 2026-08-22 那份 ACP v1 调研门给 agent 那侧定下的"帧格式这份合同小到可以自己拥有"是同一个理由。

## 会话生命周期客户端

`internal/client/acp/client.go` 的 `Client` 包了一个 `Connection`，暴露 `Initialize`、`NewSession`、`LoadSession`、`Prompt`、`Cancel`——线上字段名直接对着 `internal/harness/adapters/acp/protocol.go` 核实过（`sessionId`、`cwd`、`prompt: [{type,text}]`、`stopReason`）。`NewClient` 自己构造 `Connection`，直接拒绝空 `Handler`（`TestNewClientRejectsANilHandler`）——因为 `session/load` 重放的 `session/update` 通知可能比 `session/load` 自己的响应还先到，绝不能出现"没地方接"的情况（`TestClientLoadSessionDeliversReplayedUpdatesBeforeReturning`）。`Initialize` 发送一个空的 `clientCapabilities` 对象——没有 `fs`、没有 `terminal` 键，明确拒绝这两项能力，已经对着本仓库自己 agent 的 `initializeParams` 核实过、它现在根本不解析任何客户端能力声明（`TestClientInitializeDeclinesFsAndTerminalCapabilities`）。`Prompt` 只等自己那次调用的响应（`TestClientPromptReturnsTheStopReason`、`TestClientPromptReturnsPromptlyOnContextCancellation`）；`Cancel` 是发了就不管的通知，跟 agent 自己的取消语义一致（正在跑的 `Prompt` 会在自己那个待处理的响应上收到 `cancelled`，`TestClientCancelDuringAnInFlightPromptUnblocksItWithCancelled`）。

## 执行轨迹 reducer

`internal/client/acp/trajectory.go` 的 `Trajectory.Apply` 把一条 `session/update` 通知归约成正好一个 `RenderEvent`，按 `toolCallId` 归并状态——纯逻辑，没有 I/O（`TestTrajectoryAppliesAgentMessageChunk`、`TestTrajectoryAppliesUserMessageChunk`、`TestTrajectoryAppliesToolCall`、`TestTrajectoryToolCallThenTwoUpdatesReachesATerminalStatus`、`TestTrajectoryInterleavedToolCallsDoNotCrossContaminate`）。只覆盖本仓库自己 agent 实际会发的四种 `sessionUpdate` 变体（对着 `internal/harness/adapters/acp/project.go` 核实过）：`user_message_chunk`/`agent_message_chunk`（文本）、`tool_call`（建条目）、`tool_call_update`（改条目）。其他任何情况——不认识的变体、或者指向一个从没建过的 `toolCallId` 的 `tool_call_update`——都会产出一个带原始数据的 `RenderAnomaly`，绝不崩溃、不静默丢弃、也不凭空建幽灵条目（`TestTrajectoryUnrecognizedVariantIsALabeledAnomalyNotAPanicOrDrop`、`TestTrajectoryToolCallUpdateForAnUnknownIDIsALabeledAnomalyNotAPhantomEntry`、`TestTrajectoryMalformedParamsIsALabeledAnomaly`）。

## 权限提问处理

`internal/client/acp/permission.go` 的 `PermissionPrompter` 通过注入的 `io.Reader`/`io.Writer`（不是直接用 `os.Stdin`/`os.Stdout`）向操作者提问回答 `session/request_permission`。本仓库自己 agent 真实、已核实的形状——正好两个选项，`allow-once`/`reject-once`——会得到一个简单的 y/n 提示（`TestDecideRealTwoOptionShapeAnswersYes`、`TestDecideRealTwoOptionShapeAnswersNo`、`TestDecideReplaysThePromptOnAnUnrecognizedYesNoAnswer`）；其他任何形状都走通用的编号选择提示（`TestDecideGenericShapeAnswersByNumber`、`TestDecideGenericShapeRePromptsOnOutOfRangeAndNonNumericInput`）。操作者输入流在等待答案时耗尽会确定性地落到一个"拒绝"风格的选项、或者最后一个选项，绝不会返回一个让 ACP 调用没人应答的错误（`TestDecideEOFWhilePendingResolvesToTheRejectOptionForTheRealShape`、`TestDecideEOFWhilePendingResolvesToTheRejectOptionForTheGenericShape`）。`HandleRequestPermission` 把选中的选项包装成 `{"outcome":{"outcome":"selected","optionId":...}}`，本仓库自己 agent 真实的结果格式（`TestHandleRequestPermissionWrapsTheChoiceInTheAgentsResultShape`）。

## `cmd/acp-client`

Flag：`-agent <path>`（必填）、`-cwd <path>`（必填）、`-resume <sessionId>`（可选；不填就是 `session/new`，填了就是 `session/load`）。一个字面量 `--` 之后的所有内容都是 agent 自己的 argv，原样传递——用的是 `flag.FlagSet` 自带的解析终止符约定，不是自己写一个多值 flag 类型（`TestRunRequiresAgentFlag`、`TestRunRequiresCwdFlag`）。agent 的 stderr 原样透传到这个客户端自己的 stderr，跟 `cmd/och -acp` 一贯"stderr 只放诊断信息"的做法一致。

操作者的 stdin 上只有**一个**共享的 `*bufio.Reader`，同时服务于"读下一行 prompt"和 `PermissionPrompter` 读答案这两件事。两个各自独立、都包着同一个 stdin 的 `bufio.Reader` 会各自往自己的缓冲区里预读，可能把本该给对方的字节偷走；共用一个是安全的，因为两者从不会同时读——权限请求只会在一个 prompt 正在跑的时候出现，这时候读下一行 prompt 的逻辑本来就没有在读。`SIGINT`/`SIGTERM` 的拦截只在一个 prompt 真正在跑的那段时间内生效：第一次信号通过 `session/cancel` 取消它，第二次信号在这次取消还没落定之前又来了就立刻退出；两次 prompt 之间空闲时按 Ctrl-C 完全不拦截，走 Go 自己的默认终止行为。

渲染（`cmd/acp-client/render.go`）是纯文本、按行输出的：`RenderToolCallUpdate` 只在真终端上原地刷新状态（启动时做一次 `golang.org/x/term.IsTerminal` 检查）；非终端（管道输出、CI）永远按行追加，完全不依赖任何终端控制序列。文本片段按追加输出流式打印；`RenderAnomaly` 打印一行带标注的原始回退信息。

## 真实互通验证

`TestInteropRealAgentCompletesAnApprovedWriteFile`（`cmd/acp-client/main_test.go`）真的从源码编译出本仓库自己的 `och` 二进制、把它拉起成一个真实的操作系统子进程，然后用这个包自己的 `run()`（跟 `main()` 调的是同一份代码）全程驱动它，对接一个本地的、脚本化的、无密钥的 HTTP fixture 顶替真实模型供应商。除此之外的一切都是真的：agent 子进程、ACP 线协议握手、`session/new`、实时执行轨迹渲染、交互式权限问答、`write_file` 工具调用真的对一个真实工作区目录执行了写入、turn 最终跑到 `end_turn`。这是本仓库历史上第一次有除了自己写的脚本化测试夹具之外的东西真正驱动过 ACP v1 适配器。

## 明确排除项

本已实现合同不提供：

- 里程碑 7 那个更完整的"TypeScript TUI client"（`docs/README.md`）——这是一个更小的、Go 原生的产物，里程碑 7 可以把它当作"更完整客户端需要什么"的参考证据，也可以完全不理会；
- 一个通用（跟具体 agent 无关）的 ACP v1 客户端——是照着本仓库自己 agent 真实、已观察到的行为写的、测的，不是单纯从规范推导出来、号称兼容任意 agent 的实现；
- `fs` 或 `terminal` 客户端能力的实现——在 `initialize` 时就彻底拒绝，因为本仓库自己的 agent 早就把工作区文件系统和 `exec` 当作自己的、被限制住的工具，现在也不解析任何客户端能力声明；
- 线协议级别的可观测性（类似 Zed 独立的 `acp_tools` 视图那种原始请求/响应/通知调试日志）——本仓库自己的转录/审计面已经给了运维方足够的可见性，不需要这个客户端的执行轨迹渲染再重复一遍；
- 消费 `och export-session` 的 JSONL 输出——那是一个独立的、已实现的离线/审计工具；这个客户端只从实时 `session/update` 渲染，恢复会话靠 `session/load`；
- 多会话、多 agent、分屏 UI——一个客户端进程一辈子只对接一个 agent 进程的一个会话；
- 除 `golang.org/x/term`（只为了做一次 TTY 检查）之外的任何新的非测试依赖。
