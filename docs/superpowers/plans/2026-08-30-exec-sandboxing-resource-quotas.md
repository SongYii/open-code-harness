# Exec Sandboxing and Resource Quotas Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn `localexec.Enforcement`'s honest `"partial"` claim into real OS-level
confinement (bubblewrap + cgroup v2 on Linux, Seatbelt + `RLIMIT_AS` on macOS)
plus a fail-closed availability gate at composition time, without CGO and
without regressing any currently enforced guarantee.

**Architecture:** `internal/harness/adapters/localexec` gains per-platform,
build-tagged confinement backends behind the existing `tools.CommandRunner`
port; no new port and no new adapter package. `composition.Config` gains one
escape-hatch field. `tools.CommandResult` gains one new terminal-state field.
`application/errors.go` gains one new tool-error code pair. Each platform
backend lands and proves itself independently before the fail-closed gate
that depends on both existing is wired in a dedicated final task, so the
tree stays fully functional after every task rather than fail-closed on an
unfinished platform partway through the sequence.

**Tech Stack:** Go 1.26, `CGO_ENABLED=0` throughout (this is the property
under test, not an assumption), `golang.org/x/sys/unix` and
`golang.org/x/sys/windows` for raw syscalls, external `bwrap` and
`sandbox-exec` binaries invoked over `os/exec`, `golang.org/x/net/bpf` only
if a later slice needs the optional seccomp layer (not required by this
plan), table-driven `testing`, race and cross-build verification.

**Spec:** `docs/superpowers/specs/2026-08-30-exec-sandboxing-resource-quotas-design.md`
(English normative, Accepted); synchronized Chinese summary at
`docs/superpowers/specs/2026-08-30-exec-sandboxing-resource-quotas-design.zh-CN.md`.
Research: `docs/research/architecture-gates/2026-08-30-exec-sandboxing-and-resource-quotas.md`.

## Global Constraints

- Every currently `Enforced` guarantee in `SECURITY.md` (argv-only exec, own
  process group killed as a group, scrubbed environment, filesystem jail)
  stays true after every task in this plan. This plan only adds guarantees;
  it narrows nothing already relied on.
- `CGO_ENABLED=0 go build ./...` must stay clean after every task, not just
  at the end. If a task's own code would need CGO, the task is wrong and
  must be redesigned before landing, not fixed up later.
- No task may make `och` fail closed on a platform whose backend has not
  yet landed. The fail-closed availability gate is deliberately the last
  Linux/macOS-affecting task (Task 5), after both backends exist and are
  proven, so `go test ./...` and real usage stay green throughout.
- Windows is explicitly out of scope for a sandbox backend in this plan
  (accepted trade-off, design §5). Task 5 makes Windows fail closed by
  default with the same escape hatch other platforms without a working
  backend use — this is intentional, not an oversight, and is exactly the
  behavior change the design names and the reviewer accepted.
- No sleep-based concurrency tests. Use channels, `t.Skip` for host
  dependencies that are absent (missing `bwrap`, missing `sandbox-exec`,
  non-cgroup-v2 Linux), and the repository's bounded test timeouts.
- Every task follows red-green-refactor: write the focused test, observe
  it fail for the right reason, then implement, then run the focused
  package tests green before moving on.
- `localexec.Enforcement` is a structured, per-effect value from Task 1
  onward. No task may collapse it back into a single string or overclaim
  an effect a backend does not actually confine.

## File Map

| Path | Responsibility |
| --- | --- |
| `internal/harness/tools/ports.go` | `CommandResult.ResourceLimited`; `CommandRunner` port unchanged |
| `internal/harness/application/errors.go` | `CodeResourceLimit` / `ToolTextResourceLimit` |
| `internal/harness/adapters/localexec/{enforcement.go,doc.go,runner.go}` | Structured `Enforcement` type; per-platform wiring |
| `internal/harness/adapters/localexec/{bwrap_linux.go,cgroup_linux.go}` (+`_test.go`) | Linux probe, per-call bwrap argv, cgroup v2 memory quota and monitor |
| `internal/harness/adapters/localexec/{sandbox_darwin.go}` (+`_test.go`) | macOS probe, per-call Seatbelt `.sbpl`, `RLIMIT_AS` |
| `internal/harness/adapters/localexec/{sandbox_other.go,availability.go}` (+`_test.go`) | Cross-platform availability seam and fail-closed classification; Windows/unsupported stub |
| `internal/harness/composition/{config.go,assembly.go}` (+`_test.go`) | `AllowUnsandboxedExec` field, validation, fail-closed `Open` wiring, startup log line |
| `internal/harness/architecture/dependencies_test.go` | Confirm no new adapter-to-adapter import; `localexec` stays the sole owner of its own build-tagged files |
| `SECURITY.md` | "Enforced"/"Not enforced" sections updated to match what is actually true per platform |
| `docs/architecture/tool-runtime.md`, `.zh-CN.md` | Replace the fixed `Enforcement == "partial"` paragraph with the structured, per-platform description |
| `docs/architecture/exec-sandboxing-resource-quotas-evidence.md` | New evidence ledger (stem gate requires `<contract>-evidence.md`; `tool-runtime-evidence.md` stays a frozen Slice-5-dated snapshot per this repository's established per-slice-ledger convention — see the ACP session lifecycle ledger for precedent) |
| `docs/README.md`, `README.md` | Authority-table rows, milestone/summary prose |

---

### Task 1: Structured enforcement reporting and result vocabulary

**Files:**

- Modify: `internal/harness/tools/ports.go`
- Modify: `internal/harness/application/errors.go`
- Add: `internal/harness/adapters/localexec/enforcement.go`
- Modify: `internal/harness/adapters/localexec/{runner.go,doc.go}`
- Modify: `internal/harness/adapters/localexec/runner_test.go` (replace `TestEnforcementPartial`)
- Modify: `docs/architecture/tool-runtime.md`, `.zh-CN.md` (the one paragraph naming the old constant)

- [ ] Add `ResourceLimited bool` to `tools.CommandResult`, documented as
  mutually exclusive with `TimedOut` (a run is killed for exactly one
  reason).
- [ ] Add `CodeResourceLimit = "resource_limit"` and
  `ToolTextResourceLimit = "command exceeded a resource limit"` to
  `application/errors.go`, next to the existing `CodeExecTimeout` /
  `ToolTextExecTimeout` pair, same category treatment (a tool result, not
  a `RunTurn` error).
- [ ] Replace the package-level `const Enforcement = "partial"` with a
  `type Enforcement struct { Filesystem, Network, Memory string }` (values
  `"full"`, `"partial"`, `"none"`) and a `Runner` method or field
  reporting it, computed at construction from what is actually active —
  which, before Task 2/3/4 land, is `{"none", "none", "none"}` on every
  platform. This is a more honest baseline than the old fixed
  `"partial"`, not a regression: nothing enforced today stops being
  enforced.
- [ ] Replace `TestEnforcementPartial` with a test asserting the baseline
  all-`"none"` value, and update the one paragraph in
  `docs/architecture/tool-runtime.md` (+zh-CN) that names the old
  constant to describe the new type without yet describing platform
  mechanisms that don't exist until later tasks.
- [ ] Run:

```bash
go test ./internal/harness/adapters/localexec/... ./internal/harness/tools/... ./internal/harness/application/... -run 'Enforcement|CommandResult' -count=1
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(localexec): structured per-effect enforcement reporting`.

### Task 2: Linux — bubblewrap availability probe and per-call confinement

**Files:**

- Add: `internal/harness/adapters/localexec/bwrap_linux.go`
- Add: `internal/harness/adapters/localexec/bwrap_linux_test.go`
- Add: `internal/harness/adapters/localexec/bwrap_other.go` (`//go:build !linux`, stub returning unavailable)
- Modify: `internal/harness/adapters/localexec/runner.go`

- [ ] Add a probe that resolves `bwrap` on `PATH` and runs one scoped
  invocation (`bwrap --unshare-user --unshare-pid --ro-bind / / --proc
  /proc true`, matching the capability-probe shape verified in the
  architecture gate against Maka and DeepSeek Harness, written fresh for
  this codebase's own argv builder rather than copied). Cache the result
  for the `Runner`'s lifetime.
- [ ] Detect WSL1 distinctly from "bwrap missing" by reading
  `/proc/version` for `Microsoft` without `WSL2`, matching the
  distinction the architecture gate found in Codex's own
  `Wsl1UnsupportedForBubblewrap`. Both are "unavailable" for now (Task 5
  decides what unavailable means for admission); they must be
  distinguishable in a diagnostic, not collapsed into one message.
- [ ] When available, wrap each `Run` call's existing argv in `bwrap
  --unshare-user --unshare-pid --unshare-ipc --unshare-uts
  --unshare-cgroup --unshare-net --die-with-parent --new-session
  --cap-drop ALL --ro-bind / / --dev /dev --proc /proc --tmpfs /tmp
  --bind <workspace-root> <workspace-root> --chdir <cwd>` ahead of the
  target argv, per design §3.2. The existing scrubbed environment,
  timeout/cancel process-group kill, and output cap are unchanged — bwrap
  wraps the same argv those existing tests already exercise
  (`TestScrubbedEnv`, `TestTimeoutKillsProcessGroup`,
  `TestCancelKillsProcessGroup`, `TestOutputCapKillsAndTruncates` must
  all still pass unmodified).
- [ ] When unavailable, `Run` behaves exactly as it does today (this task
  does not gate anything yet — see Global Constraints).
- [ ] Report `Enforcement.Filesystem = "full"` and `Enforcement.Network =
  "full"` when bwrap is active (network is fully denied by
  `--unshare-net` in this slice, per design §2's exclusion of a
  proxy-routed exception), `"none"` otherwise.
- [ ] Add unit tests for argv construction (no real `bwrap` needed) and an
  integration suite gated on a real `bwrap` (`t.Skip` when absent):
  writing outside the workspace root is denied by the OS, a network
  connection attempt fails, and the confined process cannot see host
  processes outside its PID namespace.
- [ ] Run:

```bash
go test ./internal/harness/adapters/localexec/... -run 'Bwrap|Enforcement' -count=1 -v
CGO_ENABLED=0 go build ./...
GOOS=darwin GOARCH=arm64 go build ./...
GOOS=windows GOARCH=amd64 go build ./...
```

- [ ] Commit: `feat(localexec): bubblewrap confinement on Linux`.

### Task 3: Linux — cgroup v2 memory quota

**Files:**

- Add: `internal/harness/adapters/localexec/cgroup_linux.go`
- Add: `internal/harness/adapters/localexec/cgroup_linux_test.go`
- Modify: `internal/harness/adapters/localexec/{runner.go,bwrap_linux.go}`

- [ ] Detect cgroup v2 (unified hierarchy) the same way as the architecture
  gate's Grok Build reading: `/proc/self/cgroup`'s `0::` line resolves
  under `/sys/fs/cgroup`. Detection happens once, at `Runner`
  construction, alongside the bwrap probe.
- [ ] When available, create one child cgroup for the `Runner`'s lifetime
  with configurable `memory.high` (soft) and `memory.max = memory.high +
  headroom` (hard), documented defaults per design §3.3.
- [ ] Before each bwrap child starts (child exists, has not yet exec'd the
  target command), write its PID to the cgroup's `cgroup.procs`. Empty
  the cgroup after the command exits so it is a reusable per-invocation
  boundary, not a whole-`Runner` one.
- [ ] Run one monitor goroutine per `Runner` (not per call) watching
  `memory.events` via `inotify` (`golang.org/x/sys/unix`, no CGO). When
  the kernel's `high` counter increments and current usage is still above
  a configurable fraction of `memory.high` (default 90%, per Grok Build),
  kill the process group with the existing `killProcessGroup` path and
  set `CommandResult.ResourceLimited = true` instead of `TimedOut`.
- [ ] When cgroup v2 is unavailable, skip the quota with a one-line
  startup warning; this does **not** fail construction — bwrap
  confinement from Task 2 is the fail-closed-required guarantee, memory
  quota is additive best-effort, matching Grok Build's own `Option`
  treatment of every cgroup field.
- [ ] Report `Enforcement.Memory = "full"` when the cgroup quota is
  active, `"none"` otherwise.
- [ ] Add a cgroup-v2 detection unit test, and an integration test
  (`t.Skip` off Linux or off cgroup v2) that spawns a deliberately
  memory-growing test binary and asserts `ResourceLimited` becomes true
  within a bounded time with the process group fully reaped.
- [ ] Run:

```bash
go test ./internal/harness/adapters/localexec/... -run 'Cgroup|Memory|ResourceLimited' -count=1 -v
go test -race ./internal/harness/adapters/localexec/... -count=1
```

- [ ] Commit: `feat(localexec): cgroup v2 memory quota on Linux`.

### Task 4: macOS — Seatbelt confinement and RLIMIT_AS

**Files:**

- Add: `internal/harness/adapters/localexec/sandbox_darwin.go`
- Add: `internal/harness/adapters/localexec/sandbox_darwin_test.go`
- Add: `internal/harness/adapters/localexec/sandbox_other.go` (`//go:build !darwin`, stub returning unavailable)
- Modify: `internal/harness/adapters/localexec/runner.go`

- [ ] Add a probe for `sandbox-exec` on `PATH`, cached at `Runner`
  construction, mirroring Task 2's bwrap probe shape.
- [ ] Compose a `.sbpl` profile as a Go string per call: deny-by-default
  writes (`(deny file-write*)`), an explicit allow for the canonicalized
  workspace root (resolved via the same `filepath.EvalSymlinks` path
  `New` already uses, since Seatbelt matches resolved paths per the
  architecture gate), broad reads, and `(deny network*)` (no exception in
  this slice, matching Task 2's Linux network decision). Invoke via
  `sandbox-exec -p <profile> <argv...>`.
- [ ] Apply `setrlimit(RLIMIT_AS, ...)` (`golang.org/x/sys/unix`, no CGO)
  before exec as a best-effort address-space bound; document plainly
  (code comment + doc update in Task 6) that this is weaker than Linux's
  monitored cgroup ceiling and fails via the child's own allocator
  (`ENOMEM`), not a clean external kill — it does not set
  `ResourceLimited`.
- [ ] Report `Enforcement.Filesystem = "full"`, `Enforcement.Network =
  "full"` when Seatbelt is active; `Enforcement.Memory = "partial"` when
  `RLIMIT_AS` is set (always, on Darwin, once this task lands), `"none"`
  otherwise.
- [ ] Add unit tests for profile string construction and an integration
  suite gated on `runtime.GOOS == "darwin"` and `sandbox-exec` present
  (`t.Skip` otherwise): a write outside the workspace is denied, a
  network connection attempt fails.
- [ ] Run:

```bash
GOOS=darwin GOARCH=arm64 go build ./...
go test ./internal/harness/adapters/localexec/... -run 'Sandbox|Seatbelt|Enforcement' -count=1 -v
```

(The integration cases self-skip off macOS; this task's non-Darwin
verification is the cross-build plus the unit tests.)

- [ ] Commit: `feat(localexec): Seatbelt confinement and RLIMIT_AS on macOS`.

### Task 5: Fail-closed composition gate and the escape hatch

**Files:**

- Add: `internal/harness/adapters/localexec/availability.go`
- Modify: `internal/harness/composition/{config,assembly}.go`
- Modify: `internal/harness/composition/{config,assembly}_test.go`
- Modify: `internal/harness/adapters/localexec/{bwrap_other.go,sandbox_other.go}` (Windows/unsupported: always report unavailable)

- [ ] Add one cross-platform `Availability` seam in `localexec` that
  reports, per platform, whether Task 2's or Task 4's backend is usable
  (delegating to whichever probe already exists for `runtime.GOOS`; a
  platform with neither, including Windows, always reports
  unavailable — this is the accepted Windows trade-off from design §5,
  not a bug).
- [ ] Add `AllowUnsandboxedExec bool` to `composition.Config` (default
  `false`). At `composition.Open`, after the existing provider/database
  setup checks (same construction-order discipline `assembly.go` already
  documents), check `Availability`: if unavailable and
  `AllowUnsandboxedExec` is false, `Open` fails closed with a new,
  clearly named config error (mirroring the existing missing-API-key
  error's classification, not a generic one). If unavailable and the
  flag is true, `Open` proceeds and logs one loud, specific warning
  naming exactly which guarantee is absent (design §5: "not a silent
  fallback").
- [ ] Add a dependency-injection seam for the availability check itself so
  a test can force "unavailable" without needing to actually break the
  host's `bwrap`/`sandbox-exec` (parallel to how other composition-time
  checks are already testable without real infrastructure).
- [ ] Tests: `composition.Open` refuses to start when unavailable and the
  flag is unset; proceeds and logs the named warning when the flag is
  set; proceeds normally when available. A real-environment integration
  test (gated, `t.Skip` as appropriate) proves the default path succeeds
  wherever this CI actually has `bwrap` or `sandbox-exec` installed.
- [ ] Run:

```bash
go test ./internal/harness/composition/... -run 'Availability|Sandbox|Open' -count=1 -v
go test -race ./internal/harness/composition/... -count=1
```

- [ ] Commit: `feat(composition): fail closed on unavailable exec sandbox`.

### Task 6: Publish implemented-contract documentation and evidence

**Files:**

- Modify: `SECURITY.md`
- Modify: `docs/architecture/tool-runtime.md`, `.zh-CN.md`
- Add: `docs/architecture/exec-sandboxing-resource-quotas-evidence.md`
- Modify: `docs/README.md`, `README.md`

- [ ] Update `SECURITY.md`'s "Enforced" section to add: OS-level exec
  confinement (bwrap on Linux, Seatbelt on macOS), the Linux memory
  quota, and fail-closed startup behavior with its named escape hatch.
  Update "Not enforced" to remove filesystem/network confinement claims
  that are no longer true on Linux/macOS, while keeping Windows, CPU
  quota, disk-IO quota, and multi-tenant isolation listed accurately as
  still not enforced.
- [ ] Replace `docs/architecture/tool-runtime.md`'s (+zh-CN) `localexec`
  paragraph with the structured `Enforcement` description and the
  per-platform mechanism summary, citing the exact test names Tasks 1–5
  added (matching this doc's existing style of naming tests inline).
- [ ] Add `docs/architecture/exec-sandboxing-resource-quotas-evidence.md`:
  commit table for Tasks 1–6, mapping-table tests per platform,
  verification commands and their actual output (including the explicit
  `CGO_ENABLED=0 go build ./...` result), and a "Remaining" section
  naming Windows, CPU/disk quotas, Landlock, and containers/VMs as
  excluded by design, not deferred with an unstated reason. Do not fold
  this into `tool-runtime-evidence.md`: that ledger is dated to the
  original Tool Runtime slice, and this repository's established
  convention (see the ACP session lifecycle evidence ledger) is a new,
  separately dated ledger for later work touching an existing contract,
  cross-linked from it.
- [ ] Add authority-table rows for the new evidence ledger to
  `docs/README.md`; update the `tool-runtime.md` implemented-contract
  row's evidence reference if `docsguard`'s stem-ledger check requires
  it. Update `README.md`'s top-level Tool Runtime summary line and
  `docs/README.md`'s milestone 5 prose to mention real confinement now
  exists, matching how milestone 6's prose was updated after ACP
  session lifecycle landed.
- [ ] Run:

```bash
go test ./internal/docsguard/... -v
git diff --check
```

- [ ] Commit: `docs: publish exec sandboxing and resource quota contracts`.

## Final Completion Gate

- [ ] Run `gofmt -w` on changed Go files and verify `gofmt -l` prints nothing for them.
- [ ] Run `go vet ./...`.
- [ ] Run `CGO_ENABLED=0 go build ./...` — this is the plan's central claim, not an assumption; it must be checked explicitly, not inferred from other builds passing.
- [ ] Run `go test ./... -count=1`.
- [ ] Run `go test -race ./... -count=1`.
- [ ] Run `go test -race ./internal/harness/adapters/localexec -count=5` to exercise the cgroup monitor and bwrap/Seatbelt spawn paths repeatedly.
- [ ] Run `GOOS=windows GOARCH=amd64 go build ./...`.
- [ ] Run `GOOS=darwin GOARCH=arm64 go build ./...`.
- [ ] Run `git diff --check` and inspect `git status --short` so only this plan's artifacts are included.
- [ ] Confirm `och` still runs on a host with neither `bwrap` nor `sandbox-exec` only when `AllowUnsandboxedExec` is explicitly set, and that doing so is logged, not silent.
- [ ] Confirm no currently-`Enforced` `SECURITY.md` guarantee regressed: re-run `TestScrubbedEnv`, `TestArgvOnlyNoShellExpansion`, `TestTimeoutKillsProcessGroup`, `TestCancelKillsProcessGroup`, `TestOutputCapKillsAndTruncates`, `TestCwdAndArgvMustStayInWorkspace` explicitly and confirm they are unmodified in intent.
- [ ] Confirm `internal/harness/architecture` still reports no new adapter-to-adapter import.
- [ ] Request code review, address findings with focused regression tests, then create a final implementation/evidence commit if review changes are needed.
