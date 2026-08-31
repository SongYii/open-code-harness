# Exec CPU Quota — Design

- **Date:** 2026-08-31
- **Status:** Accepted 2026-08-31.
- **Stability:** extends the existing `experimental`
  `internal/harness/adapters/localexec` contract; no change to any other
  implemented contract's shape.
- **Repository:** `open-code-harness` (`github.com/SongYii/open-code-harness`)
- **Normative language:** English
- **Chinese summary:** [Exec CPU 配额设计（中文摘要）](2026-08-31-exec-cpu-quota-design.zh-CN.md)
- **Authority:** [Exec CPU and disk quotas architecture gate](../../research/architecture-gates/2026-08-31-exec-cpu-disk-quotas.md) (including its 2026-08-31 correction note)
- **Implemented contracts this slice must not change:** [Tool runtime](../../architecture/tool-runtime.md) (no new tool, no new Policy rule, no new Risk class), the existing memory-quota mechanism in `localexec` (unmodified, extended alongside)

English is normative. The Chinese file is a synchronized summary, not a
field-for-field translation.

---

## 1. Decision summary

The gate found no reference project enforces a CPU quota on a spawned
command, and initially concluded macOS had no viable externally-enforced
mechanism at all — a conclusion the gate's own 2026-08-31 correction note
retracted after finding this project's *own* existing code
(`sandbox_darwin.go`'s `beginRlimitBracket`) already solves the general
class of problem (bracket the parent's own rlimit around `Start()`, since
Go's `os/exec` has no pre-exec hook for an arbitrary child). This design
adds CPU quota enforcement to `internal/harness/adapters/localexec` on
both platforms it currently sandboxes at all, using two genuinely
different kernel mechanisms unified by one policy intent.

Six decisions:

1. **CPU quota only in this slice. Disk-IO quota (`io.max`) and
   file-descriptor limits (`RLIMIT_NOFILE`) are both explicitly deferred**,
   for different, stated reasons (§2), not bundled in to "finish"
   `SECURITY.md`'s sentence in one slice.
2. **One policy intent, two mechanisms.** Both platforms bound a spawned
   command to roughly one CPU core's worth of aggregate work — Linux via
   a bandwidth fraction (`cpu.max`, a rate), macOS via a cumulative
   CPU-seconds ceiling (`RLIMIT_CPU`) set equal to this project's own
   existing `DefaultExecTimeout` (30s). The units differ because the
   kernel primitives differ; the intent — a command using more than one
   core's worth of CPU, whether by spinning one core continuously past
   its wall-clock budget or by fanning out across several — is throttled
   or terminated well before it can monopolize the host running this
   harness's own process and other concurrent tool calls.
3. **Linux extends the existing cgroup with one more controller write; no
   new monitor.** `cpu.max` is a kernel-enforced throttle with no kill
   and no breach-notification file to watch, unlike `memory.max`. A new,
   purely diagnostic `CommandResult.Throttled` field (never a kill
   reason) is populated from `cpu.stat`'s `nr_throttled` after the run,
   regardless of how it ended.
4. **macOS extends the existing rlimit-bracket technique with
   `RLIMIT_CPU`, set to fire before the hard limit via a soft/hard split
   mirroring memory's own `memory.high`/`memory.max` shape** — a
   deliberate 1-second gap (soft 30s, hard 31s) whose purpose is
   disambiguation, not grace: a process terminated by `SIGXCPU`
   specifically (the soft-limit signal) is attributable to this quota
   with high confidence; a bare `SIGKILL` with no prior `SIGXCPU`
   observed is not automatically assumed to be this quota's doing.
5. **`Enforcement` gains a `CPU` field, reported per platform like every
   other effect** — `EnforcementFull` on both Linux (kernel-throttled,
   precise) and macOS (kernel-killed, precise), a stronger rating than
   memory's own macOS `EnforcementPartial` (which is imprecise because
   `RLIMIT_AS` bounds address space, not resident memory — `RLIMIT_CPU`
   has no equivalent imprecision in what it bounds, only in this
   project's own after-the-fact *attribution* of a kill to it, disclosed
   separately in §5, not folded into the enforcement level).
6. **Windows is unaffected.** It already fails closed for sandboxing
   entirely (prior slice); this design adds nothing there and does not
   revisit that decision.

## 2. Goals and non-goals

### Goals

- Close the "no CPU quota" portion of `SECURITY.md`'s "No CPU or disk-IO
  quota, on any platform" sentence, on both platforms this project
  currently sandboxes at all (Linux, macOS).
- Prevent a single `exec` command from monopolizing the host running this
  harness's own process — starving the harness itself or other
  concurrent tool calls — under this project's own stated threat model
  (the model is untrusted input; a prompt-injected command directing
  `exec` to spin up parallel work or a fork bomb is in scope).
- Extend already-shipped, evidence-backed infrastructure
  (`cgroup_linux.go`'s reusable per-invocation cgroup, `sandbox_darwin.go`'s
  rlimit-bracket technique) rather than building a second, parallel
  mechanism.

### Non-goals (excluded from this slice, not deferred without a reason)

- **Disk-IO quota (`io.max`).** The gate found this throttles *rate*,
  not *total disk space consumed* — it does not address the
  "workspace filled to capacity" concern `SECURITY.md`'s "disk" wording
  most naturally suggests, and its own device major:minor resolution
  problem (which block device does the workspace root, or a bind-mounted
  temp dir, actually live on?) has no established pattern this gate
  found anywhere in six reference projects. A disk-space bound (total
  bytes written, or a filesystem-level quota) is a materially different,
  harder problem than `io.max` solves, and is not designed here either.
- **File-descriptor limits (`RLIMIT_NOFILE`).** Faces the identical
  architectural constraint CPU quota does on macOS (Go's `os/exec` has no
  pre-exec hook for an arbitrary child) but has no cgroup v2 controller
  equivalent to fall back on for Linux the way CPU does — `pids.max`
  bounds process *count*, not open file descriptors. A real design for
  this needs its own mechanism search, not an assumed extension of this
  one.
- **Windows.** Already excluded from sandboxing generally (prior slice,
  reaffirmed, not revisited).
- **A configurable CPU-quota knob.** Matching this project's own existing
  precedent (`DefaultMemoryHighBytes`/`DefaultMemoryHeadroomBytes` are
  package-level constants, not `composition.Config` fields), the CPU
  bound is a fixed default for this slice. Making it configurable is a
  smaller, separable, later change if a real deployment ever needs it.

## 3. Linux: extend the existing cgroup with `cpu.max`

`newCgroupQuota` (or its direct successor) additionally writes `cpu.max`
into the same child cgroup `memory.high`/`memory.max` already use,
immediately after the memory writes, using the same
`cgroup.subtree_control` delegation this project's code already performs
(`+memory` becomes `+memory +cpu`):

```
DefaultCPUPeriodMicros = 100000  // 100ms, cgroup v2's own stated default
DefaultCPUQuotaMicros  = 100000  // 100ms per 100ms period = one full core
```

Written as `"100000 100000"` to `cpu.max`. A single-threaded command is
never throttled by this bound (it cannot exceed one core on its own); a
command that fans out across multiple cores — a parallel build, a fork
bomb, a deliberately-spawned CPU-bound flood — is capped to roughly one
core's aggregate worth of scheduled time, exactly the "monopolize the
host" risk this design targets.

**Independent failure, not all-or-nothing.** If the `cpu` controller
write fails (not delegated, cgroup v1 host, etc.) after `memory`'s writes
already succeeded, the cgroup is not torn down and the memory quota stays
active — `enforcement.CPU` is reported `EnforcementNone` independently of
`enforcement.Memory`, matching this project's own stated principle that a
`Runner` "must not report full for an effect it does not actually
confine," applied per-controller rather than per-cgroup.

**No monitor, no kill, one new diagnostic field.** Per the gate's own
primary-source reading of the kernel's cgroup v2 documentation, `cpu.max`
throttles transparently; there is nothing analogous to `memory.events`'
`high` counter to watch, and nothing to kill for CPU alone. After
`cmd.Wait()` returns (any exit path — normal, timed out, output-overflow,
or memory-resource-limited), the runner reads `cpu.stat`'s `nr_throttled`
from the same cgroup one final time before it is torn down, and sets a
new `tools.CommandResult.Throttled = nr_throttled > 0`. This is
deliberately additive, not a new kill-reason category:
`ResourceLimited`/`TimedOut` remain mutually exclusive as
`tools/ports.go`'s own existing doc comment states; `Throttled` can be
`true` alongside either of them, or alongside neither (a command that was
briefly throttled but still finished well within its timeout) — it
answers a different question ("did this command experience CPU
contention during its run") than the existing pair answers ("why, if at
all, was this command killed").

## 4. macOS: extend the existing rlimit-bracket technique with `RLIMIT_CPU`

`beginRlimitBracket`'s existing shape — lock a mutex, lower this
process's own limit, return a closure that restores it — is reused for a
second rlimit, `RLIMIT_CPU`, bracketed around the same `cmd.Start()` call
already bracketing `RLIMIT_AS`:

```
DefaultCPUSoftSeconds = 30  // matches application.DefaultExecTimeout exactly
DefaultCPUHardSeconds = 31  // a 1-second gap for signal disambiguation, not grace
```

Unlike `RLIMIT_AS` (where this project sets `Cur` only, since a single
breach behavior — `ENOMEM` — is all that's observable), `RLIMIT_CPU`'s
two-stage design is used deliberately: the kernel sends `SIGXCPU` when
the **soft** limit (`Cur`) is reached, then `SIGKILL` if the process is
still running when the **hard** limit (`Max`) is reached. Setting them
equal would make both signals fire back-to-back with no observable gap;
keeping a 1-second gap between them means a process terminated by
`SIGXCPU` specifically is attributable to this quota with high
confidence, since nothing else in this project's own design sends that
signal.

**Detecting the kill: a new code path, since today's `Run` never
inspects an exit signal.** `Runner.Run`'s existing `select` races several
outcomes (`done`, `ctx.Done()`, a timer, output overflow, the memory
cgroup's own notification channel); none of its branches inspect *why*
`cmd.Wait()` returned when the `done` case fires first — `finish` only
extracts `ExitCode()`. This design adds: when the `done` case fires
first (no other branch pre-empted it), inspect the returned
`*exec.ExitError`'s underlying `syscall.WaitStatus` for `Signaled()` with
`Signal() == syscall.SIGXCPU`, or `Signal() == syscall.SIGKILL` with no
other explanation available; either sets `ResourceLimited = true`, the
same field the Linux memory path already sets, giving both platforms one
consistent field for "this command was killed for exceeding a resource
bound" regardless of which specific resource or mechanism was responsible.

**Enforcement level and its honest caveat.** `enforcement.CPU =
EnforcementFull` on macOS: the kernel genuinely cannot let a process
exceed `RLIMIT_CPU`'s hard limit, unconditionally, independent of
Seatbelt's own availability — the same unconditional-application property
`RLIMIT_AS` already has for memory. The caveat is narrower and disclosed
separately (§6, not folded into the enforcement rating): attributing a
termination to `ResourceLimited` specifically relies on signal
inspection that could, in a genuinely rare case (a process ignores
`SIGXCPU` and something else independently sends it `SIGKILL` in the same
narrow window), misattribute an unrelated kill to this quota. The
*bound itself* is not in question — only which label an edge-case exit
gets.

## 5. `Enforcement` and `SECURITY.md`

```go
type Enforcement struct {
    Filesystem EnforcementLevel
    Network    EnforcementLevel
    Memory     EnforcementLevel
    CPU        EnforcementLevel // new
}
```

`SECURITY.md`'s "Not enforced" sentence — "No CPU or disk-IO quota, on
any platform" — is split precisely at implementation time: the CPU half
moves to "Enforced," stating exactly what it bounds (roughly one core's
aggregate worth of work, on Linux and macOS) and the disambiguation
caveat from §4; the disk-IO half stays in "Not enforced," restated to
clarify (per this gate's own finding) that even the deferred mechanism
would only throttle rate, not bound total disk space consumed — so a
future reader does not assume "disk-IO quota" was ever going to solve
disk-space exhaustion once implemented.

## 6. Verification and acceptance

- Linux unit tests (extending `cgroup_linux_test.go`'s existing style):
  `cpu.max` is written with the stated values when cgroup v2 is
  available; a CPU-bound command spawning more parallel work than one
  core can serve is measurably slower under this quota than without it
  (a real, timed integration test, not a mocked one — matching this
  project's own precedent of gating such tests on real host capability
  rather than asserting behavior no test actually observed); `cpu.stat`'s
  `nr_throttled` is read and reflected in `CommandResult.Throttled`; a
  simulated `cpu` controller write failure leaves `memory` quota active
  and reports `enforcement.CPU == EnforcementNone` independently.
- macOS unit tests (extending `sandbox_darwin_test.go`'s existing style):
  the rlimit bracket sets and restores `RLIMIT_CPU` around `Start()`
  exactly as it already does for `RLIMIT_AS`; a CPU-bound command that
  exceeds the soft limit is terminated and reported
  `ResourceLimited: true`; the bracket's mutex serializes concurrent
  `Run` calls' rlimit changes exactly as the existing `RLIMIT_AS` bracket
  already does (shared code path, same guarantee, verified again for the
  new limit specifically since it is a materially different failure mode
  — a kill, not an allocator `ENOMEM` — from what the existing test
  suite proves for `RLIMIT_AS`).
- Mutation checks on both platforms: disabling each new write/bracket
  independently must make its own dedicated test fail for the right
  reason, matching this project's own established rigor for every prior
  security-relevant slice.
- `go vet`, `gofmt`, `CGO_ENABLED=0 go build ./...`, `go test -race
  ./... -count=1`, and confirmation that `Runner.Enforcement()` reports
  `CPU` accurately against whatever this test host's own cgroup v2/rlimit
  support actually provides — the same "fact, not a promise" discipline
  every other effect in this struct already follows.

## 7. Risks

| Risk | Mitigation |
| --- | --- |
| A legitimate, CPU-intensive but single-threaded tool call (a large test suite, a slow compile) is throttled or killed unfairly. | The 1-core-equivalent bound (Linux) and the 30s-matching-the-existing-timeout bound (macOS) are both chosen so a single-threaded command is never the one that trips them — only aggregate, multi-core usage is bounded, which single-threaded work cannot produce regardless of how long it runs (bounded separately, and already, by the existing wall-clock timeout). |
| macOS's signal-based attribution misattributes an unrelated `SIGKILL` to `ResourceLimited`. | Disclosed explicitly (§4, §6) as a narrow, rare edge case in *labeling*, not in the underlying bound's correctness; the soft/hard gap specifically exists to make the common case (this quota's own kill) unambiguous via `SIGXCPU`. |
| The `cpu` controller is unavailable on a host where `memory` is (an asymmetric delegation failure this design did not previously need to handle). | §3's independent-failure design: `enforcement.CPU` reports `EnforcementNone` on its own, the memory quota is unaffected, and `composition.Open`'s existing fail-closed gate (which gates on OS-level sandbox availability, not on any individual cgroup controller) is unchanged — CPU quota unavailability was never a startup-gating condition and does not become one now. |
| Scope creep toward disk-IO or file-descriptor limits during implementation, since they are adjacent and named in the same `SECURITY.md` sentence. | §2's non-goals are explicit, each with its own stated reason; an implementation plan (next artifact) fixes the task boundary the same way every other plan in this project's history has. |

## 8. How this design answers the gate's open questions

Cross-referencing `2026-08-31-exec-cpu-disk-quotas.md`'s "Open questions
a design must resolve" directly:

1. *A new `CommandResult` field/category for throttled-but-not-killed, or
   accept indirect surfacing* → §3: a new, purely additive `Throttled`
   field, not a new kill-reason category.
2. *What "disk quota" should mean* → §2: out of scope this slice entirely,
   with the reason (`io.max` doesn't address disk-space exhaustion, the
   thing the phrase most naturally suggests) restated in `SECURITY.md`
   itself (§5) so a future reader is not misled.
3. *Resolving the workspace's block device major:minor* → moot for this
   slice, since `io.max` is deferred (§2).
4. *Whether CPU/IO quotas are in scope for macOS at all* → §1, §4: yes for
   CPU, via the gate's own corrected finding that the existing
   rlimit-bracket technique transfers directly from `RLIMIT_AS` to
   `RLIMIT_CPU`.
5. *CPU time vs. bandwidth-fraction policy shape* → §1.2: both, unified by
   one intent (roughly one core's worth of aggregate work) expressed in
   whichever unit each platform's own kernel primitive actually uses,
   rather than picking one shape and approximating it on the other
   platform.
6. *File-descriptor limits: same design cycle or separate* → §2: separate,
   explicitly, since it has no cgroup v2 controller to extend the way CPU
   does and needs its own mechanism search.
