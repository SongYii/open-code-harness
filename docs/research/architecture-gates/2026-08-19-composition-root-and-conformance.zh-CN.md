# 组合根与跨 Adapter 一致性架构门（中文阅读版）

**状态：** 完成的研究证据

**日期：** 2026-08-19

**范围：** Slice 5（组合根与跨 adapter 一致性）的一手来源验证。记录对照集在当时公开状态下的装配拓扑与合同测试拓扑，确认在 ACP adapter 之前是否必须先做一个"集成收口"切片，并固定在严格分层的 Go 模块中引入组合根的采纳/拒绝边界。

本文件是研究证据。它不改变任何已实现合同，也不授权复制参照项目的类型、包布局或运行时。

英文版本 [2026-08-19-composition-root-and-conformance.md](2026-08-19-composition-root-and-conformance.md) 是规范记录；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。

## 问题

1. 在 Slice 2–4（SQLite 规范 EventStore、JSONL 审计副本、Runtime Host）落地之后，下一步正确的工作是集成收口切片，还是直接开始里程碑 6（ACP v1）？
2. 被复验的项目是否在**单一命名的组合根**处装配各子系统？这个根是一个正式产物，还是一个顺带存在的 `main`？
3. 它们是否用**同一套 adapter 中立的合同套件**去跑同一个端口的**多个**实现，包括持久化实现？
4. 针对进程内 double 编写的合同套件，套用到真实传输 adapter 上时是否依然成立，还是必须拆分？
5. 哪些装配形态与宪章的依赖规则冲突，必须拒绝？

## 已验证的一手来源

均于 2026-08-19 从官方仓库观察：先将各自默认分支解析到一个提交，再读取该提交的代码树与文件。提交是观察到的状态，不代表背书。

| 来源 | 观察状态 | 装配与一致性入口 |
| --- | --- | --- |
| [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) | `99f6f02`，TypeScript/Cordis，2026-08-17 | `apps/cli/composition.md`、`examples/headless-agent/composition.md`、`examples/acp-agent/composition.md`、`packages/boot/app-boot`、`packages/boot/cmdline`、30+ 个 `tests/loader-composition.spec.ts` |
| [OpenAI Codex](https://github.com/openai/codex) | `d35e549`，Rust，2026-08-19 | `codex-rs/cli/src/main.rs`、`codex-rs/app-server/src/main.rs`、`codex-rs/code-mode-runtime/src/service_contract_tests.rs`、`scripts/mcp_conformance/` |
| [Pi](https://github.com/earendil-works/pi) | `59a71b2`，TypeScript，2026-08-18 | `packages/agent/src/harness/session/testing/conformance.ts`、`packages/session-backends/sqlite-node/test/conformance.test.ts`、`packages/telemetry/src/testing/conformance.ts` |
| [Kimi Code](https://github.com/MoonshotAI/kimi-code) | `cdaa80b`，TypeScript，2026-08-18 | `apps/kimi-code/src/main.ts`、`packages/acp-adapter/test/e2e-happy-path.test.ts`、`packages/acp-server/test/e2e-turn.test.ts` |
| [Grok Build](https://github.com/xai-org/grok-build) | `d92c5b0`，Rust，2026-08-19 | `crates/codegen/xai-grok-pager/tests/leader_pty_e2e/`、`crates/codegen/xai-crash-handler/tests/integration.rs` |
| [Maka](https://github.com/maka-agent/maka-agent) | `8ea593a`，TypeScript，2026-08-19 | `packages/runtime-host/src/__tests__/bootstrap-runtime-policy.test.ts`、`apps/desktop/src/main/__tests__/bootstrap-selection-lease.test.ts` |

早先架构门引用的 `badlogic/pi-mono` 现已 301 重定向到 `earendil-works/pi`。早先各门中的 Pi 引用应按新地址理解；这是仓库迁移，不是 fork。

[DeepSeek-Reasonix](https://github.com/esengine/DeepSeek-Reasonix) 仍属社区来源，非权威，仅作背景。

## 生态收敛

被验证的每个项目，跨三种语言、三种架构，都收敛到同样的两条性质。**本仓库目前这两条都不满足。**

1. **存在且仅存在一个具名的地方用来装配具体实现，而且它是一个正式产物，不是偶然产物。** DeepSeek Harness 走得最远：每个可交付物都带一份 `composition.md`，内含由 `scripts/gen-doc-graphs.ts` 生成的依赖图，并标注 `do not edit by hand`。Codex 把根命名为二进制入口（`codex-rs/cli/src/main.rs`、`codex-rs/app-server/src/main.rs`）。Kimi Code 用 `apps/kimi-code/src/main.ts`。Maka 在 `bootstrap-runtime-policy.test.ts` 里直接测试它的根。
2. **合同套件由核心导出，并被该端口的每一个实现消费，持久化实现也不例外。** Pi 与本仓库的设计最接近：`packages/agent/src/harness/session/testing/conformance.ts` 导出 `createSessionBackendConformance(fixtureFactory)`，而 `packages/session-backends/sqlite-node/test/conformance.test.ts` 用一个建立在临时目录上的真实 SQLite repository 去调用它。Pi 在 telemetry 上用了同样的形态。

DeepSeek Harness 还有第三条性质，本门记录但不整体采纳：每个包都有 `tests/loader-composition.spec.ts` 约定，即**把该包放进邻居子系统的真实装配里**去验证。观察到的 `packages/llm/llm-retry/tests/loader-composition.spec.ts` 会先构造一个含 `AgentRegistry`、`AgentLoop`、`LlmRuntime`、`SessionStore`、`SystemPrompt`、`ToolRuntime` 的真实 `Context`，再验证重试行为——该包从不只对着 mock 验证。

## 观察到的合同与边界

### C1. 组合根是被测单元，不只是一个程序

Maka 的 `bootstrap-runtime-policy.test.ts`、`bootstrap-selection-lease.test.ts`，以及 DeepSeek 的 `loader-composition.spec.ts` 家族，都说明装配逻辑是被直接断言的。一个只能靠启动进程才能触达的根，无法被本仓库现有的 race、scenario 和依赖边界门覆盖。

### C2. 持久化后端跑与内存实现相同的套件

Pi 的 SQLite session 后端原封不动地跑导出的一致性套件。本仓库在 `eventstoretest` 上已经做到了这点——`adapters/sqlite/conformance_test.go` 用真实 `Open` 出来的 store 调用 `eventstoretest.Run`——但 `enginescenariotest` 没有：它今天只在 `application/scenario_test.go` 里对 `adapters/memory` 跑。

### C3. 传输 adapter 需要一套传输能够表达的合同

Codex 把 `code-mode-runtime/src/service_contract_tests.rs`（树内合同）与 `scripts/mcp_conformance/`（对外部规范的一致性，经 `codex_conformance_adapter.py` 驱动 `regression-baseline-v1.json`）分开。两者不是同一套套件，因为可观察面不同。

这一条在本切片里是承重的。`engine/modeltest.Config` 暴露了 `ReturnNilStream`、`ReturnStreamOnStartupError` 和 `CloseError`。这些旋钮描述的是一个进程内 Go 值如何从 `Stream` 和 `Close` 返回；你无法要求一个 HTTP adapter 返回 nil stream。`modeltest.Run` 的七个子测试中，四个与传输无关（有序事件投递、流中错误、取消阻塞、并发独立流），一个可表达但错误身份会变（启动错误），两个是 double 专属（启动流/nil 配对、Close 记账）。

### C4. 端到端覆盖在每种规模上都存在，且与单元层分离

在上述观察提交上统计路径名匹配 `e2e`/`integration`/`conformance` 的数量：Grok Build 240、DeepSeek Harness 153、Kimi Code 99、Maka 49、Codex 25、Pi 11。比例随架构差异巨大，但**没有任何一个项目是零**。本仓库目前有 0 个测试把 Application、持久化 store、传输 provider adapter、工作区工具和 Runtime Host 装配在一起。

## 被拒绝的形态

### R1. 插件内核或依赖注入容器

DeepSeek Harness 通过 Cordis 插件与 loader 组合；Maka 和 Kimi Code 用包级 DI。宪章拒绝插件内核，此前每个切片也都重申了这一拒绝。**采纳**：读配置、按固定顺序调用构造函数的组合根。**拒绝**：注册表、服务定位器、基于反射的容器、插件加载器。

### R2. 生成式组合文档（暂缓）

DeepSeek 生成的 `composition.md` 是对"装配文档与装配代码漂移"这一问题目前观察到的最强答案。本切片拒绝它**仅出于顺序原因**：要生成图，先得有一个稳定的装配。它被记录为组合根存在之后的候选项，并且是本门在**本仓库自身 README 上观察到的同类漂移**的推荐解法。

### R3. 放宽依赖守卫、允许 Application 导入 adapter

"任何生产包都不导入 adapter"这条严格规则，正是让端口成为真端口的性质。根必须是唯一例外，而且该例外必须由现有架构守卫**强制执行**而非仅仅写在文档里，否则守卫会悄悄退化为"只要有一个包能导入，谁都能导入"。

### R4. 验证路径中调用真实 provider

Kimi Code 把 `real-llm-smoke.e2e.test.ts`、DeepSeek 把 `e2e.yml` 和 `pi-ai-provider-e2e.yml` 都作为独立通道而非默认门禁。无密钥验证已经是本仓库对 Provider adapter 的既定规则，予以保留：装配测试通过本地 `httptest` 服务器回放既有 SSE fixture 来驱动 `openaicompat`，走真实的 HTTP 与 SSE 代码路径，不联网、不用凭据。

## 结论

### F1. 集成收口是正确的下一个切片，应在 ACP 之前

六个切片各自已验证，但**联合起来从未被证明过**。里程碑 6（ACP v1）要把一个可用的装配暴露在协议之上；如果对着一个从未装配过的技术栈去写该 adapter，就等于把第一次端到端集成放进协议一致性工作里，而那是最难归因失败的地方。被验证的每个项目在其协议表面之下都有一个已装配的根——Codex 的 `app-server`、Kimi 的 `acp-server`、DeepSeek 的 `examples/acp-agent`。**采纳：先收口集成。**

### F2. `enginescenariotest` 必须对 SQLite adapter 运行

依据 C2，并直接类比 Pi 的 SQLite 后端运行导出的 session 一致性套件。该套件的 `Harness` 已经接受任意 `application.EventStore`，所以这是在 adapter 测试里新增一个工厂，而不是改套件。任何只在内存 adapter 上成立的行为，都是本项揭示出的缺陷。

### F3. 在 `openaicompat` 能消费之前，`modeltest` 必须拆分

依据 C3。单一套件无法同时服务进程内 double 与 HTTP adapter，除非削弱 double 的记账检查，或逼 HTTP adapter 伪造它造不出来的条件。**采纳 Codex 的分离方式**：一套任何 `engine.Model` 都满足的传输中立合同，加一套只有进程内实现才运行的 double 记账套件。

### F4. 组合根是库 + 薄二进制，并且它是被测试的

依据 C1 与 R1。只有 `main` 包的话，现有门禁无法覆盖。根是一个普通包，返回一个已装配、可关闭的值；`cmd/` 只读配置并调用它。装配由测试断言，而不是靠跑起一个进程。

### F5. 依赖守卫必须变得更精确，而不是更宽松

依据 R3。架构测试目前禁止每个受管包导入 adapter。它必须新增一个显式的 composition owner，允许其导入全部 adapter，同时其他所有包保持禁止。**"没有 owner"必须继续意味着"禁止"**，这样新增包才不会悄悄继承该例外。

### F6. 采纳清单

1. 集成收口排在 ACP 之前。
2. 一个具名组合根包；其上一个薄 `cmd/` 二进制。
3. `enginescenariotest` 同时对 SQLite 和 memory 运行。
4. `modeltest` 拆为传输中立套件与 double 专属套件；`openaicompat` 跑前者。
5. 一个装配测试覆盖 Application、SQLite、经 `httptest` 的 `openaicompat`、`workspacefs`、`localexec` 与 `runtime.Host`。
6. 无密钥、无网络的验证。

### F7. 拒绝清单

1. 不引入插件内核、容器、服务定位器或基于反射的接线。
2. 本切片不做生成式组合图。
3. 依赖守卫的放宽不超过一个被强制执行的 owner。
4. 默认验证路径中不调用真实 provider。
5. 不新增端口、事件或合同变更：本切片只增加装配并重新分配既有套件。若需要改合同，那是要上报的**发现**，不是本切片内要做的**改动**。

## 证据边界

- 仓库代码树与文件内容读取于上表所列提交。行为由源码与文件布局推断，未执行任何参照项目。
- C4 的路径计数是对路径名的匹配，不是对测试层级的语义普查；它只能确立"存在性"与大致规模。
- Pi 的一致性套件按形态阅读（`createSessionBackendConformance` 及其 SQLite 消费方），其逐条断言未做审计。
- DeepSeek Harness 的 `composition.md` 作为生成产物阅读，生成器 `scripts/gen-doc-graphs.ts` 未读。
- 不对任何项目的私有或未发布实现作出断言。本门说某项目"做了某事"，含义是**所观察的那个提交包含所引用的路径**。
