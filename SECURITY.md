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

### Not enforced

- **`exec` is not sandboxed.** `adapters/localexec` is bounded `os/exec`, not
  Seatbelt, bwrap, seccomp, or Landlock. An approved command runs with the
  full privileges of the harness process. Network egress is not blocked:
  `curl` from `exec` reaches the internet. Writes outside the workspace are
  not kernel-blocked; only the filesystem tools are jailed.
- **`PATH` is inherited from the host,** so `exec` resolves host binaries.
- **No resource isolation** beyond wall-clock timeout and output caps. There
  are no CPU, memory, file descriptor, or disk quotas.
- **No multi-tenant isolation.** One workspace root per session is a
  correctness boundary, not a security boundary between mutually distrusting
  users.
- **No secret redaction.** Provider credentials live in adapter configuration;
  event payloads and tool results are not scanned for secrets before being
  persisted or emitted.
- **Durable storage is not encrypted** and its integrity is not chained. The
  audit envelope and digest chain are deferred to a later slice.

Reports that depend only on the "Not enforced" list are documentation issues,
not vulnerabilities, unless they show a bypass of something in "Enforced".

## Dependencies

The only non-test module dependency is `modernc.org/sqlite`, a pure-Go driver
chosen so the durable adapter needs no cgo. Dependency advisories are surfaced
by `govulncheck` in CI.
