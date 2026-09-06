# Observed-State Safe File Mutation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `read_file` establish an internal file observation, make `write_file` and the new `edit_file` reject stale or unobserved destructive changes, and publish local mutations atomically.

**Architecture:** `tools.FileSystem` carries opaque versions and mandatory mutation guards, `application.Service` owns a mutex-protected per-session observation table, and `workspacefs` owns version calculation, per-target serialization, literal edit, and staged publication. Model-facing schemas never contain versions; Application derives guards after Policy/Approver and maps filesystem failures into actionable Tool Results.

**Tech Stack:** Go standard library (`os`, `io`, `sync`, `syscall`, `crypto/sha256`), existing `tools`/`application`/`workspacefs` packages, table-driven tests, race detector.

**Spec:** `docs/superpowers/specs/2026-09-04-observed-file-mutation-design.md`

## Global Constraints

- All new Go APIs remain `internal`; add no external dependency or plugin kernel.
- The model never sees or supplies `FileVersion` or `MutationGuard`.
- `edit_file` is bounded UTF-8 literal replacement, unique-match by default; `replace_all` is its only matching option.
- `MaxEditFileBytes` is `1 << 20`; `old_string` and `new_string` retain the existing 32,768-byte argument bound.
- Unseen/absent write means `create_if_absent`; present write/edit means `replace_if_version`; unseen edit fails closed.
- Policy and Approver run before filesystem mutation. Freshness never grants authorization.
- The workspace jail and symlink-escape refusal remain unchanged.
- Replacement is staged beside the target, synced, then renamed; the destination is never truncated in place.
- The guarantee excludes `exec`, external-process serialization, and Windows runtime behavior. Windows still cross-compiles.
- Observations are process-local and cleared on explicit Resume, Close, and Delete lifecycle boundaries.
- English implemented contracts receive synchronized Chinese reading copies.

---

### Task 1: Lock the version, guard, result, and error vocabulary

**Files:**
- Create: `internal/harness/tools/files.go`
- Create: `internal/harness/tools/files_test.go`
- Modify: `internal/harness/tools/errors.go`

**Interfaces:**
- Produces: `FileVersion`, `GuardKind`, `MutationGuard`, `FileRead`, `MutationOperation`, `MutationResult`, `MaxEditFileBytes`, and seven filesystem error codes.
- Consumes: existing secret-free `tools.Error` and `tools.IsCode`.

- [ ] **Step 1: Write the failing value tests**

```go
func TestMutationGuardValidate(t *testing.T) {
    tests := []struct{ guard MutationGuard; ok bool }{
        {MutationGuard{Kind: GuardCreateIfAbsent}, true},
        {MutationGuard{Kind: GuardCreateIfAbsent, Version: "v1"}, false},
        {MutationGuard{Kind: GuardReplaceIfVersion, Version: "v1"}, true},
        {MutationGuard{Kind: GuardReplaceIfVersion}, false},
        {MutationGuard{Kind: "invented"}, false},
    }
    for _, test := range tests {
        if got := test.guard.Validate() == nil; got != test.ok {
            t.Fatalf("Validate(%#v) ok = %t, want %t", test.guard, got, test.ok)
        }
    }
}
```

Add a second table proving every new code is accepted by `IsCode` and `Error()` contains neither a path nor a version.

- [ ] **Step 2: Prove the tests are red**

Run: `go test ./internal/harness/tools -run 'TestMutationGuard|TestFilesystemErrorCodes' -count=1`

Expected: compile failure because the values and codes do not exist.

- [ ] **Step 3: Implement the exact value contract**

```go
type FileVersion string
const MaxEditFileBytes = 1 << 20
type GuardKind string
const (
    GuardCreateIfAbsent GuardKind = "create_if_absent"
    GuardReplaceIfVersion GuardKind = "replace_if_version"
)
type MutationGuard struct { Kind GuardKind; Version FileVersion }
type FileRead struct { Data []byte; Truncated bool; Version FileVersion }
type MutationOperation string
const (
    MutationCreate MutationOperation = "create"
    MutationUpdate MutationOperation = "update"
)
type MutationResult struct { Version FileVersion; Operation MutationOperation }
```

`Validate` accepts only the four combinations pinned by the test. Add wire values `fs_not_observed`, `fs_stale_version`, `fs_edit_not_found`, `fs_ambiguous_edit`, `fs_not_regular_file`, `fs_not_text`, and `fs_too_large` to `validErrorCode`.

- [ ] **Step 4: Verify and commit**

Run: `go test ./internal/harness/tools -count=1`

```bash
git add internal/harness/tools/files.go internal/harness/tools/files_test.go internal/harness/tools/errors.go
git commit -m "feat(tools): define guarded file mutation values"
```

### Task 2: Replace direct truncation with guarded atomic workspace primitives

**Files:**
- Modify: `internal/harness/tools/ports.go`
- Modify: `internal/harness/tools/porttest/filesystem.go`
- Modify: `internal/harness/adapters/workspacefs/fs.go`
- Create: `internal/harness/adapters/workspacefs/mutation.go`
- Create: `internal/harness/adapters/workspacefs/version_linux.go`
- Create: `internal/harness/adapters/workspacefs/version_darwin.go`
- Create: `internal/harness/adapters/workspacefs/version_other.go`
- Modify: `internal/harness/adapters/workspacefs/fs_test.go`
- Create: `internal/harness/adapters/workspacefs/mutation_test.go`
- Modify: `internal/harness/application/pipeline.go`
- Modify: `internal/harness/application/loop_test.go`

**Interfaces:**
- Consumes: Task 1 values.
- Produces: final `tools.FileSystem` signatures and guarded `workspacefs`; Application temporarily uses create-if-absent until Task 3.

- [ ] **Step 1: Write failing port/adapter tests**

```go
read, err := files.Read(ctx, abs, 64)
if err != nil || read.Version == "" || string(read.Data) != "before" { t.Fatal(read, err) }
updated, err := files.Write(ctx, abs, []byte("after"), tools.MutationGuard{
    Kind: tools.GuardReplaceIfVersion, Version: read.Version,
})
if err != nil || updated.Operation != tools.MutationUpdate || updated.Version == read.Version { t.Fatal(updated, err) }
_, err = files.Write(ctx, abs, []byte("lost"), tools.MutationGuard{
    Kind: tools.GuardReplaceIfVersion, Version: read.Version,
})
if !tools.IsCode(err, tools.CodeFSStaleVersion) { t.Fatalf("stale error = %v", err) }
```

Add cases for guarded create, concurrent creator preservation, directories, invalid UTF-8, cancellation, jail escape, unique/missing/ambiguous/replace-all edit, guard-before-match, LF/CRLF, and mode preservation.

- [ ] **Step 2: Prove the adapter tests are red**

Run: `go test ./internal/harness/adapters/workspacefs ./internal/harness/tools/porttest -count=1`

Expected: compile failure because the port remains unguarded.

- [ ] **Step 3: Install the final port and migrate callers**

```go
type FileSystem interface {
    Resolve(context.Context, string, string) (string, error)
    Read(context.Context, string, int) (FileRead, error)
    Write(context.Context, string, []byte, MutationGuard) (MutationResult, error)
    Edit(context.Context, string, []byte, []byte, bool, MutationGuard) (MutationResult, error)
    List(context.Context, string, int, int) ([]string, bool, error)
}
```

Migrate `countingFS`, port tests, and all compile-time callers. Until Task 3, Application passes `MutationGuard{Kind: GuardCreateIfAbsent}` to write, so existing targets fail closed. Do not publish `edit_file` in the catalog yet.

- [ ] **Step 4: Implement versions and stable reads**

`versionOf` hashes a canonical binary encoding of device, inode, size, nanosecond mtime, and nanosecond ctime. Linux and Darwin helpers use their `syscall.Stat_t` layouts; `version_other.go` uses size/mode/nanosecond mtime solely so Windows cross-builds without a runtime claim. Tests treat the `sha256:` token as opaque.

`Read` opens a jailed regular file, versions the descriptor before and after reading `limit+1` bytes, rejects a changed descriptor as `fs_stale_version`, returns at most `limit`, and rejects invalid UTF-8 as `fs_not_text`.

- [ ] **Step 5: Implement literal edit and staged publication**

Add a per-target lock registry to `FileSystem`. Under the lock, re-jail/re-identify, validate the guard, and for edit read at most `MaxEditFileBytes+1`, normalize CRLF for literal matching, enforce cardinality, replace, and restore the dominant newline.

Create a private sibling staging directory with `0700`, an exclusive temp file with `0600`, write all bytes, sync, apply the prior mode (or `0600` for create), and close. Publish create via `os.Link` and replace via `os.Rename`. Best-effort sync the parent and remove staging. Any pre-publication error preserves the destination.

- [ ] **Step 6: Verify focused behavior and cross-builds**

Run:

```bash
go test ./internal/harness/tools ./internal/harness/adapters/workspacefs ./internal/harness/application -count=1
env GOOS=windows go test ./internal/harness/adapters/workspacefs -run '^$'
env GOOS=darwin go test ./internal/harness/adapters/workspacefs -run '^$'
```

Expected: PASS; Application tests pin that overwrite is refused, while Task 3 adds the specific `fs_not_observed` recovery mapping.

- [ ] **Step 7: Commit**

```bash
git add internal/harness/tools/ports.go internal/harness/tools/porttest/filesystem.go internal/harness/adapters/workspacefs internal/harness/application/pipeline.go internal/harness/application/loop_test.go
git commit -m "feat(workspacefs): add guarded atomic file mutation"
```

### Task 3: Add per-session observations and read/write recovery

**Files:**
- Create: `internal/harness/application/file_observations.go`
- Create: `internal/harness/application/file_observations_test.go`
- Modify: `internal/harness/application/service.go`
- Modify: `internal/harness/application/session.go`
- Modify: `internal/harness/application/pipeline.go`
- Modify: `internal/harness/application/errors.go`
- Modify: `internal/harness/application/loop_test.go`

**Interfaces:**
- Consumes: guarded `tools.FileSystem`.
- Produces: `guardForWrite`, `guardForEdit`, `recordPresent`, `recordAbsent`, `forget`, and stable Tool Result mappings.

- [ ] **Step 1: Write failing state-transition tests**

```go
func TestFileObservationsTransitions(t *testing.T) {
    o := newFileObservations()
    sid, target := domain.SessionID("session-1"), "/workspace/a.txt"
    if got := o.guardForWrite(sid, target); got.Kind != tools.GuardCreateIfAbsent { t.Fatal(got) }
    if _, err := o.guardForEdit(sid, target); !tools.IsCode(err, tools.CodeFSNotObserved) { t.Fatal(err) }
    o.recordPresent(sid, target, "v1")
    if got, _ := o.guardForEdit(sid, target); got.Version != "v1" { t.Fatal(got) }
    o.forget(sid)
    if _, err := o.guardForEdit(sid, target); !tools.IsCode(err, tools.CodeFSNotObserved) { t.Fatal(err) }
}
```

Add concurrent readers/recorders/forgetters for `-race`.

- [ ] **Step 2: Prove the state test is red**

Run: `go test ./internal/harness/application -run TestFileObservations -count=1`

Expected: compile failure because the table does not exist.

- [ ] **Step 3: Implement the private table**

```go
type fileObservation struct { present bool; version tools.FileVersion }
type fileObservations struct {
    mu sync.RWMutex
    bySession map[domain.SessionID]map[string]fileObservation
}
```

An existing `{present:false}` entry means observed absent; a missing entry means unseen. Never persist the table or put versions in Domain events.

- [ ] **Step 4: Wire read/write and error classification**

Construct the table in `NewService`. Pass SessionID into `invokeTool`. A successful read records present; authoritative not-found records absent; write derives its guard immediately before the adapter call and records the returned version only after success. Failed mutation never advances state.

Map the seven `tools.ErrorCode` values to identical lower-case Tool Result codes. Use bounded messages: “read the file before changing it”, “file changed since it was read; re-read it and retry”, “literal was not found”, “literal appears more than once; include more context or use replace_all”, “target is not a regular file”, “file is not valid UTF-8 text”, and “file exceeds the edit size limit”. Never render a path or version.

- [ ] **Step 5: Wire lifecycle clearing and verify**

Call `forget` after successful `ResumeSession` admission and successful `CloseSession`/`DeleteSession`. Never clear from `LoadSession`, because ordinary `RunTurn` loads canonical state and observations must survive turns.

Run:

```bash
go test ./internal/harness/application -run 'TestFileObservations|Test.*Read.*Write|Test.*Stale|Test.*Resume' -count=1
go test -race ./internal/harness/application -run TestFileObservations -count=10
```

Expected: PASS, including read A → external B → stale rejection → re-read B → guarded success.

- [ ] **Step 6: Commit**

```bash
git add internal/harness/application/file_observations.go internal/harness/application/file_observations_test.go internal/harness/application/service.go internal/harness/application/session.go internal/harness/application/pipeline.go internal/harness/application/errors.go internal/harness/application/loop_test.go
git commit -m "feat(application): guard file writes with session observations"
```

### Task 4: Expose and execute bounded literal `edit_file`

**Files:**
- Modify: `internal/harness/tools/catalog.go`
- Modify: `internal/harness/tools/catalog_test.go`
- Modify: `internal/harness/tools/schema.go`
- Modify: `internal/harness/tools/schema_test.go`
- Modify: `internal/harness/application/pipeline.go`
- Modify: `internal/harness/application/service.go`
- Modify: `internal/harness/application/loop_test.go`
- Modify: `internal/harness/policy/policy_test.go`

**Interfaces:**
- Consumes: `FileSystem.Edit` and Task 3 observations.
- Produces: default `edit_file` ToolSpec and complete execution path.

- [ ] **Step 1: Write failing catalog/schema tests**

Require five default tools and this closed schema:

```json
{"type":"object","additionalProperties":false,"required":["path","old_string","new_string"],"properties":{"path":{"type":"string","minLength":1,"maxLength":4096},"old_string":{"type":"string","minLength":1,"maxLength":32768},"new_string":{"type":"string","maxLength":32768},"replace_all":{"type":"boolean"}}}
```

Add validation cases for omitted/true/false/non-boolean `replace_all`, empty `old_string`, equal old/new strings at Application parsing, and unknown fields. Add compiler tests allowing boolean leaf properties while still rejecting boolean as a top-level Tool schema.

- [ ] **Step 2: Prove schema tests are red**

Run: `go test ./internal/harness/tools -run 'TestDefaultWorkspaceSpecs|TestValidateArgs|TestNewCatalog' -count=1`

Expected: FAIL because `edit_file` and boolean leaf validation are absent.

- [ ] **Step 3: Add the catalog and boolean leaf**

Add `NameEditFile` and a `RiskWrite`, `Mutates:true`, `SourceBuiltin` spec. Extend recursive schema compilation with a boolean leaf that accepts only bool values and rejects object/string/integer/array keywords; keep `compileSchema`'s root check object-only so a top-level boolean Tool schema remains invalid.

- [ ] **Step 4: Write failing Application behavior tests**

Prove unseen edit rejection, read-then-unique success, stale/missing/ambiguous codes, `replace_all`, read-only denial, default approval/denial, observation advancement after edit, and absence of versions from arguments/results/runtime/transcript.

- [ ] **Step 5: Prove behavior tests are red**

Run: `go test ./internal/harness/application -run 'Test.*EditFile|Test.*Edit.*Approval' -count=1`

Expected: FAIL because parse/dispatch do not recognize `edit_file`.

- [ ] **Step 6: Implement parse, guard, dispatch, and acknowledgement**

```go
OldString string `json:"old_string"`
NewString string `json:"new_string"`
ReplaceAll bool `json:"replace_all"`
```

Treat edit as a filesystem tool in port-needs and lexical scope. After Policy/Approver, derive the edit guard, call `FileSystem.Edit`, and record the returned version. Return only `edited file` or `replaced all occurrences`; do not copy the resulting file into Tool Result.

- [ ] **Step 7: Verify and commit**

Run: `go test ./internal/harness/tools ./internal/harness/policy ./internal/harness/application -count=1`

```bash
git add internal/harness/tools internal/harness/application internal/harness/policy/policy_test.go
git commit -m "feat(tools): add observed literal edit_file"
```

### Task 5: Prove fault, concurrency, lifecycle, and bypass boundaries

**Files:**
- Create: `internal/harness/adapters/workspacefs/mutation_fault_test.go`
- Create: `internal/harness/adapters/workspacefs/mutation_race_test.go`
- Create: `internal/harness/application/file_mutation_scenario_test.go`
- Modify: `internal/harness/adapters/workspacefs/mutation.go`

**Interfaces:**
- Consumes: complete guarded adapter and Application policy.
- Produces: acceptance evidence for lost-update prevention and honest exclusions.

- [ ] **Step 1: Write failing pre-publication fault tests**

Add a private test seam:

```go
type mutationHooks struct { beforePublish func() error }
```

Inject failure after staged sync/close but before link/rename. Assert an existing destination remains byte-identical, a create destination remains absent, and staging residue is removed.

- [ ] **Step 2: Prove fault tests are red**

Run: `go test ./internal/harness/adapters/workspacefs -run TestMutationFault -count=1`

Expected: FAIL because the hook is absent.

- [ ] **Step 3: Implement the private hook and cleanup**

Invoke it exactly once before publication. Install deferred descriptor close and staging cleanup before writing begins. Keep the hook unexported and nil in production.

- [ ] **Step 4: Add concurrency/lifecycle scenarios**

Prove: two writers with one observed version yield one success/one stale; two unseen creators yield one success/one not-observed; Sessions cannot share observations; an ordinary next Turn retains observation; a newly constructed Service over the same durable Session starts unseen; Resume/Close/Delete clear state; an `exec` fixture can modify the file and the following structured edit detects stale without claiming exec mediation.

- [ ] **Step 5: Run race and repetition matrices**

```bash
go test -race ./internal/harness/adapters/workspacefs ./internal/harness/application -run 'Test.*(Mutation|Observation|Stale|Concurrent|Resume)' -count=10
go test ./internal/harness/tools ./internal/harness/adapters/workspacefs ./internal/harness/application ./internal/harness/composition -count=1
```

Expected: PASS with no race report or partial destination.

- [ ] **Step 6: Run two mutation checks**

Temporarily invert stale-version equality; the stale/concurrent matrix must fail. Restore. Temporarily bypass unique-match cardinality; edit tests must fail. Restore and rerun Step 5. Record the exact failing test names for Task 6; commit neither mutant.

- [ ] **Step 7: Commit**

```bash
git add internal/harness/adapters/workspacefs/mutation.go internal/harness/adapters/workspacefs/mutation_fault_test.go internal/harness/adapters/workspacefs/mutation_race_test.go internal/harness/application/file_mutation_scenario_test.go
git commit -m "test(files): prove stale-write and atomic-publication boundaries"
```

### Task 6: Publish contract and evidence, then run the full gate

**Files:**
- Create: `docs/architecture/observed-file-mutation.md`
- Create: `docs/architecture/observed-file-mutation.zh-CN.md`
- Create: `docs/architecture/observed-file-mutation-evidence.md`
- Modify: `docs/architecture/tool-runtime.md`
- Modify: `docs/architecture/tool-runtime.zh-CN.md`
- Modify: `docs/architecture/tool-runtime-evidence.md`
- Modify: `docs/README.md`
- Modify: `README.md`
- Modify: `SECURITY.md`

**Interfaces:**
- Consumes: Tasks 1–5 commits and mutation results.
- Produces: auditable implemented behavior and limitations; no runtime change.

- [ ] **Step 1: Write the implemented contract and Chinese copy**

Document exact Go signatures, five schemas, observation transitions, errors, atomic sequence, numeric bounds, lifecycle clears, and platform status. Explicitly exclude exec and the external check-to-rename race. Link the accepted design and plan.

- [ ] **Step 2: Write the evidence ledger**

Record every task commit, exact command/output, mutation failing test names, race/repetition and cross-build results, and deviations. Never record an unrun command or claim Windows runtime coverage.

- [ ] **Step 3: Update existing authority/security docs**

Index the contract/evidence; change Tool Runtime from four to five built-ins and remove unconditional-write language; update root status; explain structured-tool protection and exec/external-writer limits in `SECURITY.md`. Do not absorb unrelated CC changes.

- [ ] **Step 4: Run docs and complete ordinary-PR gates**

```bash
go test ./internal/docsguard ./internal/harness/architecture -count=1
git diff --check
cd cmd/acp-web-bridge/web && npm ci && npm run build
cd ../../../
go vet ./...
go test -race ./... -count=1
env CGO_ENABLED=0 go build ./...
env GOOS=windows go build ./...
env GOOS=darwin go build ./...
```

Expected: every command exits 0. Record actual elapsed times and scheduled-matrix behavior.

- [ ] **Step 5: Commit docs/evidence**

```bash
git add README.md SECURITY.md docs/README.md docs/architecture/observed-file-mutation.md docs/architecture/observed-file-mutation.zh-CN.md docs/architecture/observed-file-mutation-evidence.md docs/architecture/tool-runtime.md docs/architecture/tool-runtime.zh-CN.md docs/architecture/tool-runtime-evidence.md
git commit -m "docs: record observed file mutation contract and evidence"
```

- [ ] **Step 6: Final branch review**

```bash
git diff --check main...HEAD
git log --oneline main..HEAD
git status --short
```

Map every accepted-spec requirement to a test or honestly documented exclusion; require a clean worktree before handoff.
