# Exec CPU Quota Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend `internal/harness/adapters/localexec`'s existing
resource-quota mechanisms with a CPU bound on both platforms it already
sandboxes — Linux (`cpu.max`, extending the existing cgroup) and macOS
(`RLIMIT_CPU`, extending the existing rlimit-bracket technique) — closing
the "no CPU quota" portion of `SECURITY.md`'s "No CPU or disk-IO quota,
on any platform" sentence.

**Architecture:** Linux: one more write (`cpu.max`) into the same
reusable per-invocation cgroup `memory.high`/`memory.max` already use, no
new monitor (a throttle, not a kill); a new, purely diagnostic
`tools.CommandResult.Throttled` field read from `cpu.stat` after each
run. macOS: a second rlimit (`RLIMIT_CPU`) bracketed around the same
`cmd.Start()` call already bracketing `RLIMIT_AS`, with a soft/hard gap
whose purpose is signal-based disambiguation; a new code path in
`Runner.Run`'s `select` inspects the exit signal when nothing else
pre-empted normal completion, setting the existing `ResourceLimited`
field on a `SIGXCPU`/unexplained-`SIGKILL` termination. `Enforcement`
gains a `CPU` field, reported independently per platform and,
on Linux, independently of `Memory`.

**Tech Stack:** Go 1.26, `golang.org/x/sys/unix` (already a direct
dependency), standard library `os/exec`/`syscall`. No new dependency.

**Spec:** `docs/superpowers/specs/2026-08-31-exec-cpu-quota-design.md`
(English normative, Accepted); synchronized Chinese summary at
`docs/superpowers/specs/2026-08-31-exec-cpu-quota-design.zh-CN.md`.
Research: `docs/research/architecture-gates/2026-08-31-exec-cpu-disk-quotas.md`
(including its 2026-08-31 correction note — read that note before Task 2;
it is the reason Task 2 exists at all). No Chinese reading copy for this
plan, matching the three most recent prior plans' precedent.

## Global Constraints

- `Throttled` is never a kill-reason category: `ResourceLimited` and
  `TimedOut` stay mutually exclusive exactly as `tools/ports.go`'s
  existing doc comment states. `Throttled` can be `true` alongside either
  of them, or alongside neither.
- The `cpu` cgroup controller (Linux) and the `RLIMIT_CPU` bracket
  (macOS) must each fail independently: a failure in either must never
  disable or tear down the existing, already-shipped memory quota on
  that platform, and `enforcement.CPU` must report its own true state
  independently of `enforcement.Memory`.
- No sleep-based assertions for timing-sensitive tests. Where a real
  timed integration test is unavoidable (a CPU-bound command actually
  being measurably slower under quota than without it), bound the
  assertion by a generous ratio, not an exact duration, and gate it on
  the same real-host-capability-detection pattern
  `TestCgroupMemoryQuotaKillsAMemoryGrowingCommand` and
  `TestSeatbeltConfinementDeniesNetwork` already use (skip cleanly, with
  a stated reason, on a host lacking the capability — never assert
  behavior no test actually observed).
- Every task follows red-green-refactor and adds a mutation check:
  disable the new write/bracket, confirm the task's own new test fails
  for the right reason, then restore.
- `CGO_ENABLED=0 go build ./...` and `go vet ./...` stay clean after
  every task.

## File Map

| Path | Responsibility |
| --- | --- |
| `internal/harness/adapters/localexec/cgroup_linux.go` | Add `cpu.max` write to cgroup construction; add `cpu.stat` reading for `nr_throttled` |
| `internal/harness/adapters/localexec/cgroup_other.go` | Confirm the non-Linux stub's nil-safe methods cover any new method signature added to `cgroupQuota` |
| `internal/harness/adapters/localexec/sandbox_darwin.go` | Add a second rlimit bracket for `RLIMIT_CPU` (soft/hard), alongside the existing `RLIMIT_AS` one |
| `internal/harness/adapters/localexec/sandbox_other.go` | Confirm the non-Darwin stub covers any new function signature |
| `internal/harness/adapters/localexec/runner.go` | Wire both platforms' CPU quota into `New`/`Run`; new signal-inspection code path in `Run`'s `select`; `Enforcement.CPU` field and its construction-time computation |
| `internal/harness/adapters/localexec/enforcement.go` | Add the `CPU EnforcementLevel` field |
| `internal/harness/tools/ports.go` | Add `CommandResult.Throttled bool` |
| `internal/harness/adapters/localexec/cgroup_linux_test.go`, `sandbox_darwin_test.go`, `runner_test.go` | New tests per task |
| `SECURITY.md` | Split the CPU half out of "No CPU or disk-IO quota" into "Enforced," restate the disk-IO half precisely |
| `docs/architecture/exec-sandboxing-resource-quotas.md`, `.zh-CN.md`, `-evidence.md` | Amend the existing implemented contract and evidence ledger — this extends that contract, it does not need a new standalone one |
| `docs/README.md` | Authority-table row update if the existing exec-sandboxing rows' descriptions need amending to mention CPU |

---

### Task 1: Linux `cpu.max` and the `Throttled` diagnostic

**Files:**

- Modify: `internal/harness/adapters/localexec/cgroup_linux.go`
- Modify: `internal/harness/adapters/localexec/cgroup_linux_test.go`
- Modify: `internal/harness/adapters/localexec/enforcement.go`
- Modify: `internal/harness/tools/ports.go`
- Modify: `internal/harness/adapters/localexec/runner.go`
- Modify: `internal/harness/adapters/localexec/runner_test.go`

- [ ] Add `tools.CommandResult.Throttled bool` (`ports.go`), with a doc
  comment distinguishing it from `TimedOut`/`ResourceLimited`: additive,
  never a kill reason, can be `true` alongside either or neither.
- [ ] Add `Enforcement.CPU EnforcementLevel` (`enforcement.go`).
- [ ] `cgroup_linux.go`: define `DefaultCPUPeriodMicros = 100000`,
  `DefaultCPUQuotaMicros = 100000` (one full core). Extend cgroup
  construction (`newCgroupQuota` or a sibling call at the same
  construction site) to additionally write `cpu.max` as
  `"100000 100000"` into the same child cgroup, after the existing
  `memory.high`/`memory.max` writes, adding `+cpu` to the
  `cgroup.subtree_control` write alongside the existing `+memory`.
  **A `cpu.max` write failure must not undo or fail the already-succeeded
  memory writes** — track CPU delegation success independently (a second
  boolean/reason returned alongside the existing memory-quota result, or
  a second field on `cgroupQuota` — whichever keeps `newCgroupQuota`'s
  existing nil-on-total-failure contract for memory unchanged) and wire
  `enforcement.CPU` in `runner.go`'s `New` from that independent result.
- [ ] Add a method reading `cpu.stat` from the same cgroup directory and
  parsing the `nr_throttled` key (mirroring `readHighCounter`'s existing
  parsing style for `memory.events`). Call it once, after `cmd.Wait()`
  returns on *every* exit path in `Run` (normal, timed out, output
  overflow, memory-resource-limited) — not only the happy path — setting
  `CommandResult.Throttled = nr_throttled > 0` before the cgroup is torn
  down for that invocation.
- [ ] Add unit tests: `cpu.max` is written with the stated values when
  cgroup v2 and controller delegation are available (extend
  `TestCgroupQuotaParsesMemoryEventsAndProcs`'s style for a
  `cpu.stat`-parsing equivalent); a command that spawns more parallel
  CPU-bound work than one core can serve is measurably throttled — a
  real, gated integration test following
  `TestCgroupMemoryQuotaKillsAMemoryGrowingCommand`'s own
  capability-detection-and-skip pattern, asserting `Throttled: true` and
  a wall-clock duration meaningfully longer than the same workload
  without the quota (a generous ratio bound, not an exact number); a
  short, single-threaded command reports `Throttled: false`; a simulated
  `cpu` controller delegation failure (mirroring how the existing memory
  tests simulate delegation failure) leaves the memory quota active and
  reports `enforcement.CPU == EnforcementNone` while
  `enforcement.Memory` is unaffected.
- [ ] Mutation check: comment out the `cpu.max` write, confirm the new
  throttling test fails for the right reason (no throttling observed,
  `Throttled` stays `false`), restore.
- [ ] Run:

```bash
go test ./internal/harness/adapters/localexec/... -run 'CPU|Throttl' -count=1 -v
go test -race ./internal/harness/adapters/localexec/... -count=1
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(localexec): Linux CPU quota via cgroup v2 cpu.max`.

### Task 2: macOS `RLIMIT_CPU` and signal-based `ResourceLimited` attribution

**Files:**

- Modify: `internal/harness/adapters/localexec/sandbox_darwin.go`
- Modify: `internal/harness/adapters/localexec/sandbox_darwin_test.go`
- Modify: `internal/harness/adapters/localexec/runner.go`
- Modify: `internal/harness/adapters/localexec/runner_test.go`

- [ ] Read the architecture gate's 2026-08-31 correction note and this
  design's §4 before starting: the technique this task implements is a
  direct extension of `beginRlimitBracket`'s existing `RLIMIT_AS`
  handling, not a new mechanism.
- [ ] `sandbox_darwin.go`: define `DefaultCPUSoftSeconds = 30`
  (matching `application.DefaultExecTimeout` exactly — cite that
  constant directly, do not duplicate the literal `30` independently of
  it, so the two cannot silently drift apart) and
  `DefaultCPUHardSeconds = 31`. Add a second rlimit bracket (either a new
  function alongside `beginRlimitBracket` or an extension of it to
  bracket both `RLIMIT_AS` and `RLIMIT_CPU` under the same mutex hold —
  whichever keeps `Run`'s call site simplest) that sets `RLIMIT_CPU`'s
  `Cur`/`Max` to the soft/hard values around the same `cmd.Start()` call,
  and restores the prior values afterward exactly as the existing bracket
  does for `RLIMIT_AS`.
- [ ] `rlimitEnforcementLevel`'s Darwin implementation reports `CPU:
  EnforcementFull` (unconditional, like the existing `RLIMIT_AS` report
  for `Memory`, but `Full` rather than `Partial` — see design §1.5/§4 for
  why the ratings differ).
- [ ] `runner.go`'s `Run`: in the `case waitErr := <-done:` branch (today
  the only branch that never inspects *why* the process exited), extract
  the underlying `syscall.WaitStatus` from `waitErr`'s `*exec.ExitError`
  when present, and check `Signaled()` with `Signal() ==
  syscall.SIGXCPU`, or `Signal() == syscall.SIGKILL` with no other exit
  explanation available. Either sets `ResourceLimited = true` on the
  returned `CommandResult`. This check must be a no-op (never
  false-triggering) on Linux, where this signal pairing is not how CPU
  quota is enforced at all (Task 1 uses throttling, not a kill) — guard
  it so it only ever activates given a genuinely observed `SIGXCPU`/
  unexplained-`SIGKILL`, not merely "the CPU quota mechanism happens to
  be configured on this platform."
- [ ] Add unit tests (extending `sandbox_darwin_test.go`'s existing
  style): the new bracket sets and restores `RLIMIT_CPU` around
  `Start()`, verified the same way `TestRlimitEnforcementLevelIsPartialOnDarwin`
  and the existing `RLIMIT_AS` bracket tests already verify their own
  mechanism; a CPU-bound command that exceeds the soft limit is
  terminated and the returned `CommandResult.ResourceLimited == true`
  (a real, gated integration test, `//go:build darwin`, following
  `TestSeatbeltConfinementDeniesNetwork`'s own real-subprocess pattern);
  the bracket's shared mutex still serializes concurrent `Run` calls'
  rlimit changes (extend whatever existing test already proves this for
  `RLIMIT_AS`, confirming it still holds with a second rlimit added to
  the same bracket).
- [ ] Mutation check: disable the `RLIMIT_CPU` bracket, confirm the new
  termination test fails for the right reason (command runs to
  completion instead of being killed), restore. Separately, disable the
  new signal-inspection code path (leaving the bracket itself active),
  confirm the same test fails differently (the process is genuinely
  killed by the kernel, but `ResourceLimited` stays `false` because
  nothing attributes it) — proving the two halves (enforcement,
  attribution) are independently load-bearing, matching the design's own
  distinction between them (§4, §6).
- [ ] Run:

```bash
go test ./internal/harness/adapters/localexec/... -run 'CPU|Rlimit' -count=1 -v
go test -race ./internal/harness/adapters/localexec/... -count=1
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(localexec): macOS CPU quota via RLIMIT_CPU`.

### Task 3: Publish the amended contract, evidence, and `SECURITY.md`

**Files:**

- Modify: `docs/architecture/exec-sandboxing-resource-quotas.md`, `.zh-CN.md`
- Modify: `docs/architecture/exec-sandboxing-resource-quotas-evidence.md`
- Modify: `SECURITY.md`
- Modify: `docs/README.md` (if the existing authority-table row's
  description needs amending)

- [ ] Amend `docs/architecture/exec-sandboxing-resource-quotas.md` (and
  its Chinese reading copy) in place: this is an extension of that
  already-implemented contract, not a new standalone one. Add the CPU
  mechanism per platform, the `Throttled` field, the `Enforcement.CPU`
  field, and the signal-attribution caveat on macOS. Cite the exact new
  test names from Tasks 1–2.
- [ ] Append a new, dated section to
  `docs/architecture/exec-sandboxing-resource-quotas-evidence.md`
  (matching this project's own established precedent of appending rather
  than rewriting a frozen ledger's prior content): a commit table for
  this plan's gate/design/plan/Task 1–3, the new mapping-table rows
  (pattern/mechanism → test → mutation result) for CPU on both platforms,
  fresh verification command output, and a "Remaining" note carrying
  forward disk-IO quota and file-descriptor limits as still-excluded, per
  the design's own §2.
- [ ] Update `SECURITY.md`: move the CPU half of "No CPU or disk-IO
  quota, on any platform" into "Enforced," stating precisely what it
  bounds (§5 of the design) and the macOS attribution caveat; restate the
  disk-IO half to clarify (per the gate's own finding) that even a future
  `io.max` implementation would only bound throughput rate, not total
  disk space.
- [ ] Run:

```bash
go test ./internal/docsguard/... -v
git diff --check
```

- [ ] Commit: `docs: publish the exec CPU quota extension and evidence`.

## Final Completion Gate

- [ ] Run `gofmt -w` on changed Go files and verify `gofmt -l` prints
  nothing for them.
- [ ] Run `go vet ./...`.
- [ ] Run `CGO_ENABLED=0 go build ./...`.
- [ ] Run `go test ./... -count=1`.
- [ ] Run `go test -race ./... -count=1`.
- [ ] Confirm both platforms' new mutation checks were actually performed
  and recorded in the evidence ledger, not merely planned.
- [ ] Confirm `Runner.Enforcement()` reports `CPU` accurately against
  whatever this test host's own cgroup v2/rlimit support actually
  provides — the same "fact, not a promise" discipline every other
  effect in this struct already follows.
- [ ] Confirm `SECURITY.md`'s updated "Enforced" bullet states no
  broader claim than Tasks 1–2 actually deliver (in particular: it must
  not imply disk-IO is now bounded, and must state the macOS
  attribution caveat, not merely the Linux mechanism).
- [ ] Request code review, address findings with focused regression
  tests, then create a final implementation/evidence commit if review
  changes are needed.
