# Exec Sandboxing and Resource Quotas Architecture Gate

**Status:** Complete research evidence

**Date:** 2026-08-30

**Scope:** Re-verify, at their then-current state, how this project's established
comparison set actually confines and bounds model-proposed shell/subprocess
execution, ahead of designing the sandboxing and resource-quota subsystem
named first in the
[client surface and security sequencing decision](2026-08-30-client-surface-and-security-sequencing.md).
This gate does not design or implement anything. `SECURITY.md`'s "Not
enforced" list — no OS-level sandbox, no CPU/memory/disk/fd quotas beyond
wall-clock timeout and output caps, PATH inherited from the host, no
multi-tenant isolation — is the problem statement a design following this
gate must address.

English is normative. The Chinese file is a synchronized reading copy.

## What this project already does

`internal/harness/adapters/localexec` (see `SECURITY.md`'s "Enforced"
section): commands are argv-only, never a shell string; the child runs in
its own process group and is killed as a group on timeout or cancellation;
the environment is reduced to `PATH` (inherited from the host), `HOME` (the
workspace root), and `TMPDIR` (a workspace subdirectory removed after exit).
Filesystem tools (`read_file`, `write_file`, `list_dir`) are separately
jailed by `adapters/workspacefs` via `filepath.EvalSymlinks` plus a
canonical-root check. None of this is OS-level sandboxing: an approved
`exec` command runs with the full privileges of the harness process, no
CPU/memory/disk/fd quota exists, and network egress is never blocked.

## Comparison set and pinned commits

Per Documentation rule 8, each was fetched with
`scripts/fetch-reference.sh <owner/repo> <sha>` into the gitignored
`.reference/` directory and read directly — not recalled from memory or
marketing pages.

| Project | Repository | Commit | Observed |
| --- | --- | --- | --- |
| OpenAI Codex | `openai/codex` | `dde85b4` | 2026-08-30 |
| Kimi Code | `MoonshotAI/kimi-code` | `9619277` | 2026-08-30 |
| Grok Build | `xai-org/grok-build` | `bc7f02e` | 2026-08-30 |
| Pi agent core | `badlogic/pi-mono` | `853a80d` | 2026-08-30 |
| Maka | `maka-agent/maka-agent` | `d093ba5` | 2026-08-30 |
| DeepSeek Harness | `deepseek-ai/deepseek-harness` | `cd5ef81` | 2026-08-30 |

This is the same six-project set required by the 2026-08-15 DeepSeek Harness
gate and by Documentation rule 7; no project was added or substituted.

## Per-project findings

### OpenAI Codex — the most complete cross-platform sandbox in this set

Three independent platform backends live in `codex-rs/sandboxing`,
`codex-rs/linux-sandbox`, and `codex-rs/windows-sandbox-rs`, selected by a
`SandboxManager` (`sandboxing/src/manager.rs`).

- **Linux, primary mechanism: bubblewrap.** `linux-sandbox/src/bwrap.rs`
  invokes the vendored `bwrap` binary with `--unshare-user`,
  `--unshare-pid`, `--unshare-net`, `--unshare-ipc`, `--cap-drop`,
  `--die-with-parent`, `--new-session`, `--ro-bind` for the read-only host
  root, and `--tmpfs`/`--bind` for explicit writable roots. Landlock
  (`linux-sandbox/src/landlock.rs`) is explicitly demoted: its own doc
  comment states "Filesystem restrictions are enforced by bubblewrap...
  Landlock helpers remain available here as legacy/backup utilities."
  Landlock's live use today is narrower than filesystem confinement: a
  hand-written seccomp-bpf filter (same file) additionally blocks network
  syscalls (`connect`, `bind`, `listen`, `accept`, `sendto`, raw `socket`
  outside `AF_UNIX`) as defense in depth on top of the namespace's own
  `--unshare-net`, with a distinct "proxy-routed" mode that instead permits
  `AF_INET`/`AF_INET6` only, to reach a local bridge process.
- **macOS: Seatbelt.** `sandboxing/src/seatbelt.rs` composes an Apple
  Sandbox Profile Language (`.sbpl`) document at runtime from a base policy,
  a network policy, and per-call parameters — deny-by-default writes with
  `(allow file-write* (subpath "..."))` allowlist entries — and invokes
  `sandbox-exec`.
- **Windows: restricted token + deny-read walker + WFP.**
  `windows-sandbox-rs` builds a restricted-token child process
  (`spawn_prep.rs`, `proc_thread_attr.rs`), computes a deny-read ACL set by
  walking the filesystem (`deny_read_walker.rs`/`deny_read_resolver.rs`),
  and separately configures the Windows Filtering Platform for network
  rules (`wfp_setup.rs`), with `dpapi.rs` for credential-adjacent secrets.
- **No CPU/memory/disk resource-quota enforcement found** in any of these
  three crates. The one `rlimit`-adjacent hit in `codex-rs` is
  `utils/pty/src/pty.rs` reading `RLIMIT_NOFILE` to size a poll buffer, not
  a quota. Bubblewrap's own `--unshare-cgroup` (present in the vendored
  `bubblewrap.c`) isolates the *cgroup namespace*, which is not the same as
  setting a resource ceiling inside it.

### DeepSeek Harness — fail-closed selection and honest partial-enforcement reporting

`packages/sandbox/{sandbox,sandbox-local,sandbox-windows-acl,sandbox-policy}`
is unusually well-documented (see `sandbox-local/README.md`). Two
platform-independent *principles* stand out more than any single mechanism:

1. **Fail-closed runner selection.** "An unsupported platform or an unusable
   runner fails closed: `confine()` throws `SANDBOX_UNAVAILABLE`... the
   consumer surfaces that error rather than running the command
   unconfined." There is no silent degrade-to-unsandboxed path.
2. **Enforcement level is a reported fact, not an assumed promise.** Every
   backend reports `full` or `partial`: the Windows ACL rung and older
   Landlock ABIs are named `partial` cases, so "a consumer that requires an
   absolute boundary can reject or surface them" instead of the system
   overclaiming what it actually confines.

Mechanically it converges with Codex on the same per-platform choices:
Linux runs bwrap first, Landlock second, in a probed selection chain
(`sandbox-local/src/index.ts` selection logic, described in its README);
macOS uses Seatbelt, again allow-default with `(deny file-write*)` plus a
canonicalized write allowlist (paths are resolved before matching because
"Seatbelt matches resolved paths"); Windows keeps one deterministic write
SID per workspace but a *fresh, random* SID and temp directory per
session — "a fresh provider always chooses a new temp path and SID, so
crash residue cannot block or authorize a resumed session." No CPU/memory
quota mechanism was found in this package group.

### Grok Build — the only real resource-quota enforcement in this set

`crates/codegen/xai-grok-tools/src/computer/local/cgroup.rs` implements an
actual cgroup v2 memory ceiling for spawned commands, not just permission
isolation:

- On startup, a child cgroup is created under the host process's own
  cgroup, with `memory.high` (soft) and `memory.max = memory.high +
  headroom` (hard OOM-kill boundary) configured once.
- Before each spawned command, only that child's PID (and by cgroup
  semantics its whole process-group descendants) is written into
  `cgroup.procs` — "the grok-tools process itself is never inside this
  cgroup — only spawned child commands are," and the cgroup is emptied
  after each command exits, making it a reusable per-invocation boundary
  rather than a whole-process one.
- A background monitor watches `memory.events` via `inotify` for the
  kernel's `high` counter; if RSS is still above 90% of `memory.high` when
  it fires, the monitor treats that as sustained pressure and proactively
  `SIGKILL`s the whole process group, reporting a synthetic exit code 137
  (128 + `SIGKILL`) with signal `"oom"` — a graceful, attributable kill
  ahead of the kernel's own harsher memcg OOM killer.
- This is memory-only: no `cpu.max`, `cpu.weight`, `pids.max`, or `io.max`
  controller appears anywhere in this file or elsewhere in the crate.
  `xai-grok-shell/src/util/limits.rs` separately *reads* `RLIMIT_NOFILE`,
  `RLIMIT_NPROC`, and ambient cgroup pids/memory ceilings for crash
  diagnostics only — it does not set anything.
- `xai-grok-sandbox/src/child_net.rs` additionally installs a seccomp-bpf
  filter (via `pre_exec`) that blocks a sandboxed child from calling
  `clone`/`clone3` with new-namespace flags (`CLONE_NEWUSER`,
  `CLONE_NEWNET`, `CLONE_NEWPID`, etc.) — closing a specific
  sandbox-escape path where a confined child re-unshares its own nested
  namespace.

### Maka — a third independent confirmation of the Linux baseline

`packages/runtime/src/sandbox/linux-capability.ts` probes for a working
`bwrap` by requiring `--seccomp` support plus
`--unshare-user/-pid/-ipc/-uts/-cgroup`, `--ro-bind`, `--proc`, `--dev`, and
`--die-with-parent` in one capability check — the same namespace set Codex
and DeepSeek Harness use, independently arrived at.

### Kimi Code — no dedicated exec-sandboxing subsystem found

`agent-core-v2/src/agent/toolExecutor` and `_base/execEnv` are about
scheduling and environment/shell-path probing, not confinement.
`kap-server/src/security/bindClassify.ts` — the only file under a
`security/` directory — classifies a *bind address* (loopback/LAN/public)
for kap-server's own debug/RPC listener, unrelated to confining
model-proposed commands. This matches the 2026-08-15 gate's characterization
of Kimi Code's relevant contribution as package/transport structure, not
sandboxing; nothing found here changes that.

### Pi agent core — sandboxing is an example, not core

The only "sandbox" match in `badlogic/pi-mono` is
`packages/coding-agent/examples/extensions/sandbox/index.ts`, one example
extension file, not part of the agent core. Consistent with the existing
gate's framing of Pi as "a small injectable loop and cancellation," not a
sandboxing reference; nothing here changes that either.

## Cross-cutting synthesis

- **Linux: bubblewrap (namespace unsharing) is the converged primary
  mechanism**, independently arrived at by all three projects that sandbox
  at all (Codex, DeepSeek Harness, Maka), each requiring materially the
  same namespace set (`user`, `pid`, `net`, `ipc`, and a read-only root with
  explicit writable binds). Landlock appears in two of them only as a
  narrower, explicitly-labeled fallback or as a secondary syscall-level
  layer (seccomp), never as the sole filesystem mechanism.
- **macOS: Seatbelt (`sandbox-exec` with a dynamically composed `.sbpl`
  profile)** is the mechanism in both projects that sandbox on macOS (Codex,
  DeepSeek Harness), with the same deny-default-write-plus-allowlist shape
  in both.
- **Windows has no bwrap equivalent**; both projects that sandbox on
  Windows (Codex, DeepSeek Harness) hand-build an ACL/restricted-token
  mechanism, and both explicitly report it as less than full enforcement
  rather than overclaiming it.
- **No project in this set uses a container or VM for the interactive
  per-command tool-execution path.** DeepSeek Harness's own README draws
  this line explicitly: "Choose a different mechanism when the process must
  run in an isolated environment — a container or remote executor replaces
  whole capabilities, and this provider shares the host kernel and
  filesystem." Namespace/profile-based OS sandboxing, not containerization,
  is the converged pattern for this specific use case.
- **Real resource-quota enforcement (as opposed to permission isolation) is
  rare.** Only Grok Build actually caps a resource with a kernel-enforced
  ceiling (cgroup v2 memory), and only memory — no project in this set
  enforces CPU or disk quotas on a spawned command; the closest anyone else
  gets is reading ambient rlimits for diagnostics.
- **Fail-closed sandbox selection recurs** (DeepSeek Harness's explicit
  `SANDBOX_UNAVAILABLE`; Codex's `SandboxTransformError` refusing malformed
  configuration) and matches this project's own established fail-closed
  posture elsewhere (SQLite corruption classification, ACP validation) —
  low-friction to adopt, not a new value system.
- **Honest partial-enforcement reporting is a named DeepSeek Harness
  pattern with no counterpart found elsewhere in this set**: reporting
  `full`/`partial` per backend rather than a binary sandboxed flag. Worth
  adopting regardless of which OS mechanism a later design chooses, since
  this project's own CGO-free, cross-platform (Linux/macOS/Windows)
  posture all but guarantees uneven enforcement strength per platform from
  day one.

## Open questions a design must resolve, not answered by this gate

- Whether `bwrap`, Seatbelt's `sandbox-exec`, and the Landlock/seccomp Go
  bindings this would require are reachable without breaking this
  project's `CGO_ENABLED=0` constraint (established for the SQLite driver;
  never yet tested against sandboxing primitives). `bwrap` and
  `sandbox-exec` are external binaries invoked via `os/exec`, not linked
  libraries, which is promising but unverified for this codebase's actual
  build/deploy story (bundled binary vs. required host package, per
  Codex's own vendored-`bwrap`-with-`find_system_bwrap_in_path`-fallback
  pattern).
- Whether a Windows backend is in scope for a first slice, given the
  significant, ACL-only, explicitly-partial engineering both reference
  projects needed for it.
- Whether this project wants Grok Build's active memory-quota model (a
  per-command cgroup with a soft/hard threshold and a monitored graceful
  kill) or a simpler bound; no project in this set enforces CPU or disk,
  so those remain genuinely undesigned rather than "adopt project X's
  answer."
- Network-egress policy: none of these mechanisms are optional bolt-ons
  independent of the filesystem sandbox — Codex's own network seccomp
  filter is layered *on top of* bwrap's own `--unshare-net`, meaning a
  Go design likely needs the same two-layer relationship (namespace-level
  default-deny plus an explicit syscall or proxy-routed exception), not a
  standalone network filter.

## Evidence limits

- Every citation above traces to a specific pinned commit read in this
  session; no claim is from memory or from a project's marketing page.
- This gate does not authorize copying any type name, schema, `.sbpl`
  template, or seccomp rule table verbatim from any of these projects —
  only the mechanisms and the platform choice they represent.
- "Current state" here means 2026-08-30. A future gate that revisits any
  of these six projects must re-fetch and re-read, per Documentation
  rule 7, rather than reuse this document's characterization.
- This gate does not choose a design. The next step is a normative design
  for `internal/harness/adapters/localexec`'s sandboxing and resource-quota
  extension, informed by — not dictated by — the findings above.
