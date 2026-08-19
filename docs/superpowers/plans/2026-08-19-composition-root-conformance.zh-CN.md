# 组合根与跨 Adapter 一致性实施计划（中文阅读版）

> **给智能体执行者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 按任务逐项实施本计划。步骤使用复选框（`- [ ]`）语法跟踪。

英文版本 [2026-08-19-composition-root-conformance.md](2026-08-19-composition-root-conformance.md) 是规范文本；本文是与之完整同步的中文执行阅读版。两者若有分歧，以英文为准。

**目标：** 把每个已实现 adapter 装配到一个被测试的组合根之下，让既有场景套件跑在持久化 store 上，拆分模型合同使真实 HTTP provider adapter 能够满足它，并证明整个技术栈能在无网络、无凭据的条件下端到端执行一个含工具调用的 turn。

**架构：** 不改端口、事件、错误码与合同。`internal/harness/composition` 是唯一被允许导入 adapter 的包，由架构守卫强制。`cmd/och` 是其上的薄二进制。一致性套件是**重新分配**，不是重写。

**技术栈：** Go 1.26、标准库（`net/http/httptest`、`errors`、`os/signal`）、经既有 adapter 使用的 `modernc.org/sqlite`、`testing`、race 与交叉编译工具、GitHub Actions。

## 全局约束

- 规范说明：`docs/superpowers/specs/2026-08-19-composition-root-conformance-design.md`，第 4–13 节为强制。研究证据：`docs/research/architecture-gates/2026-08-19-composition-root-and-conformance.md`。
- **本切片不增加任何能力。** 不新增端口、事件类型、错误码、错误类别或领域状态转移。若某任务看似需要，**停下并作为发现上报**，不要扩大切片。
- 不得修改任何已实现合同文档。确需修改则作为发现记入证据台账，并附独立后续切片。
- `enginescenariotest` 与 `eventstoretest` 不得改动。`engine/modeltest` 只以**抽取**方式重组：`Run` 今天执行的每个用例，拆分后仍须对 `testkit.ScriptedModel` 执行。
- `internal/harness/composition` 是唯一可导入 `internal/harness/adapters/...` 的包。`cmd/och` 只可导入 `composition` 与标准库。架构守卫中其他每条禁止项保持不变，未声明 owner 的目录仍被禁止。
- 不引入插件内核、注册表、服务定位器、反射，或 `init()` 期接线。构造是按固定顺序的显式调用。
- 验证为无密钥、无网络。provider 经 `httptest` 回放 SSE fixture 驱动。任何测试不得读取真实 API key，任何默认 CI 通道不得访问 provider。
- 生产代码中不得有仅供测试的分支。测试接缝是构造函数参数，不是 build tag 或导出的可变全局量。
- `Open` 绝不在返回非 nil 错误的同时返回非 nil `Assembly`，也绝不泄漏部分构造的资源。
- 新测试中不得使用基于 sleep 的同步。goroutine 汇合使用 channel，配以有文档说明的宽松超时，与 application 包引入的常量保持一致。
- 每项行为均 TDD：先观察到预期失败，再实现，然后跑聚焦测试与全量测试。
- 每个任务以 `gofmt`、聚焦测试、`go test ./... -count=1`、（当任务涉及并发或装配时）`go test -race ./... -count=1`、一次独立评审门禁，以及一个小提交收尾。
- 英文为规范。中文计划是完整同步的阅读版，一并提交。

## 文件映射

| 路径 | 职责 |
| --- | --- |
| `internal/harness/composition/doc.go` | 包范围：唯一点名 adapter 的地方，以及它不得做什么 |
| `internal/harness/composition/config.go` | `Config`、默认值、全量 fail-closed 的 `Validate` |
| `internal/harness/composition/assembly.go` | `Open`、`Assembly`、访问器、带 join 错误的有序 `Close` |
| `internal/harness/composition/config_test.go` | 校验表：每个字段的每条记录在案的拒绝理由 |
| `internal/harness/composition/assembly_test.go` | 构造顺序、失败不泄漏、`Close` 幂等、关闭超时 |
| `internal/harness/composition/end_to_end_test.go` | 装配测试：真实技术栈上的一个含工具调用的 turn |
| `internal/harness/composition/testdata/sse/read_file_turn.sse` | 装配测试的 SSE 脚本（若无现成 fixture 适用） |
| `cmd/och/main.go` | flag、环境、信号处理；无其他逻辑 |
| `internal/harness/engine/modeltest/contract.go` | `Contract`、`RunContract`：传输中立用例 |
| `internal/harness/engine/modeltest/suite.go` | 保留 `Run`：`RunContract` 加 double 记账用例 |
| `internal/harness/adapters/openaicompat/contract_test.go` | 经 `httptest` 运行 `RunContract`，配 `ProviderFailure` 匹配器 |
| `internal/harness/adapters/sqlite/scenario_test.go` | 对真实 store 运行 `enginescenariotest.Run` |
| `internal/harness/architecture/dependencies_test.go` | `ownerComposition`；未声明 owner 的目录仍被禁止 |

---

### 任务 1（PR 1）：模型合同拆分

**意图：** 在任何传输消费它之前，先让模型合同变成传输可表达的。

- [ ] 新增 `internal/harness/engine/modeltest/contract.go`，含 `Contract{Factory, MatchStartupError, MatchStreamError}` 与 `RunContract`。
- [ ] 把传输中立用例迁入 `RunContract`：精确请求投递与有序 unicode 的 `text_delta`/`tool_call`/`completed`、流中错误、阻塞至取消的步骤、并发独立流。
- [ ] 匹配器为 nil 时，`RunContract` 要求错误身份相等，从而保住进程内 double 今天的断言。
- [ ] 保留 `Run(t, factory)` 作为 double 入口：先以 nil 匹配器调用 `RunContract`，再跑 `ReturnNilStream`、`ReturnStreamOnStartupError` 优先级、`Close` 记账。
- [ ] 核验无用例丢失：`testkit.ScriptedModel` 拆分前后运行同一集合。

**验证：** `go test ./internal/harness/testkit ./internal/harness/engine/... -count=1`；在 `RunContract` 中故意引入变异必须让 `scripted_model_test.go` 失败。

**完成判据：** 拆分存在，`testkit` 覆盖不变，且尚无 adapter 消费 `RunContract`。

---

### 任务 2（PR 2）：`openaicompat` 满足传输合同

**意图：** 证明真实 HTTP adapter 遵守与 double 相同的 `engine.Model` 合同。

- [ ] 新增 `adapters/openaicompat/contract_test.go`，其工厂把 `modeltest.Config` 翻译成由 `httptest.Server` 提供的 SSE 字节。
- [ ] 把 `Steps` 映射到 SSE 分块：`text_delta` → content delta，`tool_call` → `tool_calls` delta，`completed` → finish 分块加 `[DONE]`。
- [ ] 启动失败以非 2xx 响应表达，流中失败以截断或畸形事件表达；提供断言 `ProviderFailure` 分类（而非错误身份）的匹配器。
- [ ] `WaitForCancel` 以「阻塞至请求 context 结束」的 handler 表达。
- [ ] 已有 `testdata/sse` fixture 能表达该用例时复用；仅在无现成 fixture 时新增。
- [ ] 在测试文件中记录哪些合同用例**传输不可表达**及其原因，并引用规范第 9 节。

**验证：** `go test -race ./internal/harness/adapters/openaicompat -count=1`；打乱 SSE 文法顺序时套件必须失败。

**完成判据：** `RunContract` 对 `openaicompat` 全绿，且 adapter 生产代码零改动。此处若需要生产改动，那是一条**发现**：先上报，再动手。

---

### 任务 3（PR 3）：场景套件跑在持久化 store 上

**意图：** 让 Engine 场景合同不再只是「内存 adapter 才成立」的保证。

- [ ] 新增 `adapters/sqlite/scenario_test.go`，用 `Harness` 调用 `enginescenariotest.Run`，其 `Store` 是在 `t.TempDir()` 上打开的真实 store，形态对齐 `conformance_test.go`。
- [ ] 不修改 `enginescenariotest`。
- [ ] 对每个失败场景，判定缺陷在 SQLite adapter，还是在某个内存 adapter 恰好满足的假设；修复 adapter 并记录该发现。
- [ ] 若某场景被证明编码了仅内存成立的假设，先上报；仅当内存 adapter 当前断言的每条行为都被保住时才修正套件。

**验证：** `go test -race ./internal/harness/adapters/sqlite ./internal/harness/application -count=1`，并对新测试跑 `-count=5` 以暴露顺序敏感性。

**完成判据：** 同一场景套件对两个 adapter 均全绿，且发现的每处分歧都已记录。

---

### 任务 4（PR 4）：组合根与依赖守卫

**意图：** 建立点名 adapter 的唯一位置，并证明别处点不了名。

- [ ] 按规范第 4–6 节新增 `composition/doc.go`、`config.go`、`assembly.go`：固定构造顺序、逆序 `Close`、join 错误、有界关闭超时、`Close` 幂等。
- [ ] `Config.Validate` 全量且 fail-closed；构造任何资源之前检查每个字段。
- [ ] `Open` 返回错误前释放已构造的一切，且绝不在返回非 nil 错误的同时返回非 nil `Assembly`。
- [ ] 扩展 `architecture/dependencies_test.go`，新增 `ownerComposition`，仅在该处允许 adapter 导入；断言未声明 owner 的目录仍被禁止，且 `application` 仍不能导入 adapter。
- [ ] 新增 `config_test.go` 与 `assembly_test.go`：校验表、失败不泄漏（含不创建数据库文件）、重复 `Close`、关闭超时。

**验证：** `go test -race ./... -count=1`；若把组合例外扩大到第二个包，`go test ./internal/harness/architecture -count=1` 必须失败。

**完成判据：** 根能干净地装配与拆解，且守卫比之前**更严格**。

---

### 任务 5（PR 5）：装配测试、二进制与证据

**意图：** 证明技术栈能跑真实 turn，并留下可审计的完成记录。

- [ ] 按规范第 10 节新增 `composition/end_to_end_test.go`：临时工作区预置文件、真实 SQLite 数据库、经 `httptest` 脚本化先 `read_file` 后作答的 `openaicompat`、`policy.ModeDefault`、host 启动并经 `Assembly.Close` 关闭。
- [ ] 断言 turn 完成、持久流回放得到相同状态、流中含 `tool.call.started`/`policy.decision.recorded`/`tool.call.completed`、最终文本反映文件内容。
- [ ] 新增 `cmd/och/main.go`：flag、环境、`composition.Open`、等待信号、`Close`、出错非零退出，别无其他。
- [ ] 把 `cmd/och` 纳入交叉编译矩阵预期；确认 `GOOS=windows` 与 `GOOS=darwin` 构建通过。
- [ ] 撰写 `docs/architecture/composition-root.md` 及其中文阅读版作为已实现合同，并撰写 `docs/architecture/composition-root-evidence.md` 作为双语证据台账，记录每个任务提交、验证命令、合同发现与遗留 GA 阻塞项。
- [ ] 更新 `docs/README.md` 权威表与里程碑状态，以及根 `README.md` 的当前状态。

**验证：** `gofmt -l .`；`go vet ./...`；`go test -race ./... -count=1`；`GOOS=windows go build ./...`；`GOOS=darwin go build ./...`；`go run ./cmd/och -help`。

**完成判据：** 一条命令构建出二进制，一个测试证明技术栈能持久地执行一个含工具调用的 turn，台账留下记录。

---

## 最终完成门禁

- [ ] 规范第 4–13 节全部满足，或每处偏离都带理由记入台账。
- [ ] `enginescenariotest.Run` 对 memory 与 SQLite 均全绿。
- [ ] `modeltest.RunContract` 对 `testkit.ScriptedModel` 与 `openaicompat` 均全绿；`modeltest.Run` 对 `testkit.ScriptedModel` 全绿且无用例丢失。
- [ ] adapter 导入仅在 `composition` 被允许；未声明 owner 的目录仍被禁止。
- [ ] 装配测试在无网络、无凭据、无 sleep 同步的条件下通过。
- [ ] `cmd/och` 在每个交叉编译平台构建通过。
- [ ] 没有已实现合同文档被修改；若有，每处修改都在台账中作为已记录的发现给出理由。
- [ ] `go test -race ./... -count=1` **连续三次**全绿，以便在进入 CI 之前捕获顺序与时序敏感性。
