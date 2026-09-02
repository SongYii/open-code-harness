# Evaluation 系统：真实 Session、证据与离线重评

**状态：** 已接受的规范设计；尚未实现

**日期：** 2026-09-02

**权威关系：** 本文是里程碑 10 Evaluation 子系统规范的同步中文阅读版。英文
[2026-09-02-evaluation-design.md](2026-09-02-evaluation-design.md) 是规范文本；两者若有分歧，
以英文为准。

**调研依据：**
[Evaluation 架构调研门](../../research/architecture-gates/2026-09-01-evaluation.zh-CN.md)

## 1. 范围与决定

本里程碑建设 Open Code Harness（OCH）的本地评测系统。它运行真实 OCH Session，冻结实验身份，
保存有界证据，并且无需再次运行 agent 就能对证据重新打分。

v1 里程碑有两个具体 OCH executor：

1. 进程内 executor，驱动真实 Composition/Application 路径；
2. 黑盒 executor，启动真实 `och -acp` 二进制，并通过公开 ACP v1 client 协议驱动它。

持久化模型保持 executor 中立，但 v1 不运行任意外部 agent。未来若加入外部 Subject，必须有真实
消费者，并重新决定隔离和 conformance 合同；本设计不预造没有消费者的通用 Agent adapter。

交付是分阶段的。第一个实现切片只建立冻结模型、仅追加的证据/重评路径、fixture 隔离，以及进程内
executor。它是有用的，但并不完成本里程碑；ACP executor 与 parity 合同在下一阶段交付，而现场评审
只有在确定性机制被验证之后才会引入。

OpenTelemetry 不属于本设计。它仍是里程碑 10 中独立的子系统，需要自己的架构调研门和设计。

## 2. 目标

实现必须：

- 运行生产 OCH 行为，而不是 eval 专用 Engine 或 Provider 路径；
- 同时证明进程内表面和公开 ACP 子进程表面；
- 为每个 Attempt 隔离 fixture、workspace、SQLite、Session、audit replica、进程资源和 artifact 目录；
- 冻结 Scenario、Subject、Executor、limits 和 scorer 身份；
- 保留只追加 Attempt 与只追加 Score；
- 在打分前保存带版本的 Evidence Manifest；
- 不重新运行 Subject 即可重评已保存证据；
- 把执行 outcome 与质量 verdict 分开；
- 把确定性 PR CI 与显式真实模型评测分开；
- 执行时间、token、并发、文件数和 artifact 字节上限；
- 对损坏、不完整、缺失或矛盾的必需证据 fail-closed；
- 能承载 Context、tool/workspace、恢复、Provider、ACP、Policy 和未来 MCP suite，且不让任何 suite
  成为 runner 的包边界。

## 3. 非目标

v1 里程碑不包括：

- 任意外部 agent 或 Harbor 式 agent adapter 矩阵；
- 容器/云调度、分布式 worker 或远端 artifact 存储；
- 把 Terminal-Bench、SWE-bench 或其他项目数据集当作 OCH 原生 Scenario schema；
- scenario 自带的任意 shell verifier；
- MCP client adapter 尚未实现时的 MCP 评测 suite；
- OpenTelemetry、eval Web UI 或第二套 client protocol；
- 联网自动查询模型价格；
- 与 `och.session.transcript` 平行的第二套 trajectory schema；
- 为 eval Attempt、manifest 或 Score 添加 Session event；
- 用少量 live 样本宣称统计显著性或 GA。

## 4. 术语与持久关系

持久关系为：

```text
EvalSet
  └── Cell = Scenario × Subject × Executor
        └── Attempt 1..N
              ├── Outcome
              ├── Evidence Manifest
              └── Score 0..N
```

- **EvalSet** 冻结有序实验矩阵、重复次数、限额、scorer 选择和配对规则。
- **Scenario** 声明任务、fixture、有序 action、所需 executor capability、证据 role 和
  scorer/verifier criteria。
- **Subject** 是一个 OCH 版本、model、Context/Policy/Tool 配置和生产 limits 的无密钥语义身份。
- **Executor** 是执行表面：`in_process` 或 `acp_subprocess`。
- **Attempt** 是一个 Cell 的一次执行和 repetition index；retry 永远创建新 Attempt。
- **Outcome** 分类执行和证据收集，不判断行为质量。
- **Evidence Manifest** 是 scorer 唯一可以读取的 artifact 清单。
- **Score** 是一个 scorer 针对一个 manifest digest 产生的一份不可变结果。

Scenario、Subject、Executor、Attempt、manifest 和 Score ID 都是最多 128 字节的小写 ASCII 不透明 ID。
用户提供的 ID 使用 `[a-z0-9][a-z0-9._-]*`；生成的 Attempt/Score ID 使用密码学随机的 128-bit
小写十六进制。路径绝不作为身份。

## 5. 所有权与依赖边界

子系统增加一个架构守卫 owner：`ownerEval`，根目录是 `internal/harness/eval`。

```text
cmd/och-eval
    │
    └── internal/harness/eval
          ├── 进程内 executor ──> composition.Open ──> Application/Session
          └── ACP executor ─────> internal/client/acp ──> och -acp
```

`internal/harness/eval` 拥有模型校验、矩阵展开、Attempt 编排、文件系统发布、证据收集、打分、重评和报告。
必要时可 import `application`、`composition`、`transcript` 类型以及 `internal/client/acp`；可以为明确
归属的隔离和子进程职责使用 `os`、`os/exec`。它不得构造或 import 具体 harness adapter；Composition
仍是唯一 adapter owner。

`cmd/och-eval` 只拥有 flag 解析、信号处理、稳定 exit code 以及 human/JSON 报告。它不包含 Scenario
语义，也不构造 harness adapter。

Application、Domain、Engine、Context Engine、transcript、adapter 和 Composition 不得 import eval。
Eval 不增加第二个 composition root，不改变 Session 权威，也不把 eval 事实写进 Session stream。

## 6. 带版本的 wire 文档

Eval 自有 JSON 文档使用 UTF-8，拒绝重复 key 和未知字段，并包含 `schema` 与 `formatVersion`。
v1 schema 为：

| 文档 | `schema` | 发布方式 |
| --- | --- | --- |
| EvalSet | `och.eval.set` | 展开前冻结一次 |
| Scenario | `och.eval.scenario` | 随 suite 提交 |
| Subject | `och.eval.subject` | 在 EvalSet 中冻结一次 |
| Executor | `och.eval.executor` | 在 EvalSet 中冻结一次 |
| Attempt | `och.eval.attempt` | 执行前写一次 |
| Outcome | `och.eval.outcome` | 执行/恢复后写一次 |
| Evidence Manifest | `och.eval.evidence-manifest` | 证据收集完成后最后发布 |
| Score | `och.eval.score` | 每次打分/重评追加一份 |
| Report | `och.eval.report` | 派生；从不作为 Attempt 权威 |

`formatVersion` 为 `1`。规范身份摘要是经过校验的冻结文档 canonical JSON 精确字节的 SHA-256。
凭证、环境变量值、机器本地绝对路径、墙钟时间和 artifact 输出不得进入 Scenario/Subject 语义摘要。

decoder 对不支持的 format version fail-closed。未来 reader 可以跳过未知 optional artifact role，
但不能跳过未知 required role，也不能跳过未知 Outcome/verdict 值。

## 7. Scenario 合同

Scenario 位于提交进仓库的 suite 目录：

```text
eval/scenarios/<suite>/<scenario>/
  scenario.json
  fixture/
```

`scenario.json` 包含：

- 稳定 Scenario ID 和人类说明；
- fixture digest 和有界复制策略；
- 有序 action；
- 所需 executor capability；
- required/optional evidence role；
- deterministic verifier ID 和 live judge criterion ID；
- 只能收紧 EvalSet limits 的单 Scenario 限额；
- baseline/candidate report 使用的 pairing tag。

v1 action 是 `prompt`、`compact`、`cancel`、`restart`、`collect`。`prompt` 带有界 UTF-8 文本；
`compact` 带公开 summary/reset strategy 以及可选的有界 focus；`cancel` 指向一个先前的 in-flight action；
`restart` 请求所选 Executor 支持的生产表面 shutdown/crash/reopen 序列；`collect` 请求声明过的
workspace path 或 verifier fact。

runner 在创建 Attempt 前拒绝不支持的 Scenario/Executor 配对。它绝不静默跳过 action 或必需 capability。

Scenario 文件不能指定任意 executable。确定性 verifier ID 只能解析到编译进 `och-eval` 的具体
verifier catalog。新增 verifier 是带测试、需 review 的代码修改，不是由数据文件执行代码。

## 8. Fixture 隔离

每个 Attempt 都得到新的绝对 root，具有独立的 `workspace/`、`database/`、`audit/`、`evidence/`
以及进程/log 目录。同一个 Cell 的重复执行也不能复用任何资源。

Fixture copy 遍历时不跟随 symlink；拒绝绝对路径、`..` 越界、symlink、link count 大于 1 的 hard link、
socket、device、FIFO 和任何不支持的文件类型。只保留普通文件 executable bit 和目录结构。在 Subject
启动前执行文件数、单文件和 fixture 总字节限额。

生成的 workspace 可由 Subject 写入，但不能越过已有 workspacefs/localexec jail。fixture 源保持只读。
证据收集再次执行路径包含关系和文件类型校验；安全输入复制不能让 Subject 后续创建的 symlink 自动可信。

## 9. EvalSet 与矩阵展开

EvalSet 冻结：

- 有序 Scenario 引用及 digest；
- 有序 Subject snapshot 及 digest；
- 有序 Executor snapshot 及 digest；
- repetition count 和 deterministic pairing seed；
- verifier/judge 配置 digest；
- 全局 limits 与 artifact root；
- `fixture` 或 `live` lane。

展开顺序固定为 Scenario、Subject、Executor、repetition index。缺少所需 executor capability 的 Cell
使整个 set 校验失败，不能成为被跳过的一行。展开后超过 4,096 个 Attempt 时，在创建任何 Attempt
目录或 Provider 资源前失败。

resume 必须重新打开完全相同的冻结 EvalSet。Scenario、Subject、Executor、scorer、lane、limit 或 digest
任一改变都需要新的 EvalSet ID。resume 只调度没有 terminal manifest 的 Cell，不修改完成的 Attempt。

## 10. Subject 身份与密钥处理

OCH Subject snapshot 包含：

- 仓库 revision 与 dirty-state marker；
- Provider adapter kind、规范化 endpoint 身份、model ID、context window 和 maximum output；
- Context 百分比、chunk/recovery/pruning cap 和 compaction timeout；
- Policy mode、tool catalog 身份、Application limits 和 sandbox policy；
- deterministic fixture-provider 或 live-provider 身份；
- 可选的冻结 price-table digest。

snapshot 只记录 credential 环境变量的名字，绝不记录值。规范化 endpoint 去掉 userinfo 和 query string。
workspace、database、audit、artifact、binary、temporary 的绝对路径属于 Attempt fact，不属于 Subject identity。

发布前，所有 JSON/log/diagnostic 字段经过已有 redaction policy，并对环境变量值和 authorization header
做精确 key suppression。Eval 不捕获完整 process environment。

## 11. Executor 身份与 parity

Executor identity 与 Subject identity 分开。

`in_process` 记录 OCH revision、eval build revision、Composition contract version。`acp_subprocess`
还记录准确 `och` binary SHA-256、去掉 credential value 的规范化 argv、ACP protocol version，以及
`initialize` 返回的 agent name/version。

Parity comparison 要求 Scenario 和 Subject 语义 digest 相等，而且只能比较声明的语义不变量。
Event ID、command ID、runtime ID、时间戳、临时路径、调度顺序和原始 transcript/audit 字节都不是 parity
字段。Session/Turn 终态、tool fact、usage fact、workspace 结果、policy decision、request-envelope
属性和 artifact 完整性可以作为 parity 字段。

## 12. Attempt 文件系统与原子发布

默认本地 artifact root 是 gitignored 的 `.eval/`。一个 EvalSet 使用：

```text
.eval/sets/<set-id>/
  set.json
  attempts/<attempt-id>/
    attempt.json
    outcome.json
    evidence/
      manifest.json
      transcript.jsonl
      audit/
      workspace/
      verifier/
      diagnostics.json
      stdout.log
      stderr.log
    scores/<score-id>.json
  reports/<report-id>.json
```

`attempt.json` 在 Subject 启动前原子发布，之后不可变。`outcome.json` 最多原子发布一次。Evidence 文件
先于 `manifest.json` 发布；manifest 是 Attempt 可打分的 commit marker。Score 使用新路径发布，永不替换
其他 Score。

原子发布使用同目录 temp file、有界写入、file sync、close、rename-without-overwrite，以及平台支持时的
directory sync。任何失败都只留下旧状态或未提交 temp。启动时只能在记录 diagnostics 后清理 eval 自有
temp 名称，绝不能猜测未提交文件已经完整。

## 13. Outcome 分类

Outcome 是执行分类，不是质量：

| 状态 | 含义 |
| --- | --- |
| `completed` | executor 到达 Scenario 边界，并完成必需证据收集 |
| `subject_failed` | OCH/provider/tool/protocol 行为失败，但 runner 权威和证据分类仍可靠 |
| `infra_failed` | fixture、spawn、storage、runner、host 或必需收集基础设施失败 |
| `indeterminate` | 持久证据无法证明失败属于 Subject 还是基础设施 |

预期的 OCH terminal failure 可以拥有 `subject_failed` Outcome，同时在负向 Scenario 的确定性 Score 中通过。
反过来，`completed` Outcome 也可能得到质量失败 Score。

Outcome 记录稳定 code、有界安全 message、start/end/duration、已知的 terminal Session/Turn fact、
limit/truncation fact、collection status、recovery status 和准确 Attempt identity。自由格式的原始 Provider
或 process error 只进入有界、已脱敏 diagnostics。

## 14. Evidence Manifest

每个 manifest entry 包含：

- 规范化相对路径；
- 稳定 role 和 media type；
- SHA-256 和字节长度；
- `required` boolean；
- `collected`、`missing`、`truncated` 或 `rejected` 状态；
- 未收集时的稳定 reason code 和有界安全 detail；
- 适用时的 producing step/verifier identity。

manifest 还记录总字节、文件数、collection start/end、Outcome digest 和 collection diagnostics digest。
manifest 不对自身做自引用哈希；Score 引用已发布 manifest 精确字节的 SHA-256。

OCH 必需证据为：

1. 完整原生 `och.session.transcript`，具有有效 snapshot 和 complete trailer；
2. 来自 Attempt 隔离 database、覆盖相同 terminal head 的 canonical verified audit-replica snapshot；
3. 冻结的 Scenario、Subject、Executor、Attempt 和 Outcome 文档；
4. usage、timing、强制 limit、truncation 和 collection diagnostics；
5. Scenario 要求的所有有界 workspace/verifier artifact。

transcript 仍是 trajectory 表面。audit replica 不是第二套 transcript：它是现有 canonical append 证据，
补充 transcript 刻意省略的 `model.request.recorded`、`policy.decision.recorded` 等事实。

SQLite backup 是 optional，只能由 recovery Scenario 明确要求；不是默认证据。scorer 不能打开 live database，
也不能跟随 manifest 中不存在的路径。

证据收集需要窄的 Composition-owned helper。transcript 使用已有 `composition.ExportSession`；实现增加一个
由 Composition 所有的 canonical audit snapshot/verification 操作，使 eval 不 import SQLite。两个 executor
都只能在各自 writer 停止后调用这些 helper。

## 15. 进程内 executor

一个 Attempt 构造一个 `composition.Config`，调用一次 `composition.Open`，通过
`Service.CreateSession` 创建一个 Session，再通过 `RunTurn`、`CompactSession` 等公开 Application method
驱动 action。它不得直接调用 Engine、Provider、Context Engine、Store 或 adapter。

executor 不在 Attempt 间复用 Assembly 或 Session。每条 terminal path 都用独立的有界 shutdown context
关闭 Assembly。Scenario request sink 可以收集有界 live diagnostics，但 canonical scoring evidence 来自
shutdown 后的 transcript/audit/workspace。

fixture lane 让真实 OpenAI-compatible adapter 连接仓库 loopback fixture server；live lane 使用冻结的
真实 Provider 配置。不存在 eval-only Provider interface 或 Application branch。

## 16. ACP 子进程 executor

executor 用 `-acp`、隔离 workspace/database/audit、唯一 runtime ID 和 Subject 完整生产配置启动准确的
`och` binary。它通过 `internal/client/acp` 调用 `initialize`、`session/new`、`session/load`、
`session/prompt`、`session/cancel`，并收集有界 ACP diagnostics。conformance test 不得把进程替换成
内存 ACP adapter。

已有 `och` assembly flags 尚未暴露全部 Subject 语义旋钮。因此实现要扩展共享 `bindAssemblyFlags`：

```text
-max-steps
-max-tool-calls-per-step
-max-assistant-bytes
-approval-timeout
-context-trigger-percent
-context-target-percent
-context-tail-percent
-context-max-summary-chunks
-context-max-overflow-compactions-per-turn
-context-max-pruned-tool-results-per-request
-context-compaction-timeout
```

普通 `och -acp` 与 `och compact-session` 使用相同 binding。ACP executor 从与进程内 executor 相同的、
已校验 Subject snapshot 生成 argv。credential value 仍只存在于命名环境变量，不进入 argv。

child 只接收 allowlist 环境：必需 OS runtime 变量、命名 Provider credential 和显式声明的 fixture 变量。
runner 绝不转发完整环境。

ACP Handler 非交互。Scenario/Subject policy 冻结 approval 响应；任何未声明 permission request 都被拒绝
并记录。manual `compact` 先干净停止 ACP writer，再对隔离 database 调同一 binary 的公开
`compact-session` 命令，随后重启 `och -acp` 并 `session/load`；绝不进程内调用 Application。

## 17. 取消与子进程清理

进程内取消会 cancel 当前 action context，等待 durable terminal result，再在 shutdown bound 内关闭 Assembly。

ACP 取消使用固定升级顺序：

```text
session/cancel
  → 等待 cancellation grace
  → 关闭 child stdin
  → 等待 shutdown grace
  → SIGTERM/process-group terminate
  → 等待 final grace
  → force-kill process group
  → reap
```

`exec.CommandContext` 不是主要取消机制，因为立即 kill 会丢掉 Scenario 正在测量的 Session terminal evidence。
每个升级阶段都要计时并记录。executor 拥有一个 process group，并在所有正常路径 reap。

ACP child stdin 只属于 runner。parent death 会关闭 pipe，使 `och -acp` 观察 EOF 并退出。恢复绝不 signal
前一进程存下来的 PID，因为 PID reuse 可能命中无关进程。

## 18. 崩溃恢复与 resume

runner 启动时分类每个 Attempt 目录：

- 有效 Outcome + 有效 Manifest：不可变 terminal Attempt；
- 有 Attempt + Outcome、无 Manifest：只 resume 有界证据收集；
- 有 Attempt、无 Outcome：不运行 Subject，只检查隔离 canonical store；
- 没有有效 Attempt 文档：未提交 temp directory，不是 Attempt。

若 canonical evidence 能证明 terminal Session/Turn，恢复发布带 `recoveryStatus: recovered` 的 Outcome
并收集证据。
若 Session 仍 active/running、commit outcome unknown、store 损坏或不同来源互相矛盾，则发布
`indeterminate` 和准确 diagnostics。

恢复永不重跑 prompt、重试 append、resume Subject 或修改已有 Outcome。operator retry 追加新 Attempt。

## 19. 资源与隐私限额

所有 limit 应用默认值后必须为正，Scenario 只能收紧。

| 限额 | 默认 | 硬上限 |
| --- | ---: | ---: |
| 并发 Attempt | 1 | 8 |
| 每 EvalSet 展开 Attempt | 256 | 4,096 |
| Attempt 墙钟 | 15 min | 2 h |
| Turn/action | 5 min | 30 min |
| 进程启动 | 30 s | 2 min |
| cancellation grace | 10 s | 1 min |
| shutdown grace | 10 s | 1 min |
| 证据收集 | 2 min | 10 min |
| fixture 文件数 | 10,000 | 100,000 |
| artifact 文件数 | 10,000 | 100,000 |
| 单 artifact | 16 MiB | 64 MiB |
| Attempt artifact 总量 | 256 MiB | 1 GiB |
| stdout/stderr 各自 | 8 MiB | 64 MiB |

EvalSet 必须提供正数 per-Attempt token cap。每个 Turn 后累计 usage；达到 cap 后不再开始下一 Turn。
单个 in-flight Turn 仍由 Provider maximum output 和 Application limits 约束。

cost cap 可选。启用时必须提供用户冻结、使用整数 microunit 的 price table，并在每个 usage record 后检查。
缺失或未知价格是 `unavailable`，绝不是零；配置了 cost cap 但价格不可用时校验失败。

所有 clip 和 refusal 都成为结构化 Outcome/manifest fact，不能只写日志。live 证据保持本地且 gitignored；
发布到 artifact root 之外是单独的 operator action。

## 20. Score 与离线重评

只有有效 manifest commit marker 存在后才能打分。scorer 得到 manifest bytes 和只能读取 collected
manifest entry 的 artifact reader；得不到 Executor、Service、Provider、network client、live Store 或
不受限 filesystem handle。

Score 记录：

- manifest digest 和 Outcome digest；
- scorer ID、implementation version、config digest 和 lane；
- `pass`、`fail` 或 `indeterminate` verdict；
- 有界数值 score 和逐 criterion 结果；
- manifest 内的 evidence reference；
- missing/contradictory evidence；
- 有界安全 rationale；
- 适用时 scorer 自己的 usage/timing/cost。

`och-eval regrade` 读取一个已发布 Attempt，校验所有引用 digest，并追加新 Score。它绝不运行/resume
Subject，也不替换旧 Score。manifest 损坏、required artifact 缺失、digest mismatch、schema 不支持或
必需证据不可用，只能产生 `indeterminate` 或 scoring infrastructure error，绝不能 `pass`。

## 21. 确定性 verifier 与模型 judge

Deterministic verifier 是编译、带版本的实现，可检查 transcript/audit invariant、Session/Turn 终态、
workspace 内容、usage、limits、policy/request fact 和 parity。Infrastructure assertion 与 quality criterion
保持为不同字段。

Model judge 只在 live lane 运行，与 Subject executor 分离，并有自己的冻结 model/config/prompt digest。
它只接收由声明 criterion 选择的有界、已脱敏 evidence。所有 Subject 写出的 string、transcript field、
tool result、file 和 prior rationale 都作为不可信证据包裹，不能向 judge 发指令。

Judge 输出使用拒绝未知字段的严格 schema：

- verdict：`pass`、`fail` 或 `indeterminate`；
- 数值 score；
- 逐 criterion score 和 status；
- manifest evidence reference；
- missing/contradictory evidence list；
- 一段有界 rationale。

输出 malformed、引用不存在、required evidence 缺失或矛盾未解决时一律 `indeterminate`。v1 不强制
multi-judge quorum；以后加入时要追加独立 Score 和 aggregate Score，不能隐藏单个 verdict。

## 22. Baseline/candidate 配对与报告

只有 Scenario digest、Executor kind、repetition index、fixture digest、limits 和 pairing seed 相同时，
baseline/candidate arm 才能配对。Subject digest 必须至少有一个声明的语义字段不同。

report 保留所有原始 Attempt/Score 引用，再派生 count、failure taxonomy、pass rate、score distribution、
token/cost/latency 和 paired delta。不能在不同时展示 raw/filtered view 的情况下把 infra failure 从分母移除；
missing pair 必须显式出现。

PR report 可以由 deterministic verifier failure 和配置的确定性 floor 卡门。Model-judge 结果不能卡普通 PR。
在 sample size、judge meta-eval、provider breadth 和 variance policy 拥有独立接受证据前，live/nightly
quality floor 只是里程碑信号。

## 23. PR fixture lane

fixture lane 是默认通道。若配置非 loopback Provider endpoint 或需要 live credential，则 fail-closed。
它不使用外部网络和密钥。Loopback fixture HTTP 只是进程内测试 transport，不是外部网络访问。

两个 executor 都进入 PR CI。ACP 路径在测试中构建真实 `och` binary 并启动它；fake ACP agent 不构成
完成证据。确定性 fixture 使用冻结响应，比较语义 fact，不比较时间戳或生成 ID。

## 24. Live lane

live 执行同时要求显式 `--live` flag 和 `live` EvalSet。缺少任一个条件，都要在读取 credential 前拒绝。
live run 是本地显式调用或 scheduled nightly，写入独立 artifact root，不是普通 PR check。

runner 记录 Provider/model identity 和 credential 环境变量名，但绝不记录值；不上传证据。即使 run 因预算、
operator cancellation 或 infrastructure 中断，也要在 evidence-collection bound 内发布最强的诚实 Outcome
和 manifest。

## 25. 首批 suites

### 25.1 Executor parity fixture

用两个 executor 跑相同 deterministic Scenario 和 Subject 语义。验证 terminal Session/Turn shape、tool/usage
fact、workspace 结果、request/policy evidence、manifest 完整性和声明的 parity 字段；不要求 ID、时间戳、
路径或字节完全相同。

### 25.2 Tool/workspace deterministic suite

覆盖 read、write、exec、Policy/approval、预期失败、取消、redaction 和 artifact 收集。用 transcript/audit
交叉校验 workspace 结果，并在 PR CI 通过两个 executor 运行。

### 25.3 Context mechanism fixture suite

确定性覆盖 pre-turn/mid-turn trigger、checkpoint、manual summary/reset、overflow recovery、restart/crash
证据、transcript/audit projection 和资源上限。已接受但 inert 的 capability 不能仅靠配置存在就通过；
声称 multi-chunk summary 或 Tool Result pruning 的 Scenario 必须实际观察行为，否则失败。

### 25.4 Context real-model quality suite

只在 live 运行。criterion 覆盖 summary fidelity、约束和决定保留、tool-result attribution、长期任务连续性、
质量、token、latency 和 stability。模型 judge 之前先跑确定性 invariant。

Context 是首批 suite，因为它是已披露 GA blocker，不是因为 eval 属于 Context Engine。恢复、Provider、
更广 ACP、Policy 和 MCP suite 使用同一 runner。MCP 缺失不阻塞 eval 系统，只阻塞诚实的 MCP suite。

## 26. CLI 合同

首期命令：

```text
och-eval run     -set PATH -artifacts PATH [--live]
och-eval regrade -attempt PATH -scorer ID
och-eval report  -set PATH [-output PATH]
```

机器输出是 stdout 上一个带版本 JSON 文档；人类 diagnostics 写 stderr。Exit code 区分 validation、
deterministic gate failure、infrastructure failure、indeterminate completion 和 internal error。非 gating live
run 的质量失败体现在 report，不能伪装成 infrastructure failure。

`run` 拒绝位于 fixture workspace 内的 artifact root，也拒绝没有 `--live` 的 live set。`regrade` 没有
Subject Executor flag 或 Subject Provider credential。model-judge scorer 可以要求自己独立的显式 live judge
配置和 credential。

## 27. 测试与完成证据

实现测试必须包括：

- 严格 JSON decoding、canonical digest、duplicate/unknown field 和 schema version 拒绝；
- matrix expansion、capability mismatch、pairing 和 4,096 Attempt 拒绝；
- fixture/evidence path traversal、symlink/hard-link/device 拒绝、文件数/字节 cap、truncation 和 digest 篡改；
- Attempt、Outcome、manifest、Score 每阶段的 atomic publish fault injection；
- 每种 partial filesystem state 的 restart classification；
- 通过真实 `composition.Open` 和 Application method 的进程内证明；
- 针对新构建真实 `och` binary 的 ACP 证明，包括 initialize、new/load/prompt/cancel、manual compact、
  restart、process-group cleanup 和无泄漏 child；
- 每个 Limits/Context 字段的 Subject-to-CLI 配置等价；
- transcript trailer 与 audit head 一致、request/policy evidence、missing/corrupt evidence fail-closed；
- 在 Executor/Subject 调用被做成不可能时仍能离线 regrade；
- deterministic verifier 与 model-judge 分离、严格 judge parser；
- fixture lane 拒绝外部 Provider/credential；
- 在访问 credential 前验证 live 双重许可（live set + `--live`）；
- time/token/cost/concurrency/artifact limit 和 cancellation race；
- 两个 executor 的语义 parity fixture；
- 首批 suite golden fixture 与 live-suite dry validation。

完成证据必须包含新的：

```text
go test ./...
go test -race ./...
go vet ./...
```

另加针对性 subprocess interoperability、corruption/fault、cancellation leak、deterministic regrade 证明，
以及不调用真实模型、展开 1、100、1,000、4,096 Attempt 的 benchmark。

Mutation evidence 至少要分别杀死：executor shortcut、Subject/Executor digest omission、manifest-last
publication、missing-required-evidence pass、transcript/audit head mismatch、retry overwrite、live consent、
token cap、artifact path containment、ACP force-kill/reap behavior。

## 28. 文档、成熟度与证据账本

实现发布必须增加：

- `docs/architecture/evaluation.md` 及同步中文阅读版；
- `docs/architecture/evaluation-evidence.md`，记录 task commit、mapping table、命令与实际输出、fault/mutation
  结果、benchmark 环境、偏离和开放 blocker；
- Scenario authoring、live-run privacy/cost、regrade 和 operator 指南；
- authority table、milestone、根 README 和 CLI help 更新。

在真实模型 Context 质量有重复证据、judge meta-evaluation 存在、provider/model coverage 更广、
variance/baseline policy 已文档化之前，子系统保持 not GA。Fixture 成功证明 runner 机制，不证明普遍 agent 质量。

## 29. 实现边界与可能文件图

实施计划可以细化文件名，但不得折叠已接受边界：

```text
internal/harness/eval/
  model.go            # 带版本冻结文档和校验
  digest.go           # canonical bytes 和 SHA-256 identity
  scenario.go         # checked-in Scenario loading 和 capability
  matrix.go           # EvalSet expansion、pairing、resume inventory
  store.go            # 有界只追加文件系统发布
  manifest.go         # artifact inventory 和受限 reader
  runner.go           # Attempt orchestration 和 limits
  executor.go         # 内部 executor contract
  inprocess.go        # 真实 Composition/Application executor
  acp.go              # 真实 och subprocess + internal/client/acp
  recovery.go         # partial Attempt 分类，永不重跑
  verifier.go         # 编译的 deterministic verifier catalog
  judge.go            # 严格有界 live judge harness
  score.go            # immutable scoring 和 offline regrade
  report.go           # raw/paired 派生报告

cmd/och-eval/
  main.go             # 只含 run/regrade/report CLI

cmd/och/, internal/harness/composition/
  完整 Limits/Context flag parity 和 Composition-owned audit evidence export

eval/scenarios/
  executor-parity/
  tool-workspace/
  context-mechanism/
  context-quality/

internal/harness/architecture/
  ownerEval dependency rules
```

后续实施计划必须给出可独立 review 的 slice。它可以先落一个 executor，再落另一个，但里程碑完成必须同时
拥有二者；在这份已接受设计下，ACP subprocess support 不是 optional follow-up。

## 30. 验收摘要

只有满足以下条件，才算落实本设计：

1. 两个 executor 都运行真实 OCH 表面，并使用语义等价的冻结 Subject；
2. 每个 Attempt 都隔离、有界、只追加且诚实分类；
3. manifest-published evidence 可以校验并离线重评；
4. transcript 与 audit evidence 共同覆盖声明的 scoring 需要，不创建第二套 trajectory；
5. 损坏或缺失的必需证据绝不能通过；
6. deterministic PR 与显式 live 通道不能混淆；
7. retry/recovery 不重写 Attempt，也不在原处重跑 outcome unknown 的工作；
8. 首批 suite 证明 executor 中立和 Context 质量覆盖；
9. 构建 runner 不需要先实现 MCP，且 MCP 存在前不声称 MCP 评测；
10. 文档与 evidence ledger 披露剩余质量/GA blocker，不从 fixture 成功反推它们已解决。
