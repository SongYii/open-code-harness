# 网页轨迹 UI 与浏览器传输架构调研门

**状态:** 调研证据已完成

**日期:** 2026-08-31

**范围:** [2026-08-30 客户端界面与安全加固顺序决策](2026-08-30-client-surface-and-security-sequencing.zh-CN.md)
把 exec 沙箱与资源配额、再到最小 ACP 原生客户端,排在"更广泛的 UI 投入"之前。
前两步现在都已经完成设计、实现与验证(exec 沙箱/资源配额系列一直做到
2026-08-31,以及 Go 原生的
[ACP 原生客户端](../../architecture/acp-native-client.zh-CN.md))。这份调研门
研究的是:一个在**浏览器**里渲染的轨迹视图——那次顺序决策点了名但没有设计
的"更广泛 UI 投入"——除了 `cmd/acp-client` 已经证明的东西之外还需要什么。
核心是:一条把 ACP v1 现有 JSON-RPC 消息送到浏览器标签页的传输通道,以及
渲染这些消息的交互设计。按照文档规则 7,本门重新核实了同样六个参考项目在
2026-08-31 的状态,并额外读了第七个子系统(Codex 自己的 `app-server`),
之所以选它,是因为它是参考项目里唯一一个已经把某种 JSON-RPC 协议桥接到
浏览器可达传输层的组件;同时也读了本项目自己的 ACP v1 与会话转录合同,
弄清楚这样一个 UI 实际能拿到哪些数据。本门不设计也不实现任何东西。

英文文件为规范版本,本文件是同步的中文阅读副本。

## 本门不重新讨论的既定决策

2026-08-30 的顺序决策门已经定了,本门当作既定前提:

1. **ACP v1 仍然是唯一的公开客户端边界。** 本项目构建的任何 UI 都是一个
   ACP 客户端,没有例外——它消费 `acp-v1.md` 里已经规定好的
   `session/update`、`session/request_permission` 和会话生命周期 RPC,
   绝不是一个新的、平行的应用协议。这正是让 harness 保持模型中立、UI 中立
   的机制,来自项目宪章。
2. **DeepSeek Harness 的网页 UI 不被集成、fork 或协议对齐。** 它那些让人
   觉得有吸引力的交互特性——按 Turn 分组的台账、明确的工具流水线、日志可
   重建性——已经在 2026-08-15 那次调研门的 Adopt 一栏里被原则性采纳了。
   它真正的前端代码、数据模型和后端协议都在边界之外;本门不授权照抄其中
   任何一样(文档规则 6/8,以及 2026-08-30 那次门的决策第 1 条)。
3. 因此,浏览器传输只是 ACP v1 现有 wire 消息的一个额外**载体**,而不是
   一个与 ACP 竞争的新协议面。

## 本项目已经有的东西

- **ACP v1 wire 合同**:标准输入输出上基于换行分隔 JSON 的 JSON-RPC 2.0
  (`ServeACP`、`cmd/och -acp`)。实时 `session/update` 携带带裁剪边界的
  工具卡片;`session/request_permission` 是一个客户端交互式应答的正常在
  途 RPC。会话生命周期(`session/list`、`session/resume`、
  `session/close`、`session/delete`)已经按能力门控规定好了。
- **`Never projected on ACP`**(`acp-v1.md` 原文):"Usage tokens, latency,
  `finishReason`, `providerRequestID`; policy rule IDs;
  `model.request.recorded`; audit digests / commit positions; raw provider
  SSE; domain error codes (fixed JSON-RPC messages remain); subagent
  origin, plans, thoughts, terminals, diffs, ACP v2 fields; verdicts."
  这是一个刻意设下的边界,不是疏漏——wire 协议恰恰不发送一个"token 用量
  + 耗时"展示会想要的那些实时字段。
- **`cmd/acp-client`**:一个真实、能跑的 Go ACP 客户端
  (`internal/client/acp`),它通过 stdio 拉起一个 agent、把实时轨迹渲染
  到终端、并应答权限请求——任何浏览器客户端现在要做的是在此基础上扩展,
  而不是取代它。
- **会话转录导出**(`docs/architecture/session-transcript.zh-CN.md`、
  `och export-session`):一个独立的、更丰富的**历史性** JSONL 投影,直接
  从 SQLite 读出。它的 `model.usage.recorded` 事实携带
  `inputTokens`、`outputTokens`、`cachedInputTokens`、`latencyMs`、
  `finishReason`、`providerRequestID`——正是 ACP 实时 wire 不发送的那些
  字段——但只是一份已完成(或已提交但仍在进行中)会话的一次性文件,不是
  浏览器今天就能订阅的实时流。

## 对比集合与固定提交

按照文档规则 8,每一个都用 `scripts/fetch-reference.sh <owner/repo> <sha>`
拉取到 `.gitignore` 掉的 `.reference/` 目录里直接读取。按照文档规则 7,
六个项目都在今天重新核实过;其中五个(`pi-mono`、`kimi-code`、
`grok-build`、`codex`、`maka-agent`)和今天早些时候 exec CPU/磁盘配额门
重新核实的提交一致,`deepseek-harness` 自那次门以来没有变动
(`0a53fb55`)。

| 项目 | 仓库 | 提交 | 观察日期 | 发现的 UI 形态 |
| --- | --- | --- | --- | --- |
| Pi(`pi-mono`) | `badlogic/pi-mono` | `853a80d` | 2026-08-31 | 终端(`packages/tui`);另有一个传输无关的客户端库(`packages/client`),但没有浏览器 UI |
| Kimi Code | `MoonshotAI/kimi-code` | `8f2c60b` | 2026-08-31 | 终端,以 `packages/pi-tui` 形式内置 Pi 自己的 TUI;没有浏览器 UI |
| Grok Build | `xai-org/grok-build` | `bc7f02e` | 2026-08-31 | 全屏 Rust TUI;据其自身 README,支持 ACP 用于编辑器内嵌;没有浏览器 UI |
| OpenAI Codex | `openai/codex` | `a9519cb` | 2026-08-31 | Rust TUI(`codex-rs/tui`);**同时**有一个暴露多种传输方式的 JSON-RPC `app-server` 子系统(见下文) |
| Maka(Apache,孵化中) | `apache/maka`(镜像为 `maka-agent/maka-agent`) | `ef94235` | 2026-08-31 | 桌面应用(`apps/desktop`);`packages/ui` 是渲染在该桌面壳里的 React 组件库,不是通过网络协议提供的网页 |
| DeepSeek Harness | `deepseek-ai/deepseek-harness` | `0a53fb55` | 2026-08-31 | 这个对比集合里唯一的浏览器网页 UI(`packages/client/web`,通过 `npx @deepseek-ai/dsh web` 启动),其中包含一个专门的 `ui-trajectory` 包 |

**六个参考项目里只有一个在浏览器里渲染轨迹。** 其余全部收敛到终端 UI;
本项目自己 2026-08-30 那次客户端界面门,在这一步更广泛的 UI 投入之前,
本来选的起点也是同一个(`cmd/acp-client`)。

## DeepSeek Harness 的轨迹与批准设计(只作为交互语言来读)

直接读取固定提交下的 `packages/client/ui-trajectory/README.md` 和
`packages/client/ui-approval/README.md`:

- **一个按 Turn 感知分组的事件台账,不是一条扁平的聊天记录。** User、
  Assistant、Tool,以及嵌套的 Subtool 记录都是可以单独选中的行。粗分割线
  标出 Turn 边界;紧凑的行内标记标出 Step。独立的压缩(compaction)请求
  拥有自己按时间顺序排列的"Between turns"分区,而有编号的压缩则留在它所
  属的 Turn 内。
- **选中一条记录会打开一个本地检查器**,而不是原地展开这一行:token 用
  量、耗时、Input、Output、Timing、以及持久化的图片都渲染在一个独立面板
  里,让台账本身保持紧凑。
- **台账上方有一条固定的时间轴总览(Overview)**,按真实的起止时间从左到
  右投影每条记录;Assistant 的耗时条会拆成"记录到的首字延迟"和"解码"两
  段。拖拽可以选中一个区间,滚轮手势可以缩放,右键点击清除选区,右键拖拽
  可以在已经缩放的视图里平移。对于还在进行中的记录,"In-flight Time 保持
  空白"——按该包自己声明的限制,视图明确拒绝为一个尚未结束的东西编造一个
  耗时。
- **长台账做虚拟化**:只挂载可见的行窗口加一小段预加载余量;视图默认停
  在最新一条,并持续跟随新记录,直到操作者向上滚动,这时会暂停自动跟随,
  这样新活动就不会打断对历史记录的查看。
- **待处理的权限请求会接管输入框的位置**,而不是弹出一个模态框:
  `ui-approval` 自己的 README 写道,它"接管 Conversation 的 composer,
  可选地渲染关联的 Tool 详情,并把用户的决定返回给等待中的 Host 请求"——
  批准界面直接顶替操作者原本会输入文字的位置,相关工具调用的详情内联展
  示;它自己声明的限制是"这个面板只提供临时性的决定"(一次性允许/拒
  绝),而持久化的权限策略留给另一个、Host 侧的界面。

以上都不是本项目可以直接照搬的数据模型或协议——它建立在 DeepSeek
Harness 自己的 `ConversationNode`/`RequestView` 会话投影之上,而本项目
的宪章已经拒绝了复用这套东西。真正可复用的是这种交互的**形状**:按 Turn
分组、选中后再查看细节、把耗时做成一个独立的总览而不是塞进文字里、让一
个待处理的批准占据输入位置而不是用对话框打断。

## 新发现:Codex 的 `app-server` 已经把一种 JSON-RPC 协议桥接到了浏览器可达的传输层

在固定提交下直接读取的 `codex-rs/app-server/README.md` 把 `codex
app-server` 描述为"Codex 用来驱动 Codex VS Code 扩展这类丰富界面的接
口"——架构上和本门要研究的问题一样:一种 JSON-RPC 2.0 协议(wire 上不带
`jsonrpc` 头,其余照标准),多种传输方式:

- **stdio**(`--stdio`,默认):换行分隔 JSON,和本项目自己 ACP v1 已经
  使用的形状一样。
- **websocket**(`--listen ws://IP:PORT`):"每个 websocket 文本帧一条
  JSON-RPC 消息"——**README 明确写着这个传输方式是"实验性/不受支持。不
  要在生产工作负载中依赖它"**,这是一个资源充足、已经在生产使用的项目自
  己披露出来的告诫,不是一个本门可以当作已解决问题的未知角落。
- **unix socket**:面向一个*本地*控制面客户端;`codex app-server proxy`
  打开一条到固定控制 socket 路径的原始连接,把字节在这条连接和标准输入
  输出之间转发,而这条被代理的流本身携带的是一次 websocket HTTP Upgrade
  握手,后面跟着 websocket 帧——即便换成非网络传输,同一套 wire 编解码
  照样复用。
- **一条已经上线的、具体的浏览器防护规则**:websocket 监听器启动时,还
  会提供 `GET /readyz` 和 `GET /healthz`。`/healthz` "在没有 Origin 头
  时返回 200 OK",而"任何带 Origin 头的请求都会被 403 Forbidden 拒
  绝"。因为浏览器对跨源请求(以及同源的 `fetch`)总会带上 `Origin` 头,
  这条规则专门拦住了任意网页 JavaScript 对这些端点的探测或驱动,同时不
  影响不带 `Origin` 头的普通命令行健康检查——这是对"怎么防止操作者自己
  浏览器里另一个恶意标签页够到本机绑定的 agent 进程"这个问题的一个真实、
  能用的答案,而这类威胁,本项目此前任何一次调研门都没有点过名。
- **背压是明确且带类型的**:传输接入、请求处理、出站写入之间是有界队
  列;接入饱和时,新请求会拿到 JSON-RPC 错误 `-32001`,
  `"Server overloaded; retry later"`,文档里明确写了这是可重试的,配合
  退避。

这是本门唯一真正新增的、有分量的发现:一个设计需要的机制形状——一套消
息 schema、一个可插拔的传输 crate、一条有名字的浏览器防护规则、带类型
的背压——在一个重要参考项目那里是被证明过的模式,但同一个项目把那条浏
览器可达的传输方式本身标为实验性且不受支持。其余五个参考项目(包括
DeepSeek Harness 本身——本门没有深入追踪它网页 UI 真正的客户端到后端传
输方式,只读了它组件级别的 README)都没有提供一个可比的、有第一手来源
的传输设计可供对照。

## 交叉综合

- **交互设计和传输设计来自两个互不相关的参考项目。** DeepSeek Harness 是
  "一个浏览器轨迹视图应该长什么样"唯一的来源;Codex 的 `app-server` 是
  "怎么把一个 JSON-RPC agent 协议送到浏览器"唯一的来源。设计阶段两者都
  需要,单独一个都不够。
- **本项目自己"Never projected on ACP"这条边界,对交互设计是一个真实的
  约束,不是一个可以留到以后再解决的细节。** DeepSeek Harness 的时间轴
  总览和逐记录 token 用量检查器,恰恰依赖 ACP 实时 wire 刻意排除掉的那些
  字段(用量、延迟)。会话转录导出确实携带这些字段,但只是一份一次性的
  历史文件,不是实时订阅。所以第一版浏览器 UI 面临一个本门不解决的真实
  选择:按 ACP 实际发送的字段做实时渲染(没有实时用量/耗时),只对已完
  成的 Turn 或会话叠加转录导出的数据,或者把这当作"Never projected"这
  条边界本身该被重新考虑的证据——最后一个选项是一个比单个客户端更大的
  协议级改动,不是本门、甚至也不是某一次客户端设计单方面能决定的。
- **浏览器引入了一类本项目此前从未命名过的、真正新的不受信输入。**
  `SECURITY.md` 里现有的每一句威胁模型陈述,处理的都是模型作为不受信输
  入接触本地工作区这件事。一个通过任意网络传输可达的浏览器标签页(哪怕
  只是回环地址)带来了第二种、不同的攻击者:同一台机器上另一个标签页里
  的任意网页内容(一种 DNS rebinding / 同机 CSRF 的形状),Codex 的
  Origin 头规则就是针对这个的一个具体防御。设计必须说明这个桥接是否仅限
  回环地址、是否需要类似 Codex 那样的规则、以及是否需要某种令牌/凭证——
  本门只呈现先例,不给答案。
- **任何参考项目的传输代码或 UI 代码都不得照抄**(文档规则 6/8,重申自
  此前每一次调研门);只有上面这些机制——一套 schema 上可插拔的传输、基
  于 Origin 的浏览器防护、带类型的背压、按 Turn 分组并配一个独立时间轴
  总览和输入框位置批准的台账——可以用来给设计提供参考。

## 设计阶段要解决、本门不回答的开放问题

- **桥接的形状**:是一个新的、小的服务器进程/模式,把 ACP v1 JSON-RPC
  在 stdio(对接 `och -acp`)和一个浏览器可达的传输方式之间 1:1 转译,
  还是让 `och -acp` 自己直接支持那种传输方式。这个传输方式是 WebSocket
  (像 Codex 那样,并带着它自己披露的"实验性/不受支持"告诫)、服务器到
  客户端方向用 Server-Sent Events、客户端请求用 HTTP POST,还是别的形
  式。
- **网络暴露面与认证**:默认是否仅限回环地址(呼应本项目自己
  `-provider-allow-insecure-loopback` 先例里"仅回环地址才特殊处理"的哲
  学)、是否需要类似 Origin 头规则的东西、以及在设计能宣称这个桥接对开
  发者本机是安全的之前,是否需要某种 bearer token 或类似机制。
- **实时数据与历史数据的调和**:第一版是否按 ACP 现有的"Never
  projected"边界原样接受(没有实时用量/token/耗时展示)、是否通过单独读
  取转录导出来为一个*已完成*的会话增补这些信息、以及考虑到这个缺口,"时
  间轴总览"这个特性在第一版里是否在范围内。
- **会话管理的范围**:一个单会话的实时查看器(呼应 `cmd/acp-client` 当
  前自己的范围),还是在同一版里就把 `session/list`/`resume`/`delete`
  也在浏览器 UI 里暴露出来。
- **权限请求的呈现位置**:输入框位置接管(DeepSeek Harness 的做法)还是
  模态框或内联行——这是一个由上面的参考阅读提供信息、但不由它决定的真实
  设计决策。
- **渲染技术选型**:本门没有研究浏览器端框架选择(React 还是别的)或构
  建工具链;这被当作设计一旦定下数据流和传输方式之后,一个实施计划层面
  的决策。

## 证据局限

- 上面每一条引用都可以追溯到本次会话中读取的某个固定提交(见上表),或
  `.reference/` 里直接读到的文件;没有一条来自记忆、搜索引擎摘要,或某
  个项目的营销页面。本次调研过程中曾经尝试通过网页搜索来了解 DeepSeek
  Harness,结果在好几个第三方站点上呈现出前后不一致、像模板化营销文案的
  内容(一个不合理的 star 数、一个不相关的框架归属);这些结果被完全弃
  用,本文档里的每一条陈述都改为直接从真正拉取下来的固定提交重新推导。
- 对 DeepSeek Harness,本门只读了 `ui-trajectory` 和 `ui-approval` 两个
  包的 README;对 Codex,只读了 `app-server` 自己的 README 加上
  `app-server-transport/src/lib.rs` 的公开导出——不是任何一方完整的客户
  端或服务端实现。更深的行为性论断(比如具体的 websocket 分帧边界情况,
  或者 DeepSeek Harness 自己前端到底怎么和后端通信)没有被追踪,本文档
  也不做这方面的断言。
- 本门不授权照抄任何参考项目的文件路径、常量名、组件名或配置形状——只
  有它们代表的机制和架构选择可以参考,这是本项目对每一次调研门对比集合
  的一贯规则。
- 这里的"当前状态"指 2026-08-31。未来任何一次重访这些项目的调研门,都
  必须按文档规则 7 重新拉取、重新阅读,而不是沿用本文档的表述。
- 本门不选择设计方案。下一步是为浏览器传输桥接和浏览器轨迹 UI 撰写正式
  设计,以上面的发现作参考、而非被其决定,并且受 2026-08-30 顺序决策门
  既定结论的约束——ACP v1 仍然是唯一的客户端协议。
