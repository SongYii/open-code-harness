# Evaluation 架构调研门

**状态：** 调研证据完成

**日期：** 2026-09-01

**范围：** `docs/README.md` 里程碑 10 仍把「场景评测、基准测试和 OpenTelemetry」写成一项未设计条目。
[2026-09-01 roadmap 门](2026-09-01-context-engine-evaluation-observability-tui.md)
只在包入口深度扫过 Evaluation，并写明 Context Engine、Evaluation、Observability、TUI
各自仍需要一份专属架构调研门才能进入正式设计。Context Engine 已经走过那份专属门、设计和已实现合同。
本文是 Evaluation 的专属调研门。

本文按当时仍公开的一手资料重新核验 agent 质量评测的实现，对照本项目已经有的路径，
记录这些资料实际支持的约束，并把剩余的产品定位选择明确留给后续设计。本文不设计包布局、
scenario 语法、CLI 开关或实施计划。OpenTelemetry 明确不在范围内：它仍是里程碑 10 里未设计的
剩余项，需要自己的调研门。

英文版本 [2026-09-01-evaluation.md](2026-09-01-evaluation.md) 是规范文本；本文是与之同步的中文阅读版。两者若有分歧，以英文为准。

## 对照集与钉住的 commit

按文档规则第 8 条，下面的仓库引用都来自 `./scripts/fetch-reference.sh --list` 取得的、gitignored
的 `.reference/` 检出。当前 OpenAI 产品文档是明确的例外：观察日直接读取了官方
`developers.openai.com` Markdown。按文档规则第 7 条，本文**不**沿用 2026-09-01 roadmap 门的
Evaluation 小节：除 `earendil-works/pi` 外，官方对照集的检出都已前移，而且那次只读到 README/包入口。

官方六个项目仍然必读。另外抓了五个评测原生仓库，因为只靠六个项目回答不了本门必须结算的架构边界问题
（评本 harness 的产品回归，还是做一个能跑任意 agent 的通用 runner）。

| 项目 | 仓库 | Commit | 观察日期 | 为何被读 |
| --- | --- | --- | --- | --- |
| Pi（agent core 源码） | `badlogic/pi-mono` | `b8b873b` | 2026-09-01 | `packages/evals`——真实 `AgentSession` 适配、隔离临时目录、原生 session 产物。`earendil-works/pi` 仍停在 `853a80d`（2026-08-28），不是 eval 真源；evals 在本仓库。 |
| Maka | `maka-agent/maka-agent` | `afbcabd` | 2026-09-01 | `packages/eval`——Experiment/Cell/Attempt、Subject/Executor 分离、append-only 存储、`subject_failed`/`infra_failed`/`indeterminate`、Maka 与外部 subject |
| DeepSeek Harness | `deepseek-ai/deepseek-harness` | `dd6322d` | 2026-09-01 | 重新核验三行 `BENCHMARK.md` 这一负面发现 |
| Codex | `openai/codex` | `67cc3c3` | 2026-09-01 | 重新核验没有 agent 质量评测包 |
| Kimi Code | `MoonshotAI/kimi-code` | `ab565e0` | 2026-09-01 | 重新核验没有 agent 质量评测包 |
| Grok Build | `xai-org/grok-build` | `bb7f39d` | 2026-09-01 | 没有独立离线 eval 包；在线目标 evaluator、证据收集和对抗式 skeptic 验证可作为 judge 设计先例 |
| Harbor | `laude-institute/harbor` | `e348ba3` | 2026-09-01 | 通用 agent/benchmark runner：Task + Verifier、artifact manifest、不重跑 agent 即可 regrade、大量已安装 agent 适配器 |
| Terminal-Bench | `laude-institute/terminal-bench` | `d28711d` | 2026-09-01 | Task 数据集形状（instruction + tests + oracle）；其 README 现已让新用户改用 Harbor |
| Inspect AI | `UKGovernmentBEIS/inspect_ai` | `84e512d` | 2026-09-01 | Task/Sample/Scorer 分层、可恢复 eval set、失败样本重试、离线 `score(log)`、时间/token/cost 限额 |
| OpenAI 官方 Evals 与 trace grading | `developers.openai.com/api/docs/guides/evals` 和 `trace-grading` | 当前文档 | 2026-09-01 | 当前 dataset/testing-criteria/run 模型、端到端 agent trace 评分，以及已公布的 Evals 平台退役边界 |
| vitest-evals | `getsentry/vitest-evals` | `aa34b64` | 2026-09-01 | Pi 实际适配的 harness-first 合同；基础设施断言与 judge 分离；可配置 CI 门槛 |
| OpenAI Evals（旧仓库） | `openai/evals` | `8eac7a7` | 2026-09-01 | 数据集版本化、Recorder、模型打分模板——作为有状态 session runner 的反面教材核验 |

## 本项目已经有的东西

没有评测 runner。`internal/harness` 没有 `eval` 包；章程里点名了 `EvaluationResult`
（`docs/superpowers/specs/2026-08-11-open-code-harness-architecture-design.md:140`），
代码里既不是领域类型，也不是 event 或投影。`docs/README.md` 里程碑 10 仍是「尚未设计」。
架构守卫的 ownership 表（`internal/harness/architecture/dependencies_test.go`）没有 eval 所有者。

执行路径并不是从零开始。

- **章程已经定义了 Eval Runner 是什么。** §6.9
  （`2026-08-11-open-code-harness-architecture-design.md:213-225`）：runner
  「直接驱动应用层或 headless Engine，不依赖 TUI」；ACP 测试是独立黑盒 conformance 层；
  分类里已经有领域单元测试、Provider 合同、Tool/Policy 安全、Context fixture、确定性回放、
  ACP/MCP conformance、中断/恢复测试、**真实仓库 scenario eval**、以及质量/成本/延迟/稳定性基线。
  已实现的部分多数已有 `go test` 覆盖，但 MCP 仍只有设计、尚未实现，因此 ACP/MCP 类别并未完成。
  真实仓库 scenario eval 和回归基线仍不存在。
- **§10.2 把 `Evaluator` 列为社区扩展面**，与 Provider、MCP server 配置、Policy、Observer 并列
  （`2026-08-11-open-code-harness-architecture-design.md:289`）。章程没有定义这个扩展面的粒度，
  因此仅凭这一行不能裁定它是 OCH-only evaluator，还是包含外部 Subject 的边界。§4 的确禁止
  尚无真实消费者的扩展点
  （`2026-08-11-open-code-harness-architecture-design.md:92`）。
- **真实 Application/Session 路径已经能无网络跑通。**
  `internal/harness/composition/end_to_end_test.go:24-34`
  （`TestAssemblyRunsAToolCallingTurnEndToEnd`）对真实 SQLite 文件调用 `composition.Open`，
  OpenAI-compatible adapter 打本地回环 fixture 服务器，加上 workspace fs 和真实 Policy Decide 表。
  `CreateSession` + `RunTurn` 就是 §6.9 说的 headless loop。未来若为评测再绕 Engine 或 Provider
  造第二条 loop，就是章程已经拒绝的 eval 专用捷径。
- **原生 session 证据已经存在。**
  `docs/architecture/session-transcript.md:15-19`：实验性 `och.session.transcript` JSONL
  是一个 EventStore session 的投影，不是副本，不是提交点，也不能写回。
  `och export-session` 是导出命令。评测不得为 OCH 运行再发明第二套 transcript 格式。
- **Composition 已经冻结了 Subject 会点名的旋钮。**
  `internal/harness/composition/config.go` 带着 Provider
  （`BaseURL`/`ModelID`/`ContextWindow`/`MaxOutput`）、Context 预算百分比、Policy 模式、
  Limits、workspace root 以及沙箱相关开关。还没有把它们包成带版本的「subject 身份」对象。
- **Context Engine 质量是已披露的 GA 阻塞项，不是评测系统的替代品。**
  `docs/architecture/context-engine.md:374-376`：在滚动摘要的真实模型质量评测出现之前，
  该里程碑保持 not GA；当前测试用的是脚本/fixture summarizer。同一份合同还写着
  「No MCP, TUI, OpenTelemetry, or milestone 10 evaluation-runner surface」。
  Context Engine 是未来的一个套件，不是评测系统的包边界。
- **仓库已经区分 PR CI 和 nightly 通道。**
  `docs/README.md:204-210`：PR 上的 `go test` 保持无密钥、无网络；`determinism` 和活链引用检查走 nightly。
  真实模型调用今天不是 PR 门（组合根设计非目标 4：「默认验证路径里不做 live provider 调用」）。

## 官方对照集

### Pi —— 真实 AgentSession、隔离目录、原生产物、仅 live

`packages/evals/README.md:1-4` 写明：该包把真实 `AgentSession` 适配到 `vitest-evals`，
「在隔离的临时项目和 agent 目录里运行」，并附上原生 Pi session 产物。它度量端到端行为，
比较的是「prompts、tools、skills、models 或其他 harness 配置」——Pi 自己的配置，不是外来 agent。

`src/pi-harness.ts` 是适配器。`createPiCodingAgentHarness`（第 246-256 行）每个套件绑定一个 harness。
`runPiCodingAgent`（第 122-151 行）创建 `mkdtemp(.../pi-eval-)`，再建立 `workspace/`、`agent/`，
以及 `sessions/` 下的 `SessionManager`，然后 `createAgentSessionFromServices` 和 `session.prompt`
——产品 session 对象，不是评测替身。删除临时树之前，把原生 session 文件快照成 artifact（第 213-218 行）。
README 第 32-34 行：每次调用打印一个 gitignore 掉的 `.eval/` 目录；`runs.jsonl` 索引已完成运行
及其在 `sessions/` 下的原生 Pi session JSONL。

必须用真实模型。README 第 8-23 行：`PI_PROVIDER` 和 `PI_MODEL` 必须一起给；认证走 Pi 正常的
`ModelRuntime`。`packages/evals` 里没有 fixture-provider 的 CI 通道。对比套件
（`README.md:104-138`）用同一批输入跑多个只在 prompt/tools/skills/model 上不同的 Pi harness；
`judgeThreshold: null` 让低分留作观察，而不是让 Vitest 失败。「硬断言只用于套件不变量和基础设施合同。」

对本项目而言，Pi 是最近的执行路径先例：驱动真实 harness 对象、每次运行隔离、用原生 session 当证据。
它不是通用 agent 框架，也不解决本项目的 PR-CI 通道。

### Maka —— 实验语义在 Runtime 之外；Subject ≠ Executor

`packages/eval/README.md:20-28`：

> `@maka/eval` 拥有实验语义。它自己不执行 Maka，也不构造 Runtime 对象。
>
> `Experiment → Cells → Attempts → Results`
> 由 Runtime Host 执行 Maka subject

一个 Experiment 冻结一个 benchmark、一个 executor、全部 subject、全部 task、重复次数、一份预算、一个 verifier
（`README.md:40-41`；`src/experiment.ts:46-70`）。Cell 是笛卡尔积 `task × repetition × subject`
（`experiment.ts:84-101`）。实验目录里是冻结的 `experiment.json` 和只追加的 attempt 记录；
「没有第二份可变的结果文件」（`README.md:42-43`）。

Subject 和 Executor 是分开的端口（`src/runner.ts:74-130`）。
`SubjectAdapter.execute` 调用一个 cell 的 agent；`ExperimentExecutor.runAttempt` 决定这次 attempt
如何隔离和验证。内置 subject 是 `kind: 'maka' | 'external'`（`experiment.ts:56-61`）。
`createMakaSubjectAdapter`（`src/maka-subject.ts:34-77`）请 Runtime Host 在专用 Host root 里跑一次
它所拥有的执行——Session/Turn 留在 Runtime Host 里。`createExternalSubjectAdapter`
（`src/external-subject.ts:39-43`）跑一条声明的命令，用于已提交的 DeepSeek Harness 臂。

Attempt 只追加（`src/attempt-store.ts:26-80`，`FileAttemptStore.append` 要求 `sequence == last+1`）。
结果状态是 `'completed' | 'subject_failed' | 'infra_failed' | 'indeterminate'`
（`src/result.ts:31-41`）。`isReplaceableAttempt`（第 87-89 行）只允许重试 `infra_failed` 和
`indeterminate`。`selectCellResult`（第 91-95 行）永远取最早一条有效 attempt。README 第 80 行：
`--cell` 替换一个失败或不确定的 cell；「结果选择永远用最早的有效 attempt。」

结果内核只有 score、归一化 usage、可归因 cost、duration、status 和 artifacts
（`README.md:49`；`result.ts:33-41`）。语义设置全部写在 spec 里；环境变量只留给凭证和机器本地路径。

Maka 今天已经能跑外部 harness 臂——已提交的
`experiments/terminal-bench-2.1-deepseek-v4-flash-maka-vs-deepseek-harness.json`
在同一个 task group 里配对 Maka 和 DeepSeek Harness，走 Harbor、Docker 和出网代理。
那是 Maka *当前* 的完整度，不是本项目第一刀该复制的形状。对本项目有承重意义的先例是分离本身：
eval 拥有实验语义；产品 runtime 执行产品 subject；attempt 只追加；subject 失败不是基础设施失败。

### Codex、Kimi Code、DeepSeek Harness —— 已核验的负面发现

按表中 commit 重新核验，不是从 roadmap 门抄来的。

- **DeepSeek Harness** 的 `BENCHMARK.md` 仍是三行：装 Python SDK，跑 `jsonrpc-agent` 变体，
  每个任务用独立 workspace 和 session ID。没有仓库内 runner、打分器或实验存储。
  Maka 的 eval README（第 127-128 行）用自己的话确认了同一件事，同时带着一份 *关于*
  DeepSeek Harness 的 Harbor profile。
- **Codex**：没有 agent 质量评测包。`codex-rs/Cargo.toml` 里唯一的 `eval` 命中是 clippy lint
  `unnecessary_lazy_evaluations`。
- **Kimi Code**：workspace `package.json` 里没有 `eval` 包。

这三个不能当蓝本。它们的 session 日志、微基准或外部 Harbor 适配器都不是评测架构。

### Grok Build —— 没有离线 runner，但有相关的证据优先 judge

Grok Build 没有独立离线 eval crate；Harbor 的已安装适配器也是 Harbor 在测它，而不是 Grok Build
交付了一套通用质量评测子系统。但只把这个包级缺失当作全部结论，会遗漏相关的一手证据。

`xai-grok-shell/src/session/goal_evaluator.rs:7-17` 定义了一个隐藏、无工具的完成度 evaluator。
它读取有界的近期 transcript、目标和可选 plan，把 transcript 内容视为不可信输入，并要求严格
schema 约束的单一决策：`continue`、`candidate_complete` 或 `blocked`，同时给出具体证据、
一个下一步和稳定 blocker key。解析器拒绝未知字段和语义组合错误（第 19-113 行）；transcript
同时有逐项和整体字节上限（第 115-157 行）。

候选完成随后进入 `goal_classifier.rs` 中由 harness 控制的对抗式验证。该阶段捕获一份共享改动
artifact 和完整变更文件列表，对 agent 写出的 final/plan 文本做净化，并把前次缺口传入后续 attempt
（第 1717-1813 行）。skeptic 0 可以给出高置信度的决定性反驳；否则其余冷启动 skeptic 并行运行，
通过需要聚合 quorum（第 1815-1897 行）。每个 skeptic 的细节和聚合结果保留为证据文件/event，
而不是没有出处的文字结论。

这是在线自验证，不是离线 scenario benchmark，因此不是 runner 蓝本；但它直接支持证据优先 judge、
严格结构化 verdict、不可信证据隔离、有界 judge 上下文、独立验证，以及对缺失或矛盾证据保守处理。

## 额外的评测原生来源

### Harbor / Terminal-Bench —— Task + Verifier、regrade、任意 agent

Harbor README（第 10-16 行）写的产品目标是：「Evaluate arbitrary agents like Claude Code、
OpenHands、Codex CLI, and more」，建设 benchmark，并通过 Daytona、Modal 等在数千个环境里并行。
`src/harbor/agents/installed/` 里有 Codex、Pi、Grok Build、Kimi Code、Claude Code 等适配器。
`BaseAgent`（`src/harbor/agents/base.py:25-59`）是 agent 端口；`capabilities` 包括 resume
和原生轨迹加载。

Harbor 的 `Task`（`src/harbor/models/task/task.py:35-50`）是一个目录：`instruction.md`、
`task.toml`、`environment/`、`solution/`、`tests/`。Verifier（`src/harbor/verifier/verifier.py`）
给任务留下的环境打分。`ArtifactManifest`（`src/harbor/models/trial/artifact_manifest.py:6-16`）
记录收集到了什么。`src/harbor/trial/regrade.py:1-12` 写得很直白：regrade 把 agent 阶段替换成
「恢复已记录的输出」，把 `agent/` 和 `artifacts/` 拷进一个新的 trial 目录，再对注入的产物跑 verifier。
「源 trial 永不修改。」能否 regrade 由记录定义：新 verifier 声明的输入必须出现在源 trial 的
artifact manifest 里。

Terminal-Bench（`README.md:21-28, 55-63`）是数据集：instruction、测试脚本、oracle 解答。
它自己的 README 现在让新用户用 Harbor 跑 Terminal-Bench 2.0。

Harbor 证明了「通用 agent 评测平台」是另一种产品：大量 agent 适配器、多种环境后端、容器/云调度。
本项目该吸收的机制更窄：**Task 和 Verifier 分开；证据是一份 manifest；打分不必重跑 agent。**
调度平台不进第一刀。

### Inspect AI —— Task/Sample/Scorer、可恢复集合、离线打分、限额

Inspect 的公开入口（`README.md:3-5`）是一个 LLM 评测框架，带 prompt engineering、工具、多轮对话
和模型打分。代码里的分层是 `eval()`（`src/inspect_ai/_eval/eval.py:118`）覆盖 `Task`
（`src/inspect_ai/_eval/task/task.py:76-80`）覆盖数据集 `Sample`
（`src/inspect_ai/dataset/_dataset.py:29-53`），加上 `Scorer` 协议
（`src/inspect_ai/scorer/_scorer.py:35-40`）。

`score()`（`src/inspect_ai/_eval/score.py:81-94`）给已有的 `EvalLog` 打分——离线重评，不重跑 solver。
`eval_set()`（`src/inspect_ai/_eval/evalset.py:226`）是可恢复的 eval 集合。样本级 `retry_on_error`、
`fail_on_error`、`token_limit`、`time_limit`、`working_limit`、`cost_limit` 都是一等参数
（`eval.py:251-276`；`evalset.py:380-400`）。`design/recover.md` 记录了崩溃后的 `.eval` ZIP
加上 sample buffer SQLite 如何重建已完成但尚未 flush 的样本。

Inspect 证明了 **可恢复 eval 集合、失败样本重试、离线重评、以及每样本资源限额**。
它不是 code-agent harness，也不驱动 OCH session。

### vitest-evals —— harness-first、基础设施 vs judge、CI 门槛

`docs/architecture.md:5-14`：主合同是每个套件一个显式 `harness`、具名测试调用 `run(input)`、
每次一条归一化 `HarnessRun`、可选 judges、以及可选的 Vitest 断言。judge harness 和应用被测 harness
是两个对象（`packages/vitest-evals/src/judges/judgeHarness.ts:45-60`）。

GitHub reporter 门槛（`packages/github-reporter/src/gate.ts:10-26, 52-61`）对合并报告施加可选的
`minPassRate` / `minScoreAverage` / `failOnFailures`。Pi 的 eval README（第 136-138 行）实际使用了
这一分离：基础设施用硬断言；质量用 judge；对比套件记录分数，而不是让这次调用失败。

vitest-evals 是 Pi 嵌入的库。本项目不应引入 Vitest/TypeScript 运行时依赖。该吸收的机制是分离：
**基础设施合同让运行失败；质量分数是观察，放在可配置门槛后面；应用 harness 和 judge harness 不是同一个对象。**

### OpenAI Evals（旧仓库）—— 数据集和模型打分，不是 session runner

`README.md:5-11` 是一份 LLM eval 注册表，外加自定义 YAML/JSONL 数据集。`evals/record.py:1-8`
定义把结果记到本地 JSON 或 Snowflake 的 Recorder。`docs/eval-templates.md:22-40` 是模型打分的
`classify` 模板（把 completion 包进评测 prompt，解析一个选项）。有 completion-function 协议可以跑
prompt chain；没有 Agent Session、没有 workspace 隔离、没有只追加的 attempt 日志、没有 subject/infra
失败分类。

有用的提醒是：数据集应当版本化，模型打分是已知模式。不能当有状态 code-agent session runner 的蓝本。
该仓库自己已经把读者指向 OpenAI Dashboard 上的 evals 产品。

### OpenAI 当前官方指南 —— dataset、criteria 与 trace grader，不是本地 runner

官方 [Evals](https://developers.openai.com/api/docs/guides/evals) 指南把 eval 定义为
`data_source_config`、`testing_criteria`/graders 和 runs 的组合。它同时公布旧 Evals 平台将于
2026-10-31 只读、2026-11-30 关闭，并引导新用户使用 Datasets。它支持版本化数据、显式评分标准和
可重复运行，但没有给本项目定义本地有状态 session runner。

官方 [Trace grading](https://developers.openai.com/api/docs/guides/trace-grading) 指南把 trace 定义为
agent 决策、工具调用和推理步骤的端到端日志，并为 trace 赋结构化分数或标签。它支持把轨迹证据和
grader 作为一等对象，也强调 trace eval 比黑盒输出评测更能定位失败；但不定义本地 session 隔离、
只追加 attempt、崩溃恢复或 OCH 的 runner 边界。

## 综合

官方六个项目里，Pi、Maka 有独立的仓库内质量评测 harness；Grok Build 有在线证据优先 judge；
另外三个没有可比子系统。评测原生的额外来源沿着同一个架构边界分裂：

| 来源 | 评什么 | 怎么执行 | 该吸收 | v1 不要抄 |
| --- | --- | --- | --- | --- |
| Pi | Pi 的配置组合 | 真实 `AgentSession` | 真实产品路径、隔离目录、原生 transcript | 仅 live；Vitest 运行时 |
| Maka | 先评 Maka subject；已有外部臂 | Runtime Host / Harbor | Subject/Executor 分离、只追加 attempt、subject vs infra vs indeterminate | Harbor/Docker/出网平台；没有消费者的适配矩阵 |
| Harbor / Terminal-Bench | 任意 agent | 已安装 agent + 容器/云环境 | Task/Verifier 分离、artifact manifest、regrade | agent 适配矩阵、环境后端、并行云 |
| Inspect AI | 模型和 LLM 任务 | Inspect solver/sandbox | 可恢复集合、重试、离线打分、限额 | 把 Inspect 的 Task/Sample 当成 OCH 领域 |
| vitest-evals | 你绑定的那个 harness | 调用方提供的 harness | 基础设施断言 vs 质量 judge、CI 门槛 | 嵌入 TypeScript/Vitest |
| Grok Build | Grok Build 内部的完成声明 | 隐藏 evaluator + skeptic panel | 证据优先严格 judge、不可信输入隔离、独立验证 | 把在线自验证当作离线 runner |
| OpenAI 旧仓库/当前指南 | 输出与 agent trace | completion function 或 hosted run/grader | 数据集版本化、结构化 criteria、trace grading | completion-function runner 或托管平台依赖 |
| Codex / Kimi / DSH | （没有可比的仓库内质量评测） | — | — | 不要从 bench 或 session 日志反推架构 |

对本项目最近的组合是 **Pi 的执行路径加上 Maka 的实验语义**，打分侧约束取 Harbor 的 regrade/manifest
和 Inspect 的离线 `score`，CI 规则取 vitest-evals 的基础设施-vs-judge 分离。Grok Build 进一步要求
judge 的证据不可信、有界、结构化并经独立核验。这个组合本身不能裁定首个产品面必须 OCH-only，
还是应包含一个外部 Subject 适配器。

## 采纳的发现

这些是有证据支撑的架构约束。资料无法裁定的产品选择仍放在未决问题中，必须由后续设计显式决定，
不能在这里悄悄升级成结论。

1. **第一刀的产品边界仍是设计选择。** 证据支持以冻结的 OCH 模型、组合配置和版本组合作为 Subject
   的产品回归 runner；Maka 和 Harbor 也证明外部 Subject 适配器可行，但会引入另一套隔离、证据和
   conformance 义务。后续设计必须在「OCH-only 起步」与「带一个刻意收窄的外部 Subject 切片」之间
   做选择；本门不替产品做决定。
2. **每个 OCH Subject 都必须驱动真实 Application/Session 路径。** 它的 executor 是
   `composition.Open` → `CreateSession` → `RunTurn`（以及 scenario 需要的、已经公开的
   `CompactSession` / 恢复路径）。没有只给评测用的 Engine 或 Provider 捷径。章程 §6.9 已经要求这一点；
   Pi 独立印证了它。
3. **评测编排放在 `internal/harness/application` 之外。** Maka 的「拥有实验语义；自己不执行 Maka、
   不构造 Runtime 对象」是放置规则。Application 仍然是 Session/Turn 的权威。eval 包是
   `composition` 的消费者，和 `cmd/och` 一样，不是第二个组合根，也不是 `contextengine` 的子包。
4. **把 Subject、Executor、Attempt、Evidence、Score 分开。** 持久形状保留
   `Scenario → Subject → Attempt → Evidence → Score`（Maka 的 Experiment/Cell/Attempt/Result、
   Harbor 的 Task/Trial/Verifier、Inspect 的 Task/Sample/Score）。Subject 是命名的冻结身份，不是
   `composition.Config` 的 Go 类型别名。第一刀是只有 OCH executor，还是再带一个外部 executor，
   取决于上面的产品边界选择。不要添加没有真实消费者的通用 `Agent` 端口；只有第一刀确有消费者时，
   才添加外部 Subject 端口。
5. **先存证据，再独立打分。** Harbor regrade 和 Inspect `score(log)` 是同一条约束：已记录的 attempt
   可以不重跑 agent 就重新打分。证据是一份 artifact manifest，不是写死的两个文件；OCH 证据可包括
   原生 `och.session.transcript`、workspace/verifier 输出、冻结的 Subject/config 身份、usage/timing、
   EventStore/audit 或 request-envelope 证据，以及收集诊断。transcript 仍是轨迹表面，不要另造第二套
   trajectory schema；但也不能假装它包含实际省略的 `model.request.recorded` 或
   `policy.decision.recorded`。
6. **双通道是强制的，仓库里已有先例。**
   - **PR CI：** 无网络、无密钥、确定性 fixture，走现有 composition/fixture-provider 路径。
     确定性 verifier 可以卡普通 PR；模型质量 judge 不可以。
   - **Live：** 显式的本地或 nightly 调用，真实模型，独立产物目录，不是普通 PR 门。
7. **区分 subject 失败、基础设施失败和不确定结局。** Maka 的状态分类和可替换 attempt 规则是先例。
   一次已经产出 verifier reward 的超时，和缺少 Docker daemon，不是同一类事件。重试追加新 attempt，
   不改写上一条。
8. **硬性基础设施断言和质量 judge 是不同步骤。** vitest-evals / Pi：不变量让运行失败；质量分数是观察，
   放在可配置门槛后面。Live eval 可以使用模型 judge；那个 judge 不是被测 harness。Grok Build 进一步
   要求证据有界、经净化，并输出严格结构化 verdict；缺失 judge 证据不得静默变成通过。
9. **Runner 必须套件中立；第一批套件仍是设计选择。** Context Engine 摘要质量是已披露的 GA 阻塞项，
   因而是强候选，但不是调研已经证明的第一个套件。runner 还必须承载 tool/workspace、恢复、Provider、
   ACP、policy 套件；把它放进 `internal/harness/contextengine` 会让这些套件变成别扭的附加物。
10. **默认不要复制 Harbor 的云控制平面。** 对进程内 OCH executor，Pi 式独立 fixture/workspace/session
    目录足够。若选中外部 Subject，可能需要子进程或容器边界，设计必须写明；这仍不自动意味着
    Daytona/Modal 或第二套云调度器。
11. **OpenTelemetry 不属于这份 eval 合同。** 里程碑 10 里它仍是单独的未设计项。领域事件已经落实章程的
    Observable 属性。OTel 调研门以后再做。

## 拒绝的形状

1. **没有真实 Subject 和隔离合同时就声称支持通用外部 agent。** 第一刀带一个收窄的外部 Subject
   仍是设计选项；没有首个消费者的推测性 adapter、照搬第三方 schema 或 Harbor 规模抽象则不是。
   若设计选择 OCH-only，应把外部 Subject 明确记为延期，而不是假装调研证明它们不值得做。
2. **只给评测用的执行捷径**，绕过 Session 直接打 Engine、Provider 或 Context Engine。与 §6.9 冲突，
   也会让 scenario 分数无法和生产路径比较。
3. **把 Context Engine 的 fixture 测试当成里程碑 10。** 那些测试证明机制。它们不证明摘要质量，
   也不是 scenario runner。
4. **把 OpenAI Evals 旧仓库或托管产品当成 session runner 蓝本。** Dataset、criteria 和 trace grader
   是有用机制；completion function 和正在退役的托管控制平面，不是本地工具型 Session 的正确权威。
5. **从 Codex / Kimi 的 bench 或 DeepSeek Harness 的 `BENCHMARK.md` 反推架构。** 这些是已确认的缺失。
   Grok Build 不属于这组负面发现：其在线 evaluator 约束 judge 设计，而不定义 runner。
6. **把 `application.Service` 或领域事件结构写进 Score schema。** 这会堵住 ACP 子进程 subject
   （仍然是 OCH）和任何后续 executor。Score 读取带版本的 evidence manifest。
7. **把真实模型调用当成普通 PR 门。** 与现有无密钥组合根验证合同冲突。
8. **把任何参考项目的类型名、JSON schema、prompt 或状态字符串原样抄进本仓库。** 只取机制。

一个仍然启动本项目 `och` 二进制的 ACP 子进程 subject **不是**外部 agent。它是第二个 OCH executor
（进程内 composition 对公开协议面）。第一刀可以从只做进程内开始；设计不得把 Subject 定义得窄到
以后加 ACP executor 还要跟 schema 打架。在同一份冻结 scenario 上比较两个 OCH git 版本，同样仍是
产品回归。

## 设计必须回答的未决问题

- **产品边界。** v1 只做 OCH 产品回归，还是包含一个有明确消费者、刻意收窄的外部 Subject？后者必须
  给出具体的启动、隔离、证据和取消合同；只有抽象 adapter 接口而没有真实消费者不够。
- **包放置。** `internal/harness/eval`（新的架构守卫 owner，可以 import `composition` 但不能 import
  adapter）还是一个留在 harness ownership 表之外的 `cmd/` 二进制。无论哪种，Application 都不承担
  评测编排，`contextengine` 也不托管它。
- **OCH executor 面。** 只做进程内 `composition.Open`，还是同一刀里再加 ACP 子进程 subject？
  进程内足以证明模型；ACP 是公开协议，产品回归主张更强。
- **第一批套件。** runner 必须套件中立。Context Engine 滚动摘要质量因当前 GA 阻塞而是强候选，
  但设计必须显式选择第一批套件，不能把 Context 概念写死进 runner。最小 tool/workspace scenario
  可用来证明中立性；恢复、Provider、ACP、policy 仍是后续候选。
- **Scenario 编码。** 提交进仓库的 Go fixture、一份冻结的 JSON/JSONL 实验文件（Maka），还是
  Harbor 式的 `instruction.md` + tests 目录。v1 的格式由 OCH 自己拥有；Terminal-Bench YAML
  不是原生 schema。
- **Subject 身份。** 哈希什么：git SHA、`composition.Config` 摘要、provider 端点、Context 百分比、
  policy、sandbox 开关、tool catalog？机器本地路径不得进入身份（Maka：它们选择产物，不改变实验语义）。
- **Attempt 布局与保留。** live 产物放哪、什么被 gitignore、PR-CI fixture 运行是否在测试进程之外
  持久化任何东西、只追加 attempt 怎么存（像 Maka 的 `NNNNNN.json` 文件，还是 SQLite）。
- **Evidence manifest 完整性。** 哪些 artifact 必需、哪些可选，如何记录哈希与收集诊断；当 transcript、
  workspace/verifier 输出、冻结 config 身份、usage/timing 或 EventStore/audit/request-envelope 证据缺失时，
  离线 regrade 可以做什么。
- **v1 live 的打分组合。** 只用确定性 verifier（workspace 测试、transcript 不变量），还是加模型 judge，
  还是两者都要。若有 judge，它是独立 harness（vitest-evals），消费有界且经净化的证据（Grok Build），
  输出严格结构化 verdict，并对缺失或矛盾证据保守处理。
- **限额。** 每次 attempt 的墙钟、token、step、cost 上限（Inspect）。它们如何与 Application 已有的
  `Limits` 和 Context overflow 上限交互。
- **领域 `EvaluationResult`。** 章程点了名，未实现。它是领域事件、eval 包 DTO，还是 attempt 文件的投影？
  第一刀不应把评测事实塞进 Session 事件日志，除非真有 Session 查询需要它们。
- **版本对版本比较。** Maka 冻结实验并比较臂。 「这个 SHA 对昨晚的 SHA」是 v1 reporter 功能还是后续套件？
- **文档规则第 4 条要求的资源和隐私边界：** 最大并发 attempt、最大产物字节、已存储 transcript 的
  secret 脱敏（现有 `redact.Text` 路径已经在持久化之前跑过——确认 eval 导出不能绕过它）、以及
  没有显式开关就 fail-closed 拒绝启动 live 运行。

## 证据限度

- 仓库引用都追溯到对照表里钉住的 commit，并在本次阅读中打开过；当前 OpenAI 指南来自表中两份
  官方 Markdown 页面。2026-09-01 roadmap 门的 Evaluation 小节只当作线索，不当证据。
- 本文不授权从任何参考项目原样复制类型名、schema、prompt、状态字符串或配置常量。
- 本文不审计任何项目评测实现的正确性、统计有效性或安全性——只看放置、机制和先例价值。
- 深度按披露不均：Pi 的 `packages/evals` 和 Maka 的 `packages/eval` 读了 README 加上
  runner/subject/attempt/result 文件；Harbor 读了 README、Task、Verifier、artifact manifest、
  regrade 和 BaseAgent，没有读遍每个已安装适配器或环境后端；Inspect 读了
  `eval`/`eval_set`/`score`/Task/Sample/Scorer 和 `design/recover.md`，没有读完整控制通道；
  vitest-evals 读了 architecture、gate 和 judge-harness；旧 OpenAI Evals 读了 README、Recorder
  和 eval-templates；Grok Build 读了上文引用的 goal evaluator 与 classifier 路径。当前 OpenAI
  Evals 和 trace-grading 指南来自对照表中的官方页面。Codex、Kimi Code、DeepSeek Harness
  按负面发现核验。
- Harbor 的云环境后端（Daytona、Modal、GKE 等）只从目录树列出，没有执行。
- Maka 的出网代理和 Harbor 的 Docker/云路径说明外部 Subject 需要显式隔离和调度合同；它们不能证明
  每个收窄的外部 adapter 都需要 Harbor 平台。较小的子进程或本地容器 executor 也可能满足该合同。
- 「当前状态」指 2026-09-01。以后再访这些项目的调研门必须按文档规则第 7 条重新抓取、重新阅读。
- 本文不选择设计。下一步是 `docs/superpowers/specs/` 下的正式评测设计，由——而不是被——上述发现
  告知。产品定位与第一批套件范围仍是设计选择。OpenTelemetry 仍是单独的未设计项。
