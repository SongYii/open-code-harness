# EventStore v2 Contract Migration 实施计划

> **面向智能体工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans，逐项实施本计划。步骤使用复选框（`- [ ]`）跟踪。

**目标：** 用具备精确语义、分页与 Receipt 的 EventStore v2 Contract 替换含糊的完整 Stream v1 边界，并将 Application 迁移到 Caller-Stable Request Admission、有界 Unknown-Outcome Resolution 和 Compact Deterministic Write State。

**架构：** Domain 保持纯净并拥有生命周期规则；Application 创建稳定 Append Metadata，并在不重复外部 Effect 的前提下编排重试；每个 Store Adapter 只拥有 Sequence 与 Commit Position。为保证中间提交始终可编译，新的 v2 Reference Adapter 与 Conformance Suite 会短暂地和 v1 并存；最终 Cutover 必须删除全部 v1 Production Surface。

**技术栈：** Go 1.26、Go 标准库、`testing`、`crypto/sha256`、`encoding/binary`、现有 JSONL Fixture、Race/Fuzz/Benchmark Tooling、GitHub Actions。

## 全局约束

- 规范规格：`docs/superpowers/specs/2026-08-13-eventstore-v2-contract-design.md`；EV2-01 至 EV2-12 全部强制执行。
- 调研证据：`docs/research/architecture-gates/2026-08-13-eventstore-v2-contract.md`。
- 本计划只实施 Slice 1：不得加入 SQLite、SQL Schema、第三方 Module、JSONL Export/Import、持久 Runtime Lease、ACP、TUI、Tool、Provider 或 Context Management。
- Domain 不得导入 Application、Engine、Adapter、Clock、Randomness、Filesystem、Logging 或 Network Package。
- Application 不得导入具体 Adapter；Engine 不感知 Storage 与 Receipt Resolution。
- `AppendID`、Proposed `EventID`、Schema Version、UTC Occurrence Time、Command Admission 与 Request Digest 必须在第一次 Store Call 前存在。
- Store 只分配 Per-Session Sequence 与 Global Commit Position。
- 禁止无界隐藏重试。默认 `AppendResolutionTimeout = 5s`，初始 Unknown Result 之后最多执行 `AppendResolutionMaxOperations = 4` 次 Store Operation。
- 上限为：单个 Canonical Event Payload 8 MiB、每个 Append 64 个 Event、Encoded Append Request 16 MiB、每个 Read Page 256 条 Record。Canonical Fact 只能拒绝，不能截断。
- 所有行为遵循 TDD：先观察预期失败，再实现并运行聚焦与完整测试。
- 每个 Task 都以 `gofmt`、聚焦测试、`go test ./... -count=1`、涉及并发时的 `go test -race ./... -count=1`、独立 Review Gate 和一个小提交结束。
- 英文是规范来源；中文计划是完整同步阅读副本，必须在同一个文档提交中更新。
- 为保证 v1 共存期间的中间提交可编译，新请求类型临时命名为 `AppendRequestV2`，新接口临时命名为 `EventStoreV2`。任务 8 会原子性提升为规格最终名称 `AppendRequest` 与 `EventStore`；Production 中不得残留临时 v2 名称。

## 文件映射

| 路径 | 职责 |
| --- | --- |
| `internal/harness/domain/ids.go` | `AppendID` 与 `RunTurnRequestID` Validation |
| `internal/harness/domain/compact_state.go` | 有界写侧 Session/Turn/Item 表示 |
| `internal/harness/domain/compact_apply.go` | 确定性 Compact Replay Transition |
| `internal/harness/domain/compact_decide.go` | 基于 Compact State 的 Command Decision |
| `internal/harness/domain/compact_equivalence_test.go` | 冻结 v1 Oracle 与 Decision Equivalence 证明 |
| `internal/harness/application/store_v2.go` | 临时 v2 Store Interface、`AppendRequestV2`、Record、Authority、Lookup 与 Receipt Type |
| `internal/harness/application/store_errors.go` | 稳定 Store Error Algebra 与 Predicate |
| `internal/harness/application/digest.go` | 严格 Digest Type 与 Versioned Framed Digest Codec |
| `internal/harness/application/read_stream.go` | Pinned-Head Pagination Helper |
| `internal/harness/application/append_v2.go` | 稳定 Append Intent 构造、Receipt Validation、Resolution 与 Apply |
| `internal/harness/application/execution_registry.go` | Per-Request Live Ownership、Phase、Waiter 与 Session Admission Gate |
| `internal/harness/application/request_result.go` | 从 Canonical Event 重建一个 Admitted Request Result |
| `internal/harness/adapters/memory/event_store_v2.go` | 确定性 Reference Store、Identity Index、Receipt、Paging 与 Fault Hook |
| `internal/harness/application/eventstoretest/v2_suite.go` | Adapter-Neutral v2 Conformance Suite |
| `internal/harness/testkit/ids.go` | 确定性 Append ID Generation |
| `internal/harness/testkit/v2_store.go` | Application Scenario Store Spy 与精确 Fault Script |
| `docs/architecture/eventstore-v2.md` | Cutover 后的 Implemented Contract |
| `docs/architecture/eventstore-v2-evidence.md` | Commit、验证输出、Benchmark Baseline、Exclusion 与剩余 Blocker |

---

### 任务 1：增加稳定 Identity、Digest Value、Store Type 与 Typed Error

**文件：**
- 修改：`internal/harness/domain/ids.go`
- 修改：`internal/harness/domain/ids_test.go`
- 创建：`internal/harness/application/store_v2.go`
- 创建：`internal/harness/application/store_errors.go`
- 创建：`internal/harness/application/store_v2_test.go`
- 修改：`internal/harness/application/ports.go`
- 修改：`internal/harness/application/ports_test.go`
- 修改：`internal/harness/application/session_test.go`
- 修改：`internal/harness/application/turn_success_test.go`
- 修改：`internal/harness/adapters/memory/event_store_test.go`
- 修改：`internal/harness/testkit/ids.go`
- 修改：`internal/harness/testkit/ids_test.go`

**接口：**
- 消费：现有 `domain.SessionID`、`TurnID`、`ItemID`、`CommandID`、`EventID`、`RecordedEvent`、`Event`。
- 产出：`domain.AppendID`、`domain.RunTurnRequestID` 及 Parser，`application.Digest`、`RuntimeID`、`WriterAuthority`、全部 EV2-03 Request/Result Type（其中新请求临时使用 `AppendRequestV2`）、`EventStoreV2`、`StoreErrorCode`、`StoreError`、`IsStoreCode` 与 `IDGenerator.NewAppendID`。

- [ ] **步骤 1：编写失败的 Identifier 与 Generator 测试**

复用现有非法输入 `""`、`"   "`、`" id"`、`"id "` 与非法 UTF-8，增加表驱动测试和精确 Generator 断言：

```go
func TestParseAppendAndRunTurnRequestIDs(t *testing.T) {
    for _, test := range []struct {
        name string
        parse func(string) error
    }{
        {"append", func(v string) error { _, err := ParseAppendID(v); return err }},
        {"request", func(v string) error { _, err := ParseRunTurnRequestID(v); return err }},
    } {
        t.Run(test.name, func(t *testing.T) {
            if err := test.parse(test.name + "-1"); err != nil { t.Fatal(err) }
            if err := test.parse(" " + test.name); !IsCode(err, CodeInvalidID) {
                t.Fatalf("error = %v, want %q", err, CodeInvalidID)
            }
        })
    }
}
```

- [ ] **步骤 2：运行聚焦测试并观察编译失败**

运行：`go test ./internal/harness/domain ./internal/harness/testkit -run 'TestParseAppendAndRunTurnRequestIDs|TestSequenceIDsAreTypedAndIndependent' -count=1`

预期：FAIL，因为新 ID Type、Parser 与 `NewAppendID` 尚不存在。

- [ ] **步骤 3：实现两个 Domain ID 与确定性 Append ID**

```go
type AppendID string
type RunTurnRequestID string

func ParseAppendID(value string) (AppendID, error) {
    if err := validateID(value); err != nil { return "", err }
    return AppendID(value), nil
}

func ParseRunTurnRequestID(value string) (RunTurnRequestID, error) {
    if err := validateID(value); err != nil { return "", err }
    return RunTurnRequestID(value), nil
}
```

为 `SequenceIDs` 增加独立的 `appends uint64` Counter 与 `NewAppendID()`；立即扩展当前 `IDGenerator` Interface，迫使所有现有 Test Double 实现或嵌入完整 Generator Contract。

- [ ] **步骤 4：编写失败的 v2 Type 与 Error Algebra 测试**

测试必须断言：

```go
var _ EventStoreV2 = (*contractStore)(nil)

func TestStoreErrorCommitKnowledge(t *testing.T) {
    for _, code := range allStoreErrorCodes {
        err := &StoreError{Code: code, MayHaveCommitted: code == StoreCodeCommitOutcomeUnknown}
        if got := err.MayHaveCommitted; got != (code == StoreCodeCommitOutcomeUnknown) {
            t.Fatalf("code %q may_have_committed = %t", code, got)
        }
        if !IsStoreCode(fmt.Errorf("wrapped: %w", err), code) { t.Fatalf("code %q not found", code) }
    }
}
```

同时测试严格 `RuntimeID` Validation、非零 Fencing Token、精确 Enum String、Receipt/Record 的 nil 规则、`Digest` 可比较性与小写 64 字符 Text Encoding。

- [ ] **步骤 5：实现 v2 Value Type，但暂不切换 Service**

按照 EV2-03 定义带 `ReadStream`、`Append`、`ResolveAppend` 与 `FindCommandRequest` 的 `EventStoreV2`，唯一过渡差异是与 v1 冲突的新请求类型临时使用 `AppendRequestV2`。定义全部 Resolution/Lookup Kind 与 11 个 Store Error Code。`StoreError.Error()` 只能包含稳定 Code 与安全的 Numeric/Identity Metadata，绝不能渲染 Wrapped Payload 或其他 Session 的 Command Record。

```go
type Digest [sha256.Size]byte
type RuntimeID string

type StoreError struct {
    Code             StoreErrorCode
    SessionID        domain.SessionID
    ExpectedVersion  uint64
    ActualVersion    uint64
    IdentityKind     string
    MayHaveCommitted bool
    Cause            error
}
```

Construction Helper 必须拒绝除 `commit_outcome_unknown` 外任何 Code 携带 `MayHaveCommitted=true`。

- [ ] **步骤 6：验证并提交任务 1**

```bash
gofmt -w internal/harness/domain internal/harness/application internal/harness/testkit
go test ./internal/harness/domain ./internal/harness/application ./internal/harness/testkit -count=1
go test ./... -count=1
git diff --check
```

预期：PASS。审阅 Type Name 与安全 Error Text 后提交：

```bash
git add internal/harness/domain/ids.go internal/harness/domain/ids_test.go internal/harness/application/store_v2.go internal/harness/application/store_errors.go internal/harness/application/store_v2_test.go internal/harness/application/ports.go internal/harness/application/ports_test.go internal/harness/application/session_test.go internal/harness/application/turn_success_test.go internal/harness/adapters/memory/event_store_test.go internal/harness/testkit/ids.go internal/harness/testkit/ids_test.go
git commit -m "feat(storage): define EventStore v2 primitives"
```

---

### 任务 2：实现 Canonical Framed Digest 与资源校验

**文件：**
- 修改：`internal/harness/domain/codec.go`
- 修改：`internal/harness/domain/codec_test.go`
- 创建：`internal/harness/application/digest.go`
- 创建：`internal/harness/application/digest_test.go`
- 创建：`internal/harness/application/digest_fuzz_test.go`

**接口：**
- 消费：任务 1 的 `Digest`、`AppendRequestV2`、`CommandAdmission`、`ProposedEvent`；Domain Strict Event Codec。
- 产出：`domain.MarshalEventPayload(Event) (eventType string, payload []byte, err error)`、`ParseDigest`、`DigestAppendRequest` 与 `DigestRunTurnRequestV1`。

- [ ] **步骤 1：编写失败的 Domain Payload Codec 测试**

对每个当前 Event Type 断言稳定 Event Type、Canonical Payload Byte、Defensive Output、严格 UTF-8 Validation，并确保不包含 Envelope Metadata：

```go
func TestMarshalEventPayloadIsCanonical(t *testing.T) {
    typ, payload, err := MarshalEventPayload(AssistantMessageCompleted{
        TurnID: "turn-1", ItemID: "item-1", Text: "你好",
    })
    if err != nil { t.Fatal(err) }
    if typ != EventAssistantMessageCompleted { t.Fatalf("type = %q", typ) }
    if string(payload) != `{"turnID":"turn-1","itemID":"item-1","text":"你好"}` {
        t.Fatalf("payload = %s", payload)
    }
}
```

- [ ] **步骤 2：运行 Codec 测试并观察缺失函数**

运行：`go test ./internal/harness/domain -run TestMarshalEventPayloadIsCanonical -count=1`

预期：FAIL，因为 `MarshalEventPayload` 不存在。

- [ ] **步骤 3：提取唯一的严格 Canonical Payload Path**

重构现有 Recorded-Event Codec，使 `MarshalRecordedEvent` 与 `MarshalEventPayload` 共享同一个 Event Type Switch 与 Payload Struct，不得增加第二套 JSON Representation。保留每个现有 Fixture Byte，并运行 `TestRecordedEventJSONUsesCanonicalEncodingForAllPayloads`。

- [ ] **步骤 4：编写失败的 Digest Test 与 Fuzz Property**

测试必须证明：相同 Logical Request 得到相同 Digest；修改任一 Covered Field 会改变 Digest；只修改 `AppendID` 或 `WriterAuthority` 不改变 Digest；Admission Presence 与每个 Admission Field 都被 Framing；Event 顺序不可交换；Embedded NUL 与模糊 String Boundary 不会碰撞；非法 ID、UTC/Time/Schema/Payload/Limit 在 Hash 前失败；`DigestRunTurnRequestV1` 区分 Session 与精确 Input Byte；`Digest.MarshalText` 输出小写 Hex，`ParseDigest` 拒绝大写、错误长度与非 Hex。

```go
func TestDigestFramingSeparatesAdjacentFields(t *testing.T) {
    left := validAppendRequest()
    right := validAppendRequest()
    left.SessionID, left.CommandID = "ab", "c"
    right.SessionID, right.CommandID = "a", "bc"
    leftDigest, leftErr := DigestAppendRequest(left)
    rightDigest, rightErr := DigestAppendRequest(right)
    if leftErr != nil || rightErr != nil { t.Fatalf("digest errors = %v, %v", leftErr, rightErr) }
    if leftDigest == rightDigest { t.Fatal("length framing did not separate adjacent fields") }
}
```

- [ ] **步骤 5：实现 Version-1 Framed Digest Codec**

使用私有 Encoder：`uint32` Big-Endian Byte Length、`uint64` Numeric Value、一个 Byte 的 Admission Flag 与显式 Event Count。写入 Byte 前完成 Validation。用 SHA-256 Hash 最终 Framed Byte Slice。构造时跟踪 Encoded Length，超过 16 MiB 即拒绝；超过 64 个 Event 或 Payload 超过 8 MiB 也拒绝。

```go
func DigestAppendRequest(request AppendRequestV2) (Digest, error)
func DigestRunTurnRequestV1(sessionID domain.SessionID, input string) (Digest, error)
func ParseDigest(text string) (Digest, error)
```

增加一个合法 Seed，并以重复计算结果一致作为精确 Fuzz Property：

```go
func FuzzDigestAppendRequest(f *testing.F) {
    f.Add("seed")
    f.Fuzz(func(t *testing.T, input string) {
        if !utf8.ValidString(input) { return }
        request := validAppendRequest()
        request.Events[0].Event = domain.AssistantMessageCompleted{
            TurnID: "turn-1", ItemID: "item-1", Text: input,
        }
        first, firstErr := DigestAppendRequest(request)
        second, secondErr := DigestAppendRequest(request)
        if (firstErr == nil) != (secondErr == nil) || (firstErr == nil && first != second) {
            t.Fatalf("non-deterministic digest: (%x,%v) then (%x,%v)", first, firstErr, second, secondErr)
        }
    })
}
```

- [ ] **步骤 6：运行聚焦测试、Fuzz Smoke、完整测试并提交**

```bash
gofmt -w internal/harness/domain internal/harness/application
go test ./internal/harness/domain ./internal/harness/application -run 'TestMarshalEventPayload|TestDigest' -count=1
go test ./internal/harness/application -run '^$' -fuzz FuzzDigestAppendRequest -fuzztime=3s
go test ./... -count=1
git diff --check
```

预期：PASS，现有 JSONL Fixture Test 不变。提交：

```bash
git add internal/harness/domain/codec.go internal/harness/domain/codec_test.go internal/harness/application/digest.go internal/harness/application/digest_test.go internal/harness/application/digest_fuzz_test.go
git commit -m "feat(storage): add canonical request digests"
```

---

### 任务 3：在 Cutover 前证明 Compact Command Aggregate

**文件：**
- 创建：`internal/harness/domain/compact_state.go`
- 创建：`internal/harness/domain/compact_apply.go`
- 创建：`internal/harness/domain/compact_decide.go`
- 修改：`internal/harness/domain/decide.go`
- 创建：`internal/harness/domain/compact_test.go`
- 创建：`internal/harness/domain/compact_equivalence_test.go`

**接口：**
- 消费：当前 v1 `Session`、`Decide`、`Apply`、`Replay` 与两个 Canonical Fixture，作为冻结 Test Oracle。
- 产出：`CompactSession`、`CompactTurn`、`CompactItem`、`ApplyCompact`、`ReplayCompact`、`DecideCompact` 与 `CheckStartAssistantTurnEligibilityCompact`。

- [ ] **步骤 1：编写失败的 Compact-State 测试**

```go
func TestReplayCompactDiscardsTerminalTranscript(t *testing.T) {
    records := fixtureRecords(t, "testdata/assistant_lifecycle.jsonl")
    got, err := ReplayCompact(records)
    if err != nil { t.Fatal(err) }
    if got.Version != uint64(len(records)) || got.ActiveTurn != nil {
        t.Fatalf("compact state = %#v", got)
    }
    if reflect.TypeOf(got).NumField() > 6 { t.Fatalf("compact state unexpectedly grew: %#v", got) }
}
```

同时覆盖一个 Active Turn/Item、错误 Terminal Identity、Terminal Irreversibility、Session Close、非法 Sequence、Clone Isolation 与不存在 Completed Collection。

- [ ] **步骤 2：运行并观察 Compact API 缺失**

运行：`go test ./internal/harness/domain -run 'TestReplayCompact|TestApplyCompact' -count=1`

预期：FAIL，因为 Compact Type/Function 不存在。

- [ ] **步骤 3：实现有界 State 与 Transition**

```go
type CompactItem struct {
    ID ItemID
    TurnID TurnID
    Kind ItemKind
    StartedAt time.Time
}

type CompactTurn struct {
    ID TurnID
    Input string
    StartedAt time.Time
    LastTransitionAt time.Time
    ActiveItem *CompactItem
}

type CompactSession struct {
    ID SessionID
    Status SessionStatus
    Version uint64
    WorkspaceRoot string
    ActiveTurn *CompactTurn
}
```

`LastTransitionAt` 是有界时间水位，不是 Transcript History。初始化为 `StartedAt`；Item Terminal Event 后更新为该 Terminal Timestamp。后续 Item Start 与 Turn Terminal 均不得早于它。Terminal Event 在把 `ActiveItem`/`ActiveTurn` 设为 nil 前，必须校验 Active Identity、Sequence 与相关时间水位；绝不保留 Terminal Text。重复 `SessionCreated` 的 Preflight 顺序必须与 v1 一致：非 Pristine State 在校验 Sequence 前返回 `session_already_exists`。

- [ ] **步骤 4：编写失败的 Decision Equivalence 测试**

用 v1 与 Compact Logic Replay 两个 Fixture 的每个 Prefix；对每个 Prefix 使用 Fresh ID 生成所有结构相关 Command，比较稳定 Error Code 与有序 Event Value：

```go
func assertDecisionEquivalent(t *testing.T, full Session, compact CompactSession, command Command) {
    wantEvents, wantErr := Decide(full, command)
    gotEvents, gotErr := DecideCompact(compact, command)
    if errorCode(gotErr) != errorCode(wantErr) || !reflect.DeepEqual(gotEvents, wantEvents) {
        t.Fatalf("compact decision = (%#v,%v), full = (%#v,%v)", gotEvents, gotErr, wantEvents, wantErr)
    }
}
```

增加 Historical Duplicate Case，证明 Compact State 本身无法拒绝旧 Completed Turn/Item ID，并明确把此职责交给任务 4 的 Store Identity Index。增加由两个 Canonical Fixture 提供 Seed 的确定性 Fuzz Target：

```go
func FuzzReplayCompact(f *testing.F) {
    f.Add(fixtureBytesForFuzz("testdata/assistant_lifecycle.jsonl"))
    f.Add(fixtureBytesForFuzz("testdata/session_lifecycle.jsonl"))
    f.Fuzz(func(t *testing.T, data []byte) {
        records, err := DecodeJSONL(bytes.NewReader(data))
        if err != nil { return }
        first, firstErr := ReplayCompact(records)
        second, secondErr := ReplayCompact(records)
        if errorCode(firstErr) != errorCode(secondErr) || !reflect.DeepEqual(first, second) {
            t.Fatalf("non-deterministic replay: (%#v,%v) then (%#v,%v)", first, firstErr, second, secondErr)
        }
    })
}
```

- [ ] **步骤 5：实现 Compact Decision 并通过 Equivalence**

Production Compact Code 不得调用 v1 `Decide`。把纯 Command Field Validation 与 Ordered Event Construction Helper 抽取到 `decide.go`，并由 v1 与 Compact Decision 共用；Full-History 与 Bounded-State Predicate 仍保持独立。不得在两个 Production Decision Path 之间复制大型 Validation/Event-Construction Block。v1 Oracle 只存在于 `_test.go`。

增加聚焦 Error Equivalence Test：重复 `SessionCreated` 且 Next Sequence 不匹配时仍返回相同稳定 Code；增加 Chronology Equivalence Test，证明两种实现都会拒绝：（1）后续 Item Start 早于上一 Completed Item 的 Terminal Time；（2）Turn Terminal 早于上一 Completed Item 的 Terminal Time。

- [ ] **步骤 6：验证并提交任务 3**

```bash
gofmt -w internal/harness/domain
go test ./internal/harness/domain -run 'Test.*Compact|TestCompact.*Equivalent' -count=1
go test ./internal/harness/domain -run '^$' -fuzz FuzzReplayCompact -fuzztime=3s
go test ./... -count=1
git diff --check
```

预期：PASS；Production Compact File 不调用 `Decide` 或 `Replay`。提交：

```bash
git add internal/harness/domain/decide.go internal/harness/domain/compact_state.go internal/harness/domain/compact_apply.go internal/harness/domain/compact_decide.go internal/harness/domain/compact_test.go internal/harness/domain/compact_equivalence_test.go
git commit -m "feat(domain): add compact command aggregate"
```

---

### 任务 4：构建 v2 Memory Reference Adapter 与 Shared Conformance Suite

**文件：**
- 创建：`internal/harness/application/eventstoretest/v2_suite.go`
- 创建：`internal/harness/application/eventstoretest/v2_cases.go`
- 创建：`internal/harness/adapters/memory/event_store_v2.go`
- 创建：`internal/harness/adapters/memory/event_store_v2_test.go`
- 创建：`internal/harness/adapters/memory/event_store_v2_benchmark_test.go`

**接口：**
- 消费：任务 1–3 的 v2 Type、Digest Codec、Compact Replay 与 Domain Identity Event。
- 产出：`memory.EventStoreV2`、`NewEventStoreV2(WriterAuthority)`、确定性 Authority Rotation/Fault Hook 与 `eventstoretest.RunV2`。

- [ ] **步骤 1：编写 Shared Conformance Harness 与失败 Case**

```go
type V2Harness struct {
    Store EventStoreV2
    RotateAuthority func(WriterAuthority)
    FailNext func(FaultPoint, error)
    CorruptReceipt func(AppendID)
    SetCommitHook func(CommitHookPoint, func())
}

type V2Factory func(*testing.T) V2Harness

func RunV2(t *testing.T, factory V2Factory) {
    t.Run("atomic append and CAS", func(t *testing.T) { testAtomicAppendAndCAS(t, factory) })
    t.Run("exact receipt retry", func(t *testing.T) { testExactReceiptRetry(t, factory) })
    t.Run("pinned pagination", func(t *testing.T) { testPinnedPagination(t, factory) })
    t.Run("admission identity", func(t *testing.T) { testAdmissionIdentity(t, factory) })
    t.Run("writer fencing", func(t *testing.T) { testWriterFencing(t, factory) })
    t.Run("unknown outcome", func(t *testing.T) { testUnknownOutcome(t, factory) })
}
```

临时名称使用 `V2Factory`，因为同一 Package 在任务 8 前仍保留 v1 `Factory`；任务 8 提升 v2 Suite 时删除该临时前缀。

Suite 还必须覆盖 EV2-05 至 EV2-09：64/65 Event Limit、Payload/Request Byte Limit、Global EventID Uniqueness、Historical Turn/Item Identity、Defensive Copy、nil/Canceled Context、Corrupt Receipt Detection、并发 Session Commit-Position Uniqueness 与 Privacy-Preserving Command Mismatch。

`CorruptReceipt` 与 `SetCommitHook` 是 Adapter-Specific Conformance Control，
不是 `EventStoreV2` 的新增接口。`CorruptReceipt` 只针对一个已提交 Append，
用于证明 Exact Retry 与 Resolution 遇到损坏状态时以 `store_corrupt`
Fail Closed。`SetCommitHook` 只允许在 `CommitHookBeforePublish` 或
`CommitHookAfterPublish` 安装一个有界 Hook；测试仅用它在 Publication Point
两侧触发 Cancel。Hook 只消费一次，不得削弱生产 Validation 或 Authorization。

- [ ] **步骤 2：把 Suite 连接到缺失 Adapter 并观察失败**

```go
func TestEventStoreV2Contract(t *testing.T) {
    eventstoretest.RunV2(t, func(t *testing.T) eventstoretest.V2Harness {
        store := mustV2Store(t, application.WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1})
        return eventstoretest.V2Harness{Store: store, RotateAuthority: store.SetAuthority, FailNext: store.FailNext, CorruptReceipt: store.CorruptReceipt, SetCommitHook: store.SetCommitHook}
    })
}
```

运行：`go test ./internal/harness/adapters/memory -run TestEventStoreV2Contract -count=1`

预期：FAIL，因为 v2 Adapter 与 Hook 不存在。

- [ ] **步骤 3：实现 Atomic In-Memory Model**

```go
type EventStoreV2 struct {
    mu sync.Mutex
    authority application.WriterAuthority
    commitPosition uint64
    streams map[domain.SessionID][]domain.RecordedEvent
    appends map[domain.AppendID]storedAppend
    requests map[domain.RunTurnRequestID]application.CommandRequestRecord
    eventIDs map[domain.EventID]struct{}
    turnIDs map[domain.SessionID]map[domain.TurnID]struct{}
    itemIDs map[domain.SessionID]map[domain.ItemID]struct{}
    faults map[FaultPoint][]error
}
```

尽可能在 Lock 前 Clone/Validate 完整 Request，然后在 Lock 内重新计算并比较 Digest。新 Append 依次执行 Receipt-before-Authority、Admission Identity、CAS、Global ID、Historical ID、Compact Replay、Commit Position、Receipt 与全部 Map 的单次 Mutation；赋值给 Live Map 前先构建所有 Candidate Copy。

Exact Retry 或 Resolution 返回前，必须依据 Immutable Append Metadata 校验
Stored Receipt。损坏 Receipt 返回 `store_corrupt`，绝不能传给调用方。Memory
Adapter 可以暴露以上两个仅用于 Conformance 的 Control，但它们不属于生产
Store Port。

- [ ] **步骤 4：实现具有精确 Commit Knowledge 的 Fault Point**

定义 `FaultBeforeCommit`、`FaultAfterCommitBeforeAck`、`FaultResolve`。Before-Commit Fault 返回 `store_unavailable` 且不 Mutation；After-Commit Fault 完成全部 Mutation 后返回 `commit_outcome_unknown`；Resolve Fault 返回 `store_unavailable`，绝不返回 `not_found`。Fault Queue 是有界 Test Control，不得循环。

Publication Point 是完整 Candidate State 的单次赋值。紧邻赋值前再次观察到
Cancellation 时必须确定无提交；赋值后触发的 Cancellation 不得被翻译为确定未
提交。Commit Hook 只为这两个边界提供确定性覆盖，不改变 Store Semantic。

- [ ] **步骤 5：实现 Pinned Pagination 与 Read Validation**

在 Mutex 内捕获 Current Head；只有校验 supplied head 后才使用它；Clone 最多 `Limit` 条 Record，并严格按 EV2-08 设置 Cursor/End。增加 Barrier Test：Page 1 后 Append，Page 2 必须排除新 Record。

- [ ] **步骤 6：运行 Conformance、Race、Benchmark Smoke 并提交**

```bash
gofmt -w internal/harness/application/eventstoretest internal/harness/adapters/memory
go test ./internal/harness/adapters/memory -run TestEventStoreV2Contract -count=1
go test -race ./internal/harness/adapters/memory -run TestEventStoreV2Contract -count=1
go test ./internal/harness/adapters/memory -run '^$' -bench BenchmarkEventStoreV2 -benchtime=100x
go test ./... -count=1
git diff --check
```

预期：全部 Conformance Subtest PASS，Race Detector 无 Race。提交：

```bash
git add internal/harness/application/eventstoretest/v2_suite.go internal/harness/application/eventstoretest/v2_cases.go internal/harness/adapters/memory/event_store_v2.go internal/harness/adapters/memory/event_store_v2_test.go internal/harness/adapters/memory/event_store_v2_benchmark_test.go
git commit -m "feat(memory): implement EventStore v2 reference"
```

---

### 任务 5：迁移 Application 的 Append 所有权与 Pinned Read

**文件：**
- 创建：`internal/harness/application/read_stream.go`
- 创建：`internal/harness/application/read_stream_test.go`
- 创建：`internal/harness/application/append_v2.go`
- 创建：`internal/harness/application/append_v2_test.go`
- 删除：`internal/harness/application/append.go`
- 修改：`internal/harness/application/service.go`
- 修改：`internal/harness/application/session.go`
- 修改：`internal/harness/application/turn.go`
- 修改：`internal/harness/application/ports_test.go`
- 修改：`internal/harness/application/errors_test.go`
- 修改：`internal/harness/application/session_test.go`
- 修改：`internal/harness/application/scenario_test.go`
- 修改：`internal/harness/application/concurrency_test.go`
- 修改：`internal/harness/application/turn_success_test.go`
- 修改：`internal/harness/application/turn_failure_test.go`
- 修改：`internal/harness/application/enginescenariotest/suite.go`
- 修改：`internal/harness/testkit/clock.go`
- 创建：`internal/harness/testkit/v2_store.go`

**接口：**
- 消费：`EventStoreV2`、任务 4 Reference Adapter、`Clock`、`IDGenerator` 与 Compact Replay API。
- 产出：`ReadWholeStreamPinned`、Immutable `AppendIntent`、`BuildAppendIntent`、`CommitAppendIntent`、v2 `Service` Construction 与迁移后的 Session/Turn Normal Path。

- [ ] **步骤 1：编写失败的 Pinned Reader 测试**

覆盖 Empty/Missing Stream、单页、多页、页间 Append、非法 Page Contract、重复 Cursor、过早 `End`、Cancellation 与 Defensive Record。恶意 Store 改变 Head 时必须映射为 `store_contract_violation`。

```go
func TestReadWholeStreamPinnedKeepsFirstHead(t *testing.T) {
    store := &pagingSpy{pages: []StreamPage{
        {Records: records(1, 2), HeadVersion: 3, NextAfterSequence: 2},
        {Records: records(3), HeadVersion: 3, NextAfterSequence: 3, End: true},
    }}
    got, err := ReadWholeStreamPinned(context.Background(), store, "session-1", 2)
    if err != nil || len(got) != 3 { t.Fatalf("got %d, %v", len(got), err) }
    if store.requests[1].HeadVersion == nil || *store.requests[1].HeadVersion != 3 {
        t.Fatalf("second request = %#v", store.requests[1])
    }
}
```

- [ ] **步骤 2：实现带 Progress Guard 的 Pinned Read**

Page Size 必须为 `1..256`；校验每条返回 Record 的 Sequence/Session/Head/Cursor；任何非 Terminal 且无进展的 Page 都拒绝。在 Load Helper 中增量调用 `domain.ApplyCompact`，Application 不得再建立第二份无界 State Copy。

- [ ] **步骤 3：编写失败的 Append-Intent Ownership 测试**

断言 Application 只调用 Clock 一次，在 Store I/O 前分配一个 Append ID 和 N 个 Event ID，Batch 使用同一 Timestamp，Store 不得改写 Proposed Metadata；校验 Receipt Range，并用 Intent + Receipt 重建 Committed `RecordedEvent`。

```go
type AppendIntent struct {
    Request AppendRequestV2
    Digest Digest
}

func BuildAppendIntent(clock Clock, ids IDGenerator, authority WriterAuthority,
    sessionID domain.SessionID, version uint64, commandID domain.CommandID,
    admission *CommandAdmission, events []domain.UncommittedEvent) (AppendIntent, error)
```

- [ ] **步骤 4：实现 Build、Commit 与 Receipt Validation**

`BuildAppendIntent` Clone 每个 Event、捕获一次 UTC Time、分配稳定 ID、构造 Request、计算 Digest，并返回 Deep Immutable Value。`CommitAppendIntent` 只调用 Store 一次，校验 `AppendID`、First/Last Sequence 与非零 Commit Position，用 Proposed Metadata 产生 Record，再 Compact Apply。Receipt 不匹配返回 `store_contract_violation`。

本任务直接删除 v1 `append.go` Helper。它绑定旧 Store Result Shape，无法在不引入
Duplicate Method 或违规的第二 Compatibility Store 的前提下与 v2
`Service.store` 共存。替代实现暂时保留 `append_v2.go` 名称，任务 8 再提升为
最终文件名。

- [ ] **步骤 5：将 Service 与 Normal-Path 测试切到 v2**

```go
func NewService(store EventStoreV2, ids IDGenerator, clock Clock, runner *engine.TurnRunner,
    authority WriterAuthority, config Config) (*Service, error)
```

迁移 `CreateSession`、`LoadSession`、`CloseSession` 与现有 `RunTurn` Normal/Failure Path。此时 `RunTurnRequest` 增加强制 `RequestID`；Task 6 加入 Attachment/Result Behavior 前，已存在的 Duplicate 暂时在不调用 Model 的情况下返回 `reconciliation_required`。

- [ ] **步骤 6：验证 Normal Scenario 并提交**

```bash
gofmt -w internal/harness/application internal/harness/testkit
go test ./internal/harness/application -run 'TestReadWholeStreamPinned|TestBuildAppendIntent|TestCreateSession|TestLoadSession|TestCloseSession|TestRunTurn' -count=1
go test ./... -count=1
go test -race ./internal/harness/application ./internal/harness/adapters/memory -count=1
git diff --check
```

预期：PASS；`rg -n '\.Load\(' internal/harness/application --glob '*.go' --glob '!**/*_test.go' --glob '!eventstoretest/**'` 无 Production v1 Load。排除 `eventstoretest` 是有意设计：其中的 v1 Adapter-Conformance Fixture 会保留到任务 8，且不属于 Application Production Path。提交：

```bash
git add internal/harness/application/read_stream.go internal/harness/application/read_stream_test.go internal/harness/application/append_v2.go internal/harness/application/append_v2_test.go internal/harness/application/append.go internal/harness/application/service.go internal/harness/application/session.go internal/harness/application/turn.go internal/harness/application/ports_test.go internal/harness/application/errors_test.go internal/harness/application/session_test.go internal/harness/application/scenario_test.go internal/harness/application/concurrency_test.go internal/harness/application/turn_success_test.go internal/harness/application/turn_failure_test.go internal/harness/application/enginescenariotest/suite.go internal/harness/testkit/clock.go internal/harness/testkit/v2_store.go
git commit -m "refactor(application): adopt EventStore v2 appends"
```

---

### 任务 6：增加 Durable Request Admission 与 Exactly-One Live Execution

**文件：**
- 创建：`internal/harness/application/execution_registry.go`
- 创建：`internal/harness/application/execution_registry_test.go`
- 创建：`internal/harness/application/request_result.go`
- 创建：`internal/harness/application/request_result_test.go`
- 修改：`internal/harness/application/turn.go`
- 修改：`internal/harness/application/turn_success_test.go`
- 修改：`internal/harness/application/concurrency_test.go`
- 修改：`internal/harness/application/service.go`
- 修改：`internal/harness/application/errors.go`

**接口：**
- 消费：任务 5 v2 Service、`FindCommandRequest`、Pinned Record、`RunTurnRequest.RequestID` 与 Digest。
- 产出：每个 Request ID 一个 Live Registry Owner、Duplicate Wait/Observe Behavior、Durable Result Reconstruction、`command_identity_mismatch` 与 `reconciliation_required` Application Error。

- [ ] **步骤 1：编写失败的 Registry Unit Test**

覆盖 Owner Creation、同 ID/同 Digest Waiter、同 ID/异 Digest Reject、Waiter Cancel 不取消 Owner、单 Resolver/Owner、Terminal Publication、全部 Waiter Detach 后才 Cleanup。

```go
func TestExecutionRegistryElectsOneOwner(t *testing.T) {
    registry := newExecutionRegistry()
    owner, first := registry.acquire("request-1", digestA, "session-1")
    waiter, second := registry.acquire("request-1", digestA, "session-1")
    if !first || second || owner.entry != waiter.entry { t.Fatal("ownership split") }
}
```

- [ ] **步骤 2：实现 Registry State 与有界 Waiter Ownership**

使用 Mutex、Per-Entry Completion Channel、Immutable Request Identity、Owner Token、Phase、Retained Append Intent、Result/Error、Waiter Count 与 Session-to-Unresolved Count。不得重复 Close Channel，也不得持锁等待。

- [ ] **步骤 3：编写失败的 Durable Result Reconstruction 测试**

给定 `CommandRequestRecord`，扫描一个 Pinned Session View，只返回 `running`、`completed`、`failed`、`interrupted` 之一。校验 Turn/Item ID 与 Admission Append Event Pair；不匹配为 `store_corrupt`。Completed Text 来自 Terminal Assistant Event；Failure/Interruption 保留安全稳定 Code。

- [ ] **步骤 4：实现 Pre-ID Lookup 与 Duplicate Path**

`RunTurn` 在 Request/Context Validation 后的第一批操作必须是：

```go
requestDigest, err := DigestRunTurnRequestV1(request.SessionID, request.Input)
lookup, err := service.store.FindCommandRequest(ctx, FindCommandRequestRequest{
    RunTurnRequestID: request.RequestID,
    SessionID: request.SessionID,
    RequestDigest: requestDigest,
})
```

只有 `not_found` 可以进入 Registry Owner Election 与 Identity Allocation。`found` Terminal 不调用 Model，直接重建；`found` Running 连接本地 Entry，或返回 `reconciliation_required`；`identity_mismatch` 返回 Conflict，不暴露 Stored Record。

- [ ] **步骤 5：将 Admission 写入首个 Append，并处理 Race**

`CommandAdmission` 填入 Request、Digest、Turn、Item 与 Command Identity。若另一 Process 以 `command_request_conflict` 获胜，再调用 `FindCommandRequest`、Pinned-Read Winner，并复用 Found Path。在调用 Model 前，本 Invocation 必须拥有已提交 Admission。

- [ ] **步骤 6：证明并发下 Model 只调用一次**

32 个 Goroutine 使用同一 Request ID/Digest 与 Counting Blocking Model。断言一个 Admission Receipt、一次 Model Start、所有 Caller 得到相同 Terminal Identity/Result，且 Race Test 干净。用一个 Input Mismatch 重复测试，断言它既不等待也不影响 Owner。

- [ ] **步骤 7：验证并提交任务 6**

```bash
gofmt -w internal/harness/application
go test ./internal/harness/application -run 'TestExecutionRegistry|TestReconstructRequestResult|TestRunTurnDuplicate|TestConcurrentSameRequest' -count=1
go test -race ./internal/harness/application -run 'TestExecutionRegistry|TestConcurrentSameRequest' -count=1
go test ./... -count=1
git diff --check
git add internal/harness/application/execution_registry.go internal/harness/application/execution_registry_test.go internal/harness/application/request_result.go internal/harness/application/request_result_test.go internal/harness/application/turn.go internal/harness/application/turn_success_test.go internal/harness/application/concurrency_test.go internal/harness/application/service.go internal/harness/application/errors.go
git commit -m "feat(application): enforce durable request admission"
```

预期：全部 PASS，Counting Model 等于一。

---

### 任务 7：实现 Unknown-Outcome Resolution 与 Cancellation Winner Rule

**文件：**
- 修改：`internal/harness/domain/events.go`
- 修改：`internal/harness/domain/commands.go`
- 修改：`internal/harness/domain/decide.go`
- 修改：`internal/harness/domain/decide_test.go`
- 修改：`internal/harness/domain/codec_test.go`
- 修改：`internal/harness/domain/apply_test.go`
- 创建：`internal/harness/application/append_resolution.go`
- 创建：`internal/harness/application/append_resolution_test.go`
- 修改：`internal/harness/application/execution_registry.go`
- 修改：`internal/harness/application/turn.go`
- 修改：`internal/harness/application/turn_failure_test.go`
- 修改：`internal/harness/application/service.go`
- 创建：`internal/harness/application/unknown_outcome_scenario_test.go`

**接口：**
- 消费：Retained `AppendIntent`、Receipt Resolution API、Registry Ownership/Phase 与 v2 Store Fault Control。
- 产出：有界 `ResolveAppendIntent`、Domain `request_abandoned` Interruption、`append_outcome_unknown` 与 Terminal Cancellation Winner State Machine。

- [ ] **步骤 1：增加失败的 `request_abandoned` Domain 测试**

断言它只被 `InterruptAssistantTurn` 接受，Item 先于 Turn Terminalize，Schema Version 1 Round-Trip，并在任一 Model Terminal Event 后拒绝。本 Slice 不加入 `process_crash`。

- [ ] **步骤 2：实现新 Interruption Code**

```go
const InterruptionRequestAbandoned = "request_abandoned"
```

加入现有 Strict Interruption Validation 与 Canonical Codec Test，不改变旧 Fixture Byte。

- [ ] **步骤 3：编写失败的有界 Resolver 测试**

覆盖以下精确 Script：

```text
unknown -> resolve committed                         => success
unknown -> resolve not_found -> exact append success => success
unknown -> unavailable x4                            => append_outcome_unknown
unknown terminal -> not_found -> exact append         => no second model call
unknown admission + caller canceled + committed       => request_abandoned, no model call
same request waiter                                   => no second resolver
```

断言初始 Unknown 后最多四次 Store Operation；5 秒 Timer 可注入；复用完全相同的 Digest/Request；Session 有 Unresolved Entry 时拒绝另一项 New Admission。

- [ ] **步骤 4：实现 `ResolveAppendIntent`**

```go
type AppendResolutionConfig struct {
    Timeout time.Duration
    MaxOperations uint32
}

func ResolveAppendIntent(ctx context.Context, store EventStoreV2, intent AppendIntent,
    config AppendResolutionConfig) (CommitReceipt, error)
```

每轮调用 `ResolveAppend`：`committed` 返回经校验 Receipt；`identity_mismatch` Fail Closed；`not_found` 允许一次 Exact `Append`。每次 Store Call 都计数；Caller Deadline、Timeout 或 Operation Cap 任一到达即停止；禁止重建或重新 Decide Intent。

- [ ] **步骤 5：增加失败的 Cancellation Phase 测试**

在 `running`、Terminal Append 前一刻、Terminal Append 开始后、Commit-before-Ack 后、Resolution 后设置 Barrier。断言 Running Cancel 可 Append Caller Interruption；进入 `terminal_append_in_flight` 后 Cancel 只改变 Delivery；Completed/Failed Retained Intent 胜过 Late Cancel；CAS Loser Reload 后报告 Durable Winner；Model Call 始终为一。

- [ ] **步骤 6：实现 Phase Transition 与 Session Gate**

每次 Store Operation 前原子切换 Registry Phase。Admission Unknown 必须在 Model 前解决；Admission 已提交但 Caller 已 Cancel 时 Append `request_abandoned`。Terminal Unknown 保留 Completed/Failed Intent 并解决。向全部 Waiter 发布一个 Terminal Result；Unresolved Entry 在解决或 Process 结束前持续占用 Session Gate。

- [ ] **步骤 7：运行 Fault Matrix、Race Suite 并提交**

```bash
gofmt -w internal/harness/domain internal/harness/application
go test ./internal/harness/domain -run RequestAbandoned -count=1
go test ./internal/harness/application -run 'TestResolveAppendIntent|TestUnknownOutcome|TestCancellationWinner' -count=1
go test -race ./internal/harness/application -run 'TestUnknownOutcome|TestCancellationWinner' -count=1
go test ./... -count=1
git diff --check
git add internal/harness/domain/events.go internal/harness/domain/commands.go internal/harness/domain/decide.go internal/harness/domain/decide_test.go internal/harness/domain/codec_test.go internal/harness/domain/apply_test.go internal/harness/application/append_resolution.go internal/harness/application/append_resolution_test.go internal/harness/application/execution_registry.go internal/harness/application/turn.go internal/harness/application/turn_failure_test.go internal/harness/application/service.go internal/harness/application/unknown_outcome_scenario_test.go
git commit -m "feat(application): resolve uncertain appends safely"
```

预期：全部 Scripted Model 最多调用一次。

---

### 任务 8：切换 Compact Domain State 并删除全部 v1 Store Surface

**文件：**
- 修改：`internal/harness/domain/state.go`、`apply.go`、`decide.go`、`replay.go`
- 删除：`internal/harness/domain/compact_state.go`、`compact_apply.go`、`compact_decide.go`
- 修改：`internal/harness/domain/apply_test.go`、`decide_test.go`、`replay_test.go`、`compact_test.go`、`compact_equivalence_test.go`
- 修改：`internal/harness/application/ports.go`
- 合并后删除：`internal/harness/application/store_v2.go`
- Rename/Merge：`internal/harness/application/append_v2.go` -> `internal/harness/application/append.go`
- 删除：`internal/harness/application/eventstoretest/suite.go`
- Rename/Merge：`eventstoretest/v2_suite.go` -> `eventstoretest/suite.go`，`eventstoretest/v2_cases.go` -> `eventstoretest/cases.go`
- 删除：`internal/harness/adapters/memory/event_store.go` 与旧 `event_store_test.go`
- Rename/Merge：`event_store_v2.go` -> `event_store.go`，`event_store_v2_test.go` -> `event_store_test.go`
- Rename：`event_store_v2_benchmark_test.go` -> `event_store_benchmark_test.go`
- 修改：`internal/harness/application/ports_test.go`、`errors_test.go`、`session_test.go`、`scenario_test.go`、`concurrency_test.go`、`turn_success_test.go`、`turn_failure_test.go`、`enginescenariotest/suite.go`
- 修改：`internal/harness/architecture/dependencies_test.go`

**接口：**
- 消费：已证明的 Compact Implementation 与已完全迁移的 Application。
- 产出：只有 v2 语义的最终 `domain.Session`、`Decide`、`Apply`、`Replay`、`application.EventStore` 与 `memory.EventStore` 名称。

- [ ] **步骤 1：增加失败的 Repository Surface Guard**

扩展 Architecture Test，使用 Go Syntax 扫描 Production Declaration/Selector，拒绝 `EventStoreV2`、带 v1 Session Stream Signature 的 `EventStore.Load`、返回 `[]domain.RecordedEvent` 的旧 Append、`Session.Turns` 与 `Session.TurnOrder`。不得用 Raw Substring 扫描 Comment 或 Test-Only Oracle。

- [ ] **步骤 2：运行 Guard 并观察旧/临时 Surface**

运行：`go test ./internal/harness/architecture -run TestNoEventStoreV1Surface -count=1`

预期：FAIL，并列出临时 v2 名称与旧 Full-History Structure。

- [ ] **步骤 3：用 Compact Shape 替换 `Session`**

把证明过的 Compact Field/Logic 移到 `state.go`、`apply.go`、`decide.go`、`replay.go`，删除临时 Compact Production File。Caller 改用 `Session.ActiveTurn`，不再使用 Map/Order Array。冻结的 v1 Oracle 以重命名 Type 仅保留在 `_test.go`。

- [ ] **步骤 4：提升 v2 名称并删除 v1 Implementation**

将 `EventStoreV2` 改为 `EventStore`、`AppendRequestV2` 改为 `AppendRequest`、`memory.EventStoreV2` 改为 `memory.EventStore`。把临时 Type 合并到 `ports.go` 后删除 `store_v2.go`。删除 v1 `Load`、旧 Append Result Semantic、Adapter-Owned Clock/ID、旧 Fault Hook 与 Compatibility Helper；把 Conformance Suite 与 Benchmark 提升为最终文件名。

- [ ] **步骤 5：验证缺失旧 Surface 与 Behavioral Parity**

```bash
gofmt -w internal/harness
go test ./internal/harness/architecture -run TestNoEventStoreV1Surface -count=1
go test ./internal/harness/domain -run 'TestCompact.*Equivalent|TestReplay' -count=1
go test ./internal/harness/adapters/memory -run TestEventStoreContract -count=1
go test ./... -count=1
go test -race ./... -count=1
git diff --check
rg -n 'EventStoreV2|AppendRequestV2|\.Load\(|\[\]domain\.RecordedEvent, error\)' internal/harness --glob '*.go' --glob '!**/*_test.go'
```

预期：前述测试 PASS，`rg` 无输出。提交：

```bash
git add -A internal/harness
git commit -m "refactor(storage): complete EventStore v2 cutover"
```

---

### 任务 9：发布 Implemented Contract 与完成证据

**文件：**
- 创建：`docs/architecture/eventstore-v2.md`
- 创建：`docs/architecture/eventstore-v2.zh-CN.md`
- 创建：`docs/architecture/eventstore-v2-evidence.md`
- 修改：`docs/architecture/domain-events.md`
- 修改：`docs/architecture/engine-vertical-slice.md`
- 修改：`docs/architecture/engine-vertical-slice.zh-CN.md`
- 修改：`docs/README.md`
- 修改：`README.md`
- 修改：`.github/workflows/ci.yml`

**接口：**
- 消费：任务 1–8 与对应精确 Commit Hash。
- 产出：双语 Implemented Contract、Auditable Evidence Ledger、CI Gate、Benchmark Baseline，以及 Slice 2 之后的可见 Exclusion。

- [ ] **步骤 1：编写 Implemented Contract 与同步中文副本**

记录最终四方法 Store Interface、Identity Ownership、Digest Format/Version、Error Algebra、Pagination Truth Table、Admission Behavior、Compact State、Resolver Budget、Cancellation Winner Table、Resource Bound 与显式 Exclusion。v1.0 前将 Internal Stability 标为 `experimental`。

- [ ] **步骤 2：增加 CI 与 Repository Guard**

保留 Format/Vet/Race Gate，增加确定性 Digest/Fixture Test 与 Architecture Surface Guard。除正常 Go Module Resolution 外不得引入 Network Download；本 Slice 继续只用标准库。

- [ ] **步骤 3：从 Clean Index 运行最终验证**

```bash
test -z "$(gofmt -l .)"
GOCACHE=/private/tmp/open-code-harness-go-cache go vet ./...
GOCACHE=/private/tmp/open-code-harness-go-cache go test ./... -count=1
GOCACHE=/private/tmp/open-code-harness-go-cache go test -race ./... -count=1
GOCACHE=/private/tmp/open-code-harness-go-cache go test ./internal/harness/application -run '^$' -fuzz FuzzDigestAppendRequest -fuzztime=10s
GOCACHE=/private/tmp/open-code-harness-go-cache go test ./internal/harness/domain -run '^$' -fuzz FuzzReplayCompact -fuzztime=10s
GOCACHE=/private/tmp/open-code-harness-go-cache CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...
GOCACHE=/private/tmp/open-code-harness-go-cache CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build ./...
GOCACHE=/private/tmp/open-code-harness-go-cache CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...
go test ./internal/harness/adapters/memory -run '^$' -bench BenchmarkEventStore -benchmem -benchtime=1s
git diff --check
git status --short
```

预期：全部命令通过；Commit 前 Status 只列出任务 9 的 Documentation/CI File。

- [ ] **步骤 4：记录证据，但不夸大成熟度**

Evidence Ledger 列出每个 Task Commit、精确 Command/Output Summary、Benchmark Environment/Result、Conformance/Fault Case 与剩余 Blocker：SQLite Durability、JSONL Replica/Import、Durable Runtime Host/Recovery、ACP、TUI。必须明确 Reference Memory Adapter 不是 Durable Production Storage。

- [ ] **步骤 5：提交任务 9 并验证最终 Tree**

```bash
git add README.md docs/README.md docs/architecture/eventstore-v2.md docs/architecture/eventstore-v2.zh-CN.md docs/architecture/eventstore-v2-evidence.md docs/architecture/domain-events.md docs/architecture/engine-vertical-slice.md docs/architecture/engine-vertical-slice.zh-CN.md .github/workflows/ci.yml
git commit -m "docs(storage): publish EventStore v2 evidence"
git status --short
```

预期：Worktree Clean。

## 完成 Gate

只有以下条件全部满足，EventStore v2 才能标记完成：EV2-01 至 EV2-12 每条都有自动化测试映射；v1 Production Surface Guard 通过；全量、Race、Fuzz Smoke 与三平台 Build 通过；English/中文文档同步；Evidence Ledger 记录真实结果；SQLite、Durable Runtime Recovery 与其他后续 Slice 明确保持未实现状态。
