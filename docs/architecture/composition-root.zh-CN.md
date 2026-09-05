# 组合根 — 已实现合同（中文阅读版）

**状态：** 已实现；非 GA

**日期：** 2026-08-19

**权威：** [组合根与跨 Adapter 一致性（Slice 5）设计](../superpowers/specs/2026-08-19-composition-root-conformance-design.zh-CN.md)

**证据：** [组合根完成证据](composition-root-evidence.md)

**包：** `internal/harness/composition`、`internal/harness/adapters/system`、`cmd/och`

英文版本 [composition-root.md](composition-root.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。

## 范围

`composition` 是唯一点名具体实现并将其接线成运行装配的地方。它是**库**而非 `main`，因此装配由测试断言、而不是靠启动进程；`cmd/och` 是其上的薄二进制，只含 flag 解析与信号处理。

该包只做构造：没有领域状态转移、没有重试或准入策略、没有仅为测试而存在的分支。它施加的每条边界都来自已经拥有该边界的组件；它自己只引入一条——`Close` 可以等待多久。

## 装配

`Open(ctx, Config) (*Assembly, error)` 先校验配置、从环境读取 provider 凭据，再按固定顺序构造：Runtime Host（它打开 SQLite store 并完成启动重整）→ provider model 与 turn runner → 工作区文件系统与命令执行器 → 工具目录 → Application service。service 接收的是作为 `AuthoritySource` 的 SQLite store，而不是一份 `WriterAuthority` 快照，因此过期接管造成的 fencing 令牌轮转对下一次追加可见。

`Open` **绝不**在返回非 nil 错误的同时返回非 nil `Assembly`。host 启动之后的任何失败都会先释放 host 再返回，因此失败的装配不会留下被持有的租约或被锁住的数据库。若释放本身失败，两个错误会被 join，而不是其中一个覆盖另一个。

### MCP 服务器

`Config.MCPServers` 列出 `Open` 要连接的 MCP 服务器，按顺序在命令执行器之后、工具目录之前进行。留空——这是默认值，也是几乎所有装配的实际情况——意味着完全不构造 MCP 客户端，装配与这项能力存在之前**逐字节相同**（`TestOpenWithNoMCPServersIsUnchanged`）。

每台被配置的服务器都通过 `localexec` 的受限命令入口启动、经 SDK 握手连接、然后被询问工具清单。发现到的每个工具都进入**与四个内置工具同一次** `tools.NewCatalog` 调用——一个目录、一次重名检查、一张 Policy 表、一份审计记录——这正是外部工具能够自动继承 `RiskExec` 审批门禁、而不需要第二套机制的原因。`Assembly.Catalog()` 暴露该结果。

MCP 命令工厂位于 `composition` 内，因为它是唯一被允许同时导入 `localexec` 与 `adapters/mcp` 的包；MCP 适配器只声明端口，绝不导入其兄弟适配器。

有三条行为是 fail-closed 的，并有对应测试：

- 被配置的服务器若连不上、若突破发现阶段的资源上限、或其工具无法投影，**`Open` 直接失败**。这里没有类似 `AllowUnsandboxedExec` 的逃生阀：操作者配置了一台服务器就是要它的工具，在报告成功的同时不带这些工具启动，会让它们看起来像不存在而不是坏了。
- **两台服务器配置了相同名字**会让 `Open` 失败，并作为配置错误指名道姓，而不是变成 `NewCatalog` 报出的那种由工具名推导出来的重名错误。
- 中途失败会**拆除已经连上的服务器**再返回，因此半成品装配不会泄漏子进程。

`Close` 会先停掉每一台已连接的服务器，再关闭 host——它们是本装配的叶子节点，先停它们意味着一台迟钝的服务器无法拖延写入端自身的租约释放。两边的错误会被 join。
`Assembly` 以只读访问器暴露 `Service()`、`Host()`、`Store()`，并拥有它返回的每个资源。`ServeACP` 在调用方提供的双工上讲 ACP v1 JSON-RPC，写入端只收到 ACP 帧。Application 服务构造时带 `tools.Slot` Approver，因此 ACP 服务器无需重建 Service 即可接入。

`Close()` 停止准入，在 `Config.ShutdownTimeout`（默认 10 秒）内等待 host 的循环，释放租约，关闭 store。它是**幂等**的：第二次调用返回首次结果，而不是再关一次——那会释放本装配已不再拥有的租约。丢弃 `Assembly` 而不 `Close` 会泄漏 SQLite 句柄与 host goroutine；此点如实声明，不做防御。

## 配置

`Config.Validate` 是全量且 fail-closed 的：构造任何资源之前检查每个字段，因此被拒绝的配置**不创建数据库文件、不获取租约**。错误指名字段，且不包装成 adapter 错误类型——那是调用方的错误，不是组件的失败。

provider 凭据**不是** `Config` 字段。`Provider.APIKeyEnv` 指定一个环境变量名，在 `Open` 时读取；以字面量传入的密钥会进入测试夹具、shell 历史和进程列表。

边界只转发、绝不重定义：step、工具调用、助手字节数与审批上限均来自 `application.DefaultConfig`，`Config.Limits` 中的零值即表示采用 Application 默认。

provider profile 使用 `ProfileToolsSupported`：装配始终启用工作区工具目录，而 Application 会拒绝 profile 不支持 native tools 的目录。

## 系统端口

`adapters/system` 实现 `application.Clock` 与 `application.IDGenerator`。**本切片之前这两个端口没有生产实现**：只有 `testkit` 满足它们，而生产包不得导入 `testkit`。

`Clock` 返回 UTC。`IDs` 每个标识符从 `crypto/rand` 取 128 位，并按种类加前缀以便阅读；Domain 与 Application 中没有任何代码解析该前缀，也不得开始解析。读取随机源失败会**返回错误**而非退化到更弱的源——标识符承载准入与追加身份，可预测的标识符比失败的命令更糟。

## 依赖规则

`composition` 是唯一可导入 `internal/harness/adapters` 下任何内容的包，由 `internal/harness/architecture` 强制执行：

- 除 `composition` 外，每个 owner 都被禁止点名自身以外的 adapter，且以**穷举**方式覆盖每个 owner × adapter 组合。
- `runtime` 是唯一且狭窄的例外，只允许 `sqlite`：Runtime Host 拥有规范 store 的生命周期，其 `Config` 内嵌 `sqlite.Config`。
- **未声明 owner 的目录会被检查而非跳过**，因此新增包不会因为"没被列出"而继承组合例外。
- `composition` 不得导入 `testkit`：生产接线不得伸手去拿替身。
- `cmd/och` 只可导入 `composition` 与标准库。

## 验证

`TestAssemblyRunsAToolCallingTurnEndToEnd` 把 Domain、Application step 循环、SQLite 规范 EventStore、Runtime Host、OpenAI 兼容 provider adapter、工作区文件系统与 policy 引擎装配在一起，跑通一个「模型请求 `read_file`、再依据结果作答」的完整 turn。

它走的是真实路径：真实数据库文件、adapter 自己的 HTTP 与 SSE 处理（对本地服务器）、以及 `policy.ModeDefault` 而非 allow-all 旁路。断言针对**从数据库读回的持久事件流**而非内存结果，并重放该流以确认可重建出「会话活跃、无 turn 在运行」的状态。无网络、无凭据（除测试自设的环境变量）、无基于 sleep 的同步。

## 排除项

- ACP、TUI、MCP、Context Engine、评测、OpenTelemetry。
- 配置文件格式与优先级；`cmd/och` 只接受 flag。
- 进程监管、重启策略、守护进程化与日志策略。
- 多 host、多工作区、多租户装配。
- 生成式组合文档。
- **`Approver` 以外的固定审批器**：未设置时仍 fail closed（`DenyApprover`）。交互式审批在 `ServeACP` 把 ACP 服务器装进槽位之后到来。
- GA 阻塞项：无长时运行装配的 soak 测试，无进程级崩溃注入，无对真实 provider 的验证，无装配路径的性能刻画。
