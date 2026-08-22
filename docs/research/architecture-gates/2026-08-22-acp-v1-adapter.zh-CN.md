# ACP v1 Adapter 架构门

**状态：** 完整研究证据

**日期：** 2026-08-22

本文档是英文正本
[2026-08-22-acp-v1-adapter.md](2026-08-22-acp-v1-adapter.md)
的中文同步阅读版；两份副本如有分歧，以英文正本为准。

**范围：** 里程碑 6（ACP v1 adapter 与一致性测试）的一手来源验证。记录
Agent Client Protocol 当时的公开协议面，以及对照集中各 agent 端适配器拓扑；
确定第一个 adapter 面向的协议版本、传输适配器在本仓库严格分层 Go module
中的位置、ACP session 到已实现 Session/Turn 状态机与 EventStore 重放的映射、
Policy Decide 表到 `session/request_permission` 的桥接方式，以及无密钥验证
ACP agent 的方法。

本文档是研究证据。它不改变任何已实现合同，也不授权复制参考项目的类型、包
布局、schema 或运行时。

## 问题

1. ACP 对 agent 一侧有什么要求？里程碑 6 的最小正确基线是什么？
2. 第一个 adapter 应面向哪个协议版本——v1 还是较新的草案 v2？
3. 各已验证实现如何把 ACP 的 session、prompt、流式更新与 turn 结算映射到
   由持久事件支撑的内部会话/turn 模型上？哪种映射契合本仓库的 Session/Turn
   状态机、EventStore 固定读重放与 Runtime Host？
4. 传输适配器在本仓库分层中的位置在哪里？谁引用它？这对依赖守卫意味着什么？
5. 各已验证实现如何把工具权限决策桥接到 `session/request_permission`？当
   客户端失败、取消或始终不响应时会发生什么？
6. 各实现在无网络、无凭据条件下如何验证一个 ACP agent？

## 已验证一手来源

全部于 2026-08-22 从官方仓库观察：将各默认分支解析到具体 commit 并通读该
commit 的文件树（经 `scripts/fetch-reference.sh`）。commit 只是观察状态，
不是背书。

| 来源 | 观察状态 | ACP 入口 |
| --- | --- | --- |
| [agentclientprotocol/agent-client-protocol](https://github.com/agentclientprotocol/agent-client-protocol) | `83dad56`，Rust schema crate + MDX 文档 | `docs/protocol/v1/*.mdx`、`docs/protocol/v2/*.mdx`（草案）、`schema/`、官方 Rust 与 TypeScript 库；社区注册表在 `docs/libraries/community.mdx` |
| [zed-industries/codex-acp](https://github.com/zed-industries/codex-acp) | `296069e`，Rust | `src/codex_agent.rs`、`src/thread.rs`、`src/lib.rs`；README 横幅声明开发已迁至 `agentclientprotocol/codex-acp` |
| [MoonshotAI/kimi-code](https://github.com/MoonshotAI/kimi-code) | `d4e0ad4`，TypeScript | `packages/acp-server/`（经 klient 门面接引擎 v2）、`packages/acp-adapter/`（经 SDK 门面接引擎 v1）、`test/e2e-turn.test.ts` |
| [deepseek-ai/deepseek-harness](https://github.com/deepseek-ai/deepseek-harness) | `b150a55`，TypeScript/Cordis | `packages/acp/acp/src/index.ts`、`examples/acp-agent/cordis.yml`、`packages/acp/acp/tests/harness.ts` |

两个命名事实影响后续引用。规范仓库已从
`zed-industries/agent-client-protocol` 迁至 `agentclientprotocol`
组织（旧 API 路径返回 HTTP 301）；早前架构门对旧地址的引用应按新地址解读。
另外 `codex-acp` 自身 README 标明它是 Zed 的遗留适配器，活跃开发在新组织里
基于 Codex 的 App Server 继续；本架构门把它当作"ACP 到核心翻译层"最完整的
公开样本阅读，而非生态现状。

社区 Go 库确实存在——`coder/acp-go-sdk`、`ironpark/acp-go`、
`eino-contrib/acp`、`spachava753/acp-sdk`，均列于规范自带的注册表。
本架构门未拉取、未验证其中任何一个。

## 生态收敛

所有已验证实现横跨两种语言、三种核心架构，收敛于同一组性质。

1. **今天所有人都在服务 v1 线协议；v2 存在但其作者自己给它上了闸。** v2
   迁移指南称 v2 是"整合性发布"，其"协议面整体仍标记为草案"，并要求实现者
   按连接协商版本、同时保持 v1 可用："放弃 v1 的 Agent 会把自己与现有
   Client 切断。" Kimi Code 的两个包分别钉住 `@agentclientprotocol/sdk
   ^0.23.0`（v1 时代）与 `^1.3.0`；codex-acp 钉住
   `agent-client-protocol =0.14.0`。
2. **适配器是两个事件系统之间的翻译层，自身不含业务逻辑。** codex-acp 在
   ACP 请求与 codex-core 的 `Op` 提交 / `Event` 流之间翻译；Kimi 的
   acp-server 通过纯映射模块（`convert.ts`、`events-map.ts`）在 ACP 方法与
   引擎事件（`assistant.delta`、`tool.call.*`、`turn.ended`）之间翻译；
   DeepSeek 的插件只在 ACP 与已提交的 session 日志事件之间翻译。
3. **Turn 结算来自内部的回合结束信号，而不是超时启发式。** Kimi 只依据
   `turn.ended` 结算；codex-acp 在 `TurnComplete`/`TurnAborted` 时 resolve
   prompt。DeepSeek 是例外：经过静默闸门（admission 完成 +
   `agent.whenIdle()` + 有序输出排空）后才 resolve。
4. **stdout 纪律是结构性强制的。** codex-acp 以
   `#![deny(clippy::print_stdout, clippy::print_stderr)]` 编译；DeepSeek 的
   ACP 组合禁止 stdout logger，因为"stdout 承载 ACP JSON-RPC"；Kimi 在任何
   代码运行前把 `console.*` 重定向到 stderr。
5. **验证方式是：真实 JSON-RPC 帧协议跑在内存双工流上，对着真实组装的引擎，
   只有模型被替换。** 没有任何已验证项目把 stdio 子进程放进默认测试门禁；
   启动真实二进制属于独立的快照通道（DeepSeek）。

## 观察到的合同与边界

### C1. 传输帧协议小而严格

ACP v1 是换行分隔的 UTF-8 JSON-RPC 2.0，消息不得内嵌换行。agent 不得向
stdout 写入任何非 ACP 内容（`transports.mdx`："MUST NOT write anything to
its `stdout` that is not a valid ACP message"），可向 stderr 记日志，且应
支持 stdio。规范同时允许保留消息格式的自定义传输——这正是所有已验证项目
测试时使用的缝隙。

### C2. 生命周期是 initialize → authenticate? → session/new | load | resume → prompt 回合

`initialize` 协商 `protocolVersion`（v1 基线：`"protocolVersion": 1`）并
交换能力对象。`session/new` 接收 cwd 与 MCP server 列表，返回 sessionId。
`session/load` 恢复既有 session，并在响应之前以 `session/update`
通知重放历史；`session/resume` 恢复但不重放。四个已验证实现都宣告支持
load，且都从自己的持久日志重放：codex-acp 经 `replay_history` 重发 rollout
条目；Kimi 的 acp-server 把存储的上下文历史投影为
`projectHistoryToSessionUpdates`（load 与 resume 之间"唯一的差异点"）；
DeepSeek 的会话基于持久 inbox，历史本来就是已提交日志内容。

### C3. 一个 prompt 请求恰好结算一次，带停止原因

`session/prompt` 请求以 `StopReason`（`end_turn`、`cancelled`、`refusal`
等）结束。回合期间的一切——消息块、思考、工具调用、计划、用量——都以
`session/update` 通知到达。同一 session 上的并发 prompt 被确定性拒绝：
Kimi 经 `assertNoActiveTurn()` 主动以 `-32600` 拒绝，因为"否则引擎会悄悄
排队"；DeepSeek 从单个每会话槽位（同步预留）返回
`invalidParams("a prompt is already in flight for this session")`；
codex-acp 经单 actor 信箱把每会话工作串行化，重叠根本不会出现。

### C4. 权限桥接处处失败关闭（fail-closed）

工具审批以反向 RPC 方法 `session/request_permission` 跨线传输，携带类型化
选项（`allow_once`/`allow_always`/`reject_once`/`reject_always`）和待审批
的工具调用供展示；回合取消时客户端必须回答 `"cancelled"` 结果。每个实现
的默认值都是拒绝：

- Kimi：任何 RPC 失败都映射为 `{decision:'rejected'}`；`approve_always`
  变成引擎里的会话级允许规则。
- codex-acp：取消/未选择的结果默认 `ReviewDecision::Abort`；不支持的
  elicitation 自动拒绝。
- DeepSeek：审批缝隙在 fail-closed 瀑布上组合应答者——策略为 `'never'`
  时每次询问都不提示直接拒绝；无人应答的 `'ask'` 链落到 `'unavailable'`；
  且 ACP 应答者"绝不从未知的客户端响应推断持久授权"，只贡献一次性选择。

### C5. 错误卫生：内部信息不上线

Kimi 把鉴权失败（prompt 发起被拒和 turn 失败两条路径都算）映射为
`auth_required` 让客户端驱动重新登录，busy 映射为 `-32600`，其余一切固定为
`-32603 "session prompt failed"`，其 e2e 明确断言原始引擎消息永不泄漏。
未知 sessionId 只在请求上报错（`invalid_params`），在通知上被吞掉
（`session/cancel`）。codex-acp 使用类型化错误构造器，通知发送失败记日志
而不致失败。销毁时把进行中的 prompt 结算为 cancelled，保证拆解后没有客户
端请求悬挂。

### C6. 取消有三个窗口，且全都被处理

回合前（任何模型调用之前）、发起竞态（turn id 未知时取消到来——Kimi 缓冲
早期 turn 事件、置位 `cancelRequested`、先发未定址取消、id 落地后再补发定
址取消）、活跃回合中（`session/cancel` / `$/cancel_request` 汇入同一条内
部取消路径）。之后 prompt 响应携带 `stopReason: cancelled`。

### C7. 无密钥验证 = 真实帧协议 + 真实引擎 + 脚本化模型

Kimi 的旗舰用例 `e2e-turn.test.ts` 启动"完整 agent-core-v2 引擎和真实 ACP
线协议（内存流上的 ND-JSON）……只伪造网络 LLM 调用"：脚本化 provider 通过
遮蔽 DI 种子注入，服务器运行在交叉连接的 PassThrough 对后面的
`runAcpServerWithStream` 上，裸 NDJSON 测试客户端断言精确的通知序列、权限
桥接、`-32600` 并发拒绝、错误卫生，以及三个窗口的取消。DeepSeek 的
`tests/harness.ts` 用一句话概括了同一模式："In-memory ACP transport
fixture over the real agent factory and loop." 启动构建产物的快照套件对着
录制转录本回放，是另一条独立的无密钥通道（record 模式才使用线上 API）。

## 被拒绝的形态

### R1. 把适配器耦合到别的项目的核心上

codex-acp 按标签（`rust-v0.137.0`）钉住十三个以上的 `codex-rs` crate，
并整体 vendor 了一个。这种耦合对 Zed 是正确的——Codex 核心不属于它——但
与本仓库无关：我们拥有自己的核心，adapter 必须落在自己的端口上。vendor
形态没有任何可采纳之处。

### R2. 先做 v2

迁移指南自己就把 v2 标为草案，并要求以协商方式并行交付。里程碑 6 只面向
v1；设计不得排除日后加入 v2 协商的可能，但本切片不依赖任何 v2 面。

### R3. 整体采纳未经验证的社区 Go SDK

规范注册表列出四个 Go 库；本次无一在具体 commit 上审计过，而帧协议合同
（C1）小到可以自有。切片究竟是"验证并钉住其一"还是"在端口后自持最小
codec"，由聚焦规范决策，参考依赖规则：无论哪种，选择都必须钉死，一致性
必须对照规范自身的 schema 校验，而不是信任一个移动中的库。

### R4. 基于静默的结算

DeepSeek 在整个 agent 空闲加输出排空之后才 resolve prompt。这把 steering
与自治工作折叠进同一个响应——性质有趣，记录在此——但结算边界变成活性
启发式而非持久事实。本仓库的 turn 以 `turn-ended` 事件提交为准；结算采用
Kimi/codex 规则：在事件上恰好 resolve 一次。

### R5. 业务逻辑进入适配器

模式目录、slash 命令语义、权限预设住在适配器代码里（codex-acp 的预设映射、
Kimi 的 slash 意图处理均属此类）——在本仓库，这些属于 Application 持有的
Step loop 和 Policy Decide 表。适配器负责翻译，不做决策。

## 结论

### F1. 里程碑 6 面向 ACP v1

依 C2 与 R2：服务 `protocolVersion: 1`，诚实地向下协商，并把 codec 结构
设计成日后可以增量加入 v2 协商。最小正确基线是：initialize（有凭据则
authenticate）、session/new、带重放的 session/load、带更新流的
session/prompt、session/cancel，以及 `session/request_permission` 桥接。
Session 模式、配置项、终端、elicitation、slash 命令宣告都是可选的 v1 面，
不在第一片范围内。

### F2. 适配器是与其它 adapter 同等对待的新传输包

`adapters/acp` 包只消费 Application/Runtime 端口，不引用其它 adapter，并且
只被唯一的生产包引用：组合根。Slice 5 的依赖守卫已经强制了这个形状——一
个具名 owner，缺席即禁止——所以守卫无需放宽；聚焦规范须确认新包落在现有
例外之内，而不是制造第二个例外。

### F3. Session 映射我们的 Session 身份；load 即重放；结算就是 turn-ended 事件

EventStore v2 已提供身份所有权、摘要链追加、分页和固定读——正是 C7 式重
放需要的基底。映射：ACP `sessionId` ↔ 持久 Session 身份；`session/load`
经固定读重放已提交历史并投影为 `session/update` 通知后再响应；
`session/prompt` 提交既有 Application 命令；turn-ended 事件提交时请求恰好
resolve 一次，按 F5 映射停止原因。DeepSeek 式静默结算被拒（R4）。

### F4. 并发拒绝是确定性且局部的

Engine 的 step loop 每个 session 持有一个 turn，因此第二个并发
`session/prompt` 在触碰引擎之前就被本地同步拒绝并返回 invalid-request 类错
误——即 Kimi 的 `assertNoActiveTurn` 形态，而非排队。

### F5. 停止原因来自已实现的结果代数

已实现的 Engine/Application 结果代数已经区分完成、取消与阻断/拒绝类终局。
到 ACP 停止原因（`end_turn`、`cancelled`、`refusal`）的映射是适配器内的
固定全函数；未知结局走 JSON-RPC 错误通道，绝不编造停止原因。

### F6. 权限桥接是 Policy Decide 的薄而 fail-closed 的投影

Policy Decide 的 `ask` 结局变成每次询问一条 `session/request_permission`，
最少提供 `allow_once`/`reject_once`；传输失败、客户端取消与拆解一律默认
拒绝（C4）。always-allow 的作用域映射等到 Policy 合同获得作用域规则之后再
做——记录为候选，不在本切片内改动合同。

### F7. 验证扩展 Slice 5 的组装测试，保持无密钥

e2e 门禁在完整组合根周围——SQLite 存储、工作区工具、Runtime Host、经
`httptest` fixture 的脚本化 provider——通过内存双工对话真实的换行分隔
JSON-RPC，断言上述 C2/C3/C4/C5/C6 行为。真实二进制冒烟（构建二进制、启动、
驱动一个回合）是单独的非门禁检查。默认门禁路径无网络、无凭据、无子进程
（R6 维持）。

### F8. 采纳清单

1. 只面向 ACP v1；让 v2 保持"设计上可增量"。
2. 新 `adapters/acp` 传输包位于既有端口之后；仅由组合根引用；依赖守卫不变。
3. 最小基线：initialize、按需 authenticate、session/new、带固定读重放的
   session/load、带流式更新的 prompt、cancel、权限桥接。
4. 在已提交的 turn-ended 事件上恰好结算一次；并发 prompt 本地拒绝。
5. Policy Decide 的 fail-closed 权限投影。
6. 固定的停止原因与错误映射，内部信息零泄漏。
7. 围绕真实组装、经内存 NDJSON 双工的无密钥 e2e；可选的真实二进制冒烟通道。

### F9. 拒绝清单

1. 不做 v2 面、不做静默结算、业务逻辑不进适配器。
2. 不整体采纳未验证的社区 SDK；任何依赖都要钉死并对照规范 schema 做一致性
   校验。
3. 不新增端口、不改合同：adapter 暴露的是 Slice 1–5 已实现的东西。若暴露
   需要合同变更，那是聚焦规范的发现，不是静默修改。
4. 默认验证路径中没有在线模型调用、没有子进程。

## 证据边界

- 仓库文件树均在所列 commit 上读取；行为从源码与测试推断，未执行任何参考
  项目。
- 规范以文档加 schema 布局的形式在 `83dad56` 阅读；生成的 JSON schema 未与
  官方库逐一比对。
- 社区 Go SDK 仅作为注册表条目引用；未拉取、未审计、未审阅许可证。
- ACP v2 仅经迁移页阅读；v2 草案面未经审计。
- codex-acp 为 Zed 遗留适配器；后继仓库未检视。
- 对任何项目的私有或未发布实现不作任何断言。
