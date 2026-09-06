# MCP 客户端适配器 — 已实现合同

**状态：** 已实现；尚未 GA（参见[成熟度与已知限制](#成熟度与已知限制)）

**权威来源：** [MCP 客户端适配器设计](../superpowers/specs/2026-08-30-mcp-client-adapter-design.md)，含其 2026-09-04 与 2026-09-05 的修订

**已实现计划：** [MCP 客户端适配器实施计划](../superpowers/plans/2026-09-04-mcp-client-adapter.md)

**完成证据：** [MCP 客户端适配器证据台账](mcp-client-evidence.md)

**English original:** [MCP Client Adapter — Implemented Contract](mcp-client.md)。两份副本若有分歧，以英文版为准。

**包：** `internal/harness/adapters/mcp`（发现、命名限定、调用、拆除）、`internal/harness/adapters/localexec`（它通过端口消费的长生命周期受限命令）、`internal/harness/composition`（配置、接线、命令工厂、调用路由）、`internal/harness/application`（分发）、`internal/harness/tools`（`ExternalTools` 端口与按来源区分的 schema 校验）

本文档记录的是当前代码与测试实际强制执行的行为，属于内部 Go 合同，并非稳定的公开协议，也尚未构成 GA 保证。

## 范围

MCP 服务器是本 harness 通过 stdio 启动的外部程序。它提供的每个工具都会被投影为本项目自己的 `domain.ToolSpec`，并注册进**与四个内置工作区工具同一个** `tools.Catalog`，因此它走的是同一张 `Policy.Decide` 表、同一个 `Approver` 插槽、同一份审计记录。不存在第二套审批子系统、第二张策略表，也不存在任何 MCP 专用的旁路。

刻意排除的内容：Streamable HTTP 传输、OAuth、按会话的服务器配置、服务器死亡后的重启，以及 Windows 上的进程组拆除。每一项排除都在[成熟度与已知限制](#成熟度与已知限制)中给出了理由。

## SDK 边界

线缆协议不在这里实现。钉在精确版本上的 `github.com/modelcontextprotocol/go-sdk` 拥有分帧、initialize 握手、它所携带的四个协议版本之间的协商，以及 `tools/list` 的游标分页。

这与 ACP 那边的先例相反——本项目在 ACP 上自己拥有了三份独立的线缆实现——而这个反转是刻意的：ACP 的 NDJSON 分帧既小又稳定，而 MCP 规范在两年内发布了五个 schema 修订版，并且存在一个活跃的、向后不兼容的纪元分裂。章程 §12.1 陈述了一般规则：先调研再动手，且**决定自己写时必须写明理由**。

## 配置与准入

`composition.Config.MCPServers` 是在 `Open` 时读取一次的静态列表。它**就是**哪些服务器可以存在的准入控制：不存在按会话的配置，ACP adapter 现有的对 `session/load`/`session/resume` 上 `mcpServers` 的 fail-closed 拒绝保持不变。

留空——这是默认值，也是几乎所有装配的实际情况——意味着完全不构造 MCP 客户端，装配与这项能力存在之前完全一致（`TestOpenWithNoMCPServersIsUnchanged`）。

## 沙箱，以及适配器为何绝不导入 `localexec`

每台服务器都通过 `localexec` 施加于 `exec` 工具的同一套操作系统级沙箱启动——Linux 上是 bwrap 加 cgroup v2 配额，macOS 上是 Seatbelt 加 `RLIMIT_AS`——带一个三名字白名单的子进程环境（`PATH`、`HOME`、`TMPDIR`；绝不用 `os.Environ()`），并拥有自己的进程组。

设计一方面禁止适配器导入兄弟适配器，另一方面又要求这种复用。这两条互相矛盾，解决方式是一个端口：`mcp.CommandFactory` 与 `mcp.Command` 由**消费方**声明，而 `composition`——唯一被允许同时导入两者的包——提供由 `localexec` 支撑的实现。`localexec` 因此不欠 MCP 任何东西。

`localexec.NewConfinedCommand` 返回**已配置但尚未启动**的命令，因为 MCP stdio 服务器的 stdin/stdout 就是协议传输通道：由 SDK 自己的 `CommandTransport` 接上管道并调用 `Start`。句柄持有私有临时目录与配额登记直到进程结束，而 `Run` 那种一次性形状只把它们限定在单次调用内。

有一处与 `Run` 的差异是明确披露而非隐藏的：`Run` 在自己的 `cmd.Start` 外围持有 macOS 的 `RLIMIT_AS` 括号，而这里 `Start` 归调用方，因此该括号以 `StartBracket()` 暴露，并在 SDK 的 `Connect` 外围被取用。

## 发现

`Open` 时按顺序对每台配置的服务器：启动、运行 SDK 握手、列出工具。

| 上限 | 值 | 理由 |
| --- | --- | --- |
| `MaxToolsPerServer` | 256 | 与 `tools.MaxListDirEntries` 一致，是本项目对外部提供的列表既有的整数上限。服务器可能无限翻页，这就是让它停下来的东西。 |
| `MaxToolDefinitionBytes` | 65536 | 单个工具的描述加 schema。封住另一条耗尽路径：一个带巨大描述的工具。 |

**两种失败被刻意区别对待。** 突破上限会让**整台服务器**失败，因为只接收一台行为异常的服务器的任意前缀，比一个都不接收更糟。而**单个**无法注册的工具只丢弃**它自己**并给出理由，因为放它进入 `tools.NewCatalog` 会让目录失败、进而让 `composition.Open` 失败——那等于把一个针对整个 harness 的拒绝服务能力交给了一个畸形工具。

每个存活下来的工具都被固定标注为 `Source = tools.SourceMCP`、`Risk = domain.RiskExec`、`Mutates = true`，**永远不从服务器的自我声明推导**：规范要求在无法确立服务器可信度时，把工具自己的 `readOnlyHint`/`destructiveHint` 视为不可信，而本项目在一般情况下无法确立这种可信度。`InputSchema` **原样**保存，因为同一个字段正是 Provider 适配器发给模型的内容。

`DiscoveryResult` 还会报告**哪些工具被丢弃及原因**，以及**哪些工具的参数在调用时会被宽松校验**。后者是一项合同承诺：降级是可审计的，不是隐形的。

## 工具命名

服务器给出的原始工具名是不可信输入。`QualifyToolName` 产出一个 Catalog 合法、抗碰撞的名字：每一部分被清洗成 `[a-zA-Z0-9-]`——**不含下划线**，它被保留作分隔符，把分隔符排除在部件字母表之外正是让拼接具备单射性的全部机制；被清洗**改动过**的部件会再带上其原始字符串的 8 位十六进制 FNV-1a 后缀（因为清洗有损，`a/b` 与 `a.b` 都会变成 `a-b`），本来就合法的名字原样通过；限定名上限 64 字节，超出则截断并追加对未截断名字取的稳定后缀。

没有这套规则，前缀并非单射：服务器 `a` 的工具 `b__c` 与服务器 `a__b` 的工具 `c` 会得到同一个名字。原始名来自不可信服务器，而 `NewCatalog` 在 `composition.Open` 处 fail-closed，因此恶意服务器本可以挑一个与**另一台**服务器的工具相撞的名字，让整个 harness 起不来。

## Schema 校验

`tools.compileSchema` 是为本项目自己那四个内置工具写的：12 个关键字递归生效、只认四种类型、对象必须写 `additionalProperties: false`。已发布的 MCP 工具使用完整 JSON Schema，因此对它们强制要求会拒绝参数上的 `description`、number 与 boolean 类型、`$schema`、`title`、`anyOf` 与 `default`——实际上等于拒绝每一台健康服务器的每一个工具，而启动过程还报告成功。

因此校验是**按来源区分**的，并且是降级而非丢弃：注册只要求 `SourceMCP` 的 schema 是一个受 `tools.MaxMCPSchemaBytes`（32 KiB）限制、无尾随内容的 JSON 对象；`ValidateArgs` 每次调用先尝试 `compileSchema`，能编译就得到与内置工具同等强度的检查，不能编译则降级为要求参数是一个格式良好、无尾随内容的 JSON 对象——**降级不等于没有检查**。内置路径完全不变，并由测试证明而非断言。

## 分发

`application.invokeTool` 以 `spec.Source` 而非 `spec.Name` 分支——外部工具的名字由操作者与服务器决定，封闭的四名字 switch 永远匹配不到。该分支之前跳过两个**仅适用于内置工具**的步骤：`parseToolArgs` 的固定字段解码，以及 `scopePath`/`Resolve` 的包含性检查（外部服务器不是本工作区内的位置）。因此每次外部调用的 `WorkspaceIn` 都为真。

跳过包含性检查**不是放松**：每个外部工具都是 `RiskExec` 且会改变状态，因此 `ModeReadOnly`/`ModeDenyAll` 直接拒绝、`ModeDefault`/`ModeAllowWrites` 要求审批，与内置 `exec` 完全一致。

参数按模型产出的样子原样转发。一个**运行了并报告自身失败**的工具会成为该 Turn 内的工具失败（`CodeExternalToolFailed`，携带工具自己的消息）；只有**根本无法触达**该工具的调用才是终结该 Turn 的错误。这个区分是承重的：工具失败是模型能读到并据以调整的普通事件，混为一谈会让一次寻常的文件不存在拆掉整个会话。

路由按限定名精确匹配。没有任何已配置服务器认领的名字会被拒绝，而不是广播或猜测——猜测会让一台服务器替另一台的工具作答。

## 启动与关闭

`Open` 在三种情况下 fail-closed，且每种都有 mutation 验证：服务器连不上、服务器突破发现阶段上限、两台服务器同名。这里刻意没有类似 `AllowUnsandboxedExec` 的逃生阀。中途失败会拆除已经连上的服务器。

`Assembly.Close` 在关闭 host **之前**停掉每一台已连接的服务器：它们是本装配的叶子节点，先停它们意味着一台迟钝的服务器无法拖延写入端自身的租约释放。

拆除先运行 SDK 自己的 stdio 关闭流程，再升级越过它不做的两件事：它的最后一级只对**进程本身**发信号（自行启动过子进程的服务器会把它们留成孤儿），而且它返回时并未证明进程已被回收（**发信号不等于回收**）。证据来自 SDK 关闭流程的干净返回，以及之后用信号 0 的探测。**进程组与组长必须都消失**：如果某个进程并非组长，`kill(-pid, 0)` 打到的是可能不存在的组并返回 `ESRCH`，而进程还活着，把这当作证据就是假报成功。`mcp.ErrTeardownUnproven` 会如实报告两者都无法确立的情况。

## 成熟度与已知限制

已实现，**尚未 GA**。以下每一项都是明确声明的边界，而非疏漏：

- **没有 Streamable HTTP，没有 OAuth。** 只做 stdio。接受远程传输就必须防御服务器提供的元数据指向 `https://169.254.169.254/…` 或私有段地址这类内网盲 SSRF，而这是本仓库已经为 Provider 适配器推理过一次、在这里要针对服务器可控输入重建一遍的防线。尽管如此，`golang.org/x/oauth2` 仍在构建图中（经由 `mcp` → `auth` → `oauthex`），只是这里没有任何代码调用它。
- **Windows 上没有进程组拆除。** 进程组及针对它们的信号是 POSIX 概念；本仓库对 ACP 子进程监管在该平台本来就是直接拒绝而非近似替代。因此 Windows 构建只得到 SDK 自己那套阶梯，自行启动过子进程的服务器可能把它们留在运行中。
- **没有服务器重启。** 会话中途死亡的服务器会留下一个进程已消失的目录条目；调用会失败。
- **没有按会话的配置。** 服务器只在 `Open` 时命名一次。
- **`MaxToolsPerServer = 256` 是继承来的，不是测出来的。**
- **两个上限因继承而非决策而不一致。** 发现阶段允许定义 64 KiB，注册阶段允许 schema 32 KiB，夹在中间的 schema 会通过前者、在后者被丢弃。结果是安全的，且有测试钉住这个不对称。
- **没有 MCP 评测套件。**
- **没有证明能抵御提示注入。** 工具描述与结果来自不可信服务器并会到达模型。脱敏在既有 Application 路径上生效，每个 MCP 工具也都需要审批，但这里没有任何自动化测试能证明真实模型会抵抗被注入的指令。
