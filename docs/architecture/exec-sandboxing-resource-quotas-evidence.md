# Exec sandboxing and resource quotas completion evidence

**Status:** Complete evidence ledger

**Date:** 2026-08-30

**Design:** [Exec sandboxing and resource quotas](../superpowers/specs/2026-08-30-exec-sandboxing-resource-quotas-design.md)

**Plan:** [Exec sandboxing and resource quotas implementation plan](../superpowers/plans/2026-08-30-exec-sandboxing-resource-quotas.md)

**Contracts:** [Tool runtime](tool-runtime.md) (`localexec.Runner`'s
`Enforcement()` and its per-platform backends), [Tool runtime completion
evidence](tool-runtime-evidence.md) (stem ledger this ledger extends for
the `localexec`/composition surface only — the rest of that ledger's scope
is unchanged)

This ledger records real OS-level exec confinement — bubblewrap and cgroup
v2 on Linux, Seatbelt and `RLIMIT_AS` on macOS — plus a fail-closed
`composition.Open` gate with a named, logged escape hatch, replacing the
prior all-`"none"` `Enforcement` baseline. Completion is claimed from the
evidence below, not from checkbox state.

## Commits

| Task | Commit | Subject |
| --- | --- | --- |
| plan | `8871300` | docs: exec sandboxing and resource quotas implementation plan |
| 1 | `7844176` | feat(localexec): structured per-effect enforcement reporting |
| 2 | `80821c9` | feat(localexec): bubblewrap confinement on Linux |
| 3 | `aa018b4` | feat(localexec): cgroup v2 memory quota on Linux |
| 4 fix | `616a662` | fix(localexec): use a shell-portable network probe in the bwrap test |
| 4 | `9bce246` | feat(localexec): Seatbelt confinement and RLIMIT_AS on macOS |
| 5 | `afafd1d` | feat(composition): fail closed on unavailable exec sandbox |
| 6 | this commit | docs: publish exec sandboxing and resource quota contracts |

Each of Tasks 1–5 shipped as its own reviewed, merged pull request (PRs
50–54) with its full verification suite (see each commit's own message)
run before merge, not only the aggregate run below.

## Mapping-table tests

| Surface | Tests |
| --- | --- |
| Structured enforcement vocabulary | `TestEnforcementReportsNoneWithoutAPlatformBackend`, `TestExecResourceLimitedFailsToolWithFrozenText` |
| Linux bwrap: argv and WSL1 detection | `TestBwrapArgvWrapsTargetWithRequiredNamespaceIsolation`, `TestBwrapArgvPlacesTargetAfterSandboxFlags`, `TestIsWSL1Version` |
| Linux bwrap: `Run()` wiring (unconditional) | `TestRunWrapsArgvInBwrapWhenAvailable` |
| Linux bwrap: real-OS confinement (gated) | `TestBwrapConfinementDeniesWritesOutsideWorkspace`, `TestBwrapConfinementDeniesNetwork`, `TestBwrapConfinementHidesHostProcesses` |
| Linux cgroup v2: detection and parsing | `TestCgroupV2SelfPath`, `TestCgroupQuotaParsesMemoryEventsAndProcs`, `TestCgroupQuotaNilReceiverMethodsAreNoOps` |
| Linux cgroup v2: `Run()` wiring (unconditional) | `TestRunKillsOnResourceLimitSignal` |
| Linux cgroup v2: real-OS memory kill (gated) | `TestCgroupMemoryQuotaKillsAMemoryGrowingCommand` |
| macOS Seatbelt: argv and policy construction | `TestSeatbeltArgvBindsWorkspaceRootAndAppendsTarget`, `TestSeatbeltPolicyDenyByDefaultWithWorkspaceWriteException`, `TestSeatbeltCommandArgvUsesHardcodedExecutable` |
| macOS `RLIMIT_AS` | `TestRlimitEnforcementLevelIsPartialOnDarwin` |
| macOS Seatbelt: `Run()` wiring (unconditional) | `TestRunWrapsArgvInSeatbeltWhenAvailable` |
| macOS Seatbelt: real-OS confinement (gated) | `TestSeatbeltConfinementDeniesWritesOutsideWorkspace`, `TestSeatbeltConfinementDeniesNetwork` |
| Composition fail-closed gate | `TestOpenFailsClosedWhenSandboxUnavailableAndFlagUnset`, `TestOpenProceedsAndLogsWhenFlagSetAndSandboxUnavailable`, `TestOpenProceedsSilentlyWhenSandboxAvailable` |
| Composition real default path (gated) | `TestOpenSucceedsWithDefaultFlagWhenSandboxIsAvailable` |

"Gated" tests `t.Skip` when this environment's backend is only
structurally present, not functionally usable (see "Per-platform
verification reality" below) — they are not vacuous everywhere, only here.

## Per-platform verification reality

This entire slice was implemented from one Linux x86_64 host with no
macOS access at all, and that host's own bwrap and cgroup v2 are each
present but administratively blocked:

- **bwrap**: installed, but unprivileged user namespace creation is
  blocked by Ubuntu 24.04's AppArmor `restrict_unprivileged_userns`
  (`bwrap: setting up uid map: Permission denied`).
- **cgroup v2**: the unified hierarchy exists, but this host's own
  interactive session cgroup already holds a resident process (this
  shell), and cgroup v2's "no internal process" constraint forbids
  delegating `subtree_control` from a cgroup with a resident process —
  confirmed directly: `printf '+memory' > cgroup.subtree_control` returns
  `Device or resource busy`.
- **Seatbelt**: Darwin-only by filename (`sandbox_darwin.go`,
  `sandbox_darwin_test.go`), so nothing in that file executes on this
  host at all — not even a build tag, the Go toolchain's own filename
  convention excludes it from every non-Darwin build.

Given this, verification split into three tiers, applied consistently
across Tasks 2–5:

1. **Real execution** of every test not gated on a specific backend
   (argv/policy construction, parsing, the nil-safe/no-backend baseline,
   and — critically — the `Run()`-level wiring for both bwrap and
   Seatbelt, each proven by hand-wiring the relevant field
   (`bwrapAvailable`/`seatbeltAvailable`/a bare `cgroupQuota`) and
   substituting a fake executable or channel, independent of whether the
   real backend is functionally usable here).
2. **Honest `t.Skip`** for anything that needs a functionally working
   backend this host doesn't have, with a `Runner.Enforcement()`-based
   (not merely binary-presence-based) skip condition, so the test would
   actually run wherever a real backend works.
3. **Cross-compile + `go vet`** for the Darwin-only files, confirmed to
   genuinely type-check (not just silently skip) by deliberately
   injecting a syntax error into `sandbox_darwin_test.go`, seeing
   `GOOS=darwin GOARCH=arm64 go vet ./...` catch it, then restoring and
   confirming clean.

This hand-tracing (tier 1's wiring tests, done because tier 2 couldn't
cover the real Run() path here) caught two real bugs before they shipped:
a `\n`-delimited argv-capture assertion that broke because the Seatbelt
policy string itself contains newlines (fixed to NUL-delimited), and an
already-merged Task 2 network-denial test that used bash's `/dev/tcp/`
under `sh -c` — meaningless under dash, `/bin/sh` on Ubuntu — fixed in the
same commit series (`616a662`) to a curl-against-a-raw-IP probe with
explicit exit-code handling.

## Verification commands and output

All keyless and network-free.

```text
$ test -z "$(gofmt -l .)"
(clean)

$ go vet ./...
(clean)

$ CGO_ENABLED=0 go build ./...
(clean)

$ go test -count=1 ./...
ok   github.com/SongYii/open-code-harness/cmd/och
ok   github.com/SongYii/open-code-harness/internal/docsguard
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/acp
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/localexec
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/memory
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/openaicompat
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/system
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/workspacefs
ok   github.com/SongYii/open-code-harness/internal/harness/application
ok   github.com/SongYii/open-code-harness/internal/harness/architecture
ok   github.com/SongYii/open-code-harness/internal/harness/composition
ok   github.com/SongYii/open-code-harness/internal/harness/domain
ok   github.com/SongYii/open-code-harness/internal/harness/engine
ok   github.com/SongYii/open-code-harness/internal/harness/policy
ok   github.com/SongYii/open-code-harness/internal/harness/runtime
ok   github.com/SongYii/open-code-harness/internal/harness/testkit
ok   github.com/SongYii/open-code-harness/internal/harness/tools
ok   github.com/SongYii/open-code-harness/internal/harness/transcript

$ go test -race -count=1 ./...
ok   (every package listed above)

$ go test -race -count=5 ./internal/harness/adapters/localexec -timeout 300s
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/localexec

$ GOOS=windows GOARCH=amd64 go build ./...
(clean)

$ GOOS=darwin GOARCH=arm64 go build ./...
(clean)

$ GOOS=darwin GOARCH=arm64 go vet ./...
(clean)

$ git diff --check
(clean)
```

Explicit re-run of every test naming a currently-`Enforced` `SECURITY.md`
guarantee, confirming none regressed:

```text
$ go test ./internal/harness/adapters/localexec/... -run \
  'TestScrubbedEnv|TestArgvOnlyNoShellExpansion|TestTimeoutKillsProcessGroup|TestCancelKillsProcessGroup|TestOutputCapKillsAndTruncates|TestCwdAndArgvMustStayInWorkspace' \
  -count=1 -v
--- PASS: TestArgvOnlyNoShellExpansion
--- PASS: TestScrubbedEnv
--- PASS: TestTimeoutKillsProcessGroup
--- PASS: TestCancelKillsProcessGroup
--- PASS: TestOutputCapKillsAndTruncates
--- PASS: TestCwdAndArgvMustStayInWorkspace
PASS
```

Real binary, end to end, on this host (bwrap functionally unavailable —
the exact scenario the fail-closed gate exists for):

```text
$ och -workspace $WS1 -database $DBDIR1/harness.db -runtime-id t1 \
  -provider-url https://provider.invalid/v1 -model m \
  -context-window 8192 -max-output 1024
och: composition: invalid config: exec sandbox is unavailable and
AllowUnsandboxedExec is false: bwrap probe failed: exit status 1
exit=1

$ och -workspace $WS2 -database $DBDIR2/harness.db -runtime-id t2 \
  -provider-url https://provider.invalid/v1 -model m \
  -context-window 8192 -max-output 1024 -allow-unsandboxed-exec
2026/08/30 12:03:36 composition: AllowUnsandboxedExec is true - proceeding
without OS-level exec confinement: bwrap probe failed: exit status 1
och: ready; workspace=... database=... runtime=t2
```

## Mutation checks

Each was reverted immediately after confirming the RED result, before the
GREEN commit:

- **Task 2** (`Run()`'s bwrap-wrapping branch): removing the
  `name = "bwrap"; runArgs = bwrapArgv(...)` assignment made
  `TestRunWrapsArgvInBwrapWhenAvailable` fail with "fake bwrap was not
  invoked" instead of a captured-argv mismatch — confirming the test
  actually exercises the wiring, not just `bwrapArgv`'s own pure output.
- **Task 3** (`Run()`'s cgroup-resource-limited branch): dropping
  `result.ResourceLimited = true` from that `select` case made
  `TestRunKillsOnResourceLimitSignal` fail on the `ResourceLimited`
  assertion specifically, with the process still correctly killed —
  confirming the test distinguishes "killed" from "killed and correctly
  classified".
- **Task 5** (the fail-closed branch itself): removing the
  `if !config.AllowUnsandboxedExec { return ... }` guard, leaving only the
  log line, made `TestOpenFailsClosedWhenSandboxUnavailableAndFlagUnset`
  fail with "Open() error = nil, want a sandbox-unavailable failure" —
  confirming the gate, not just the log message, is under test.

## Deviations from the plan's stated file maps

Recorded because the frozen plan named specific files per task and actual
implementation diverged in a few small, disclosed ways — each is a
narrower or clarifying change, not new scope:

- **Task 2**: no changes to `bwrap_linux.go` were needed beyond its own
  creation; the plan's "Modify: runner.go, bwrap_linux.go" turned out not
  to require touching the latter.
- **Task 3**: `DefaultMemoryHighBytes`/`DefaultMemoryHeadroomBytes` live
  in `runner.go`, not `cgroup_linux.go`, because `runner.go` (built on
  every platform) references them unconditionally; a `cgroup_other.go`
  stub was added, mirroring the existing `bwrap_linux.go`/`bwrap_other.go`
  split, though the plan's file map only named `cgroup_linux.go` and
  `cgroup_linux_test.go`.
- **Task 4**: no changes to `bwrap_other.go`/`sandbox_other.go` were
  needed for Task 4 itself (Task 5's own plan text already anticipated
  touching them, for Task 5's reason, not Task 4's).
- **Task 5**: `bwrap_other.go` and `sandbox_other.go` needed no changes —
  both already reported unavailable unconditionally on every other
  platform, which is exactly correct for Windows too, so
  `localexec.Availability()`'s own `default` case (not a change to either
  file) is what names Windows explicitly. `cmd/och/main.go` was modified
  even though the plan's Task 5 file map didn't name it, because the
  `AllowUnsandboxedExec` escape hatch would otherwise be unreachable from
  the real binary — a `-allow-unsandboxed-exec` flag was added.

## Exclusions

Recorded as out of this slice by design, not as deferred bugs inside it:

- **Windows**: no OS-level exec sandbox backend. `composition.Open` fails
  closed there by default, same as any other platform lacking one — an
  accepted regression from today's unsandboxed-but-functional Windows
  `exec`, named explicitly in the design (§5) and reviewer-accepted
  2026-08-30. A follow-up slice implementing Windows Job Objects
  (`SetInformationJobObject`, `golang.org/x/sys/windows`, no CGO) remains
  a real, verified option, not decided now.
- **CPU quota**: no mechanism in this slice, on any platform.
- **Disk-IO quota**: no mechanism in this slice, on any platform.
- **Landlock** (Linux): rejected in the architecture gate — requires CGO
  via `libcap/psx` for correctness on pre-ABI-V8 kernels, violating this
  project's `CGO_ENABLED=0` constraint.
- **Containers/VMs** (gVisor, Firecracker, Docker-as-sandbox): out of
  scope — this slice confines a single local `exec` call, not the whole
  harness process; a container/VM boundary is a deployment-level decision
  orthogonal to what `localexec.Runner` itself can enforce.
- **Multi-tenant isolation**: unchanged from before this slice; one
  workspace root per session remains a correctness boundary, not a
  security boundary between mutually distrusting users.
- A network proxy exception (allow specific egress instead of denying
  all): explicitly excluded from v1 by the design (§2); `--unshare-net` /
  `(deny network*)` deny everything.
- A BPF seccomp filter layered under bwrap's own namespace confinement:
  available in principle (`golang.org/x/net/bpf` plus bwrap's `--seccomp
  FD`) but not needed to satisfy v1's "no network" goal and not built.

## CPU quota extension (2026-08-31)

**Gate:** [Exec CPU and disk quotas architecture gate](../research/architecture-gates/2026-08-31-exec-cpu-disk-quotas.md) (including its 2026-08-31 correction note)

**Design:** [Exec CPU quota](../superpowers/specs/2026-08-31-exec-cpu-quota-design.md)

**Plan:** [Exec CPU quota implementation plan](../superpowers/plans/2026-08-31-exec-cpu-quota.md)

This section records the CPU-quota extension to the slice above: Linux
`cpu.max` and macOS `RLIMIT_CPU`, closing the CPU half of `SECURITY.md`'s
former "No CPU or disk-IO quota, on any platform" sentence. Disk-IO
quota remains unaddressed (see Remaining below); this section does not
claim otherwise.

### Commits

| # | Commit | Subject |
| --- | --- | --- |
| Gate | `eaa54cc` | docs: add the exec CPU/disk quota architecture gate |
| Design | `9ace12b` | docs: design exec CPU quota; correct the gate's macOS finding |
| Plan | `55c647e` | docs: freeze the exec CPU quota implementation plan |
| Task 1 | `b1d2f5b` | feat(localexec): Linux CPU quota via cgroup v2 cpu.max |
| Task 2 | `69499a8` | feat(localexec): macOS CPU quota via RLIMIT_CPU |

### Mapping table: mechanism → test → verification reality

| Mechanism | Test | Verification reality |
| --- | --- | --- |
| Linux `cpu.max` write (extends the existing cgroup) | `TestCPUControllerFailureLeavesMemoryQuotaActive` | Skips in this session's own sandboxed dev environment — it cannot delegate even the pre-existing memory controller (`TestCgroupMemoryQuotaKillsAMemoryGrowingCommand` already skips here too); the independent-failure logic itself was still exercised by the code path, only the real-host assertion is unverified here |
| `cpu.stat` `nr_throttled` parsing | `TestCgroupQuotaParsesCPUStatThrottledCount`, `TestCgroupQuotaReadThrottledCountMissingFileIsNotAHardFailure` | Ran and passed for real (pure file-parsing, no cgroup dependency) |
| `Run()`'s `Throttled`-reporting wiring | `TestRunReportsThrottledFromHandWiredCPUStat`, `TestRunReportsNotThrottledWhenCPUStatShowsNoThrottling` | Ran and passed for real, hand-wiring a fake `cgroupQuota.fsPath` independent of real cgroup delegation (mirroring `TestRunKillsOnResourceLimitSignal`'s own existing technique for the memory-kill path) |
| Real parallel-workload throttling | `TestCgroupCPUQuotaThrottlesParallelWork` | Skips in this session's own environment for the same cgroup-delegation reason as above |
| macOS `RLIMIT_CPU` bracket | `TestRlimitBracketSetsAndRestoresRLIMIT_CPU` | Compiles cleanly cross-compiled (`darwin/arm64`, `darwin/amd64`); never executed — no macOS host in this session |
| macOS `cpuRlimitEnforcementLevel` | `TestCPURlimitEnforcementLevelIsFullOnDarwin` | Compiles cleanly cross-compiled; never executed |
| macOS signal attribution (`isCPUResourceLimitExit`) | `TestIsCPUResourceLimitExitDetectsSIGXCPUOnly`, `TestCPUQuotaKillsARunawayCPUCommand` | Compiles cleanly cross-compiled; never executed |

### Verification commands and output (fresh, this session, Linux host)

```
$ gofmt -l .
(no output)
$ go vet ./...
(no output)
$ GOOS=darwin GOARCH=arm64 go build ./... && GOOS=darwin GOARCH=amd64 go build ./...
(no output; both succeed)
$ GOOS=darwin GOARCH=arm64 go vet ./...
(no output)
$ GOOS=darwin GOARCH=arm64 go test -c ./internal/harness/adapters/localexec/...
(compiles; not run)
$ CGO_ENABLED=0 go build ./...
(no output)
$ go test ./... -count=1
(all packages ok)
$ go test -race ./internal/harness/adapters/localexec/... -count=1
ok
```

### Mutation checks

1. **Linux, `Run()`-side `Throttled` wiring**: forced `throttled()` to
   always return `false` regardless of `cpu.stat` contents —
   `TestRunReportsThrottledFromHandWiredCPUStat` failed for the right
   reason; restored.
2. **Linux, `cpu.stat` parsing**: changed the parsed key from
   `nr_throttled` to `nr_periods` — `TestCgroupQuotaParsesCPUStatThrottledCount`
   and `TestRunReportsThrottledFromHandWiredCPUStat` both failed for the
   right reason; restored.
3. **Linux, the real `cpu.max` write itself**: removed it entirely (the
   function returned an always-empty `cpuReason` without ever writing the
   file) — the full suite still passed in this session's own environment,
   since neither test that would catch it
   (`TestCgroupCPUQuotaThrottlesParallelWork`,
   `TestCPUControllerFailureLeavesMemoryQuotaActive`) can run here at all.
   Disclosed as a real, pre-existing environment limitation (matching the
   memory quota's own inability to be verified for real here), not a gap
   this task introduces or hides.
4. **macOS**: no mutation check could be executed at all — there is no
   way to run a `darwin`-tagged test from this Linux session. Correctness
   for `beginRlimitBracket`'s `RLIMIT_CPU` handling and
   `isCPUResourceLimitExit`'s signal discrimination rests on code review
   and cross-compilation only, disclosed plainly rather than implied to
   be test-proven.

### Deviations from the plan

- **Task 2's checklist instructed attributing a bare, unexplained
  `SIGKILL` to `ResourceLimited` in addition to `SIGXCPU`; the design's
  own prose said the opposite (SIGXCPU only).** Implemented per the
  design: a bare `SIGKILL` is not attributed, since it cannot be
  distinguished from an unrelated external kill in the same narrow
  window, and the overwhelming majority of real commands already die
  from `SIGXCPU`'s own default disposition before the hard limit's
  `SIGKILL` would ever fire. The plan's checklist wording was wrong.
- **Task 2's checklist instructed citing `application.DefaultExecTimeout`
  directly so the two 30-second values could not drift apart.**
  Impossible: `internal/harness/architecture`'s own dependency-boundary
  rule (`TestForbiddenImport`, `"localexec cannot import application"`)
  forbids it — `localexec` is a lower-level adapter and `application` is
  the layer above it that consumes adapters through ports, not the
  reverse. `DefaultCPUSoftSeconds` is a plain, documented constant
  instead, with no compiler-enforced link; a future change to
  `DefaultExecTimeout` needs a matching, manual update here.
- **Task 3's checklist named a nonexistent file**,
  `docs/architecture/exec-sandboxing-resource-quotas.md` — no such
  standalone implemented-contract document exists; exec sandboxing's
  contract has always lived inside [Tool runtime](tool-runtime.md) (the
  file amended in this task instead) and `SECURITY.md` directly. The
  plan's own File Map entry was wrong.

## Remaining

- No macOS or Windows CI/dev host has run any part of this slice —
  including this CPU-quota extension — for real; see "Per-platform
  verification reality" above and the CPU quota extension's own mapping
  table for exactly what substitutes for that from this Linux-only
  environment.
- `RLIMIT_AS` on macOS is a best-effort virtual-address-space bound, not
  a monitored ceiling; a breach surfaces as the child's own allocator
  hitting `ENOMEM`, not a clean external kill, and never sets
  `ResourceLimited`. This is a documented, accepted platform gap (design
  §4), not an oversight.
- The Linux cgroup v2 memory quota and the macOS `RLIMIT_AS` bound share
  the same numeric defaults (`DefaultMemoryHighBytes` = 512 MiB,
  `DefaultMemoryHeadroomBytes` = 256 MiB) but neither is yet exposed
  through `composition.Config`; only the plan's stated defaults exist
  today, not per-deployment tuning.
- **Disk-IO quota and file-descriptor limits remain unaddressed**,
  exactly as the CPU quota design's own §2 scoped them out: `io.max`
  would only throttle throughput rate, not bound total disk space
  consumed (the concern `SECURITY.md`'s former wording most naturally
  suggested), and file-descriptor limits face the same
  no-pre-exec-hook constraint as CPU on macOS with no Linux cgroup
  fallback (`pids.max` bounds process count, not descriptors).
- `TestEnforcementReportsNoneWithoutAPlatformBackend` (tagged `unix`,
  nominally covering Darwin) asserts an all-`"none"` `Enforcement` that
  was already inconsistent with `rlimitEnforcementLevel`'s unconditional
  `"partial"` for `Memory` on Darwin *before* the CPU quota extension;
  that extension's own unconditional `"full"` for `CPU` inherits the
  same pre-existing, never-caught-for-real gap rather than introducing a
  new one. Noted here, not fixed — out of scope for a CPU-quota-focused
  plan.
- Surfaces remain `experimental`; not GA.
