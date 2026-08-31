# Security Policy

## Project status

Open Code Harness is pre-v0 and is not a general availability release. No
version is supported for production deployment, and no security fix backports
exist. Run it only against workspaces and credentials you are willing to lose.

## Reporting a vulnerability

Report privately through GitHub Security Advisories:
<https://github.com/SongYii/open-code-harness/security/advisories/new>

Do not open a public issue for an unpatched vulnerability. Expect an initial
acknowledgement within seven days. Because the project is pre-v0, a fix may
land as an ordinary pull request with the advisory published afterwards.

## Threat model

The harness executes model-proposed tool calls against a local workspace. The
model is treated as untrusted input: prompt injection reaching a tool call is
in scope. The following boundaries are the ones the code actually enforces
today, stated with their limits.

### Enforced

- **Policy is the only authorizer.** `internal/harness/policy` is a pure
  `Decide` table over `Risk x workspace x mode`. It imports no host I/O and
  no tool package, so a tool cannot self-authorize. Default mode allows reads,
  and requires approval for writes and `exec`.
- **Filesystem jail.** `adapters/workspacefs` resolves through
  `filepath.EvalSymlinks` and rejects any path outside the canonical workspace
  root. `Resolve` is a scope probe: it must not create, truncate, or write.
- **Bounded tool input and output.** Arguments are validated against a closed
  JSON Schema subset with explicit size limits, and results are capped and
  marked `Truncated` rather than streamed unbounded.
- **Auditable decisions.** Every authorization outcome is recorded as a
  `policy.decision.recorded` domain event alongside the tool call it governs.
- **Process containment for `exec`.** Commands are argv-only, never shell
  strings. The child runs in its own process group, is killed as a group on
  timeout or cancellation, and receives an environment reduced to `PATH`
  (inherited), `HOME` (the workspace root), and `TMPDIR` (a workspace
  subdirectory removed after exit).
- **OS-level exec confinement when available.** On Linux, `exec` runs under
  `bwrap` (unshared user/pid/ipc/uts/cgroup/network namespaces, every
  capability dropped, a read-only host view with only the workspace root
  rebound writable) when `bwrap` is on `PATH` and a scoped probe succeeds. On
  macOS, `exec` runs under a Seatbelt (`sandbox-exec`) profile denying writes
  outside the workspace root and all network egress when
  `/usr/bin/sandbox-exec` is present and its probe succeeds. This is a fact,
  not an assumption: `Runner.Enforcement()` reports the real per-effect state
  (`"full"`, `"partial"`, or `"none"`), and a missing or non-functional
  backend is the fail-closed case below, not a silent downgrade.
- **A Linux memory quota.** A cgroup v2 child cgroup bounds each command's
  memory (`memory.high`/`memory.max`); a monitored breach kills the process
  group and reports `CommandResult.ResourceLimited` instead of `TimedOut`.
- **Fail-closed startup.** `composition.Open` refuses to start when neither
  OS-level backend is available, unless the operator explicitly sets
  `Config.AllowUnsandboxedExec` (`-allow-unsandboxed-exec` on the `och`
  binary) — a loud, logged opt-out naming exactly which guarantee is absent,
  never a silent fallback.
- **Secret redaction for tool results and assistant text.**
  `internal/harness/redact.Text` scans the exact content becoming a tool
  call's result or failure message (`application/pipeline.go`) and the
  model's final assistant message (`application/loop.go`) for a small,
  hardcoded set of secret-shaped substrings — `Authorization`/`Bearer`
  headers, provider-style `sk-` keys, a generic
  `key`/`token`/`secret`/`password`/`credential` assignment shape, AWS
  access key IDs, GitHub tokens, and PEM private-key blocks — replacing
  each matched value with a `[redacted]` marker before it is persisted as
  a domain event, replicated into the JSONL audit trail, or projected onto
  ACP `session/update` (live or replayed). See the [secret redaction
  contract](docs/architecture/secret-redaction.md).
- **A CPU quota on `exec`, on Linux and macOS.** On Linux, the same
  per-invocation cgroup that bounds memory also gets `cpu.max` set to one
  full core's worth of scheduled bandwidth per 100ms period — a
  kernel-enforced throttle, not a kill, reported via
  `CommandResult.Throttled`. On macOS, `RLIMIT_CPU` (soft 30s / hard 31s)
  is bracketed around the same window as the existing `RLIMIT_AS` bound;
  a process terminated by `SIGXCPU` specifically sets
  `CommandResult.ResourceLimited` — a bare `SIGKILL` alone is not
  attributed to this quota, since it cannot be distinguished from an
  unrelated external kill in the same narrow window. No macOS host has
  run this for real in this repository's own history; it is verified
  only by cross-compilation and code review. See [Tool
  runtime](docs/architecture/tool-runtime.md) and the [exec sandboxing
  and resource quotas evidence
  ledger](docs/architecture/exec-sandboxing-resource-quotas-evidence.md)'s
  CPU quota extension section.
- **The browser trajectory UI bridge (`cmd/acp-web-bridge`) is
  loopback-only and gated by two independent checks.** It binds
  `127.0.0.1` only, hardcoded, with no flag to change it. Every
  WebSocket upgrade (and the `/config` endpoint, which reveals a real
  workspace path) requires both a matching `Origin` header and a
  per-invocation, `crypto/rand`-generated token printed to stderr with
  the ready URL — defending, respectively, against a hostile page in
  another browser tab and against another local account or process on a
  shared host. This is a **local development tool**, not hardened for
  exposure beyond loopback: no TLS, no support for binding elsewhere.
  See [Web trajectory UI](docs/architecture/web-trajectory-ui.md).

### Not enforced

- **Windows has no OS-level `exec` confinement in this slice.**
  `composition.Open` fails closed there by default, the same as any host
  missing its platform's backend; an operator must explicitly accept running
  unconfined via `AllowUnsandboxedExec`. This is a real, named capability gap
  relative to a working Linux/macOS deployment, not an oversight.
- **Confinement mechanisms have real limits even where active.** There is no
  seccomp-level syscall filtering and no Landlock (rejected: it needs CGO for
  correctness on pre-ABI-V8 kernels, which this project's pure-Go constraint
  excludes). macOS's memory bound (`RLIMIT_AS`) is best-effort: it caps
  virtual address space, not resident memory, and a breach surfaces as the
  child's own allocator getting `ENOMEM`, not a clean external kill —
  `CommandResult.ResourceLimited` is never set there.
- **`PATH` is inherited from the host,** so `exec` resolves host binaries.
- **No disk-IO quota, on any platform. No file-descriptor limit.** Even a
  future disk-IO throttle (Linux `io.max`) would only bound throughput
  rate, not total disk space consumed — it would not, on its own, prevent
  a workspace from being filled to capacity by a single large write.
  File-descriptor limits face the same constraint CPU quota did on macOS
  before this project added `RLIMIT_CPU` (Go's `os/exec` has no
  pre-exec hook for an arbitrary child), with no Linux cgroup v2
  controller fallback (`pids.max` bounds process count, not open file
  descriptors).
- **No multi-tenant isolation.** One workspace root per session is a
  correctness boundary, not a security boundary between mutually distrusting
  users.
- **Secret redaction is a small, hardcoded pattern match, not an exhaustive
  scanner.** It catches only the shapes named above; a secret with no
  recognizable prefix (a raw high-entropy string) is not detected. It never
  scans a tool call's own arguments — redacting the actual input before use
  would corrupt what `write_file`/`exec` do with it — or live
  `model.text.delta` streaming chunks, since a secret could span two
  chunks with no buffer to redact against. The Provider API key itself is
  protected structurally instead (never stored past request time, never
  logged), a code-review convention rather than a compiler-enforced type.
- **Durable storage is not encrypted** and its integrity is not chained. The
  audit envelope and digest chain are deferred to a later slice.

Reports that depend only on the "Not enforced" list are documentation issues,
not vulnerabilities, unless they show a bypass of something in "Enforced".

## Dependencies

The only non-test module dependency is `modernc.org/sqlite`, a pure-Go driver
chosen so the durable adapter needs no cgo. Dependency advisories are surfaced
by `govulncheck` in CI.
