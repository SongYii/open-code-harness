# MCP 客户端适配器架构调研门

**状态：** 调研证据完成

**日期：** 2026-08-30

**范围：** `docs/README.md` 里程碑第 9 项——"MCP client adapter——尚未设计"。本文是给它打的第一份地基：Model Context Protocol（MCP）在其当前版本上到底要求客户端做什么，六个官方参考 agent 项目各自把 MCP 客户端摆在跟自己工具执行/审批管线的什么相对位置，以及本项目自己的 `internal/harness/tools` 端口架构里已经预留了什么、还缺什么。本文不做任何设计或实现。

这次的架构方向跟前一份 ACP 原生客户端调研门刚好相反：那份研究的是一个完全在 `internal/harness/` 之外的客户端。MCP 客户端适配器是反过来的——它是站在 `internal/harness/tools` 已有端口**背后**的一个适配器，把外部 MCP 工具转换成本项目自己的 `domain.ToolSpec`，走跟四个内置工作区工具一样的 Policy `Decide` 表、`Approver` slot 和审计轨迹——这正是基础架构宪章早就写下的："外部工具和资源优先通过 MCP 接入；内置工具与 MCP 工具进入统一的工具执行、权限和审计管线"（`2026-08-11-open-code-harness-architecture-design.md` 第 20 行）。

英文版本 [2026-08-30-mcp-client-adapter.md](2026-08-30-mcp-client-adapter.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。

## 对照集与钉住的 commit

按文档规则第 8 条抓取、直读源码；按第 7 条，规则点名的六个参考 agent 项目（Pi、Kimi Code、Grok Build、Codex、Maka、DeepSeek Harness）这次全部重新验证到各自仓库当前的默认分支 HEAD——六个里有五个自上次调研门钉的 commit 之后已经往前走了，只有 `grok-build` 没变。

| 项目 | 仓库 | Commit | 观察日期 | 为什么读它 |
| --- | --- | --- | --- | --- |
| MCP 规范 | `modelcontextprotocol/modelcontextprotocol` | `ca4ab30` | 2026-08-30 | 协议自己的规范源头；本项目第一次抓取 |
| MCP Go SDK | `modelcontextprotocol/go-sdk` | `21c18c6` | 2026-08-30 | 官方纯 Go 客户端/服务端 SDK，跟本项目自己 `CGO_ENABLED=0` 的约束直接相关 |
| Codex | `openai/codex` | `88f7765`（原 `dde85b4`） | 2026-08-30 | Rust；有专门的 `codex-rmcp-client` crate，以及 MCP 专属的审批/策略类型 |
| Kimi Code | `MoonshotAI/kimi-code` | `cbe0a77`（原 `9619277`） | 2026-08-30 | TypeScript；MCP 服务器配置是从自己的 ACP-adapter 层接进来的，不是从一个 harness 内部端口 |
| Grok Build | `xai-org/grok-build` | `bc7f02e`（未变） | 2026-08-30 | Rust；有专门的 `xai-grok-mcp`、`xai-computer-hub-mcp-adapter` crate，紧挨着自己的 `permission/state.rs` |
| Maka | `maka-agent/maka-agent` | `b832348`（原 `d093ba5`） | 2026-08-30 | TypeScript；一个自成一体的 `packages/mcp`：传输安全、OAuth、有界工具发现 |
| DeepSeek Harness | `deepseek-ai/deepseek-harness` | `0a53fb5`（原 `cd5ef81`） | 2026-08-30 | TypeScript；`mcp-client` 包直接桥接进一个 `ToolRuntime`，是本项目自己 `tools` 端口最接近的对照物 |
| Pi | `earendil-works/pi` | `853a80d`（原 `59a71b2`） | 2026-08-30 | 按第 7 条重新验证；直接查过而不是想当然认为没有 |

`Pi` 目前的 HEAD 跟另一份调研门钉的 `badlogic/pi-mono` 那个 commit 完全是同一个哈希（`853a80d26c9...`，作者 Armin Ronacher）——两个仓库现在共享同一个最新 commit。本文没有去查这是怎么回事（合并、镜像还是改名），因为跟 MCP 这件事没关系，这里记一笔只是免得读者看到不同仓库名下钉着同一个 SHA 觉得奇怪。

## 本项目自己已有的东西

- **`tools.SourceMCP` 和 `domain.RiskNetwork` 已经是预留了但没人用的占位符。** `internal/harness/tools/catalog.go:17` 定义了 `SourceMCP = "mcp"`，`validateSpec` 已经接受它作为合法的 `ToolSpec.Source`。`docs/architecture/tool-runtime.md` 原文写着："Source `builtin` 已经上线；source `mcp` 在 catalog 里是合法的，留给以后的适配器往同一个类型里投影。目前没有 MCP 客户端。"这份调研门就是朝那个方向迈出的第一步。
- **Policy 的表今天在所有模式下都无条件拒绝 `RiskNetwork`。** `internal/harness/policy/engine.go:91`：`input.Network` 或 `input.Risk == domain.RiskNetwork` 会在进入按模式分类的表之前就直接被拒——哪怕是 `ModeAllowWrites` 也不例外。如果 MCP 工具因为"调用意味着访问外部服务器"就被归成 `RiskNetwork`，今天的 Policy 引擎会在不改设计的前提下把所有模式下的所有 MCP 工具调用都拒掉。设计阶段必须明确回答：MCP 工具怎么分类——按每个工具自己声明的 `RiskRead`/`RiskWrite`/`RiskExec` 走（把 `RiskNetwork` 继续留给一个真正不同的、依然一律拒绝的场景），还是给 Policy 表加新的维度。
- **Step 循环的工具分发是一个封闭的四分支 switch，不是一个开放的注册表。** `internal/harness/application/pipeline.go:232-286` 的 `invokeTool` 就是 `switch spec.Name` 对四个内置名字各写一个 case，`parseToolArgs`（`pipeline.go:346-362`）解码的是一个固定字段的 `toolArgs` 结构体。今天完全没有一条路径能调用一个"名字任意、JSON Schema 任意、参数形状任意"的动态发现工具。MCP 工具没法像加第五个内置工具那样简单往 `DefaultWorkspaceSpecs` 里加一条——要么加一个按 `spec.Source == tools.SourceMCP` 分流、参数是 `map[string]any` 的通用兜底分支，接到一个新的调用端口上，要么做一次更大的分发重构。
- **`Catalog` 要求整个目录里名字唯一，而且目录只在组合根启动时构建一次，是静态的。** `tools.NewCatalog`（`catalog.go:41-57`）不区分来源，一律拒绝重名；`composition/assembly.go:141` 是它唯一的构造点，只在 `composition.Open` 时调用一次。外部服务器上一个叫 `read_file` 的 MCP 工具会跟内置的 `read_file` 直接撞名，除非采用某种命名空间约定（见下文 DeepSeek Harness 的做法），而且目前完全没有"启动之后目录还能变"的路径——跟规范自己的 `notifications/tools/list_changed`（见下文）直接相关。
- **本项目自己的 ACP v1 adapter 已经解析并 fail-closed 拒绝了 `mcpServers`（三个建会话调用里的两个）。** `protocol.go:77,100` 在 `sessionLoadParams` 和 `sessionResumeParams` 上都声明了 `MCPServers`；`server.go:236` 和 `server.go:431` 都是"只要 `mcpServers` 非空就直接拒绝"。`sessionNewParams`（`protocol.go:66-68`）压根没声明这个字段，所以 `session/new` 会默默忽略它而不是拒绝——这是已经存在、不是本文引入的一个不对称。这说明 ACP 协议本身确实有一条真实机制，能让一个客户端（比如 Zed）按会话告诉 agent 该用哪些 MCP 服务器——本项目 agent 一侧今天整个拒绝这个输入，这是一个真实存在、现在就要面对的设计分叉：在 `composition.Open` 时静态配置 MCP 服务器（跟今天 `localexec`/`workspacefs` 的接法一样），还是以后也接受按 ACP 会话传入（一个明显更大、目前被主动拒绝的面）。

## 协议本身：读它当前的版本（2026-07-28）

规范仓库按日期发布版本；`2026-07-28` 是当前版本（`draft/` 是更靠后的在制品）。这次直接读它很关键，因为这一版跟本项目自己宪章（写于 `2026-08-11`，早于这一版规范）默认假设的"initialize 握手"形状,以及现在大多数已部署 MCP 服务器实现的形状,有明显区别——不能想当然认为协议还是原来那样。

- **两套互不兼容的"时代"并存，规范自己给它们起了名字。** **Modern**（`2026-07-28` 及以后）：协议版本、身份、能力都作为每次请求的 `_meta` 字段携带，完全没有建会话的握手。**Legacy**（`2025-11-25` 及更早）：就是熟悉的 `initialize` → `notifications/initialized` 握手，建立一个跟会话绑定的连接。**Dual-era** 是两种都支持的实现。规范自己的兼容性矩阵写得很明确：一个只会 legacy 的客户端碰上只会 modern 的服务器，就是直接失败，没有任何向前回退的路径。
- **Modern MCP 按设计就是无状态的。** "MCP 是一个无状态协议：处理一个请求所需要的全部信息都在这个请求本身里……客户端不应该把某一个任务、线程或对话当作 stdio 进程的生命周期边界"，并且明确写了："一个打开的连接，比如一个 STDIO 进程，本身并不是一次对话或一个会话"。每个 modern 请求都要带 `_meta["io.modelcontextprotocol/protocolVersion"]`（必填）和 `clientCapabilities`（必填），少一个就以 `-32602` 拒绝。
- **版本协商是按请求发生的，不是握手的结果。** 服务器不支持请求的版本时返回 `UnsupportedProtocolVersionError`（`-32022`），列出自己支持的版本集合；`server/discover` 可以提前探测，但在 modern 时代是可选的。
- **stdio 分帧就是本项目自己已经熟悉的那套形状**：每行一条 JSON-RPC 消息，不能有嵌入换行，server 的 stderr 是自由格式的，客户端"不应该假设 stderr 有输出就代表出错"——跟本项目自己 `adapters/acp/codec.go`（agent 端）和 `internal/client/acp/wire.go`（客户端端）已经实现的 NDJSON 分帧和 stdout/stderr 纪律是同一套东西。关服的顺序是"先关 stdin、等退出、再升级到 SIGTERM/SIGKILL";服务器进程意外退出时,客户端"应该"重启它,因为协议本身无状态,飞行中的请求直接算丢——这跟 ACP 有一个真实的运维差异：ACP 里被拉起的是本项目自己的 agent 进程，不指望它中途重启。
- **Streamable HTTP 在这同一版规范里去掉了协议级会话。** 2026-07-28 移除了 GET 流端点和协议级会话；每个请求都是自己独立的一次 POST，答复要么是单个 JSON 对象要么是一条限定在这一次请求范围内的 SSE 流。服务器本地跑的时候"应该只绑定 localhost"，并且"必须校验 Origin 头"防 DNS rebinding——这跟本项目自己最近那个 `-provider-allow-insecure-loopback` 逃生阀（ACP 原生客户端计划 Task 5）要应付的是同一类问题，只是换了个 HTTP 面。
- **对客户端来说真正要紧的两个方法是 `tools/list` 和 `tools/call`。** `tools/list` 接受可选的 `cursor`，返回 `tools`、`nextCursor`,这一版还新加了 `ttlMs`/`cacheScope` 用于列表缓存。`tools/call` 接受 `name`/`arguments`，返回 `content`（文本/图片/音频/资源链接/嵌入资源等带类型的内容块）和/或按 `outputSchema` 校验过的 `structuredContent`。
- **两条错误通道，规范明确说了哪一条该喂给模型。** *协议错误*（未知工具、请求格式不对）是普通的 JSON-RPC 错误，"客户端可以把协议错误喂给模型，但这类错误更不容易被模型自己恢复"。*工具执行错误*（一次 API 调用失败、参数不对）是一个**成功**的 JSON-RPC 响应，只是 `result` 里带 `isError: true` 和描述出错原因的 `content`——"客户端应该把工具执行错误喂给模型，让模型有机会自己纠正重试"。本项目自己 `application/pipeline.go` 对四个内置工具已经有一套结构完全类似的双通道：`failToolAndContinue`（模型能看到的、记进领域事件的工具失败，比如 `CodeExecTimeout`）和会中断整轮的应用层 `err != nil` 失败——把 MCP 的 `isError: true` 映到已有的 `failToolAndContinue` 路径、把 MCP 的协议错误映到已有的应用层错误路径，看起来是水到渠成的事，不是一个全新概念，但具体怎么映不是本文该定的。
- **规范自己已经写明了"人在环内"的期望，跟本项目 Policy/Approver 管线对内置工具已经做的事是一回事。** "出于信任与安全考虑，应该始终有一个人在环里，能拒绝工具调用"；安全注意事项里写："客户端应该对敏感操作要求用户确认""应该在调用服务器之前把工具的输入展示给用户看，避免恶意或意外的数据泄露"。规范没有规定具体用哪种同意机制（工具的 `readOnlyHint`/`destructiveHint` 之类的标注只是建议性元数据，"客户端必须把它们当作不可信的,除非来自可信的服务器")——这跟本项目宪章自己"把 MCP 工具接进已有 Policy/Approver 管线，而不是信任服务器自报的标注"的直觉是一致的。
- **Resources 和 Prompts 是两个独立、真实存在的原语，本文不替设计阶段决定要不要纳入范围。** 服务器还可以额外暴露 `resources/list`+`resources/read`（用 URI 定位的内容，跟工具结果是两回事）和 `prompts/list`+`prompts/get`（参数化的提示模板）——两者都各自独立地由能力声明控制，本文只确认了它们存在，没有深入读。

## 官方 Go SDK 的客户端形状

`modelcontextprotocol/go-sdk`（`21c18c6`）的 `examples/client/listfeatures/main.go`（不到 30 行就是一个能跑的客户端）最能说明采用这个 SDK 能省掉多少手写的东西：

- **`CommandTransport` 直接包一个已经构造好的 `*exec.Cmd`**——跟本项目自己 `cmd/acp-client`（上一份计划）的 `-agent` flag 是同一种"自己拉子进程再交给库"的写法，一个基于这个 SDK 的客户端可以原样复用这个模式来配置 `mcp-server`,不用另发明一套。
- **时代判断（modern/legacy）是 SDK 内部自己处理的，不是暴露给调用方去决定的一件事。** `mcp/client.go` 的 `discover`/`usesNewProtocol()` 都在 `Client` 内部——采用这个 SDK 基本上是白得 dual-era 支持；自己手写就得把 `server/discover` 探测再回退的整套流程实现一遍。
- **`Tools`/`Resources`/`Prompts` 都是自动翻页的迭代器**，内部自己吃掉 `tools/list` 的游标分页——采用这个 SDK 不用自己写游标遍历；不采用的话就得像 Maka 那样自己写（见下文）。
- **`AddRoots`/`RemoveRoots`、sampling、elicitation** 这些客户端侧能力（`roots`、`sampling`、`elicitation`）SDK 里都有，但都可以按需声明不支持——就跟本项目自己的 ACP 原生客户端已经声明不要 `fs`/`terminal` 一样，SDK 不强制客户端把没打算用的能力都实现全。

## 逐项目发现

### openai/codex——MCP 工具调用的审批走的是跟内置工具建议**同一个**判别空间

采用官方 SDK（`rmcp = "=3.1.3"`，精确钉版本，不是范围）而不是自己写协议；自己写的 `codex-rmcp-client` 在 SDK 之上加了 OAuth、重试、stdio 拉起——SDK 管线协议机制，Codex 自己管运维层（鉴权、重试、进程生命周期）。最直接的一条证据：`mcp_approval_meta.rs` 里 `APPROVAL_KIND_MCP_TOOL_CALL` 和 `APPROVAL_KIND_TOOL_SUGGESTION` 共用同一个 `APPROVAL_KIND_KEY`,还共用 `PERSIST_KEY`（`session`/`always`）——这是这次调研门里最清楚的一条证据，证明宪章里"统一管线"这个直觉在一个真实上线的实现里确实站得住：Codex 没有给 MCP 工具审批单独建一套系统。另外还有一层独立于单次调用审批之外的东西：`EnvironmentMcpPolicy` 让环境管理员按精确匹配/前缀/正则限定哪些 MCP 服务器允许被配置——这是"这个服务器允不允许接进来"和"这个已经接进来的服务器的这次调用允不允许跑"两个完全不同的同意层，本项目自己的组合根对内置工具没有对应物。

### MoonshotAI/kimi-code——MCP 服务器配置是从 ACP 本身接进来的，不是自己拥有的端口

`packages/acp-adapter/src/mcp.ts` 把入站 ACP `session/new` 的 `McpServer[]`（按 `type: http/sse/acp/stdio` 判别）翻译成 kimi 自己内核的配置格式；这次搜索范围内没找到 kimi 自己的 MCP 线协议客户端——它的 MCP 支持基本就是"ACP 客户端告诉我用哪些服务器，我就转发给内核用"。`type: 'acp'` 这种传输方式被直接 warn 后丢弃（还不支持）。这直接证实了本文"本项目自己已有的东西"一节独立发现的事实：ACP 的 `session/new`/`load`/`resume` schema 里确实有一个真实的、按会话传的 `mcpServers` 字段，真实客户端（kimi 上游的那些 ACP 客户端）会用它——本项目自己的 ACP adapter 今天对非空值一律拒绝，比 kimi 自己的立场（接受并使用）更严格。

### xai-org/grok-build——MCP 挨着一个专门的权限状态模块

`xai-grok-mcp`、`xai-computer-hub-mcp-adapter` 是独立于 `xai-grok-workspace` 的 crate，`xai-grok-workspace` 自己内部 `src/mcp.rs` 和 `src/permission/state.rs` 是相邻文件——本文没有深读这两个 crate 的内部实现（时间预算优先给了 Codex 那条已经很扎实的审批证据），但这个文件相邻本身就是第二个独立的数据点（继 Codex 之后），支持"权限/审批状态跟 MCP 接线是刻意放在一起的"这个判断。

### maka-agent/maka-agent——一个自成体系、明确设了界限的 MCP 包

`packages/mcp/src/tool-discovery.ts` 用官方 TypeScript SDK（`@modelcontextprotocol/client`）而不是自己写协议，但自己在它之上手写了分页遍历，并且给了明确的数字上限：`maxPages: 1000`、`maxTools: 1000`、`maxDefinitionBytes: 16MB`——这直接回答了本项目自己文档规则第 4 条要求设计阶段必须回答的"资源边界"问题：MCP 服务器是不可信的外部输入，无界分页或超大的工具 schema 就是一个真实的资源耗尽攻击面,必须像本项目自己给内置工具设 `MaxListDirEntries`、`MaxArgvItemBytes` 那样明确设界限。`credential-coordinator.ts`、`oauth.ts`、`transport-security.ts` 各自独立成文件——进一步证明成熟实现里"MCP 客户端"不是一个适配器，而是一个小子系统：传输安全、凭证/OAuth、工具发现/绑定是分开的关注点。

### deepseek-ai/deepseek-harness——跟本项目自己 `tools` 端口结构最像的对照物

`packages/mcp/mcp-client/src/tools.ts` 自己就是这么描述的："工具桥接：发现 MCP 工具，把它们按确定性的、带服务器前缀的公开名字注册进 harness 的 ToolRuntime，并处理服务器工具列表变化时的重新同步"——这跟本项目自己 `tools.Catalog` + `application.Service.invokeTool` 占据的位置完全一样。**这次调研门自己标记为"待解决"的命名冲突问题，这里有一个具体、能跑的先例**：每个 MCP 工具的稳定身份是 `(serverName, rawName)`，模型看到的公开名字是 `mcp__<serverName>__<rawName>`；原始名字只在线协议里（`tools/call`）出现，公开名字从来不会被反解析回原始名字。这直接回答了外部服务器上一个 `read_file` 怎么跟本项目内置的 `read_file` 在同一个要求名字唯一的 `Catalog` 里共存——用服务器身份给模型可见的名字加前缀，原始 MCP 名字纯粹当线协议层面的状态，模型不用看到也不用还原它。同样采用官方 TypeScript SDK（`@modelcontextprotocol/sdk/client`）——这是这次对照集里第三个独立、两种语言的项目选择采用官方 SDK 而不是自己写协议。

### earendil-works/pi——没有 MCP 客户端，直接查过

`pi` 源码里唯一命中"mcp"的地方（`tool-result-images.ts` 里一句提到"MCP 桥接"的注释）只是顺带一提，不是 MCP 客户端代码；没有任何 `package.json` 依赖 `@modelcontextprotocol/sdk` 或任何带 MCP 名字的包。这是一个直接查证过的真实负面发现：`pi` 在当前 commit 上没有实现 MCP 客户端，跟这次对照集里另外五个参考 agent 不一样。

## 交叉综合

- **三个各自独立的项目，两种语言，同一个答案：采用官方 SDK，不自己写协议。** Codex（`rmcp`）、Maka（`@modelcontextprotocol/client`）、DeepSeek Harness（`@modelcontextprotocol/sdk`）都是在官方 SDK 之上做客户端层，自己的工程精力花在鉴权、重试、发现边界、工具命名、审批路由这些运维层面的事情上。这跟 2026-08-22 那份 ACP v1 调研门给 *agent* 侧定下的结论（"帧格式契约小到值得自己拥有"）恰好相反，也跟 ACP *客户端* 侧目前还悬而未决的同一个问题不一样——但 MCP 的协议面（工具/资源/提示发现带分页缓存、两种传输各自有自己的双时代兼容流程、HTTP 上的 OAuth、sampling/elicitation/roots 这些客户端能力）比 ACP 大得多，上面的收敛结果很可能正是因为这个体量差异。设计阶段应该把"采用 `modelcontextprotocol/go-sdk`"当作最有力的候选项，而不是简单套用 ACP 那边的先例。
- **MCP 工具调用的同意机制收敛到复用一个已有的通用审批机制，而不是另建一套**——Codex 用同一个 `APPROVAL_KIND_KEY` 判别空间同时装 MCP 工具调用和自己的内置工具建议是最直接的证据，Grok Build 把 `mcp.rs` 和 `permission/state.rs` 放在一起是独立的第二个佐证。这支持宪章自己的意图，也支持把本项目已有的 `tools.Approver`/`Policy.Decide` 当作复用目标，而不是另起一套 MCP 专属的审批路径。
- **还有一层独立的同意机制——哪些 MCP 服务器允许被配置——是真实存在的，本项目自己的组合根今天完全没有对应物。** 只有 Codex 的 `EnvironmentMcpPolicy` 把这个明确建了模；本项目 `composition.Open` 只给内置工具接了唯一一对 `localexec`/`workspacefs`，没有类似"这个部署允许接哪些 MCP 服务器配置"的东西。
- **外部工具名字要进一个要求全局唯一的 `Catalog`之前，需要一个消歧约定**，DeepSeek Harness 的 `mcp__<server>__<tool>` 是一个具体、能跑的答案，直接对应本文自己读 `tools.NewCatalog` 时预见到的撞名问题。
- **MCP 自己的"modern"（2026-07-28）和"legacy"（2025-11-25 及更早）两个时代是一个活的兼容性问题，不是已经尘埃落定的事**——这次调研门没有深入确认六个参考 agent 项目各自的 MCP 客户端代码到底面向哪个时代（下面明确记为空白），但规范自己认真到专门定义了一张兼容性矩阵和一套强制的探测流程,足以说明这事有多重要。不能想当然认为"实现了 initialize"就够了，也不能假设现实里所有 MCP 服务器都已经说 modern 这套无状态协议。
- **本项目自己的 ACP v1 adapter 已经为这件事留了一个 fail-closed 的占位。** `session/load`/`session/resume` 已经解析并无条件拒绝非空的 `mcpServers`；kimi-code 自己对同一个 ACP 字段的处理方式（接受并接线）是"真实 ACP 客户端将来可能会发什么"的具体证据——这是本项目协议层已经表过态的一个设计问题，不是假设。

## 本文没有回答、留给设计阶段的问题

- **静态的、组合根时刻配置的 MCP 服务器，还是按 ACP 会话传的 `mcpServers`（或者两者都要）。** 本项目 ACP adapter 目前对后者完全 fail-close；kimi-code 的先例说明真实客户端可能想用它。
- **要面向哪个（些）协议时代**——modern（无状态、按请求 `_meta`）、legacy（`initialize` 握手）还是 dual-era，以及采用 `modelcontextprotocol/go-sdk`（它内部已经处理了这个问题）是不是能直接绕开这个决策本身。
- **自己写协议还是把 `modelcontextprotocol/go-sdk` 当钉住版本的依赖引进来**——三个独立项目对 MCP specifically 都选了采用官方 SDK,本文摆出这个收敛结果,但不替设计阶段决定这是否推翻本项目在 ACP 那边"倾向自己拥有小的线协议契约"的既有立场。
- **MCP 工具怎么套进已有的 Policy `Risk` 枚举。** 今天的 `RiskNetwork` 在所有模式下都无条件拒绝；一个只读外部系统的 MCP 工具显然不该跟内置 `exec` 改写工作区是同一个风险等级。设计阶段必须决定：MCP 工具按自己声明的 `RiskRead`/`RiskWrite`/`RiskExec` 走（`RiskNetwork` 继续是另一个、依然一律拒绝的独立问题），还是给 Policy 表加一个真正新的维度——本文在本项目自己代码里没找到任何"一个动态发现的工具该怎么声明风险等级"的先例，因为今天四个内置工具的风险等级都是写死的 Go 常量。
- **一个动态发现的工具集怎么接到 Step 循环的调用路径上**，考虑到 `application/pipeline.go` 的 `invokeTool` 今天是四个固定名字的封闭 switch，配一个固定字段的参数结构体。这是设计阶段要实打实规划的实现工作，不是靠读参考项目就能照搬答案的地方，因为它们都没有本项目这套具体的 Step 循环架构。
- **要不要采用一个工具名消歧约定**——DeepSeek Harness 的 `mcp__<server>__<tool>` 是一个值得权衡的先例，不是已经采纳的答案。
- **一层独立于单次调用审批之外的、管理级的"哪些 MCP 服务器允许被配置"策略**——本文找到的唯一先例是 Codex 的 `EnvironmentMcpPolicy`；本项目对现有内置工具没有对应的面可以参照。
- **Resources 和 Prompts：第一个切片要不要一起做，还是先只做 tools。** 本文只确认了这两个原语存在且各自由能力声明控制，没有深入研究。
- **工具发现和工具调用负载的资源边界**，参照 Maka 明确给出的 `maxPages`/`maxTools`/`maxDefinitionBytes`——本项目自己文档规则第 4 条要求设计阶段必须给出这些边界，而本项目现有代码里没有任何一处（内置工具数量固定、编译期已知）预见过"一个外部提供、可能是恶意的工具列表"这种情况。
- **MCP 服务器子进程要不要跟本项目 `exec` 工具现在一样的操作系统级隔离（Linux 上 bwrap/cgroup v2，macOS 上 Seatbelt/RLIMIT_AS）**——一个走 stdio 的 MCP 服务器，从操作系统的角度看正是沙箱工作原本要防的那类不可信子进程，但本文没有去查任何参考项目是否对自己拉起的 MCP 服务器进程做了操作系统级隔离。

## 证据边界

- 上面每一条都对应本次会话里实际读过的一个钉住 commit（见上表）；没有一条来自记忆或营销页。
- 本文不授权照抄任何参考项目里的类型名、schema 形状或审批键命名约定——只授权借鉴机制本身和它们代表的架构选择，跟本项目此前每一份调研门对各自对照集的表态一样。
- 参考项目的 MCP 客户端代码是专门针对"摆放位置、审批路由、命名、依赖选择"这几个问题读的；本文没有审计任何一个项目 MCP 实现的正确性、安全性或协议一致性，也不认为它们是可以照抄的范本，只是可以权衡的数据点。
- 五个有真实 MCP 客户端代码的参考 agent 各自面向哪个（些）协议时代，本文都没有确认——只读了摆放位置、审批路由和 SDK 选型，没有追踪线协议层面的版本协商。
- `kimi-code` 自己的 MCP 线协议客户端（区别于本文读过的"ACP 配置转内核配置"那部分）没有找到或读过；它可能活在 `packages/acp-adapter` 这次搜索范围之外的某个内核包（`@moonshot-ai/agent-core`）里。
- `grok-build` 的 `xai-grok-mcp` 和 `xai-computer-hub-mcp-adapter` 只确认了存在、以及跟 `permission/state.rs` 相邻,没有深读。
- `pi` 和 `pi-mono` 目前在两个不同名字的 GitHub 仓库下共享同一个最新 commit；本文只是观察到了这一点，没有去查原因，因为跟这次要研究的 MCP 问题无关。
- 这里的"当前状态"指 2026-08-30。未来若有调研门要重新评估这几个项目，或者 MCP 规范本身（本文发现它直到 2026-07-28 还在做不向后兼容的修订），必须按文档规则第 7 条重新抓取、重新阅读，而不是沿用本文的描述。
- 本文不做设计选择。下一步是给 MCP 客户端适配器写一份规范设计——参考上面的发现，但不由它们决定。
