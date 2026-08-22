# Composition Root — Implemented Contract

**Status:** Implemented; not GA

**Date:** 2026-08-19

**Authority:** [Composition Root and Cross-Adapter Conformance (Slice 5) design](../superpowers/specs/2026-08-19-composition-root-conformance-design.md)

**Evidence:** [Composition root completion evidence](composition-root-evidence.md)

**Packages:** `internal/harness/composition`, `internal/harness/adapters/system`, `cmd/och`

## Scope

`composition` is the single place where concrete implementations are named and
wired into a running assembly. It is a library, so assembly is asserted by
tests rather than by launching a process; `cmd/och` is a thin binary over it
containing only flag parsing and signal handling.

The package constructs. It contains no domain transition, no retry or
admission policy, and no branch that exists only for tests. Every bound it
applies is forwarded from the component that already owns it. It introduces
exactly one bound of its own: how long `Close` may wait.

## Assembly

`Open(ctx, Config) (*Assembly, error)` validates the configuration, reads the
provider credential from the environment, and then constructs in a fixed
order: Runtime Host — which opens the SQLite store and completes startup
reconciliation — then the provider model and turn runner, then the workspace
filesystem and command runner, then the tool catalog, then the Application
service. The service receives the SQLite store as an `AuthoritySource`, not
a `WriterAuthority` snapshot, so an expired-takeover fencing-token rotation
is visible on the next append.

`Open` never returns a non-nil `Assembly` with a non-nil error. Every failure
after the host has launched releases the host before returning, so a failed
assembly never leaves a lease held or a database locked. When a release itself
fails, both errors are joined rather than one replacing the other.

`Assembly` exposes `Service()`, `Host()`, and `Store()` as read-only
accessors. It owns every resource it returns.

`Close()` stops admission, waits for the host's loops within
`Config.ShutdownTimeout` (default 10s), releases the lease, and closes the
store. It is idempotent: a second call returns the first result rather than
shutting down again, which would release a lease the assembly no longer owns.
Abandoning an `Assembly` without `Close` leaks the SQLite handle and the host
goroutines; this is stated, not defended against.

## Configuration

`Config.Validate` is total and fail-closed: every field is checked before any
resource is constructed, so a rejected configuration creates no database file
and acquires no lease. Errors name the field and are not wrapped in an adapter
error type, because they are the caller's mistake rather than a component's
failure.

The provider credential is not a `Config` field. `Provider.APIKeyEnv` names an
environment variable, read at `Open`; a key passed as a literal would reach
test fixtures, shell history, and process listings.

Bounds are forwarded, never redefined: step, tool-call, assistant-byte, and
approval limits come from `application.DefaultConfig`, and a zero value in
`Config.Limits` means the Application default.

The provider profile is `ProfileToolsSupported`. The assembly always enables
the workspace catalog, and Application refuses a catalog whose profile does
not support native tools.

## System ports

`adapters/system` implements `application.Clock` and
`application.IDGenerator`, which had no production implementation before this
slice: only `testkit` satisfied them, and no production package may import
`testkit`.

`Clock` returns UTC. `IDs` draws 128 bits from `crypto/rand` per identifier
and prefixes it by kind for readability; nothing in Domain or Application
parses the prefix, and no code may start doing so. A failure to read the
random source is returned rather than replaced with a weaker source, because
identifiers carry admission and append identity.

## Dependency rule

`composition` is the only package permitted to import anything under
`internal/harness/adapters`, and `internal/harness/architecture` enforces it:

- Every owner except `composition` is forbidden from naming an adapter other
  than itself, asserted exhaustively over every owner and adapter pairing.
- `runtime` is the one narrow exception, permitted `sqlite` and nothing else,
  because the Runtime Host owns the canonical store's lifecycle and its
  `Config` embeds `sqlite.Config`.
- A directory with no declared owner is checked rather than skipped, so a new
  package cannot inherit the composition exception by not being listed.
- `composition` may not import `testkit`. Production wiring must not reach for
  a double.
- `cmd/och` imports `composition` and the standard library only.

## Verification

`TestAssemblyRunsAToolCallingTurnEndToEnd` assembles Domain, the Application
step loop, the SQLite canonical EventStore, the Runtime Host, the
OpenAI-compatible provider adapter, the workspace filesystem, and the policy
engine, and runs one turn in which the model requests `read_file` and answers
from the result.

It exercises real paths: a real database file, the adapter's own HTTP and SSE
handling against a loopback server, and `policy.ModeDefault` rather than an
allow-all bypass. Assertions are made against the durable stream read back
from the database, not the in-memory result, and the stream is replayed to
confirm it reconstructs a session with no turn still running. There is no
network, no credential beyond an environment variable the test sets, and no
sleep-based synchronization.

## Exclusions

- ACP, TUI, MCP, the Context Engine, evaluation, OpenTelemetry.
- Configuration file formats and precedence; `cmd/och` takes flags only.
- Process supervision, restart policy, daemonization, and logging policy.
- Multi-host, multi-workspace, and multi-tenant assembly.
- Generated composition documentation.
- An `Approver`: the assembly supplies none, so any tool the policy table
  routes to approval fails closed. Interactive approval arrives with a client.
- GA blockers: no soak test of a long-lived assembly, no process-level crash
  injection, no verification against a live provider, and no performance
  characterization of the assembled path.
