# ACP 原生客户端架构调研门

**状态：** 调研证据完成

**日期：** 2026-08-30

**范围：** 按 [客户端界面复用与安全加固顺序决策](2026-08-30-client-surface-and-security-sequencing.zh-CN.md) 定下的顺序，在设计"一个最小的 ACP 原生客户端，足以发一条 prompt、并从 `session/update` 通知和 `och export-session` 输出渲染出执行轨迹"（该决策排序里的第 2 步）之前，先确认有没有真正原生支持 ACP 的参考客户端值得研究——exec 沙箱与资源配额（第 1 步）已经实现完毕。本文不做任何设计或实现。

本项目自己那份 [ACP v1 adapter 架构调研门](2026-08-22-acp-v1-adapter.zh-CN.md) 已经深入研究过 ACP 的**agent/server 端**（线协议帧格式、生命周期、权限桥接、取消、验证方式），本项目今天实现的正是这一端（`adapters/acp`，见[已实现 ACP v1 Adapter 合同](../../architecture/acp-v1.zh-CN.md)）。这次是它的镜像：**客户端**这一端——负责通过 stdio 拉起一个 agent 子进程、走完 `initialize` → `session/new` → `session/prompt`，再把收到的一串 `session/update` 通知变成人能看懂的东西。本文不重新推导 2026-08-22 那份调研门已经从同一份规范里确立的线协议层面事实（帧格式、生命周期顺序、权限请求形状），而是只聚焦这次真正新增的部分：真实的客户端架构。

英文版本 [2026-08-30-acp-native-client.md](2026-08-30-acp-native-client.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。

## 对照集与钉住的 commit

按文档规则第 8 条，每个都用 `scripts/fetch-reference.sh <owner/repo> <sha>` 抓到 gitignore 的 `.reference/` 目录后直接读源码——不是凭记忆或营销页判断。这次选了三个而不是六个：每一个都读得比较深，而不是泛泛扫一个更大的集合，这也符合本项目自己一贯的研究取向——一旦几个各自独立的实现收敛到同一个模式，深度比广度更有价值。

| 项目 | 仓库 | Commit | 观察日期 | 为什么选它 |
| --- | --- | --- | --- | --- |
| Zed | `zed-industries/zed` | `399258f` | 2026-08-30 | 协议的发源方、最权威的客户端；2026-08-22 那份调研门里已经确认过其他 agent（比如 codex-acp）最早就是为了接进 Zed 而做的 |
| acp-go-sdk | `coder/acp-go-sdk` | `0845a3b` | 2026-08-30 | 考虑到本项目自己纯 Go、`CGO_ENABLED=0` 的约束，这是最直接相关的、来自可信维护方（Coder）的地道 Go 客户端写法参考 |
| Toad | `batrachianai/toad` | `dd4f90e` | 2026-08-30 | 一个渲染执行轨迹的最小终端客户端——就架构形态而言，是目前公开项目里离顺序决策第 2 步实际要求的东西最近的一个，不是一整个 IDE。作者是 Will McGugan（Rich/Textual 的作者）。**协议：AGPL**——这里只是作为引用事实记录一下；本文下面的"不授权照抄"规则跟任何项目的许可证无关，一律适用 |

`MoonshotAI/kimi-code` 和 `deepseek-ai/deepseek-harness`——2026-08-22 那份调研门已经为它们各自的 agent/server 端 ACP 工作钉过 commit——这次没有重新抓取：从那次调研门读到的目录结构看（`packages/acp-server`、`packages/acp-adapter`、`packages/acp/acp/src/index.ts`），两个项目的仓库结构都没有透露出有对应的客户端侧实现，而这次选的三个来源已经收敛得足够强（见下文），再泛读第四个只会增加广度、不会增加信号。

## 逐项目发现

### coder/acp-go-sdk——最小 Go 客户端的形状

`example/client/main.go` 是一个完整、可运行的最小客户端，只有约 270 行。它的结构是对"客户端到底必须做什么"最朴素的回答：

- **拉起进程**：用 `exec.CommandContext`，`StdinPipe()`/`StdoutPipe()` 接到连接上，`Stderr` 保留成客户端自己的 stderr（绝不混进 ACP 数据流里——跟 2026-08-22 那份调研门在 agent 端发现的 stdout/stderr 纪律是同一件事，只是这次方向倒过来：agent 不能弄脏 stdout，客户端同样不能，因为 ACP 是同一对管道上的对称 NDJSON）。
- **线协议顺序**：`Initialize`（声明 `ClientCapabilities.Fs` 和 `.Terminal`）→ `NewSession` → `Prompt`。撑起一轮对话只需要这些。
- **`Client` 接口**（`types_gen.go:9431`）正好七个方法：`ReadTextFile`、`WriteTextFile`、`RequestPermission`、`SessionUpdate`，加四个终端方法（`CreateTerminal`/`KillTerminal`/`TerminalOutput`/`ReleaseTerminal`/`WaitForTerminalExit`）。一个在 `initialize` 时就声明不支持 `fs` 和 `terminal` 能力的客户端，真正需要有实际逻辑的只剩 `RequestPermission` 和 `SessionUpdate`；其余方法只是为了满足接口，可以固定返回"不支持"。这就是真正的底线：两个真方法。
- **渲染就是对 `SessionUpdate` 里哪个字段非空做一次裸 `switch`**（`AgentMessageChunk`、`ToolCall`、`ToolCallUpdate`、`Plan`、`AgentThoughtChunk`、`UserMessageChunk`），用 `fmt.Println` 打出来——这个例子里没有跨更新保留任何状态。它老实地承认自己只是个演示，不是执行轨迹渲染的参考写法；这方面看下面的 Toad 和 Zed。
- **传输层实现**（`connection.go`）：一个读取 goroutine 用 `bufio.Scanner` 配一个会扩容的缓冲区（初始 1 MiB、上限 10 MiB）做 NDJSON 分帧——不是 `bufio.Reader.ReadString`，这样能把单行过大的情况框住而不是直接失败。同一条流上收到的东西，用了两种不同的并发规则：agent 发来的入站**请求**（比如 `session/request_permission`）各自分配一个 goroutine 处理，按请求 id 记在一个 `inflight` map 里，这样后到的 `$/cancel_request` 才能精确取消那一个 handler 的 context；入站**通知**（`session/update`）则被塞进一个有界的、带序号的 channel，由一个专门的 goroutine 单独消费——真正保证更新顺序的正是这个设计：如果通知也像请求那样各自开 goroutine 处理，顺序就完全没有保证了，而一个按 `toolCallId` 归并状态的 reducer（见下文 Toad/Zed）恰恰依赖这个顺序保证才能算对。

### batrachianai/toad——生产环境里的最小终端客户端

Toad（基于 [Textual](https://github.com/Textualize/textual) 的 TUI，Python）是目前公开项目里跟本项目实际目标形态最接近的一个。除了上面已经说过的共性，还有两点值得单独记：

- **`src/toad/acp/agent.py` 的 `session_update` 处理函数是对更新的 `sessionUpdate` 判别字段做结构化模式匹配**，只维护一份跟渲染相关的会话级状态：`self.tool_calls: dict[str, protocol.ToolCall]`，按 `toolCallId` 建索引。一条 `tool_call` 更新会新建一条记录；一条 `tool_call_update` 会把非空字段合并进已有记录，再把合并后的结果重新发出去——这是一个小而明确的 reducer，不是一份只增不改的日志。它自己代码里那段兜底逻辑的注释值得原文引用一下，因为它点出了一个真实的互操作性坑："The agent can send a tool call update, without previously sending the tool call \*rolls eyes\*"（agent 有时候会直接发一条 tool call update，前面根本没发过对应的 tool call）——Toad 遇到这种情况会合成一条占位的 `tool_call` 记录，而不是直接丢掉这条更新。每一个分支都会先把原始的线协议数据转成一个带类型的内部消息（`self.post_message(messages.X(...))`），然后才交给渲染用的 widget——解析线协议和渲染是两层，靠 Textual 自己的消息传递机制连起来，不是一个函数把两件事都干了。
- **Toad 自己不保留一份完整的执行轨迹内容。** `src/toad/db.py` 是一张很小的 SQLite 表，只存会话*元数据*（id、标题、`last_used` 时间戳、按最近使用排序的列表）——没有任何一张表或文件存消息片段、工具调用或计划条目。Toad 重新接上一个已有会话时，调的是 agent 自己的 `session_load`（`src/toad/acp/agent.py:796`），然后从 agent 重放回来的 `session/update` 通知里重建内存里的 `tool_calls` 状态和渲染出来的历史——跟这些通知是实时到达的处理方式完全一样。历史只活在 agent 那一份里，客户端不重复存一份。

### zed-industries/zed——同一个模式在生产级规模上、被独立地再次印证

三个 crate：`agent_servers`（子进程生命周期和线协议）、`acp_thread`（reducer/状态模型）、`acp_tools`（一个线协议调试器，本文没有深读，只记录它存在）。

- **`agent_servers/src/acp.rs`** 通过一个 shell builder 拉起 agent（显式处理了 Windows 和 Unix 调用方式的差异，`cfg!(windows)`），把 stdin/stdout/stderr 都接成管道，并且在这些行流进入 JSON-RPC 层之前，把入站和出站的每一行都接了一个 `AcpDebugLog` 分流出去——每一条 ACP 消息，不管方向，都会被记录下来供一个实时的检查器视图使用（`acp_tools` 这个 crate），而不只是为了最终渲染出来的执行轨迹。这是一个独立、刻意做的功能（线协议层面的可观测性），跟执行轨迹渲染本身是两回事，值得作为下面一个独立的开放问题记下来，而不是想当然地认为本项目已有的转录投影已经覆盖了同样的需求。
- **`acp_thread/src/acp_thread.rs` 的 `handle_session_update`（第 2549 行）**，跟 acp-go-sdk 的例子和 Toad 的 `agent.py` 是同一种"按 `SessionUpdate` 变体做匹配"的分发形状——三个互不相干的代码库、三种语言，各自独立收敛到了完全一样的分发形状。拥有这个方法的 `AcpThread` 结构体，是一个比 Toad 大得多的有状态 reducer——它还要追踪 `plan`、`token_usage`、`cost`、`prompt_capabilities`、`available_commands`、每轮的终端实体，还有一个 `StreamingTextBuffer`，故意*延迟*把已经收到的文本展示给界面、用定时器逐步吐出来，纯粹是为了打字动画更顺滑——这是一个 UI 打磨层面的考虑，跟协议正确性无关，这里特意提一下只是为了避免后续设计把它误认成协议要求。
- **`AcpThread` 里很多条目类型都实现了 `to_markdown(&self, cx: &App) -> String`**——这份活着的、内存里的执行轨迹能把自己渲染成 Markdown，形状上跟一份静态导出是一样的，不过本文没有去追这个序列化到底多大程度上是用于持久历史，还是只是给界面内复制/引用之类的功能用的；这里标成"没查清楚"，而不是假设一个结论。
- 关于线协议顺序或接口形状这两个问题，本文没有再拿 Zed 重复验证一遍——这一段里的每一条都是 acp-go-sdk 和 Toad 已经确立的事实之外的新信息，这也是本文停在三个来源、没有再读第四个的原因。

## 交叉综合

- **三个各自独立写出来的客户端，三种语言（Go、Python、Rust），同一种分发形状**：把线协议里的 `SessionUpdate` 解析一次，对它实际填充的变体做一次 `match`/`switch`，再把一个带类型的内部事件交给单独的渲染层。三家都没有在解析线协议的代码里顺手把渲染也做了。这个收敛程度已经足够强，可以把"一个按 `toolCallId` 归并状态的 reducer，由一条保证顺序的通知流驱动，喂给一个独立的渲染步骤"当作接近定论——就像 exec 沙箱那份调研门在三个独立项目都落到同一套命名空间集合之后，把"Linux 上用 bwrap"当作收敛结论一样。
- **顺序保证是有实际作用的，而且是传输层的责任，不是客户端业务逻辑的责任。** acp-go-sdk 的 Connection 明确把并发分发的入站*请求*和严格有序、单 goroutine 消费的入站*通知*分开，原因正是为了这个。设计阶段必须明确决定本项目自己的客户端传输层怎么保证 `session/update` 的顺序——2026-08-22 那份调研门里的传输层发现（C1、C7）覆盖的是 agent 端的 NDJSON 分帧，但那一侧只需要*发出*通知，不需要处理一条有序的通知流，所以没有碰到这个"顺序 vs. 并发"的取舍。
- **在唯一一个不自带转录存储的生产级客户端（Toad）里，历史重放是 agent 的活，不是客户端的活。** Zed 的 `to_markdown` 能力暗示它那边可能有一套更完整的应用内历史/导出机制，但本文没有确认 Zed 重新接上一个既有会话时，靠的是自己的存储还是 `session/load` 重放——这是下面留给设计阶段的一个开放问题，不是本文的发现。
- **线协议层面的可观测性（一份独立于渲染出来的执行轨迹之外的原始请求/响应/通知日志）是最成熟的那个客户端（Zed 的 `acp_tools`）里一个真实的、单独做出来的功能**，不是顺手挂在执行轨迹渲染上的附加品。值得当作一个独立的设计问题提出来，而不是想当然地认为一份执行轨迹视图就能覆盖本项目自己的运维方可能有的同样需求。
- **客户端侧真正最小的 `Client` 接口很小**（按 acp-go-sdk 的写法）：如果客户端在 `initialize` 时就声明不支持文件系统和终端能力，真正需要写实际逻辑的只有两个方法（`RequestPermission`、`SessionUpdate`）。本项目自己的定位里，工作区文件系统和 `exec` 本来就是**这个 harness**自己 jail 住的工具，不是客户端的活——针对本项目自己的 agent 端写一个最小客户端，有一个真实、成本很低的机会：直接在能力协商时声明不要 `fs` 和 `terminal`，而不是在客户端这边把读/写/终端代理再实现一遍——毕竟这些效果本来就已经被 agent（也就是本项目自己）拥有并限制住了。

## 本文没有回答、留给设计阶段的问题

- **实时 `session/update` 流、重放 `och export-session` 的 JSONL、还是两者都要。** 顺序决策里写得很明确——"未来的客户端要从这些已有的界面渲染执行轨迹，而不是靠一个外来的数据模型"——指的正是本项目自己的会话转录 JSONL 投影和实时 `session/update` 通知这两个候选来源。Toad 的做法（客户端自己完全不存，永远通过 agent 自己的 `session/load` 重放）跟"历史读 `och export-session` 的输出、当前这一轮读实时 `session/update`"这个思路是直接对应的，也是一个真实、能跑、够格当参考的最小实现先例——但它是一个要拿来权衡的先例，不是一个已经采纳的答案：本文没有验证 Zed 那套更丰富的 `to_markdown`/历史机制,是不是暗示了某些 Toad 这种简单设计不用面对的场景（比如没有实时 agent 连着也要能离线浏览、多个客户端同时接同一个会话之类的），需要客户端自己也留一份持久拷贝。
- **一份线协议层面的可观测性（类似 Zed 的 `acp_tools`，记原始 ACP 消息）算不算一个"最小"客户端的范围内**，还是说本项目自己已有的转录/审计界面已经给运维方足够的可见性，第一个切片不需要再单独做一个调试视图。
- **"完全声明不支持 `fs`/`terminal` 能力"和"把它们实现成薄薄一层代理"这两种形状，哪个更贴合本项目自己的定位**，考虑到这个 harness 本来就已经在 agent 一侧把这些效果 jail 住了——本文把这个选项摆出来（见上文），但没有替设计阶段做决定,因为这取决于 ACP 能力协商的具体细节（如果本项目自己的 agent 端未来真的要求客户端具备 `fs`/`terminal` 能力，那会需要什么），而 2026-08-22 那份调研门只研究了 agent 单侧，没有碰到这个问题。
- **一个 Go 客户端应该自己写一套最小传输层（跟本项目 `adapters/acp` 的 server 端 codec 对应），还是把 `coder/acp-go-sdk`（或者注册表里的另一个 SDK）当作钉住版本的依赖引进来。** 2026-08-22 那份调研门已经在 *agent* 一侧否决了整体照搬一个未经验证的社区 SDK（R3，"这个帧格式契约小到值得自己拥有"）；这个理由能不能原样搬到 *客户端* 一侧——这次发现同一个 SDK 恰好在客户端这个角色上给出了一份相当完整、写法地道的参考实现——是一个真实的设计决策，不是随便哪边都说得通的定论。
- **对第一个切片来说，执行轨迹渲染这块功能到底要做到多"终端 UI"的程度**（Toad 的 Textual widget 层、Zed 的 `gpui` 实体/渲染模型），还是更接近 acp-go-sdk 那个例子——一份朴素的、按行输出的 CLI——这是顺序决策里明确推迟到"这个客户端做出来之后"的 UI 投入 vs. 极简取舍问题，但设计阶段仍然要选一个起点。

## 证据边界

- 上面每一条引用都对应本次会话里实际读取过的一个钉住 commit（见上表）；没有一条来自记忆、某个项目的营销页或者截图。
- 本文不授权照抄这三个项目里的任何类型名、schema、线协议编解码形状或渲染用的 widget 结构——只授权借鉴机制本身和它们代表的架构选择，跟 2026-08-22 和 2026-08-30（exec 沙箱）那两份调研门对各自对照集的表态完全一样。
- Zed 的 `acp_thread` 和 `agent_servers` 两个 crate 体量很大（读到的文件里分别约 10200 行和约 2300 行）；本文只具体读了更新分发、进程拉起、调试分流这几条代码路径，没有通读整个 crate。Zed 更偏 UI/UX 层面的机制（`StreamingTextBuffer` 打字动画、完整的 elicitation/mention/diff/terminal 子系统）只看到足以知道它们存在，没有细审。
- Zed 自己重新接上一个既有会话时靠的是客户端侧存储、`session/load` 重放，还是两者都用，本文没有确认；已经在上面明确标成"没查清楚"，而不是猜一个答案。
- `kimi-code` 和 `deepseek-harness` 这次没有重新抓取或重新阅读；"两者似乎都没有客户端侧 ACP 对应实现"这个判断,依据的是 2026-08-22 那份调研门当时读到的它们各自 agent/server 端包结构，不是这次专门针对客户端重新读过的结果。
- 这里的"当前状态"指 2026-08-30。未来若有调研门要重新评估这三个项目中的任何一个，按文档规则第 7 条必须重新抓取、重新阅读，而不是沿用本文的描述。
- 本文不做设计选择。下一步是给一个最小的 ACP 原生客户端写一份规范设计——参考上面的发现，但不由它们决定。
