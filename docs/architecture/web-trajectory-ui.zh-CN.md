# 网页轨迹 UI — 已实现合同

英文版 [web-trajectory-ui.md](web-trajectory-ui.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。

**状态：** 已实现；未 GA

**稳定性：** v1.0 之前均为 `experimental`

**成熟度：** pre-v0；不是正式发布版本

**权威来源：** [网页轨迹 UI 与浏览器传输设计](../superpowers/specs/2026-08-31-web-trajectory-ui-design.md)

**证据：** [网页轨迹 UI 证据台账](web-trajectory-ui-evidence.md)

**包：** `internal/client/acpweb`（子进程中继、Origin/令牌升级门禁、HTTP/WebSocket 服务器）；`cmd/acp-web-bridge`（二进制本身：命令行参数、嵌入式前端）；`cmd/acp-web-bridge/web`（独立的 TypeScript ACP v1 客户端、按 Turn 分组的台账、输入框位置的权限批准 UI）

本文档记录的是当前代码和测试实际强制执行的行为。Go 那一半是 Go 合同；TypeScript 那一半是一个浏览器侧合同，除了双方约定好的 wire 字节之外,和 Go 那一半没有编译期强制的关联。

## 范围

一个面向 ACP v1 会话的浏览器轨迹查看器。`cmd/acp-web-bridge` 是一个哑巴中继,不是第二个 ACP 客户端:它通过 stdio 拉起一个 ACP v1 agent(照搬 `cmd/acp-client` 自己的 `-agent`/`-cwd` 形状),把它的 wire 字节原样中继给同一时刻最多一个浏览器 WebSocket 连接,不做任何解析。所有 ACP v1 语义——`initialize`、`session/new`/`load`、`session/prompt`、`session/request_permission`、轨迹归约——都在浏览器自己的 TypeScript 里独立实现,从桥接进程嵌入的前端资源提供。ACP v1 仍然是唯一的客户端协议(2026-08-30 客户端界面与安全加固顺序决策,本文档实现的设计重申了这一点):桥接进程不引入任何新的应用协议。

## 中继(`internal/client/acpweb.Relay`)

拉起一个 agent 子进程(`NewRelay`,照搬 `cmd/acp-client` 自己的 `exec.Command`/`StdoutPipe`/`StdinPipe` 模式),把它的 NDJSON stdout/stdin 行搬运到、以及搬运自当前活跃的那个 `Conn`(真实的 WebSocket 连接,或测试里的假连接)。中继从不解析 JSON-RPC,不检查方法名,也不做任何轨迹归约:

| 方向 | 分帧方式 |
| --- | --- |
| 子进程 stdout → 活跃 `Conn` | 一行 NDJSON(去掉结尾的 `\n`,用一个缓冲区大小设到 `MaxRelayFrameBytes` 的 `bufio.Scanner`——照搬 `internal/client/acp` 自己 `decodeFrames` 的技巧,避免在一个合法的大帧上触发 `bufio.ErrTooLong`)变成一次 `Conn.WriteMessage` 调用 |
| 活跃 `Conn` → 子进程 stdin | 一次 `Conn.ReadMessage` 的结果(补上 `\n`)变成子进程 stdin 的一行 |

没有 `Conn` 活跃时,stdout 是**被丢弃、不是被缓冲**——这只是一个实时视图,跟本项目已有的"没人在监听时,一次 `session/update` 写入失败就被吞掉"这条先例一致。`MaxRelayFrameBytes`(2 MiB)是刻意留在 ACP v1 自己 1 MiB 出站帧上限(`acp-v1.md` 的裁剪边界表)之上的余量,不是中继自己另立的一道独立上限。

**重连是重新接线,不是重启。** `SetConn` 替换活跃连接,并把上一个返回给调用方(HTTP 服务器)去关闭;子进程从不重启,也不会被告知——从它的角度看,自己的 stdin 只是短暂没有写入者,而一个 ACP v1 agent 本就能正确处理这种情况。

## 浏览器防护:Origin 白名单加每次启动的令牌(`internal/client/acpweb/security.go`)

浏览器给本项目的威胁模型引入了一类此前从未命名过的不受信输入:操作者自己机器上另一个标签页里的恶意页面,能够到任何回环端口,不管这个端口实际服务的是不是操作者真正的 UI。每一次 WebSocket 升级(以及会暴露真实工作区路径的 `/config` 端点)在中继任何东西之前,都要过两道独立、必须通过的检查:

- **`CheckOrigin(selfOrigin, requestOrigin)`**:如果 `Origin` 头*存在*,必须和桥接进程自己服务的 origin 完全一致。完全不带 `Origin` 头的请求,单凭这一条检查不会被拒绝——下面的令牌检查照样要过。
- **`ValidateToken(want, got)`**:用 `crypto/subtle.ConstantTimeCompare` 比对一个每次启动都用 `crypto/rand` 生成的令牌,连同就绪 URL(`http://127.0.0.1:<port>/?token=<token>`)一起打印到 stderr——跟本项目 exec 沙箱自己那个"记录下来、操作者可见"的逃生舱先例一致。

`UpgradeAllowed` 要求两者都通过;拒绝时统一返回 `403`,不透露具体是哪一条检查没过。Origin 绑定和令牌谁都不能替代谁:Origin 防的是恶意的*浏览器标签页*;令牌防的是另一个能够到回环接口、但从没见过打印出来的 URL 的*本地账号或进程*。

## 网络暴露面

`-listen`(默认 `127.0.0.1:0`,操作系统分配的临时端口)只接受一个端口;主机部分写死为 `127.0.0.1`,在解析命令行参数时就会拒绝别的值。没有开关能改绑定地址。只用明文 `http://`/`ws://`——没有 TLS。这是一个**本地开发工具**,没有为回环地址之外的暴露做加固;详见 `SECURITY.md`。

## `cmd/acp-web-bridge`

命令行参数:`-agent`(必填)、`-cwd`(必填)、`-resume`(可选,通过 `/config` 传给前端,Go 侧从不使用它去发起任何 ACP 调用,因为桥接进程根本不发 ACP 调用)、`-listen`(默认 `127.0.0.1:0`)。会把 `acp-web-bridge: ready at http://host:port/?token=...` 打印到 stderr。信号处理和清理顺序(先关 agent 的 stdin,再等它退出)照搬 `cmd/acp-client`。

前端在编译期通过 `//go:embed web/dist` 嵌入。**光跑 `go build ./...` 并不能产出一个带真实资源的可用二进制**,除非前端至少构建过一次(`cd cmd/acp-web-bridge/web && npm ci && npm run build`)——嵌入的目录否则会退化成磁盘上当时有什么就是什么。详见根目录 `README.md` 的 Development 一节。

## 独立的 TypeScript ACP v1 客户端(`web/src/acp-client.ts`)

这是同一份 wire 合同的一份真正独立的实现,`internal/client/acp` 用 Go 满足的是同一份合同——不是对它的封装,因为 Go 那份实现根本没法跑在浏览器里。`AcpClient` 对一条入站帧的分类方式,和 `internal/client/acp/wire.go` 的 `message.isResponse`/`isRequest`/`isNotification` 完全一致(带 id 不带 method 是响应;带 id 又带 method 是入站调用;带 method 不带 id 是通知),对着 `acp-v1.md` 精确的参数/结果形状实现了 `initialize`、`session/new`、`session/load`、`session/prompt`、`session/cancel`(以一次性通知的形式发出,跟 Go 客户端自己的取消语义一致),并通过调用方传入的 `Handler` 应答入站的 `session/request_permission`——和 `internal/client/acp.Handler` 用的是同一种拆分。

`WebSocketTransport` 会把底层 `WebSocket` 完成连接握手(`CONNECTING` → `OPEN`)之前发生的每一次 `send()` 调用都排队,等它真正打开之后按顺序全部发出去。这不是防御性代码,而是真正起作用的修复:`AcpClient` 自己构造函数紧接着调用的 `initialize()`,发生的时机远早于那次握手能够完成,而浏览器对一个仍处于 `CONNECTING` 状态的 socket 调用 `send()` 会直接抛异常——这是被本合同自己要求的真实浏览器互操作性证明(见下文)发现并驱动修复的一个真实 bug,不是事先假定它是对的。

## 按 Turn 分组的台账(`web/src/ledger.ts`)

不需要任何新的 ACP wire 字段:`toolCallId` 本来就编码了 `turnID + "/" + callID`(`acp-v1.md`),而本项目自己的单飞行 Prompt 规则(`acp-v1.md`:"Concurrent prompts on one session are `-32600`")意味着实时状态下最多同时只有一个 Turn 是打开的。`Ledger` 通过解析 `toolCallId` 来归属带工具形状的更新(`tool_call`/`tool_call_update`)——从不靠猜上下文——一个解析不了的 `toolCallId` 会被放进 `unassigned` 桶,而不是让归约器崩溃或者悄悄丢掉;一段纯文本增量(它在 wire 上不携带任何自己的标识符)会被归属到 `beginTurn`/`endTurn` 当前打开着的那个 Turn。每一个推导出来的耗时字段都带着 `approximateTiming: true` 标记,让渲染层没法在不带这个标签的情况下展示它:这是一个**本地的、基于接收时间的近似值**,绝不是服务商上报的值——ACP v1 自己的"Never projected on ACP"清单(`acp-v1.md`)本来就对每一个客户端(不管是不是浏览器)都不发 token 用量、延迟和 `finishReason`,本合同不会去绕过这条边界。

## 渲染(`web/src/ui.ts`)

`TrajectoryView` 渲染台账(每个 Turn 分隔符和每条记录各一行),选中一条记录会打开一个本地检查器,展示 `rawInput`、内容、状态和带标签的耗时近似值。对于很长的会话,虚拟化/窗口化渲染明确不在这一版范围内:每一条已加载的记录都直接挂载。

**权限请求会接管输入框的位置**,而不是弹一个模态框(`showPermissionRequest`):操作者原本会输入提示词的位置,被替换成关联工具调用的标题/kind 和允许一次/拒绝控件,回答之后立刻恢复原状。一个待处理请求还没结束时又来了第二个请求——按 ACP 自己的单飞行规则本不该发生,但依然做了防御——会被排队,等第一个应答完之后才渲染。

## 应用接线(`web/src/main.ts`)

拉取 `/config` 获得工作区路径和(如果有的话)`-resume` 会话 id,在一个指向 `/ws`、携带页面自己 URL 里那个令牌的 `WebSocketTransport` 上构造 `AcpClient`,并把 `Ledger`/`TrajectoryView` 接到客户端的 `Handler` 回调上。把新建或恢复的会话 id 记在页面自己的 URL 里(`history.replaceState`),这样标签页刷新会自然变成对同一个会话的 `session/load`——桥接进程自己不追踪任何会话状态。每个 Turn 结束后展示解析出来的 `stopReason`(比如 `[end_turn]`)。

## 真实互操作性证明

`TestInteropRealBrowserCompletesAnApprovedWriteFile`(`cmd/acp-web-bridge/interop_test.go`)从源码构建本仓库自己真实的 `och` 二进制,通过这个包自己真实的 `run()`(`main()` 调用的正是这段代码)拉起它,并用一个真实的、独立控制的无头 Chrome 实例通过 Chrome DevTools 协议(`chromedp`)驱动被服务的页面——不是 mock,除了模型提供方本身之外,也不是本仓库自己的脚本化 fixture。其余一切都是真的:agent 子进程、WebSocket 中继、跑在那个真实浏览器里的独立 TypeScript ACP v1 客户端、由真实 UI 渲染出来的交互式权限批准(对真实的 `.permission-allow` 按钮的一次真实点击),以及真正针对一个真实工作区目录执行的 `write_file` 工具调用。这个测试发现并驱动修复了上面提到的 `WebSocketTransport` 连接竞态 bug——这是第一次有什么东西真的拿前端自己的时序去对一个真实的、异步的 WebSocket 握手做验证,而不是对着一个同步的假连接。

这个测试的运行依赖一个真实可用的 Chrome/Chromium 可执行文件(`CHROME_EXECUTABLE` 环境变量,或者 `PATH` 里的 `google-chrome`/`chromium`/`chromium-browser`);缺失时会带着明确的理由干净地跳过,跟本项目对环境依赖型测试的既有惯例一致,不会在一台没有浏览器的主机上断言任何没有被真正观察到的行为。

## 明确排除的内容

本已实现合同不提供:

- **实时 token 用量、延迟、`finishReason`,或者首字延迟/解码耗时拆分的时间轴总览。** ACP v1 的 wire 合同本来就对每一个客户端隐去这些字段。会话转录导出对*已完成*的会话确实有这些数据,但把它接进实时浏览器视图是另一个、更晚的阶段。
- **一个会话被多个浏览器同时查看。** 同一时刻只有一个活跃 `Conn`;新连接会替换上一个的接线。没有扇出,标签页之间也没有写入仲裁。
- **浏览器 UI 里的 `session/list`/`session/resume`/`session/delete`。** 范围和 `cmd/acp-client` 自己的一致:每次桥接调用一个会话。
- **非回环的网络暴露和 TLS。** 只用 `127.0.0.1`,写死;只用明文 `http://`/`ws://`。
- **超出现有实现的特定前端框架。** Vite + 原生 TypeScript + Vitest,不用 UI 框架,跟本项目对一个单会话、单观察者页面一贯的极简主义一致。
- **对很长会话的虚拟化/窗口化台账渲染。**
