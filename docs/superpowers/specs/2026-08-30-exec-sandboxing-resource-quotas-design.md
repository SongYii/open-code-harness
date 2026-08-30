# Exec Sandboxing and Resource Quotas — Design

- **Date:** 2026-08-30
- **Status:** Accepted 2026-08-30. The human reviewer confirmed §5's
  Windows trade-off explicitly: Windows is out of scope for this slice,
  and that is acceptable for now.
- **Stability:** touches an existing `experimental`/pre-GA surface (`tools.CommandRunner`, `adapters/localexec`)
- **Repository:** `open-code-harness` (`github.com/SongYii/open-code-harness`)
- **Normative language:** English
- **Chinese summary:** [Exec 沙箱与资源配额设计](2026-08-30-exec-sandboxing-resource-quotas-design.zh-CN.md)
- **Authority:** [Client surface and security sequencing decision](../../research/architecture-gates/2026-08-30-client-surface-and-security-sequencing.md); [Exec sandboxing and resource quotas architecture gate](../../research/architecture-gates/2026-08-30-exec-sandboxing-and-resource-quotas.md)

English is normative. The Chinese file is a synchronized summary, not a
field-for-field translation.

---

## 1. Decision summary

`internal/harness/adapters/localexec` today implements `tools.CommandRunner`
with argv-only exec, a killed process group, and a scrubbed environment —
documented and tested as `Enforcement == "partial"`
(`internal/harness/adapters/localexec/doc.go`,
`docs/architecture/tool-runtime.md`). This design turns that honest
`"partial"` claim into real OS-level confinement plus a memory quota, on the
platforms where a proven mechanism exists, while keeping every currently
enforced guarantee (`SECURITY.md` → "Enforced") unchanged and the project's
`CGO_ENABLED=0` pure-Go constraint fully intact.

The central decision the architecture gate's findings forced: **the
confinement mechanism must run as an external process, not as CGO-linked or
in-process code**, because the two real candidates for a syscall-level
mechanism (Landlock, seccomp) each have a Go correctness problem that
os/exec cannot avoid — `go-landlock`'s own `AllThreadsLandlockRestrictSelf`
requires `libcap/psx`, which needs CGO to reach all OS threads on pre-ABI-V8
kernels (verified by reading `landlock-lsm/go-landlock` `4b35c42`,
`landlock/syscall/allthreads_linux.go`), and installing a seccomp-bpf filter
between fork and exec is exactly the pattern Go's runtime documents as
unsafe (goroutines and the GC are not fork-safe). Bubblewrap sidesteps both
problems: it is a separate ELF binary invoked over `os/exec`, and it accepts
a pre-built BPF program over an fd (`--seccomp FD`) so the *filter bytes*
can be assembled in pure Go (`golang.org/x/net/bpf`) without this process
ever forking. This is also the mechanism three of six reference projects
(Codex, DeepSeek Harness, Maka) independently converged on for Linux, and
it is what `SysProcAttr` cannot give us on its own.

Second decision: **fail closed, checked once at composition time, not per
call.** If the platform's mechanism is unavailable, `composition.Open`
refuses to start — mirroring how it already refuses on a missing provider
API key or an unreachable database — rather than silently running `exec`
unconfined per call. An explicit, off-by-default configuration escape
hatch exists for operators who need the current (unsandboxed) behavior
during migration; using it is loud (logged at startup, not silent).

## 2. Goals and exclusions

### Goals

1. Linux: process, filesystem, and network confinement via bubblewrap, with
   a cgroup v2 memory ceiling and a graceful, attributable kill ahead of
   the kernel's own OOM killer.
2. macOS: filesystem and network confinement via `sandbox-exec` with a
   generated Seatbelt profile, plus a best-effort address-space bound
   (`RLIMIT_AS`).
3. A single fail-closed availability check at `composition.Open`, with one
   explicit, off-by-default, loudly-logged override for operators who
   accept the risk.
4. A structured, honest enforcement report — not a single hardcoded
   string — replacing the current fixed `localexec.Enforcement = "partial"`
   constant, following the one principle adopted from DeepSeek Harness that
   is independent of any specific OS mechanism: report what is actually
   confined, per effect, rather than a boolean or an overclaimed constant.
5. A new, distinctly classified tool result for an OOM-style kill, parallel
   to the existing `TimedOut` handling, so Policy/Tool Runtime can tell "the
   command ran out of memory" apart from "the command hung."

### Exclusions

- **Windows OS-level sandboxing.** Both reference projects that attempt it
  (Codex, DeepSeek Harness) needed a hand-built ACL/restricted-token
  mechanism and both report it as less than full enforcement. This design
  does not build one. See §5 for the consequence and the explicit
  trade-off this creates.
- **Landlock**, for the CGO reason in §1. Bubblewrap's own mount namespace
  already covers the filesystem confinement Landlock would add on Linux;
  nothing here forecloses adding it later behind a build tag once a
  CGO-free path (Landlock ABI V8's native thread-sync flag, without
  `libpsx`) is verified as this project's minimum supported kernel.
- **Containers and VMs.** Rejected by the architecture gate: no project in
  the comparison set uses one for the interactive per-command path, and
  DeepSeek Harness's own documentation draws this line explicitly — a
  container replaces whole capabilities and shares nothing with the host,
  which is a different problem than confining one command inside an
  already-trusted workspace.
- **CPU and disk-IO quotas.** No project in the comparison set enforces
  either on a spawned command. Genuinely undesigned, not deferred with a
  known answer.
- **Bundling or vendoring a `bwrap` binary.** v1 requires an
  operator-installed `bwrap` on `PATH`; Codex's approach of shipping one is
  a larger release-engineering commitment this design does not take on.
  A missing `bwrap` is exactly the fail-closed case in §4.
- **Network allowlisting, a proxy-routed exception, or per-tool custom
  profiles.** v1 denies all network access from `exec`; there is no
  approved-domain or MITM-proxy concept in this codebase to route through,
  unlike Codex's `NetworkSeccompMode::ProxyRouted`. Revisit only if a
  concrete consumer needs partial network access.
- **A durable domain fact recording that a session ran unsandboxed.** The
  escape hatch in §5 is composition-time configuration, logged at startup;
  it does not touch the domain event log. Treating "sandboxing was
  disabled for this session" as a first-class audited fact is a real,
  separately-scoped idea this design intentionally leaves for whoever
  designs it, rather than smuggling a domain-layer change into an
  adapter-layer design.

## 3. Linux mechanism

### 3.1 Availability check (composition time)

`composition.Open` resolves `bwrap` on `PATH` once (mirroring the existing
provider-API-key and database-open failures — see `assembly.go`'s
construction order) and runs one process-group-scoped probe invocation
(`bwrap --unshare-user --unshare-pid --ro-bind / / --proc /proc true`,
matching the capability-probe shape Maka's `linux-capability.ts` and
DeepSeek Harness's `sandbox-local` both use, adapted to this project's own
argv builder rather than copied). Success is cached for the assembly's
lifetime, matching every reference project's "probe once, cache for
provider lifetime" pattern. Failure — missing binary, probe exits nonzero,
or WSL1 (detected via `/proc/version` containing `Microsoft` without
`WSL2`, the same distinction Codex's own `Wsl1UnsupportedForBubblewrap`
names) — is the fail-closed case in §5.

### 3.2 Per-call confinement

Each `Runner.Run` invocation wraps the existing argv in a `bwrap` argv
built with: `--unshare-user --unshare-pid --unshare-ipc --unshare-uts
--unshare-cgroup --unshare-net --die-with-parent --new-session --cap-drop
ALL --ro-bind / / --dev /dev --proc /proc --tmpfs /tmp --bind
<workspace-root> <workspace-root> --chdir <cwd>`. This is materially the
same namespace set all three Linux-sandboxing reference projects require
(§3, architecture gate). `--unshare-net` alone denies all network access in
v1 (no proxy-routed exception, per §2); a BPF filter built with
`golang.org/x/net/bpf` and passed via `--seccomp FD` remains available as a
second layer if a future slice needs finer-grained syscall denial, but is
not required to satisfy v1's "no network" goal and is not built in this
slice.

The existing scrubbed environment, argv-only exec, output cap, and
process-group timeout/cancel handling (`TestScrubbedEnv`,
`TestArgvOnlyNoShellExpansion`, `TestOutputCapKillsAndTruncates`,
`TestTimeoutKillsProcessGroup`, `TestCancelKillsProcessGroup`) are
unchanged; bwrap wraps the same argv these tests already exercise.

### 3.3 Memory quota (cgroup v2)

At `composition.Open`, if `/sys/fs/cgroup` is cgroup v2 (unified hierarchy;
detected the same way `xai-grok-tools/cgroup.rs` does, by reading
`/proc/self/cgroup`'s `0::` line), create one child cgroup for the
assembly's lifetime and set `memory.high` (soft) and `memory.max =
memory.high + headroom` (hard), both configurable with documented
defaults. This adopts Grok Build's model, which is the only real resource
QUOTA enforcement — as opposed to permission isolation — found anywhere in
the comparison set:

- Before each `bwrap` child starts, its PID is written to the cgroup's
  `cgroup.procs` (moving the whole confined process tree, since cgroup
  membership is inherited by children) once the child exists but has not
  yet exec'd the target command.
- A background monitor (one per assembly, not per call) watches
  `memory.events` via `inotify` (`golang.org/x/sys/unix`, no CGO) for the
  kernel's `high` counter. When it increments and current usage is still
  above a configurable fraction of `memory.high` (Grok Build's default is
  90%; adopted as this project's default too, configurable), the monitor
  kills the process group with `SIGKILL` — the same mechanism §3.2's
  timeout path already uses — ahead of the kernel's harsher memcg OOM
  killer.
- The cgroup is emptied after each command exits (Grok Build's "reusable
  per-invocation boundary," not a whole-process one); the assembly's own
  process never joins it.
- If cgroup v2 is unavailable (older distribution, no delegated cgroup
  controller, or a container host without nested cgroup support), the
  memory quota is skipped with a startup warning, but this **does not**
  fail `composition.Open` — bwrap's filesystem/process/network confinement
  is the fail-closed-required guarantee; the memory quota is additive
  best-effort, exactly as it is additive and optional in Grok Build itself
  (which reports cgroup fields as `Option` throughout).

### 3.4 Result classification

`tools.CommandResult` gains `ResourceLimited bool`, parallel to the
existing `TimedOut bool` and mutually exclusive with it (a run is killed
for exactly one reason). `internal/harness/application/errors.go` gains
`CodeResourceLimit` and `ToolTextResourceLimit` constants alongside the
existing `CodeExecTimeout`/`ToolTextExecTimeout` pair, following the exact
naming and layering convention already established there — this is a tool
result, not a `RunTurn` error, matching how `CodeExecTimeout` is already
classified.

## 4. macOS mechanism

`sandbox-exec` (ships with macOS; DeepSeek Harness's own documentation
flags it as deprecated with no announced replacement, a risk this design
inherits rather than solves, same as the rest of the industry per the
architecture gate). At `composition.Open`, probe for the binary and run one
scoped invocation; failure is the fail-closed case in §5.

Per call, a `.sbpl` profile is composed in Go as a string (no template
files, matching Codex's and DeepSeek Harness's runtime-composition
approach rather than a static asset): deny-by-default writes
(`(deny file-write*)`) with an explicit allow for the canonicalized
workspace root (`(allow file-write* (subpath "<resolved workspace
root>"))` — canonicalized because, per the architecture gate, "Seatbelt
matches resolved paths"), broad read access (matching Codex's own base
policy, since restrictive reads have historically been the fragile
direction for this mechanism), and `(deny network*)` for v1 (no exception,
matching §3.2's Linux decision).

No cgroup-equivalent mechanism exists on macOS. As a best-effort memory
bound, the child process gets `setrlimit(RLIMIT_AS, ...)` (plain
`golang.org/x/sys/unix`, no CGO) before exec. This is materially weaker
than Linux's monitored cgroup ceiling — `RLIMIT_AS` caps virtual address
space, not resident memory, and the failure mode is the process's own
allocator receiving `ENOMEM` rather than a clean external kill — and is
reported as such in the structured enforcement value (§6), not conflated
with Linux's memory-quota guarantee.

## 5. Windows: fail-closed by default, with a named regression

Windows gets no OS-level sandbox in this slice. `composition.Open` on
Windows therefore fails closed by default — a real behavior change from
today, where `exec` runs unsandboxed but functional on Windows. This is a
capability **regression**, not an incomplete feature, and this design does
not paper over that: it names the trade-off explicitly rather than
resolving it unilaterally.

The mitigation is a single, cross-platform, off-by-default configuration
flag (name to be finalized in the implementation plan; working name
`AllowUnsandboxedExec`) that lets an operator explicitly accept running
without OS-level confinement, on Windows or on any other platform where the
primary mechanism is unavailable (missing `bwrap`, missing `sandbox-exec`,
WSL1). Setting it is loud: a startup log line naming exactly which
guarantee is absent, not a silent fallback.

**Reviewer decision (2026-08-30):** this trade-off — Windows out of scope
for this slice, fail-closed there by default — is explicitly accepted.
Windows is not a near-term priority; a later slice may revisit it.

A follow-up slice implementing Windows Job Objects
(`SetInformationJobObject` with `JOBOBJECT_EXTENDED_LIMIT_INFORMATION`,
reachable via `golang.org/x/sys/windows` with no CGO) could give Windows a
real memory/CPU/process-count quota independent of the harder
filesystem/permission sandboxing problem — noted here as a real, verified
option for later, not decided now.

## 6. Structured enforcement reporting

`localexec.Enforcement` stops being a package-level constant. It becomes a
value computed once at `Runner` construction (after the composition-time
probe in §3.1/§4), reported per effect rather than collapsed into one word,
following the one platform-independent principle this design adopts from
DeepSeek Harness regardless of which OS mechanism applies:

```go
type Enforcement struct {
    Filesystem string // "full" | "none" (bwrap/Seatbelt confines the write surface, or nothing does)
    Network    string // "full" | "none" (v1 has no partial network mode)
    Memory     string // "full" | "partial" | "none" (cgroup ceiling vs RLIMIT_AS vs nothing)
}
```

The existing `TestEnforcementPartial` test and its doc line
(`docs/architecture/tool-runtime.md`) are superseded by this design — the
implementation plan must replace them with per-effect assertions rather
than delete the coverage.

## 7. Verification and acceptance

1. Argv/profile construction is unit-tested without a real `bwrap` or
   `sandbox-exec` present (pure string/slice assertions).
2. An integration suite gated on the real binary being present (`t.Skip`
   when absent, matching existing patterns elsewhere in this codebase
   rather than failing CI on a missing host dependency) proves: a write
   outside the workspace root is denied by the OS, not just by
   `workspacefs`; a network connection attempt fails; the confined process
   cannot see host processes outside its PID namespace (Linux).
3. A memory-quota test spawns a deliberately memory-growing test binary
   under the real cgroup path (Linux only, `t.Skip` off cgroup v2 or
   non-Linux) and asserts `ResourceLimited` becomes true within a bounded
   time, with the process group fully reaped.
4. A fail-closed test forces the availability probe to fail (dependency
   injection, not deleting the real binary) and asserts `composition.Open`
   refuses to start, and that setting the escape hatch flag both logs a
   named warning and allows startup to proceed with `Enforcement` reporting
   `"none"` for the affected effect.
5. `go test -race ./...` and `GOOS=windows GOARCH=amd64 go build ./...` /
   `GOOS=darwin GOARCH=arm64 go build ./...` stay green, per the project's
   standard verification list — plus an explicit `CGO_ENABLED=0 go build
   ./...` check specifically, since this design's central claim is that it
   never needs CGO; that claim must be checked, not assumed.

## 8. Risks

| Risk | Mitigation |
| --- | --- |
| `bwrap` must be pre-installed; v1 does not vendor it | Named exclusion (§2); fails closed rather than silently degrading, and the escape hatch exists for operators who need to keep running before their environment has it |
| Windows loses working (if unsandboxed) `exec` by default | Named explicitly in §5 as a regression; reviewer accepted this trade-off 2026-08-30 — Windows is not a near-term priority. An explicit opt-out still preserves today's behavior for operators who need it |
| WSL1 does not support bubblewrap | Detected and classified distinctly from "bwrap missing" (§3.1), same distinction Codex's own code makes |
| `sandbox-exec` is a deprecated, unreplaced Apple API | Inherited industry-wide risk (architecture gate), not unique to this design; no mitigation beyond monitoring Apple's own deprecation timeline |
| Memory-quota monitor adds a background goroutine and inotify fd per assembly | Scoped to composition lifetime, same lifecycle discipline as the existing heartbeat/exporter loops in `runtime.Host` |
| A structured `Enforcement` value with three effects is more to reason about than one string | This is the direct cost of not overclaiming; the alternative (a single word) is exactly what this design's motivating principle rejects |
