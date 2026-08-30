# MCP 客户端适配器设计（中文摘要）

**状态：** 已接受（2026-08-30）；本文是与英文规范同步的中文摘要，不是逐字翻译。两者若有分歧，以英文 [2026-08-30-mcp-client-adapter-design.md](2026-08-30-mcp-client-adapter-design.md) 为准。

**审阅问答：** 人类审阅者问了一个直接的问题——如果别的都不依赖它，现在有必要实现吗？答案是：不必要。已实现的合同（Tool runtime、组合根、ACP v1 adapter）今天完全不依赖 MCP 客户端就能正常工作，里程碑顺序里也没有任何一项卡在它上面。这份设计仍然值得现在写——趁调研门的发现还新鲜，把摆放位置、风险分类、审批路由这些问题先钉下来——但这一轮不跟着写实施计划。这跟里程碑 7（TUI 客户端）和 Context Engine 本体现在的状态是同一种做法：已设计（或已接受方向），未承诺实现。`tools.SourceMCP` 在这份设计之后依然是一个"目录里合法但没有适配器"的占位符，直到出现具体的外部工具需求。

## 核心决策（七条，逐一对应调研门留下的开放问题）

1. **采用 `modelcontextprotocol/go-sdk` 作为钉版本依赖，不自己写协议**——这是对 ACP 那边"协议小到值得自己拥有"先例的反转，理由很具体：调研门里三个独立项目（Codex/`rmcp`、Maka/官方 TS SDK、DeepSeek Harness/官方 TS SDK）都选择采用官方 SDK；而且规范本身在 2026-07-28 这一版做了不向后兼容的修订，官方 SDK 内部已经处理了新旧两代协议共存的问题，自己写就得跟着规范的变动反复重做。
2. **只支持 stdio 传输，Streamable HTTP 这次不做**——本地拉起的 MCP 服务器子进程跟本项目 `exec` 工具面对的操作系统级威胁模型是同一回事；远程 HTTP 服务器会带来 OAuth、TLS、Origin 校验这些跟"把外部工具接进现有管线"这个核心目标无关的复杂度，先不解决。
3. **MCP 服务器配置是组合根时刻的静态列表，ACP adapter 现在对 `mcpServers` 的 fail-closed 拒绝保持不变**——跟 `localexec`/`workspacefs` 今天的接线方式一样，是配置项，不是运行时可变的东西。
4. **每一个被发现的 MCP 工具一律分类成 `domain.RiskExec`，绝不新开一个风险维度，也绝不信任工具自己声明的只读/破坏性标注**——规范自己写得很明确：这些标注"必须被当作不可信的，除非来自可信的服务器"。`RiskNetwork`（今天在所有模式下都无条件拒绝）继续留给未来可能的内置联网工具，不挪用给 MCP：MCP 工具在本项目自己的风险语汇里不是"联网"，而是"不可信的外部代码执行"，这正是 `RiskExec` 已经代表的含义。
5. **工具重名靠前缀解决，不改 `Catalog` 本身**——`mcp__<server>__<原始名字>`，直接采用 DeepSeek Harness 的先例；`Catalog` 依然要求全局唯一、依然不可变。
6. **MCP 工具调用的参数完全绕开固定字段的 `toolArgs` 解码和工作区路径校验流程**——`tools.ValidateArgs` 今天已经在 `parseToolArgs` 之前、且与工具来源无关地按 schema 校验过原始参数，这条已经是现成的、不用改的东西；MCP 调用不属于本项目自己的工作区文件系统，所以"是否在工作区内"这个问题对它不适用，一律按 `WorkspaceIn: true` 处理。
7. **审批路由完全不变**：MCP 工具调用走跟 `exec` 今天一模一样的 `Policy.Decide` 表和 `Approver`/`tools.Slot` RPC，不新建一套审批子系统——这是调研门里最扎实的一条交叉证据（Codex 的 MCP 工具审批和自己内置工具建议共用同一个判别键空间）的直接应用。

## 范围之外（这次明确不做，不是漏做）

不写实施计划；不做 Streamable HTTP；不接受 ACP 按会话传的 `mcpServers`；不做 Resources/Prompts；不声明 `sampling`/`elicitation`/`roots` 客户端能力；不做目录热更新（`notifications/tools/list_changed`，目录依然是组合根启动时构建一次、不可变）；协议新旧时代的判断完全交给采用的 SDK，本项目自己不做选择。

## 落地位置

新适配器包 `internal/harness/adapters/mcp`，遵循现有 `adapters/*` 的目录架构规则（只有 `composition` 能导入它，它不能导入任何兄弟 adapter）——这是对 `internal/harness/architecture` 现有依赖边界测试表的一次机械式扩展，不是新机制。子进程复用 `localexec` 现成的操作系统级隔离（Linux 上 bwrap/cgroup v2、macOS 上 Seatbelt/RLIMIT_AS），不新建第二套沙箱机制。发现阶段设了具体的资源边界（每服务器最多 256 个工具、单个工具定义最多 64KiB），超界或发现失败会让 `composition.Open` 直接失败——fail-closed，没有类似 `AllowUnsandboxedExec` 的逃生阀，因为正确的补救方式是修好或删掉那条服务器配置，而不是带着一个已知坏掉的工具悄悄跑起来。

## 风险

主要风险和缓解措施详见英文版 §10：协议新旧时代分裂交给 SDK 吸收；恶意/异常服务器的工具列表用资源边界防护；把所有 MCP 工具统一按 `RiskExec` 处理是刻意选择的保守立场（而不是遗漏）；长生命周期的服务器子进程跟一次性的 `exec` 调用在资源配额跟踪上的差异,留给未来的实施计划去对着 `localexec` 真实 API 解决;没有具体外部工具需求就去实现,本身就是一种风险——这也是这份设计止步于"设计"、不写实施计划的直接原因。
