# EventStore v2 Contract Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the ambiguous full-stream EventStore v1 boundary with an exact, paginated, receipt-bearing v2 contract and migrate Application to caller-stable request admission, bounded unknown-outcome resolution, and compact deterministic write state.

**Architecture:** Domain remains pure and owns lifecycle rules; Application creates stable append metadata and orchestrates retries without repeating effects; every Store adapter owns only sequence and commit position. A new v2 reference adapter and conformance suite are introduced alongside v1 only long enough to keep intermediate commits buildable, then the final cutover deletes every v1 production surface.

**Tech Stack:** Go 1.26, Go standard library, `testing`, `crypto/sha256`, `encoding/binary`, existing JSONL fixtures, race/fuzz/benchmark tooling, GitHub Actions.

## Global Constraints

- Normative specification: `docs/superpowers/specs/2026-08-13-eventstore-v2-contract-design.md`; EV2-01 through EV2-12 are mandatory.
- Research evidence: `docs/research/architecture-gates/2026-08-13-eventstore-v2-contract.md`.
- This is Slice 1 only: do not add SQLite, SQL schema, third-party modules, JSONL export/import, durable Runtime leases, ACP, TUI, tools, providers, or context management.
- Domain imports no Application, Engine, adapter, clock, randomness, filesystem, logging, or network package.
- Application imports no concrete adapter; Engine remains unaware of storage and receipt resolution.
- `AppendID`, proposed `EventID`, schema version, UTC occurrence time, Command admission, and request digests exist before the first Store call.
- Store assigns only per-Session sequence and global commit position.
- No unbounded hidden retry. Defaults are `AppendResolutionTimeout = 5s` and `AppendResolutionMaxOperations = 4` after the initial unknown result.
- Limits are 8 MiB per canonical Event payload, 64 Events per append, 16 MiB per encoded append request, and 256 Records per read page. Canonical facts are rejected, never truncated.
- Every behavior is TDD: observe the intended failure before implementation, then run focused and full tests.
- Every task ends with `gofmt`, focused tests, `go test ./... -count=1`, `go test -race ./... -count=1` when the task changes concurrency, an independent review gate, and one small commit.
- English is normative. The Chinese plan is a complete synchronized reading copy and must change in the same documentation commit.
- To keep intermediate commits compilable beside v1, the new request type is temporarily named `AppendRequestV2` and the new interface `EventStoreV2`. Task 8 atomically promotes them to the final specification names `AppendRequest` and `EventStore`; no temporary v2 name remains in production.

## File map

| Path | Responsibility |
| --- | --- |
| `internal/harness/domain/ids.go` | `AppendID` and `RunTurnRequestID` validation |
| `internal/harness/domain/compact_state.go` | Bounded write-side Session/Turn/Item representation |
| `internal/harness/domain/compact_apply.go` | Deterministic compact replay transitions |
| `internal/harness/domain/compact_decide.go` | Command decisions against compact state |
| `internal/harness/domain/compact_equivalence_test.go` | Frozen v1 oracle and decision-equivalence proof |
| `internal/harness/application/store_v2.go` | Temporary v2 Store interface, `AppendRequestV2`, records, authority, lookup, and receipt types |
| `internal/harness/application/store_errors.go` | Stable Store error algebra and predicates |
| `internal/harness/application/digest.go` | Strict digest type and versioned framed digest codecs |
| `internal/harness/application/read_stream.go` | Pinned-head pagination helper |
| `internal/harness/application/append_v2.go` | Stable append-intent construction, receipt validation, resolution, and apply |
| `internal/harness/application/execution_registry.go` | Per-request live ownership, phases, waiters, and Session admission gate |
| `internal/harness/application/request_result.go` | Reconstruct one admitted request result from canonical events |
| `internal/harness/adapters/memory/event_store_v2.go` | Deterministic reference Store, identity indexes, receipts, paging, and fault hooks |
| `internal/harness/application/eventstoretest/v2_suite.go` | Adapter-neutral v2 conformance suite |
| `internal/harness/testkit/ids.go` | Deterministic Append ID generation |
| `internal/harness/testkit/v2_store.go` | Application scenario Store spies and exact fault scripts |
| `docs/architecture/eventstore-v2.md` | Implemented contract after cutover |
| `docs/architecture/eventstore-v2-evidence.md` | Commits, verification output, benchmark baseline, exclusions, and remaining blockers |

---

### Task 1: Add stable identities, digest values, Store types, and typed errors

**Files:**
- Modify: `internal/harness/domain/ids.go`
- Modify: `internal/harness/domain/ids_test.go`
- Create: `internal/harness/application/store_v2.go`
- Create: `internal/harness/application/store_errors.go`
- Create: `internal/harness/application/store_v2_test.go`
- Modify: `internal/harness/application/ports.go`
- Modify: `internal/harness/application/ports_test.go`
- Modify: `internal/harness/application/session_test.go`
- Modify: `internal/harness/application/turn_success_test.go`
- Modify: `internal/harness/adapters/memory/event_store_test.go`
- Modify: `internal/harness/testkit/ids.go`
- Modify: `internal/harness/testkit/ids_test.go`

**Interfaces:**
- Consumes: existing `domain.SessionID`, `TurnID`, `ItemID`, `CommandID`, `EventID`, `RecordedEvent`, `Event`.
- Produces: `domain.AppendID`, `domain.RunTurnRequestID`, their parsers, `application.Digest`, `RuntimeID`, `WriterAuthority`, all EV2-03 request/result types (with temporary `AppendRequestV2`), `EventStoreV2`, `StoreErrorCode`, `StoreError`, `IsStoreCode`, and `IDGenerator.NewAppendID`.

- [ ] **Step 1: Write failing identifier and generator tests**

Add table tests that reuse the existing invalid cases `""`, `"   "`, `" id"`, `"id "`, and invalid UTF-8. Add exact generator assertions:

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

- [ ] **Step 2: Run the focused tests and observe the compile failure**

Run: `go test ./internal/harness/domain ./internal/harness/testkit -run 'TestParseAppendAndRunTurnRequestIDs|TestSequenceIDsAreTypedAndIndependent' -count=1`

Expected: FAIL because the new ID types, parsers, and `NewAppendID` do not exist.

- [ ] **Step 3: Implement the two Domain IDs and deterministic Append IDs**

Add:

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

Extend `SequenceIDs` with an independent `appends uint64` counter and `NewAppendID()`, and extend the current `IDGenerator` interface immediately so every existing test double is forced to implement or embed the complete generator contract.

- [ ] **Step 4: Write failing v2 type and error-algebra tests**

The tests must assert:

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

Also test strict `RuntimeID` validation, non-zero fencing token validation, exact enum strings, nil/non-nil receipt and record rules, `Digest` comparability, and lower-case 64-character text encoding.

- [ ] **Step 5: Implement v2 value types without switching Service yet**

Define `EventStoreV2` with `ReadStream`, `Append`, `ResolveAppend`, and `FindCommandRequest` exactly as EV2-03 except that the colliding new request type is temporarily `AppendRequestV2`. Define constants for every resolution/lookup kind and all eleven Store error codes. `StoreError.Error()` must contain only stable code and safe numeric/identity metadata; it must never render wrapped payloads or another Session's command record.

Use:

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

Construction helpers must reject `MayHaveCommitted=true` for every code except `commit_outcome_unknown`.

- [ ] **Step 6: Verify and commit Task 1**

Run:

```bash
gofmt -w internal/harness/domain internal/harness/application internal/harness/testkit
go test ./internal/harness/domain ./internal/harness/application ./internal/harness/testkit -count=1
go test ./... -count=1
git diff --check
```

Expected: PASS. Review the diff for type names and safe error text, then commit:

```bash
git add internal/harness/domain/ids.go internal/harness/domain/ids_test.go internal/harness/application/store_v2.go internal/harness/application/store_errors.go internal/harness/application/store_v2_test.go internal/harness/application/ports.go internal/harness/application/ports_test.go internal/harness/application/session_test.go internal/harness/application/turn_success_test.go internal/harness/adapters/memory/event_store_test.go internal/harness/testkit/ids.go internal/harness/testkit/ids_test.go
git commit -m "feat(storage): define EventStore v2 primitives"
```

---

### Task 2: Implement canonical framed digests and resource validation

**Files:**
- Modify: `internal/harness/domain/codec.go`
- Modify: `internal/harness/domain/codec_test.go`
- Create: `internal/harness/application/digest.go`
- Create: `internal/harness/application/digest_test.go`
- Create: `internal/harness/application/digest_fuzz_test.go`

**Interfaces:**
- Consumes: Task 1 `Digest`, `AppendRequestV2`, `CommandAdmission`, `ProposedEvent`; Domain strict event codec.
- Produces: `domain.MarshalEventPayload(Event) (eventType string, payload []byte, err error)`, `ParseDigest`, `DigestAppendRequest`, and `DigestRunTurnRequestV1`.

- [ ] **Step 1: Write failing Domain payload-codec tests**

For every current Event type, assert stable event type, canonical payload bytes, defensive output, strict UTF-8 validation, and no envelope metadata. Example:

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

- [ ] **Step 2: Run the codec test and observe the missing function**

Run: `go test ./internal/harness/domain -run TestMarshalEventPayloadIsCanonical -count=1`

Expected: FAIL because `MarshalEventPayload` does not exist.

- [ ] **Step 3: Extract one strict canonical payload path**

Refactor the existing recorded-event codec so `MarshalRecordedEvent` and `MarshalEventPayload` share the same event type switch and payload structs. Do not add a second JSON representation. Preserve every existing fixture byte and run `TestRecordedEventJSONUsesCanonicalEncodingForAllPayloads`.

- [ ] **Step 4: Write failing digest tests and fuzz properties**

Tests must prove:

- same logical request produces the same digest;
- changing any covered field changes it;
- changing only `AppendID` or `WriterAuthority` does not change it;
- admission presence and every admission field are framed;
- ordered Events do not commute;
- embedded NUL and ambiguous string boundaries cannot collide;
- invalid IDs, UTC/time/schema/payload/limit violations fail before hashing;
- `DigestRunTurnRequestV1` distinguishes Session and exact Input bytes;
- `Digest.MarshalText` produces lower-case hex and `ParseDigest` rejects upper-case, wrong length, and non-hex.

Use an ambiguity regression:

```go
func TestDigestFramingSeparatesAdjacentFields(t *testing.T) {
    left := validAppendRequest()
    right := validAppendRequest()
    left.SessionID, left.CommandID = "ab", "c"
    right.SessionID, right.CommandID = "a", "bc"
    leftDigest, leftErr := DigestAppendRequest(left)
    rightDigest, rightErr := DigestAppendRequest(right)
    if leftErr != nil || rightErr != nil { t.Fatalf("digest errors = %v, %v", leftErr, rightErr) }
    if leftDigest == rightDigest {
        t.Fatal("length framing did not separate adjacent fields")
    }
}
```

- [ ] **Step 5: Implement the version-1 framed digest codec**

Use a private encoder with `uint32` big-endian byte lengths, `uint64` numeric values, a one-byte admission flag, and explicit event count. Validate before writing bytes. Hash the final framed byte slice with SHA-256. Track encoded length during construction and reject above 16 MiB; reject more than 64 Events or payloads above 8 MiB.

Expose signatures:

```go
func DigestAppendRequest(request AppendRequestV2) (Digest, error)
func DigestRunTurnRequestV1(sessionID domain.SessionID, input string) (Digest, error)
func ParseDigest(text string) (Digest, error)
```

Add the fuzz target with one valid seed and a deterministic repeat property:

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

- [ ] **Step 6: Run focused tests, fuzz smoke, full tests, and commit**

Run:

```bash
gofmt -w internal/harness/domain internal/harness/application
go test ./internal/harness/domain ./internal/harness/application -run 'TestMarshalEventPayload|TestDigest' -count=1
go test ./internal/harness/application -run '^$' -fuzz FuzzDigestAppendRequest -fuzztime=3s
go test ./... -count=1
git diff --check
```

Expected: PASS with existing JSONL fixture tests unchanged. Commit:

```bash
git add internal/harness/domain/codec.go internal/harness/domain/codec_test.go internal/harness/application/digest.go internal/harness/application/digest_test.go internal/harness/application/digest_fuzz_test.go
git commit -m "feat(storage): add canonical request digests"
```

---

### Task 3: Prove the compact command aggregate before cutover

**Files:**
- Create: `internal/harness/domain/compact_state.go`
- Create: `internal/harness/domain/compact_apply.go`
- Create: `internal/harness/domain/compact_decide.go`
- Modify: `internal/harness/domain/decide.go`
- Create: `internal/harness/domain/compact_test.go`
- Create: `internal/harness/domain/compact_equivalence_test.go`

**Interfaces:**
- Consumes: current v1 `Session`, `Decide`, `Apply`, `Replay`, and both canonical fixtures as a frozen test oracle.
- Produces: `CompactSession`, `CompactTurn`, `CompactItem`, `ApplyCompact`, `ReplayCompact`, `DecideCompact`, and `CheckStartAssistantTurnEligibilityCompact`.

- [ ] **Step 1: Write failing compact-state tests**

Define tests that expect bounded state after terminalization:

```go
func TestReplayCompactDiscardsTerminalTranscript(t *testing.T) {
    records := fixtureRecords(t, "testdata/assistant_lifecycle.jsonl")
    got, err := ReplayCompact(records)
    if err != nil { t.Fatal(err) }
    if got.Version != uint64(len(records)) || got.ActiveTurn != nil {
        t.Fatalf("compact state = %#v", got)
    }
    if reflect.TypeOf(got).NumField() > 6 {
        t.Fatalf("compact state unexpectedly grew: %#v", got)
    }
}
```

Also cover one active Turn/Item, wrong terminal identity, terminal irreversibility, session close, invalid sequence, clone isolation, and no completed collection.

- [ ] **Step 2: Run and observe missing compact APIs**

Run: `go test ./internal/harness/domain -run 'TestReplayCompact|TestApplyCompact' -count=1`

Expected: FAIL because compact types/functions do not exist.

- [ ] **Step 3: Implement bounded state and transitions**

Use these shapes:

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

`LastTransitionAt` is a bounded chronology watermark, not transcript history. Initialize it to `StartedAt`; after an Item terminal event set it to that terminal timestamp. A later Item start and the Turn terminal must not precede it. Terminal events validate active identities, sequence, and the relevant chronology watermark before setting `ActiveItem`/`ActiveTurn` to nil; they never retain terminal text. Match v1 preflight order for duplicate `SessionCreated`: a non-pristine state returns `session_already_exists` before sequence validation.

- [ ] **Step 4: Write failing decision-equivalence tests**

Replay every prefix of both current fixtures through v1 and compact logic. For each prefix, generate all structurally relevant commands with fresh IDs, invoke both decision functions, and compare stable error code plus ordered Event values:

```go
func assertDecisionEquivalent(t *testing.T, full Session, compact CompactSession, command Command) {
    wantEvents, wantErr := Decide(full, command)
    gotEvents, gotErr := DecideCompact(compact, command)
    if errorCode(gotErr) != errorCode(wantErr) || !reflect.DeepEqual(gotEvents, wantEvents) {
        t.Fatalf("compact decision = (%#v,%v), full = (%#v,%v)", gotEvents, gotErr, wantEvents, wantErr)
    }
}
```

Add a historical duplicate case showing compact state alone cannot reject an old completed Turn/Item ID; record this as the exact case delegated to Store identity indexes in Task 4.

Add a deterministic fuzz target seeded by both canonical fixtures:

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

- [ ] **Step 5: Implement compact decisions and pass equivalence**

Do not call v1 `Decide` from production compact code. Extract pure command-field validation and ordered Event-construction helpers into `decide.go`, and call those helpers from both v1 and compact decisions. Full-history and bounded-state predicates remain separate; do not duplicate large validation/event-construction blocks between the two production decision paths. Keep the v1 oracle exclusively in `_test.go`.

Add focused error-equivalence tests for duplicate `SessionCreated` with a mismatched next sequence and chronology-equivalence tests proving both implementations reject: (1) a later Item starting before the prior completed Item's terminal time; and (2) a Turn terminal before the prior completed Item's terminal time.

- [ ] **Step 6: Verify and commit Task 3**

Run:

```bash
gofmt -w internal/harness/domain
go test ./internal/harness/domain -run 'Test.*Compact|TestCompact.*Equivalent' -count=1
go test ./internal/harness/domain -run '^$' -fuzz FuzzReplayCompact -fuzztime=3s
go test ./... -count=1
git diff --check
```

Expected: PASS; production compact files contain no call to `Decide` or `Replay`. Commit:

```bash
git add internal/harness/domain/decide.go internal/harness/domain/compact_state.go internal/harness/domain/compact_apply.go internal/harness/domain/compact_decide.go internal/harness/domain/compact_test.go internal/harness/domain/compact_equivalence_test.go
git commit -m "feat(domain): add compact command aggregate"
```

---

### Task 4: Build the v2 memory reference adapter and shared conformance suite

**Files:**
- Create: `internal/harness/application/eventstoretest/v2_suite.go`
- Create: `internal/harness/application/eventstoretest/v2_cases.go`
- Create: `internal/harness/adapters/memory/event_store_v2.go`
- Create: `internal/harness/adapters/memory/event_store_v2_test.go`
- Create: `internal/harness/adapters/memory/event_store_v2_benchmark_test.go`

**Interfaces:**
- Consumes: Tasks 1–3 v2 types, digest codec, compact replay, and Domain identity events.
- Produces: `memory.EventStoreV2`, `NewEventStoreV2(WriterAuthority)`, deterministic authority rotation/fault hooks, and `eventstoretest.RunV2`.

- [ ] **Step 1: Write the shared conformance harness and failing cases**

Define:

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

The temporary name is `V2Factory` because the same package retains the v1 `Factory` until Task 8; Task 8 promotes the v2 suite and removes the temporary prefix.

The suite must additionally test all EV2-05 through EV2-09 rules: 64/65 Event limits, payload/request byte limits, global EventID uniqueness, historical Turn/Item identity, defensive copies, nil/canceled contexts, corrupt receipt detection, concurrent Session commit-position uniqueness, and privacy-preserving command mismatch.

`CorruptReceipt` and `SetCommitHook` are adapter-specific conformance controls,
not additions to `EventStoreV2`. `CorruptReceipt` targets one committed Append so
exact retry and resolution can prove corrupt state fails closed with
`store_corrupt`. `SetCommitHook` installs one bounded hook at either
`CommitHookBeforePublish` or `CommitHookAfterPublish`; tests use it only to
cancel at the two sides of the publication point. Hooks are consumed once and
must never weaken production validation or authorization.

- [ ] **Step 2: Wire the suite to a missing adapter and observe failure**

Add:

```go
func TestEventStoreV2Contract(t *testing.T) {
    eventstoretest.RunV2(t, func(t *testing.T) eventstoretest.V2Harness {
        store := mustV2Store(t, application.WriterAuthority{RuntimeID: "runtime-1", FencingToken: 1})
        return eventstoretest.V2Harness{Store: store, RotateAuthority: store.SetAuthority, FailNext: store.FailNext, CorruptReceipt: store.CorruptReceipt, SetCommitHook: store.SetCommitHook}
    })
}
```

Run: `go test ./internal/harness/adapters/memory -run TestEventStoreV2Contract -count=1`

Expected: FAIL because the v2 adapter and hooks do not exist.

- [ ] **Step 3: Implement the atomic in-memory model**

Use one mutex and these separately owned maps/counters:

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

Clone and validate the complete request before locking where possible, then recompute/compare the digest under the lock. For new appends, check receipt before authority, then admission identity, CAS, global IDs, historical IDs, compact replay, commit position, receipt, and all maps as one mutation. Build all candidate copies before assigning any live map.

Stored receipts are validated against their immutable append metadata before
exact-retry or resolution output. Corrupt receipts return `store_corrupt` and
are never returned to callers. The in-memory adapter may expose the two
conformance-only controls above, but they are not part of the production Store
port.

- [ ] **Step 4: Implement fault points with exact commit knowledge**

Define `FaultBeforeCommit`, `FaultAfterCommitBeforeAck`, and `FaultResolve`. A before-commit fault returns `store_unavailable` with no mutation. An after-commit fault performs the full mutation and returns `commit_outcome_unknown`. A resolve fault returns `store_unavailable`, never `not_found`. Fault queues are bounded test controls and never loop.

The publication point is the single assignment of the fully constructed
candidate state. Cancellation rechecked immediately before that assignment is
definitely absent; cancellation triggered after it cannot be translated into a
definite non-commit result. The commit hooks provide deterministic coverage of
both sides without changing Store semantics.

- [ ] **Step 5: Implement pinned pagination and read validation**

Capture the current head while holding the mutex, use a supplied head only after validating it, clone at most `Limit` records, and set cursor/end exactly per EV2-08. Add a barrier test that appends after page 1 and proves page 2 excludes the new record.

- [ ] **Step 6: Run conformance, race, benchmark smoke, and commit**

Run:

```bash
gofmt -w internal/harness/application/eventstoretest internal/harness/adapters/memory
go test ./internal/harness/adapters/memory -run TestEventStoreV2Contract -count=1
go test -race ./internal/harness/adapters/memory -run TestEventStoreV2Contract -count=1
go test ./internal/harness/adapters/memory -run '^$' -bench BenchmarkEventStoreV2 -benchtime=100x
go test ./... -count=1
git diff --check
```

Expected: every conformance subtest passes and the race detector reports no race. Commit:

```bash
git add internal/harness/application/eventstoretest/v2_suite.go internal/harness/application/eventstoretest/v2_cases.go internal/harness/adapters/memory/event_store_v2.go internal/harness/adapters/memory/event_store_v2_test.go internal/harness/adapters/memory/event_store_v2_benchmark_test.go
git commit -m "feat(memory): implement EventStore v2 reference"
```

---

### Task 5: Migrate Application append ownership and pinned reads

**Files:**
- Create: `internal/harness/application/read_stream.go`
- Create: `internal/harness/application/read_stream_test.go`
- Create: `internal/harness/application/append_v2.go`
- Create: `internal/harness/application/append_v2_test.go`
- Delete: `internal/harness/application/append.go`
- Modify: `internal/harness/application/service.go`
- Modify: `internal/harness/application/session.go`
- Modify: `internal/harness/application/turn.go`
- Modify: `internal/harness/application/ports_test.go`
- Modify: `internal/harness/application/errors_test.go`
- Modify: `internal/harness/application/session_test.go`
- Modify: `internal/harness/application/scenario_test.go`
- Modify: `internal/harness/application/concurrency_test.go`
- Modify: `internal/harness/application/turn_success_test.go`
- Modify: `internal/harness/application/turn_failure_test.go`
- Modify: `internal/harness/application/enginescenariotest/suite.go`
- Modify: `internal/harness/testkit/clock.go`
- Create: `internal/harness/testkit/v2_store.go`

**Interfaces:**
- Consumes: `EventStoreV2`, Task 4 reference adapter, `Clock`, `IDGenerator`, and compact replay APIs.
- Produces: `ReadWholeStreamPinned`, immutable `AppendIntent`, `BuildAppendIntent`, `CommitAppendIntent`, v2 `Service` construction, and migrated Session/Turn normal paths.

- [ ] **Step 1: Write failing pinned-reader tests**

Test empty/missing streams, one page, multiple pages, append between pages, invalid page contract, repeated cursor, early `End`, cancellation, and defensive records. A malicious Store returning a changing head must map to `store_contract_violation`.

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

- [ ] **Step 2: Implement pinned read with progress guards**

Require page size `1..256`, validate every returned sequence/session/head/cursor, and reject any non-terminal page that makes no progress. Apply `domain.ApplyCompact` incrementally in the load helper so Application never needs a second unbounded state copy.

- [ ] **Step 3: Write failing append-intent ownership tests**

Assert Application calls Clock once, allocates one Append ID and N Event IDs before Store I/O, uses one timestamp for the batch, never lets Store rewrite proposed metadata, validates the returned receipt range, and reconstructs committed `RecordedEvent` values from intent plus receipt.

Use the exact immutable shape:

```go
type AppendIntent struct {
    Request AppendRequestV2
    Digest Digest
}

func BuildAppendIntent(clock Clock, ids IDGenerator, authority WriterAuthority,
    sessionID domain.SessionID, version uint64, commandID domain.CommandID,
    admission *CommandAdmission, events []domain.UncommittedEvent) (AppendIntent, error)
```

- [ ] **Step 4: Implement build, commit, and receipt validation**

`BuildAppendIntent` clones every Event, captures one UTC time, allocates stable IDs, builds the request, computes its digest, and returns a deep immutable value. `CommitAppendIntent` calls Store once, validates `AppendID`, first/last sequence, and non-zero commit position, creates `RecordedEvent` values from the proposed metadata, then applies them compactly. A mismatched receipt is `store_contract_violation`.

Delete the v1 `append.go` helper in this task. It is tied to the old Store result
shape and cannot coexist with the v2 `Service.store` without either a duplicate
method or a forbidden second compatibility Store. Keep the replacement under
the temporary `append_v2.go` name until Task 8 promotes it.

- [ ] **Step 5: Switch Service and all normal-path tests to v2**

Change construction to:

```go
func NewService(store EventStoreV2, ids IDGenerator, clock Clock, runner *engine.TurnRunner,
    authority WriterAuthority, config Config) (*Service, error)
```

Migrate `CreateSession`, `LoadSession`, `CloseSession`, and existing `RunTurn` normal/failure paths to pinned reads and Append intents. At this intermediate point, `RunTurnRequest` gains required `RequestID`; duplicates that are already found return `reconciliation_required` without a model call until Task 6 adds attachment/result behavior.

- [ ] **Step 6: Verify all migrated normal scenarios and commit**

Run:

```bash
gofmt -w internal/harness/application internal/harness/testkit
go test ./internal/harness/application -run 'TestReadWholeStreamPinned|TestBuildAppendIntent|TestCreateSession|TestLoadSession|TestCloseSession|TestRunTurn' -count=1
go test ./... -count=1
go test -race ./internal/harness/application ./internal/harness/adapters/memory -count=1
git diff --check
```

Expected: PASS; `rg -n '\.Load\(' internal/harness/application --glob '*.go' --glob '!**/*_test.go'` finds no production v1 load. Commit the paths declared in this task:

```bash
git add internal/harness/application/read_stream.go internal/harness/application/read_stream_test.go internal/harness/application/append_v2.go internal/harness/application/append_v2_test.go internal/harness/application/append.go internal/harness/application/service.go internal/harness/application/session.go internal/harness/application/turn.go internal/harness/application/ports_test.go internal/harness/application/errors_test.go internal/harness/application/session_test.go internal/harness/application/scenario_test.go internal/harness/application/concurrency_test.go internal/harness/application/turn_success_test.go internal/harness/application/turn_failure_test.go internal/harness/application/enginescenariotest/suite.go internal/harness/testkit/clock.go internal/harness/testkit/v2_store.go
git commit -m "refactor(application): adopt EventStore v2 appends"
```

---

### Task 6: Add durable request admission and exactly-one live execution

**Files:**
- Create: `internal/harness/application/execution_registry.go`
- Create: `internal/harness/application/execution_registry_test.go`
- Create: `internal/harness/application/request_result.go`
- Create: `internal/harness/application/request_result_test.go`
- Modify: `internal/harness/application/turn.go`
- Modify: `internal/harness/application/turn_success_test.go`
- Modify: `internal/harness/application/concurrency_test.go`
- Modify: `internal/harness/application/service.go`
- Modify: `internal/harness/application/errors.go`

**Interfaces:**
- Consumes: Task 5 v2 Service, `FindCommandRequest`, pinned records, `RunTurnRequest.RequestID` and digest.
- Produces: one live registry owner per Request ID, duplicate wait/observe behavior, durable-result reconstruction, `command_identity_mismatch`, and `reconciliation_required` Application errors.

- [ ] **Step 1: Write failing registry unit tests**

Cover owner creation, same-ID/same-digest waiter, same-ID/different-digest rejection, waiter cancellation without owner cancellation, one resolver/owner, terminal publication, and entry cleanup only after all waiters detach.

```go
func TestExecutionRegistryElectsOneOwner(t *testing.T) {
    registry := newExecutionRegistry()
    owner, first := registry.acquire("request-1", digestA, "session-1")
    waiter, second := registry.acquire("request-1", digestA, "session-1")
    if !first || second || owner.entry != waiter.entry { t.Fatal("ownership split") }
}
```

- [ ] **Step 2: Implement registry state and bounded waiter ownership**

Use a mutex, per-entry completion channel, immutable request identity, owner token, phase, retained append intent, result/error, waiter count, and a Session-to-unresolved count. Never close a channel twice and never wait while holding the mutex.

- [ ] **Step 3: Write failing durable-result reconstruction tests**

Given `CommandRequestRecord`, scan one pinned Session view and return exactly one of `running`, `completed`, `failed`, or `interrupted`. Verify the Turn/Item IDs and admission Append event pair; mismatches are `store_corrupt`, not a guessed result. Completed text comes from the terminal assistant Event; failures and interruptions preserve safe stable codes.

- [ ] **Step 4: Implement pre-ID lookup and duplicate paths**

The first operations in `RunTurn` after request/context validation must be:

```go
requestDigest, err := DigestRunTurnRequestV1(request.SessionID, request.Input)
lookup, err := service.store.FindCommandRequest(ctx, FindCommandRequestRequest{
    RunTurnRequestID: request.RequestID,
    SessionID: request.SessionID,
    RequestDigest: requestDigest,
})
```

Only `not_found` may enter registry owner election and identity allocation. `found` terminal reconstructs without a model call. `found` running attaches to a local entry or returns `reconciliation_required`. `identity_mismatch` returns conflict without exposing the stored record.

- [ ] **Step 5: Add admission to the first Append and handle races**

Populate `CommandAdmission` with Request, digest, Turn, Item, and Command identities. If another process wins with `command_request_conflict`, call `FindCommandRequest`, pinned-read the winner, and follow the same found path. Never call the model until this invocation owns a committed admission.

- [ ] **Step 6: Prove exactly one model call under concurrency**

Run 32 goroutines with one Request ID/digest and a counting blocking model. Assert one admission receipt, one model start, all callers receive the same terminal identities/result, and `go test -race` stays clean. Repeat with one mismatching input and assert it never waits on or influences the owner.

- [ ] **Step 7: Verify and commit Task 6**

Run:

```bash
gofmt -w internal/harness/application
go test ./internal/harness/application -run 'TestExecutionRegistry|TestReconstructRequestResult|TestRunTurnDuplicate|TestConcurrentSameRequest' -count=1
go test -race ./internal/harness/application -run 'TestExecutionRegistry|TestConcurrentSameRequest' -count=1
go test ./... -count=1
git diff --check
```

Expected: PASS and counting model equals one. Commit:

```bash
git add internal/harness/application/execution_registry.go internal/harness/application/execution_registry_test.go internal/harness/application/request_result.go internal/harness/application/request_result_test.go internal/harness/application/turn.go internal/harness/application/turn_success_test.go internal/harness/application/concurrency_test.go internal/harness/application/service.go internal/harness/application/errors.go
git commit -m "feat(application): enforce durable request admission"
```

---

### Task 7: Implement unknown-outcome resolution and cancellation winner rules

**Files:**
- Modify: `internal/harness/domain/events.go`
- Modify: `internal/harness/domain/commands.go`
- Modify: `internal/harness/domain/decide.go`
- Modify: `internal/harness/domain/decide_test.go`
- Modify: `internal/harness/domain/codec_test.go`
- Modify: `internal/harness/domain/apply_test.go`
- Create: `internal/harness/application/append_resolution.go`
- Create: `internal/harness/application/append_resolution_test.go`
- Modify: `internal/harness/application/execution_registry.go`
- Modify: `internal/harness/application/turn.go`
- Modify: `internal/harness/application/turn_failure_test.go`
- Modify: `internal/harness/application/service.go`
- Create: `internal/harness/application/unknown_outcome_scenario_test.go`

**Interfaces:**
- Consumes: retained `AppendIntent`, receipt resolution API, registry ownership/phases, and v2 Store fault controls.
- Produces: bounded `ResolveAppendIntent`, `request_abandoned` Domain interruption, `append_outcome_unknown`, and the terminal cancellation winner state machine.

- [ ] **Step 1: Add failing Domain tests for `request_abandoned`**

Assert it is accepted only by `InterruptAssistantTurn`, terminalizes Item before Turn, round-trips through schema version 1, and is rejected after any model terminal Event. Keep `process_crash` absent from this Slice.

- [ ] **Step 2: Implement the new interruption code**

Add:

```go
const InterruptionRequestAbandoned = "request_abandoned"
```

Include it in the existing strict interruption validation and canonical codec tests without changing old fixture bytes.

- [ ] **Step 3: Write failing bounded resolver tests**

Cover these exact scripts:

```text
unknown -> resolve committed                         => success
unknown -> resolve not_found -> exact append success => success
unknown -> unavailable x4                            => append_outcome_unknown
unknown terminal -> not_found -> exact append         => no second model call
unknown admission + caller canceled + committed       => request_abandoned, no model call
same request waiter                                   => no second resolver
```

Assert no more than four post-unknown Store operations, the 5-second timer is injectable in tests, the exact same digest/request is reused, and a Session with an unresolved entry rejects a different new admission.

- [ ] **Step 4: Implement `ResolveAppendIntent`**

Use:

```go
type AppendResolutionConfig struct {
    Timeout time.Duration
    MaxOperations uint32
}

func ResolveAppendIntent(ctx context.Context, store EventStoreV2, intent AppendIntent,
    config AppendResolutionConfig) (CommitReceipt, error)
```

Each cycle calls `ResolveAppend`; `committed` returns its validated receipt, `identity_mismatch` fails closed, and `not_found` permits one exact `Append`. Count every Store call. Stop at caller deadline, timeout, or operation cap. Never rebuild or re-decide the intent.

- [ ] **Step 5: Add failing cancellation phase tests**

Use barriers at `running`, immediately before terminal append, after terminal append begins, after commit-before-ack, and after resolution. Assert:

- cancel in `running` may append caller interruption;
- cancel after `terminal_append_in_flight` changes delivery only;
- completed/failed retained intent beats late cancellation;
- a CAS loser reloads and reports the durable winner;
- model invocation count remains one.

- [ ] **Step 6: Implement phase transitions and Session gate**

Transition registry phase atomically before each Store operation. Admission unknown resolves before model; committed-but-canceled admission appends `request_abandoned`. Terminal unknown retains the completed/failed intent and resolves it. Publish one terminal result to all waiters. An unresolved entry keeps the Session gate until it resolves or the process ends.

- [ ] **Step 7: Run the fault matrix, race suite, and commit**

Run:

```bash
gofmt -w internal/harness/domain internal/harness/application
go test ./internal/harness/domain -run RequestAbandoned -count=1
go test ./internal/harness/application -run 'TestResolveAppendIntent|TestUnknownOutcome|TestCancellationWinner' -count=1
go test -race ./internal/harness/application -run 'TestUnknownOutcome|TestCancellationWinner' -count=1
go test ./... -count=1
git diff --check
```

Expected: PASS; all scripted models report at most one invocation. Commit:

```bash
git add internal/harness/domain/events.go internal/harness/domain/commands.go internal/harness/domain/decide.go internal/harness/domain/decide_test.go internal/harness/domain/codec_test.go internal/harness/domain/apply_test.go internal/harness/application/append_resolution.go internal/harness/application/append_resolution_test.go internal/harness/application/execution_registry.go internal/harness/application/turn.go internal/harness/application/turn_failure_test.go internal/harness/application/service.go internal/harness/application/unknown_outcome_scenario_test.go
git commit -m "feat(application): resolve uncertain appends safely"
```

---

### Task 8: Cut over compact Domain state and delete every v1 Store surface

**Files:**
- Modify: `internal/harness/domain/state.go`
- Modify: `internal/harness/domain/apply.go`
- Modify: `internal/harness/domain/decide.go`
- Modify: `internal/harness/domain/replay.go`
- Delete: `internal/harness/domain/compact_state.go`
- Delete: `internal/harness/domain/compact_apply.go`
- Delete: `internal/harness/domain/compact_decide.go`
- Modify: `internal/harness/domain/apply_test.go`
- Modify: `internal/harness/domain/decide_test.go`
- Modify: `internal/harness/domain/replay_test.go`
- Modify: `internal/harness/domain/compact_test.go`
- Modify: `internal/harness/domain/compact_equivalence_test.go`
- Modify: `internal/harness/application/ports.go`
- Delete after merge: `internal/harness/application/store_v2.go`
- Rename/merge: `internal/harness/application/append_v2.go` -> `internal/harness/application/append.go`
- Delete: `internal/harness/application/eventstoretest/suite.go`
- Rename/merge: `internal/harness/application/eventstoretest/v2_suite.go` -> `internal/harness/application/eventstoretest/suite.go`
- Rename/merge: `internal/harness/application/eventstoretest/v2_cases.go` -> `internal/harness/application/eventstoretest/cases.go`
- Delete: `internal/harness/adapters/memory/event_store.go`
- Rename/merge: `internal/harness/adapters/memory/event_store_v2.go` -> `internal/harness/adapters/memory/event_store.go`
- Delete: `internal/harness/adapters/memory/event_store_test.go`
- Rename/merge: `internal/harness/adapters/memory/event_store_v2_test.go` -> `internal/harness/adapters/memory/event_store_test.go`
- Rename: `internal/harness/adapters/memory/event_store_v2_benchmark_test.go` -> `internal/harness/adapters/memory/event_store_benchmark_test.go`
- Modify: `internal/harness/application/ports_test.go`
- Modify: `internal/harness/application/errors_test.go`
- Modify: `internal/harness/application/session_test.go`
- Modify: `internal/harness/application/scenario_test.go`
- Modify: `internal/harness/application/concurrency_test.go`
- Modify: `internal/harness/application/turn_success_test.go`
- Modify: `internal/harness/application/turn_failure_test.go`
- Modify: `internal/harness/application/enginescenariotest/suite.go`
- Modify: `internal/harness/architecture/dependencies_test.go`

**Interfaces:**
- Consumes: proven compact implementation and fully migrated Application.
- Produces: final `domain.Session`, `Decide`, `Apply`, `Replay`, `application.EventStore`, and `memory.EventStore` names with v2 semantics only.

- [ ] **Step 1: Add a failing repository-surface guard**

Extend architecture tests to scan production Go syntax and fail on:

```text
EventStoreV2
an EventStore method named Load with the v1 Session stream signature
Append(context.Context, AppendRequest) ([]domain.RecordedEvent, error)
Session.Turns
Session.TurnOrder
```

Do not search comments or test-only oracle files by raw substring; inspect declarations/selectors so the guard cannot be bypassed by renaming a comment.

- [ ] **Step 2: Run the guard and observe v1/temporary names**

Run: `go test ./internal/harness/architecture -run TestNoEventStoreV1Surface -count=1`

Expected: FAIL listing the temporary v2 names and old full-history structures.

- [ ] **Step 3: Replace `Session` with the compact shape**

Move the proven compact fields/logic into `state.go`, `apply.go`, `decide.go`, and `replay.go`; remove the temporary compact production files. Update callers to use `Session.ActiveTurn` rather than maps/order arrays. Preserve the test-only frozen oracle under a `_test.go`-only renamed type.

- [ ] **Step 4: Promote v2 names and remove v1 implementations**

Rename `EventStoreV2` to `EventStore`, `AppendRequestV2` to `AppendRequest`, and `memory.EventStoreV2` to `memory.EventStore`. Merge the temporary type declarations into `ports.go`, then delete `store_v2.go`. Delete v1 `Load`, v1 Append result semantics, adapter-owned Clock/ID generation, old fault hooks, and all compatibility helpers. Promote the v2 conformance files and benchmark to their final names; update shared suites and test doubles to the single final interface.

- [ ] **Step 5: Verify absence and behavioral parity**

Run:

```bash
gofmt -w internal/harness
go test ./internal/harness/architecture -run TestNoEventStoreV1Surface -count=1
go test ./internal/harness/domain -run 'TestCompact.*Equivalent|TestReplay' -count=1
go test ./internal/harness/adapters/memory -run TestEventStoreContract -count=1
go test ./... -count=1
go test -race ./... -count=1
git diff --check
```

Also run:

```bash
rg -n 'EventStoreV2|AppendRequestV2|\.Load\(|\[\]domain\.RecordedEvent, error\)' internal/harness --glob '*.go' --glob '!**/*_test.go'
```

Expected: no output. Commit:

```bash
git add -A internal/harness
git commit -m "refactor(storage): complete EventStore v2 cutover"
```

---

### Task 9: Publish implemented contracts and completion evidence

**Files:**
- Create: `docs/architecture/eventstore-v2.md`
- Create: `docs/architecture/eventstore-v2.zh-CN.md`
- Create: `docs/architecture/eventstore-v2-evidence.md`
- Modify: `docs/architecture/domain-events.md`
- Modify: `docs/architecture/engine-vertical-slice.md`
- Modify: `docs/architecture/engine-vertical-slice.zh-CN.md`
- Modify: `docs/README.md`
- Modify: `README.md`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: completed Tasks 1–8 and their exact commit hashes.
- Produces: implemented bilingual contract, auditable evidence ledger, CI gates, benchmark baseline, and visible exclusions for Slice 2 onward.

- [ ] **Step 1: Write the implemented contract and synchronized Chinese copy**

Document the final four-method Store interface, identity ownership, digest format/version, error algebra, pagination truth table, admission behavior, compact state, resolver budget, cancellation winner table, resource bounds, and explicit exclusions. Mark internal stability as `experimental` before v1.0.

- [ ] **Step 2: Add CI and repository guards**

Keep existing format/vet/race gates and add deterministic commands for digest/fixture tests and architecture surface guard. Do not add network downloads beyond normal Go module resolution; this Slice remains standard-library only.

- [ ] **Step 3: Run final verification from a clean index**

Run exactly:

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

Expected: all commands pass; status lists only the Task 9 documentation/CI files before commit.

- [ ] **Step 4: Record evidence without overstating maturity**

The evidence ledger lists every Task commit, exact command/output summary, benchmark environment/result, conformance/fault cases, and these remaining blockers: SQLite durability, JSONL replica/import, durable Runtime host/recovery, ACP, and TUI. It must state that the reference memory adapter is not durable production storage.

- [ ] **Step 5: Commit Task 9 and verify the final tree**

```bash
git add README.md docs/README.md docs/architecture/eventstore-v2.md docs/architecture/eventstore-v2.zh-CN.md docs/architecture/eventstore-v2-evidence.md docs/architecture/domain-events.md docs/architecture/engine-vertical-slice.md docs/architecture/engine-vertical-slice.zh-CN.md .github/workflows/ci.yml
git commit -m "docs: publish EventStore v2 contract evidence"
git status --short
```

Expected: the commit succeeds and `git status --short` prints nothing.

## Final completion gate

Slice 1 is complete only when:

- all nine task commits exist and map to their review gates;
- the final tree contains one Store interface with v2 semantics and no v1 compatibility path;
- all shared conformance, duplicate-request, unknown-outcome, cancellation, compact-equivalence, race, fuzz-smoke, architecture, and platform-build gates pass;
- the English implemented contract and complete Chinese reading copy agree;
- completion evidence is committed and explicitly states that SQLite implementation has not begun.
