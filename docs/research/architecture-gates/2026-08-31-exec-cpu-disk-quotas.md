# Exec CPU and Disk Quotas Architecture Gate

**Status:** Complete research evidence

**Date:** 2026-08-31

**Scope:** [`SECURITY.md`](../../../SECURITY.md)'s "Not enforced" list states
"No CPU or disk-IO quota, on any platform. No file-descriptor limit." The
[2026-08-30 exec sandboxing and resource quotas
gate](2026-08-30-exec-sandboxing-and-resource-quotas.md) already
researched this project's comparison set for OS-level sandboxing and
resource-quota mechanisms in general and found "no project in this set
enforces CPU or disk quotas on a spawned command," leaving it "genuinely
undesigned" when the design that followed shipped memory-only quotas.
This gate does not re-derive that prior gate's settled findings
(mechanism convergence for filesystem sandboxing, fail-closed selection,
honest partial-enforcement reporting); it re-verifies, per Documentation
rule 7, whether any of the same six projects have added CPU or disk
enforcement since, and researches the primary-source mechanics of the
two concrete Linux controllers (`cpu.max`, `io.max`) a design would
extend this project's own existing cgroup v2 memory-quota code with. This
gate does not design or implement anything.

English is normative. The Chinese file is a synchronized reading copy.

## What this project already has

`internal/harness/adapters/localexec/cgroup_linux.go` implements a
reusable, per-invocation cgroup v2 child directory: `memory.high`/
`memory.max` are written once at construction (Grok Build's own model,
adopted in the prior design), each `Run` call moves only that command's
PID into the cgroup via `cgroup.procs`, and a background goroutine
watches `memory.events`' `high` counter through `inotify`, proactively
`SIGKILL`ing the process group — reporting `CommandResult.ResourceLimited`
— if usage is still above 90% of `memory.high` when the kernel's own
counter increments. This monitor-and-kill shape exists specifically
because `memory.max` is a hard OOM boundary this project chooses to
preempt gracefully; as this gate's own research below establishes,
neither `cpu.max` nor `io.max` needs — or has any hook for — an
equivalent monitor, since both are throttling controllers the kernel
enforces transparently.

On macOS, the equivalent memory bound is `RLIMIT_AS` (`sandbox_darwin.go`),
explicitly documented as best-effort: it caps virtual address space, not
resident memory, and a breach surfaces as the child's own allocator
getting `ENOMEM`, never a clean external kill — `SECURITY.md`'s own
"Not enforced" list already states `CommandResult.ResourceLimited` is
never set there.

## Comparison set and pinned commits

Per Documentation rule 8, each was fetched with
`scripts/fetch-reference.sh <owner/repo> <sha>` into the gitignored
`.reference/` directory and read directly. Per Documentation rule 7, all
six were re-verified today against the prior gate's own pins: `grok-build`,
`deepseek-harness`, and `pi-mono` had not moved; `codex`, `kimi-code`, and
`maka-agent` had each advanced, re-fetched and checked out below.

| Project | Repository | Commit (prior gate → today) | Observed | Re-verification result |
| --- | --- | --- | --- | --- |
| OpenAI Codex | `openai/codex` | `dde85b4` → `a9519cb` | 2026-08-31 | No diff in `linux-sandbox`, `execpolicy`, or `core/src/seatbelt.rs` between the two commits; nothing to re-read |
| Kimi Code | `MoonshotAI/kimi-code` | `9619277` → `8f2c60b` | 2026-08-31 | All changed files are under `packages/transcript/`; unrelated to sandboxing or resource quotas |
| Grok Build | `xai-org/grok-build` | `bc7f02e` (unchanged) | 2026-08-31 | Re-read `cgroup.rs` directly; still no `cpu.max`, `cpu.weight`, `io.max`, `pids.max`, or `blkio` reference anywhere in the crate |
| Maka | `maka-agent/maka-agent` | `d093ba5`/`5d519d6` → `ef94235` | 2026-08-31 | `packages/runtime/src/sandbox/diagnostics.ts` (474 lines) and several other sandbox files were deleted or shrank in this range; the surviving files still have no `cpu`/`io`/`blkio`/`rlimit`-cpu reference. This gate did not trace why `diagnostics.ts` was removed — noted as an observed change, not investigated further, since it is not itself a resource-quota mechanism |
| DeepSeek Harness | `deepseek-ai/deepseek-harness` | `cd5ef81` (unchanged) | 2026-08-31 | See the new finding below — a *different* sandbox in this same repository, not the one the prior gate studied |
| Pi (`pi-mono`) | `badlogic/pi-mono` | `853a80d` (unchanged) | 2026-08-31 | Unchanged; the prior gate's "sandboxing is an example, not core" framing stands |

A targeted re-grep across all six repositories at today's commits for
`cpu.max`, `io.max`, `cpu.weight`, `blkio`, and `RLIMIT_CPU` found exactly
one hit, addressed below. **No project in this comparison set enforces a
CPU or disk-IO quota on its shell/exec tool today** — the prior gate's
finding stands, re-verified rather than assumed to still hold.

## New finding: DeepSeek Harness's *Python code-runtime* self-sets `RLIMIT_CPU` — a different sandbox, a different mechanism shape

`packages/code-runtime/code-runtime-python/src/protocol.ts` documents a
`BootMessage.cpuSeconds` field: *"RLIMIT_CPU seconds; the Python bootstrap
sets this on itself before executing model code."* This is not the shell
tool the prior gate studied — it is a separate sandbox for running
model-generated Python, where DeepSeek Harness controls the bootstrap
script that starts inside the sandboxed process.

This is a materially different mechanism shape from anything a design for
this project's own `exec` tool could adopt directly: the limit is set by
the *target process itself*, immediately after it starts and before it
runs untrusted code — not by a parent process reaching in from outside
after the child is already running arbitrary, operator/model-supplied
code. Go's `os/exec` has no equivalent hook (unlike Python's own
`preexec_fn` or a C `posix_spawn_file_actions` callback) to inject
"set your own `RLIMIT_CPU`" between fork and exec for an *arbitrary*
command this project does not control the source of — `exec`'s whole
point is running a caller-supplied argv, not a bootstrap script this
project authors. Confirmed by reading `os/exec`'s and `syscall.SysProcAttr`'s
own Go standard library documentation: `SysProcAttr` on Linux exposes
`Setsid`, `Setpgid`, `Chroot`, `Credential`, `Ptrace`, and similar
process-creation flags, but no rlimit field. The closest Linux-only
external-facing primitive, `prlimit(2)` (which *can* set another already-
running process's limits, given sufficient privilege), is not demonstrated
by any of the six projects in this comparison set for this purpose, and
has no macOS equivalent at all.

## Primary source: cgroup v2 `cpu.max` and `io.max` semantics

Read directly from the kernel's own admin-guide documentation
(`docs.kernel.org/admin-guide/cgroup-v2.html`), the same authority this
project's prior sandboxing design already relied on for `memory.high`/
`memory.max`:

- **`cpu.max`** accepts `"$MAX $PERIOD"` in microseconds (for example
  `"50000 100000"` caps a cgroup at 50% of one CPU), or the literal
  `"max"` for no limit; writing a single number updates only `$MAX`. The
  kernel enforces this by **throttling**, not killing — a cgroup that
  exceeds its allotted CPU time within a period simply stops being
  scheduled until the next period, with no OOM-killer-equivalent
  intervention and no daemon or monitor required. `cpu.stat` reports
  `nr_periods`, `nr_throttled`, and `throttled_usec` for observability.
- **`io.max`** accepts a nested-keyed format identifying a block device by
  major:minor number, with `rbps`/`wbps` (bytes/sec) and `riops`/`wiops`
  (IO operations/sec) keys — for example `"8:16 rbps=2097152 wiops=120"`.
  Enforcement is the kernel's own `blk-throttle`, again with **no kill and
  no monitor**: "IOs are delayed if limit is reached... temporary bursts
  are allowed." `io.stat` reports per-device `rbytes`/`wbytes`/`rios`/`wios`.
- Both controllers require the same delegation shape this project's own
  `newCgroupQuota` already performs for `memory`: the parent cgroup must
  list the controller in `cgroup.subtree_control` before a child can use
  it, subject to the same "no internal process" constraint the existing
  memory code already works around.
- **Neither controller has anything resembling `memory.events`' `high`
  counter or a breach notification mechanism.** The monitor-goroutine/
  inotify/graceful-kill architecture `cgroup_linux.go` built specifically
  for memory has no CPU or IO counterpart to extend — a CPU or IO quota
  is, mechanically, just writing a value once at cgroup construction, with
  nothing to watch afterward.

## Cross-cutting synthesis

- **CPU and IO cgroup v2 controllers are architecturally simpler to add
  than memory was, on Linux specifically**, precisely because they need
  no monitor: extending `newCgroupQuota` with two more `os.WriteFile`
  calls (`cpu.max`, `io.max`) into the same reusable child cgroup this
  project already creates, delegates, and tears down for memory is a
  small, incremental change to already-shipped, evidence-backed
  infrastructure — not a new subsystem.
- **A throttled command does not fail the way a memory-limited one
  does — it just runs slower**, which this project's own existing
  `CommandResult` shape (`TimedOut` XOR `ResourceLimited`, per
  `tools/ports.go`'s own doc comment: "a run is killed for exactly one
  reason") has no established category for. A CPU- or IO-throttled
  command's most likely observable outcome is hitting the *existing*
  wall-clock timeout — indistinguishable, without reading `cpu.stat`/
  `io.stat` afterward, from a command that was simply slow on its own.
  Whether that distinction is worth surfacing to the model/operator is a
  real design question (below), not something this gate resolves.
- **`io.max` throttles *rate*, not *total disk space consumed*.** A
  command bounded to a modest `wbps` can still, given enough wall-clock
  time (bounded separately by the existing timeout), write an arbitrarily
  large file — `io.max` alone does not solve "a workspace filled to
  capacity" the way `SECURITY.md`'s "disk" wording might suggest at first
  reading. This project already has a *different*, existing bound in the
  same neighborhood — `MaxToolResultBytes`/output truncation — but that
  caps what is *captured and returned* to the model, not what a command
  actually writes to the workspace filesystem underneath it.
- **`io.max` requires resolving which block device(s) the workspace root
  (and any temp directory) actually live on** (major:minor numbers) —
  a genuinely new, environment-dependent resolution problem memory's
  single, universal ceiling never had, and one this gate did not find any
  of the six reference projects solving, since none of them implement
  `io.max` at all.
- **macOS has no cgroup-v2-equivalent mechanism for CPU or IO throttling
  found anywhere in this six-project comparison set, and no other
  externally-enforced primitive was found either.** Unlike memory
  (`RLIMIT_AS`, a real if best-effort POSIX rlimit any process can set on
  itself and its own descendants inherit), the honest answer to "what
  externally enforces a CPU or IO ceiling on an arbitrary already-spawned
  child on macOS" appears, from this comparison set, to be: nothing this
  gate or the six reference projects demonstrate. This is a stronger,
  more definitive gap than memory's "partial/best-effort" one, not merely
  an unresearched corner.

**Correction (2026-08-31, found while preparing the design that follows):**
the macOS finding above is wrong, and the error was not in the six
reference projects — it was in not first checking this project's *own*
existing code. `internal/harness/adapters/localexec/sandbox_darwin.go`'s
`beginRlimitBracket` already demonstrates a real, working technique for
exactly this class of problem: since `RLIMIT_AS` is inherited by a child
at `fork()`, and Go's `os/exec` gives no hook to set a limit only in the
child between fork and exec, this project brackets its *own* process's
rlimit around the parent's `Start()` call — lower it, start the child (a
narrow, mutex-guarded window during which the harness's own process is
also bound by the lowered ceiling), then restore it. `RLIMIT_CPU` is
inherited at fork identically to `RLIMIT_AS`; the same bracket technique
applies to it without modification. This gate's claim that "nothing this
gate or the six reference projects demonstrate" externally enforces a
CPU ceiling on macOS was true only of the six external projects — this
project's own existing memory-quota code already solved the general
problem. The design that follows uses this corrected understanding, not
the paragraph above; the paragraph is left unedited, with this note
attached, rather than silently rewritten, per this project's own
established practice for correcting a merged document.

## Open questions a design must resolve, not answered by this gate

- **Whether a throttled-but-not-killed outcome deserves a new
  `CommandResult` field or reporting category**, distinct from today's
  `TimedOut`/`ResourceLimited` pair, versus accepting that CPU/IO
  throttling surfaces only indirectly (a command that would otherwise
  have finished in time now times out) with no explicit signal — and
  whether reading `cpu.stat`'s `nr_throttled`/`throttled_usec` after a
  timeout to attribute it is worth the added complexity.
- **Whether "disk quota" should mean IO throughput (`io.max`), a cap on
  total bytes written to the workspace during one command, or both** —
  `io.max` alone does not address disk-space exhaustion, and this gate
  found no reference-project precedent for either total-write-byte
  accounting or a disk-space quota on a spawned command specifically (as
  opposed to this project's own separate, already-implemented output-
  capture truncation).
- **How to resolve the workspace root's (and temp directory's) block
  device major:minor for `io.max`**, including the case where they live
  on different devices, a network filesystem, or a device whose
  major:minor changes across reboots or containers.
- **Whether CPU/IO quotas are in scope for macOS at all in a first
  slice**, given this gate found no externally-enforced mechanism
  comparable to `RLIMIT_AS`'s (imperfect but real) memory story — a
  disclosed, named gap (matching this project's own precedent for
  Windows in the prior sandboxing slice) may be the honest answer rather
  than inventing an untested mechanism no reference project demonstrates.
- **Whether a CPU time or wall-clock-proportional default is more useful
  than a fixed absolute bound**, given `cpu.max`'s `$MAX/$PERIOD` shape
  is inherently a bandwidth fraction (e.g., "50% of one core"), not a
  total-seconds cap the way `RLIMIT_CPU` is — the two express genuinely
  different policies and this gate does not recommend one.
- **File-descriptor limits** (also named in `SECURITY.md`'s same "Not
  enforced" sentence as CPU/disk-IO) were not researched by this gate at
  all — `RLIMIT_NOFILE` is a real, simple, portable POSIX rlimit
  (Codex's own `pty.rs` already *reads* it, per the prior gate), but
  whether it is in scope for the same design cycle as CPU/disk quotas or
  a separate, smaller one is left open here.

## Evidence limits

- Every citation above traces to a specific pinned commit read in this
  session (table above), or to the kernel's own admin-guide documentation
  fetched directly; no claim is from memory or from a project's marketing
  page.
- This gate does not authorize copying any file path, constant name, or
  configuration shape verbatim from any reference project — only the
  mechanisms and architectural choices they represent, per this project's
  standing rule for every prior gate's comparison set.
- Maka's `diagnostics.ts` removal (592 lines net across several sandbox
  files) was observed but not investigated — it did not appear to be
  resource-quota code before its removal (the prior gate's own reading of
  Maka covered only `linux-capability.ts`'s bwrap probe), but this gate
  did not read the deleted file's prior content to confirm that absence
  rather than infer it.
- The `prlimit(2)`/Linux-only-external-rlimit-setting possibility is
  named as a real Linux syscall this gate is aware of, not as something
  independently verified against Go's `syscall` package support or tested
  in this codebase — flagged as a fact requiring its own verification if
  a design phase considers it as an alternative to cgroups on Linux.
- "Current state" here means 2026-08-31. A future gate revisiting any of
  these six projects, or the kernel's own cgroup v2 documentation, must
  re-fetch and re-read per Documentation rule 7, rather than reuse this
  document's characterization.
- This gate does not choose a design. The next step is a normative design
  for extending `internal/harness/adapters/localexec`'s existing
  resource-quota mechanism with CPU and/or disk bounds, informed by — not
  dictated by — the findings above.
