# 评估系统：确定性夹具、ACP 对等性与需授权的实时评审 — 已实现合同

**状态：** 已实现；尚未 GA（参见[成熟度与 GA 阻碍项](#成熟度与-ga-阻碍项)）

**权威来源：** [第 10 里程碑评估设计](../superpowers/specs/2026-09-02-evaluation-design.md)

**已实现计划：** [第 10 里程碑实施计划](../superpowers/plans/2026-09-02-evaluation-system.md)

**完成证据：** [评估证据台账](evaluation-evidence.md)

**English original:** [Evaluation System — Implemented Contract](evaluation.md)

**包：** `internal/harness/eval`（纯评估领域逻辑 — 模型、摘要、运行器、验证器、评审器、对等性比较）、`cmd/och-eval`（CLI：`run`、`regrade`、`report`）、`internal/harness/composition`/`internal/harness/adapters/sqlite`（eval 读取证据所依赖的规范审计快照/校验操作）

本文档记录的是当前代码与测试实际强制执行的行为，属于内部 Go 合同，并非稳定的公开协议，也尚未构成 GA 保证。

## 范围

评估系统将一个冻结的 **Scenario**（一段脚本化的 prompt/compact/cancel/restart/collect 动作序列）与一个冻结的 **Subject**（Provider/Policy/Context 配置）通过两种 **Executor** 之一驱动执行 —— `in_process`（直接在本进程内驱动真实的 `composition.Assembly`，不启动子进程）或 `acp_subprocess`（通过 ACP v1 协议驱动一个真正独立启动的 `och -acp` 进程）—— 并发布只追加的证据，供后续一个完全独立的步骤在**从不重新运行 Subject** 的前提下评分。它绝不编辑或评分生产 Session 数据；每个 Attempt 都拥有各自隔离的工作区、SQLite 数据库与审计目录（设计 §8）。

`internal/harness/eval` 的依赖边界刻意收窄：它可以导入 `internal/harness/application`、`internal/harness/composition` 与 `internal/harness/transcript`，但绝不导入任何具体的 harness 适配器（`internal/harness/adapters/*`），也绝不导入 `internal/harness/testkit` —— 这是一条实时、被强制检查的边界（`internal/harness/architecture/dependencies_test.go` 中的 `TestForbiddenImport` 与 `TestClientPackagesAreIsolatedFromInternalHarness`），而非一种约定。其他任何 harness 包（`application`、`domain`、`engine`、`composition`、`transcript`、任意适配器）也都不得导入 `eval` —— 这条依赖关系是单向的。`internal/client/acp` 是一个被单独拥有的包，尽管它与 `eval` 说的是同一种协议，`eval` 也绝不导入它 —— 本仓库构建的每一个 ACP 客户端（`internal/client/acp`、`internal/harness/adapters/acp`、以及 `internal/harness/eval` 自己的 `acp_wire.go`）都各自拥有一份独立的 NDJSON 分帧与协议形状实现，而非共享一份，这是一个明确的设计选择（参见 `internal/client/acp` 自身的合同），而非疏漏。

## 持久文档与目录布局

每份文档都是 UTF-8 JSON，带有 `schema` 字符串与整数 `formatVersion` 字段，解码时拒绝重复键与未知字段（`internal/harness/eval/model.go` 的 `decodeStrict`）；其中四种身份文档还带有基于自身精确校验字节的规范 SHA-256 摘要（`ScenarioDigest`、`SubjectDigest`、`ExecutorDigest`，以及派生出的 `Attempt.ScenarioDigest`/`SubjectDigest`/`ExecutorDigest` 字段）。凭据值、机器本地的绝对路径、挂钟时间戳绝不会进入 Scenario 或 Subject 的摘要 —— 只会记录凭据**环境变量名**本身（设计 §10）。

| 文档 | Schema | 写入方式 | 发布后是否可变 |
| --- | --- | --- | --- |
| Scenario | `och.eval.scenario` | 检入 `eval/scenarios/<id>/scenario.json` | 否 —— 它是源文件，不是运行期产物 |
| Subject | `och.eval.subject` | 检入 `eval/subjects/<id>.json` | 否 |
| Executor | `och.eval.executor` | 检入 `eval/executors/<id>.json` | 否 |
| EvalSet | `och.eval.set` | 检入 `eval/sets/<id>.json` | 否 |
| Attempt | `och.eval.attempt` | 在 Subject 启动前，一次性、原子地写入 | 永不 |
| Outcome | `och.eval.outcome` | 执行结束或崩溃恢复后，至多一次、原子地写入 | 永不 |
| EvidenceManifest | `och.eval.evidence-manifest` | 证据暂存完成后一次性、原子地写入 | 永不 —— 它是一个 Attempt 可被评分的提交标记 |
| Score | `och.eval.score` | 每次评分/重新评分调用各写入一次 | 永不覆盖更早的 Score；重新评分总是追加 |

单个 Attempt 自己隔离的文件系统根目录（`NewAttemptRoot`，`internal/harness/eval/fixture.go`）：

```text
<artifactRoot>/<attempt-id>/
  attempt.json
  outcome.json
  workspace/       （Scenario 自己的夹具树，被复制并重新计算摘要）
  database/        （该 Attempt 私有的 SQLite 文件）
  audit/           （该 Attempt 私有的 JSONL 审计副本）
  process/
  log/
  evidence/
    manifest.json
    transcript.jsonl
    audit/segments/*.jsonl
    workspace/<已收集的路径>
    scenario.json, subject.json, executor.json, attempt.json （已暂存的副本，彼此交叉校验摘要）
```

`AttemptID`/`ScoreID` 是 128 位加密安全的随机小写十六进制字符串（`NewAttemptID`，使用 `crypto/rand`，绝不使用 `math/rand`）；`ScenarioID`/`SubjectID`/`ExecutorID`/`EvalSetID` 由使用者提供，匹配 `[a-z0-9][a-z0-9._-]*`，长度不超过 128 字节。路径本身永远不是身份标识（`AttemptPaths` —— 设计 §10/§12）。

## 矩阵展开

`EvalSet.ExpandCells()` 是 EvalSet 所声明的每个 Scenario × Subject × Executor 引用的一个扁平笛卡尔积 —— 单个 EvalSet 文档内部没有按 Cell 选择性配对的机制。`ExpandAttempts` 还会重新校验每个被引用文档的摘要是否仍与 EvalSet 冻结时一致，检查每个 Cell 的 Scenario 所要求的能力是否存在于其 Executor 上（**整个 EvalSet** 因缺失能力而失败，而不是跳过某一行 —— 设计 §9），强制夹具通道与实时通道各自的 Subject 一致性，并在总量超过 `EvalSetLimits.MaxExpandedAttempts`（默认 256，硬上限 4096）时先行拒绝，不返回任何结果。

正因为没有选择性配对，一个 EvalSet 如果对**同一个** Scenario 需要两组不同的 Subject/Executor 配对（例如执行器对等性测试的基线组与候选组），就无法在一个文档里同时列出两组配对而不产生多余的交叉 Cell —— 参见 [`eval/sets/pr-parity-baseline.json`](../../eval/sets/pr-parity-baseline.json) 与 [`pr-parity-candidate.json`](../../eval/sets/pr-parity-candidate.json)：两个共享同一产物根目录、基数各自最小化的独立文档，而非一份合并文件。

## 夹具隔离

Scenario 自身的 `fixtureDigest` 是 `DigestFixtureTree` 对其 `fixture/` 源目录树做路径排序、内容与可执行位绑定的规范 JSON 编码后计算出的 SHA-256（时间戳与属主信息被排除在外）。`RunEvalSet` 会在把该目录树复制进全新的 Attempt 工作区**之前**校验一次这个摘要，复制完成**之后**再校验一次 —— 一个被改动过的检入夹具，或一次损坏的复制，都会在任何 Subject 启动之前被拒绝,而不是事后才被发现。

夹具通道的 Subject 的 `provider.normalizedEndpoint` 使用符号化的 `fixture://<script-name>` 协议；`cmd/och-eval/fixture.go` 的 `resolveFixtureSubjects` 会为每个被引用的脚本名启动一个真实的进程内 `httptest.Server`，并**仅在内存中**将端点改写为真实的 `http://127.0.0.1:<port>` 地址，从不写回磁盘 —— 检入的 Subject 文档自身的字节与摘要永不改变。这就是 `RunnerInputs.ProviderEndpointOverrides`，一个纯粹的执行期事实（`resolveExecutionSubjects`，`internal/harness/eval/runtime_subject.go`）；`ExpandAttempts`/`Attempt` 的摘要始终使用冻结的 Subject，绝不使用这个覆盖值。

## 执行器生命周期

### `in_process`

`RunAttempt`（`internal/harness/eval/inprocess.go`）在本进程内直接打开一个真实的 `composition.Assembly`（`composition.Open`），针对它驱动每一个 Scenario 动作（`Service.RunTurn`/`CompactSession`，一个脚本化的 `ApprovalMatcher` 被接入作为该 Assembly 自己的 `tools.Approver`），并在每一条终止路径上关闭它（`assembly.Close()`），在返回之前证明 `WriterStopped`。`restart` 动作会在**同一个**数据库/工作区之上,以一个新的运行时 ID 重新打开一个全新的 Assembly；这个执行器只接受 `clean_shutdown` 模式 —— `interrupt`/`kill` 会被拒绝为 `infra_failed/unsupported_restart_mode`，因为这里根本没有一个独立的进程可供两者中的任意一种去粗暴终止。

### `acp_subprocess`

`RunACPAttempt`（`internal/harness/eval/acp_executor.go`）在它自己的进程组中（`startACPProcess`，`Setpgid`）启动一个真实的、独立构建出来的 `och -acp` 二进制文件，通过一个极简的、独立实现的 ACP v1 NDJSON 客户端（`acp_wire.go` —— 刻意不使用 `internal/client/acp`，遵循上文的隔离边界）驱动它，并监管它完整的生命周期：受限的 stderr 捕获、白名单化的子进程环境变量（绝不整体传递 `os.Environ()` —— `BuildChildEnvironment`），以及二进制哈希锁定（`ResolveACPBinary`）。与 `in_process` 不同，这个执行器接受**全部三种**重启模式，并将 `compact` 实现为一个真实的、租约安全的三进程事务（见下文）。在 Windows 上完全不受支持（`acpProcessSupported = false`，`internal/harness/eval/acp_process_windows.go`）—— 设计明确拒绝为 Windows 缺失的真实进程组终止路径去近似出一个"只终止父进程"的替代方案。

`RunEvalSet`/`och-eval run -och-binary` 会依据某个 Cell 自身 `Executor.Kind` 的取值分派到相应的执行器；一个声明为 `acp_subprocess` 但没有解析出二进制路径的 Cell，会在创建任何 Attempt 之前就被拒绝。

## 取消与重启

`escalateCancel`（`internal/harness/eval/acp_actions.go`）实现了设计 §16/§17 中精确的四级升级阶梯，用于处理某个后续 `cancel` 动作所指向的一个正在进行中的 prompt：`session/cancel` → 等待 `CancelGrace` → 关闭 stdin → 等待 `ShutdownGrace` → 向所属进程组发送 SIGTERM → 等待一个宽限期 → 向所属进程组发送 SIGKILL → 回收。每一级都会让"该 prompt 自身被解决"与"该级自身的宽限期到期"两者赛跑，以先发生者为准；只有最温和的一级（仅靠 `session/cancel` 就解决）会让写入进程继续存活 —— 超过这一级的任何一级都会将其彻底拆除，且该 Attempt 无法再继续执行该动作之后的内容（`indeterminate/acp_cancel_escalated`，如果连 SIGKILL 本身的回收都无法被证明，则为 `indeterminate/acp_cancel_reap_unproven`）。`exec.CommandContext` 自身由 ctx 触发的终止从来都不是主要的终止路径 —— 它只能触及一个进程的直接子进程，而无法触及整个进程组 —— 并且没有任何取消或重启路径会向一个从租约或数据库状态中读回来的 PID 发送信号，只会面向本包自己启动的进程句柄。进程内执行器自己的 `cancel` 动作根本没有子进程可供升级：它直接通过 `context.Cancel` 中断正在进行的 Go 调用。

重启（`runACPRestart`）会先关闭当前连接，再依据模式分派：`clean_shutdown` 关闭 stdin 并正常等待；`interrupt` 向所属进程组发送 SIGINT；`kill` 发送 SIGKILL。只有在**证明**前一个写入进程的回收已经完成之后，才会以一个新的、不同的运行时 ID 启动后继进程，并通过 `session/load` 恢复**同一个** ACP 会话 —— 未被证明的回收是调用方自己的一个不确定事实，绝不会被悄悄当作成功处理。这里有一个真实、经过验证的与运行时自身单写者围栏租约（`internal/harness/adapters/sqlite/lease.go`）的交互：一个被粗暴终止的写入进程永远不会主动释放它的租约，因此后继进程（一个不同的运行时 ID）在该租约自然过期（默认 30 秒）之前都无法获取它 —— `relaunchACPSuccessor` 会在一个专门的 `RelaunchGrace` 边界内（默认值充分超过 30 秒）反复重试"启动 + 初始化"序列，而不是在第一次尝试就直接判定失败。`RestartModeInterrupt` 针对一个真实的、原本处于空闲状态的代理，被单独发现在任何边界内都无法可靠终止 —— `internal/harness/adapters/acp` 自身的 `Serve`/`decodeFrames` 循环只在已解码的帧之间才会检查 `ctx.Err()`，在阻塞等待下一帧时永远不会检查 —— 这是那个包自身 Serve 循环的一个属性，与本包无关；`RunACPAttempt` 在这种情况下会正确地报告 `infra_failed`，而不是一个虚假的成功（具体复现步骤参见证据台账）。

## 将手动压缩实现为一个租约安全的事务（仅限 `acp_subprocess`）

`runACPActionCompact`（`internal/harness/eval/acp_compact.go`）是一个三阶段事务，每一阶段都以上一阶段自身的证明作为门槛：

1. 关闭当前写入进程，并**证明**其回收（如果无法获得该证明，则为 `indeterminate/acp_shutdown_unproven`；如果它以非零状态回收，则为 `infra_failed/acp_shutdown_failed`）—— 在获得该证明之前，压缩进程绝不会被启动。
2. 以一个新的、不同的运行时 ID 启动 `och compact-session`，并等待**它自己**被证明的退出（任何非零退出、超时或无法解码的标准输出都会导致 `infra_failed/acp_compactor_failed`）—— `runACPCompactor` 是一个独立的一次性进程启动器（不同于 `startACPProcess` 面向长生命周期 NDJSON 服务器的实现），会对超时未退出的压缩进程强制发送 SIGKILL，确保绝不会留下一个悬挂的进程。
3. 以**第三个**不同的运行时 ID 重新启动一个后继写入进程，并通过 `session/load` 恢复同一个 Session。在压缩进程**已被证明干净回收**之后仍然出现的重启失败，会被单独分类（`infra_failed/runtime_lease_not_released`，通过匹配 `internal/harness/runtime` 自身 `ErrLeaseHeld.Error()` 产生的真实、已验证的 stderr 文本得出），与任何其他重启失败（`indeterminate/acp_compact_relaunch_unproven`）区分开来。

## 证据信任模型

评分器或验证器从不直接读取原始文件 —— 只能通过 `ArtifactReader`（`internal/harness/eval/artifact_reader.go`）读取，它在每次读取时都会依据已发布的清单重新校验文件大小与 SHA-256，并拒绝自收集以来发生的符号链接替换、硬链接、或类型变化。`EvidenceManifest` 的发布是一个 Attempt 可被评分的提交标记（设计 §12）；清单中缺失某个必需角色会得到 `Indeterminate`，绝不会被悄悄当作不存在处理。冻结的身份文档（Scenario/Subject/Executor/Attempt）会被**作为证据本身**暂存进清单（`EvidenceDocuments`，`evidence_identity.go`），并带有交叉摘要校验，因此 `RegradeAttempt` 完全不需要任何外部提供的 Scenario 输入 —— 它需要的一切，包括该 Attempt 所归属的通道，都能从该 Attempt 自己已提交的证据中读出。

## 恢复

`ClassifyAttemptDirectory`（`internal/harness/eval/recovery.go`）是对单个 Attempt 目录自身磁盘文档的一次纯粹的、四态的读取，按以下固定顺序检查：

| 状态 | 条件 |
| --- | --- |
| `Uncommitted` | `attempt.json` 本身不存在或无法解析 |
| `InspectRequired` | Attempt 存在，`outcome.json` 不存在 |
| `ResumeCollectionOnly` | Attempt 与 Outcome 都存在，`manifest.json` 不存在 |
| `Terminal` | 三者都存在 |

`ResumeCollection` 执行本包自动化的唯一恢复步骤：针对一个**已经存在**的 Outcome 重新暂存证据并发布清单，绝不重新发布或修改该 Outcome —— 恢复流程从不为了获得一个"新鲜"的写入进程而重新打开一个。

## 限制

`AttemptExecutionLimits`（`internal/harness/eval/limits.go`）会将每一项 EvalSet 声明的限制，依据一份文档化的默认值与硬性上限来解析（总时长、单个动作时长、进程启动、取消/关闭宽限期、证据收集时长）—— 一个 EvalSet 只能**收紧** Scenario 自身声明的限制，绝不能放宽超过其自身的限制。`CollectionLimits` 单独限制证据暂存本身（最大文件数、单个产物最大字节数、总字节数上限），确保一个失控的工作区永远不会让证据收集本身变得无界。

## 评分：确定性验证器与实时评审器

一个 `Verifier`（`internal/harness/eval/verifier.go`）是一个固定的、带版本号的、编译进二进制的 Go 函数 —— 绝不是 EvalSet 或 Scenario 可以提供的数据文件代码 —— 通过 ID 在 `verifierCatalog` 中索引；一个未注册的 ID 属于非法的 EvalSet 输入，而不是一个需要恢复的运行时故障。本里程碑发布的每一个验证器都被证明是失败即拒绝（fail-closed）的：真实、可读、但确实不包含所声称行为的证据会返回 `Fail`，而根本未被收集到的证据会返回 `Indeterminate` —— 这两种情况都绝不会返回 `Pass`。

实时模型评审器（`internal/harness/eval/judge.go`，Task 17）是面向另一条通道的另一种机制：`RunJudge` 只依据某个 `JudgeConfig` 自身 `Criteria` 所声明的清单角色，构建一个有界的、经过脱敏的证据包，将其发送给一个可注入的 `JudgeCaller`（因此一次真实的实时模型调用与一个测试替身实现的是完全相同的函数类型 —— `RunJudge` 自身从不打开网络连接），并严格解码其响应。在调用任何评审器之前，冻结的评审配置会先经过验证：模型标识与内嵌提示词的精确摘要均为必填项，评判标准 ID 与证据角色必须非空且唯一，并且受信任的评判标准合同会随证据包一同发送。

`JudgeConfig` 是一份文档而非内存中的值：schema 为 `och.eval.judge-config`，其验证与摘要方式与 Scenario/Subject/Executor 完全一致。这正是一个实时 Score 的评审器身份能够离线自证的原因。实时 `EvalSet` 必须声明 `judgeConfigDigest`，fixture 通道的 EvalSet 则必须不声明；每一个新 Attempt 都会把其冻结的 `EvalSet` 作为 `eval-set.json`（角色 `eval_set`）纳入证据，实时 Attempt 还会额外纳入 `judge-config.json`（角色 `judge_config`），因此清单会对两者取哈希，任何后来的读者都能重建出某个判定究竟来自哪一份配置，而无需信任产出它的调用方。这一绑定在读取时（`readJudgeEvidenceDocuments`）会重新校验，而不只是在写入时校验 —— 几个月后打开某个 Attempt 的读者并没有展开步骤可以依赖。在这些角色出现之前采集的 Attempt 仍然可以确定性重新评分，但永远无法接受实时评审 —— 这是诚实的结果：它的证据无法证明自己有此资格。

证据的选取是清单与配置的纯函数。被声明的角色存放在集合中，而 Go 的 map 迭代顺序是随机的，因此候选列表会在任何字节预算生效**之前**被完整排序 —— 否则对同一个 Attempt 判定两次，可能让评审器看到不同的证据，并接受不同的 `evidenceReferences`。遗漏是失败即拒绝的，而不是把问题缩小：某个被声明但清单从未采集的角色，或者总预算无法容纳的条目，都会在调用评审器之前中止本次运行，并记入 `missingEvidence` —— 因为一个被询问了自己从未见过的材料的模型，其"通过"回答与真正读过材料后给出的回答是无法区分的。逐条目截断仍然被允许 —— 该契约本就提供有界摘录 —— 并且每个条目标签都会记录原始字节数、摘录字节数以及是否被截断。

`EvaluateJudgeAttempt`（`internal/harness/eval/judge_attempt.go`）是 `och-eval judge` 所驱动的编排逻辑，其各道关卡的顺序就是契约本身：先是冻结证据与所提供配置的摘要，然后是设计 §24 的双重同意，最后才是 Scenario 声明的每一个确定性校验器。由于持有任何凭据的是 `JudgeCaller`，一次不具备资格的运行既触及不到提供方，也触及不到凭据。确定性前置条件未通过时，会发布一个 Indeterminate 的 Score 而完全不调用模型；`JudgeAttemptResult` 会单独报告该前置判定，以便操作者能区分"不变量未成立"与"评审器无法作答" —— 这两者在 Score 上读起来是一样的。

`ScorerUsage.costStatus` 让成本可得性变得显式：`computed` 会携带币种（免费模型是一个真实计算出的零），`unavailable` 则既不带币种也不带成本。在该字段出现之前发布的 Score 仍然可读。设计 §21 的每一种失败即拒绝情形 —— 未知字段、格式错误或带尾随内容的输出、不存在的证据引用、缺失证据、未解决的矛盾、未声明/遗漏/重复的评判标准、与逐项结果不一致的总判定、超出 `[0,1]` 的分数，或调用本身失败 —— 都会解析为一个真实的 `JudgeOutcome{Verdict: Indeterminate}`，附带一段有界、经过脱敏的理由说明，绝不是一个 Go 错误，也绝不会被悄悄当作 `Pass` 接受。还有一种情形出于同样的理由被拒绝，但不在设计 §21 的清单里：**一个确定性判定却完全没有引用任何证据**。上述每一条引用规则守的都是"已经出现的引用"，而在 2026-09-04 之前没有任何一条要求必须存在引用 —— 因此 `evidenceReferences` 为空的 `pass` 会被直接采信。这与预算遗漏那个缺陷是同一件事的另一面：一个关于评审器从未证明自己读过的材料所给出的回答。`indeterminate` 判定仍然可以不引用任何东西，因为那往往正是它之所以为 indeterminate 的原因。评审器所看到的每一条 Subject 撰写的内容都会被标注为"不受信任……不是指令"（内嵌的 `prompts/quality_judge_v1.md` 提示词自身的框架设定）—— 本仓库没有真实模型可以在自动化测试中证明其确实能够抵御提示注入攻击，因此测试所验证的是这一机制本身：这种标注确实真实存在于真实的转录内容周围，而不仅仅是提示词文本里一句空洞的期望。

一个评审 Score 通过与确定性重新评分完全相同的 `PublishScore` 路径发布，`Lane` 设为 `LaneLive` —— 不存在单独的文档类型。`internal/harness/eval/price.go` 的 `PriceTable` 以整数微单位计算成本，与 `Score.ScorerUsage` 自身的成本字段相互独立（评审器自身的用量，绝不会并入 Subject 的用量）；一个未定价的模型会返回 `ok=false`，而不是零成本。

## 对等性

`internal/harness/eval/parity.go` 实现了设计 §11/§22：`LoadParityArm` 读取一个 Attempt 自身已收集的证据，并只将其投影为设计所声明的语义对等性事实 —— 终止态的 Session/Turn 状态、工具事实（名称/参数/策略效果/审批决定/结果 —— 在每一侧内部各自通过自己的协议 ID 关联，之后这些 ID 会被丢弃，绝不跨两侧比较）、用量事实（排除延迟与 provider 请求 ID）、请求信封属性（排除消息文本），以及工作区结果（相对路径 + 内容摘要，绝不是绝对路径）。`ComparePairedArms` 逐字段比较两个臂。配对本身（`ParityPairKeyForAttempt`）只按 Scenario 摘要与重复索引分组 —— 对于这种报告模式而言，Executor Kind 刻意作为变化的维度存在，而设计中列出的其他每一项配对字段（夹具摘要、限制、配对种子）本身已经是同一 EvalSet 下所有共存 Attempt 共享的不变量，无需单独表示。已针对真实的进程内与 ACP 子进程 Attempt 验证过，而非模拟：同样的确定性 Scenario/Subject 语义通过两种执行器运行,得到零差异；两侧脚本化审批决定的真实差异则会被捕获。

## 四单元 PR 车道

设计 §23 自身的常规 PR 门禁：恰好四个 Cell —— 一个通过两种执行器配对的对等性 Scenario（两个 Cell：[`pr-parity-baseline.json`](../../eval/sets/pr-parity-baseline.json)/[`pr-parity-candidate.json`](../../eval/sets/pr-parity-candidate.json)）、一个进程内的工具/审批/失败 Cell，以及一个进程内的 Context 压缩 Cell（两者都在 [`pr-tool-and-compaction.json`](../../eval/sets/pr-tool-and-compaction.json) 中）。`cmd/och-eval/report.go` 会为同一产物根目录下每一个终止且已完整收集的 Attempt 加载一个 `ParityArm`，按 `ParityPairKey` 分组，并依据任何非空差异列表来决定该报告自身的退出码。`TestPRLaneExpandsToExactlyFourFixtureCells` 与 `TestPRLaneRunAndReportEndToEnd`（`cmd/och-eval`）都是普通的 Go 测试，因此本仓库现有的 CI `go` 任务（`go test -race ./... -count=1`）已经会在每个 PR 上对这条车道进行门禁检查，无需任何专门的 CI 工作流步骤。

完整的确定性矩阵（两种执行器 × 每一个 Scenario）只通过显式命令运行（`go test ./internal/harness/eval/... -run TestCheckedInDeterministicFullSetProvesToolWorkspaceSuite`，或 `och-eval run -set eval/sets/deterministic-full.json`），从不出现在常规 PR CI 中。

完整的 Context 机制矩阵 —— 九个 EvalSet，其中五个会拉起真实的 `och -acp` 子进程 —— 通过 `OCH_EVAL_SCHEDULED_CONTEXT_MATRIX=1` 按名称显式启用。未设置该变量时，`TestContextScheduledLaneRunsEveryPairedSet` 会跳过。CI 中唯一设置该变量的任务是 `context-matrix`，它以 `if: github.event_name == 'schedule'` 为条件，并且只运行一条聚焦命令一次（`go test -race ./cmd/och-eval -run '^TestContextScheduledLane' -count=1`）；`go`、`determinism` 与 `soak` 任务都不设置该变量，因此 `-count=1`、`-count=3` 与 `-count=10` 的全量套件运行都不会把它展开。

这条边界是被强制执行的，而不只是被声明的。`TestFullContextMatrixSkipsWithoutTheOptIn` 会在剥离该环境变量后重新调用测试二进制，并要求出现 SKIP；`TestCIEnablesTheFullContextMatrixOnlyInAScheduledJob` 与 `TestBroadSuiteJobsNeverEnableTheFullContextMatrix`（均在 `cmd/och-eval`）会解析 `.github/workflows/ci.yml`，要求恰好一个任务设置该变量、该任务以 schedule 为门禁、其命令是聚焦且 `-count=1` 的，并且没有任何全量套件任务携带它。在 2026-09-04 的 `10190a2` 与本次修改之间，这条边界只存在于文字之中 —— 该车道的门禁是 `testing.Short()`，而没有任何 CI 任务传入 `-short` —— 因此完整矩阵实际在每个 PR 上都会运行，`go` 任务一次、`determinism` 再三次，而本节当时却声称它从不运行。上一段所声明的内容，现在是一个测试。

## 实时车道

`internal/harness/eval/live.go` 的 `RequireLiveConsent` 是设计 §24 双重授权门禁的唯一权威来源：一个 EvalSet 自身声明的通道与显式的 `--live` 标志必须精确一致，并且实时通道还额外要求环境中存在 `OCH_EVAL_LIVE_CONFIRM=I_UNDERSTAND` —— `RequireLiveConsent` 自身从不读取环境变量或凭据，因此一个先检查它、并在遇到非空错误时拒绝继续执行的调用方，才真正落实了"在读取任何凭据之前"这一承诺。`cmd/och-eval/run.go` 自己的 `checkLaneConsent` 委托给它执行，而不是重复实现这条规则。一次实时运行总是写入一个独立的产物根目录，本仓库也从不会自动将任何证据上传到任何地方。

## 平台支持

| 平台 | `in_process` | `acp_subprocess` |
| --- | --- | --- |
| Linux | 支持 | 支持（`acp_process_unix.go`，`//go:build unix`） |
| macOS | 支持 | 支持（同一文件，`unix` 构建标签覆盖 darwin） |
| Windows | 支持 | 不支持 —— 在任何进程被启动之前就被拒绝（`acpProcessSupported = false`） |

## 成熟度与 GA 阻碍项

评估系统**已实现，但尚未 GA**。在做出 GA 声明之前明确悬而未决的事项包括：实时评审所需的真实模型样本规模、超出本仓库当前所携带的八个对抗性夹具（注入、证据缺失、矛盾、无支撑主张、已知通过/失败、凭空捏造的引用、真实存在但从未被展示过的引用，以及一个不引用任何证据的确定性判定）之外更广泛夹具集合上的评审器元评估 —— 其中原有五例里有两例在 2026-09-04 被发现是被一条比它们所声称的更早的检查拒绝的，因而对它们本该守护的那条防线什么也没有证明；两者均已修正，现在会断言拒绝的具体原因 ——、超出本仓库目前唯一一个 OpenAI 兼容适配器之外的更广 provider 覆盖面，以及一份被接受的实时/质量信号方差策略。MCP 是这个运行器未来可以承载的一个测试套件，绝不是运行器自身的前置条件 —— 它的缺席不会阻碍本文档所记录的任何内容。
