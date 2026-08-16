# Runtime Host and Crash Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the single Runtime Host over the Application service and SQLite store: crash reconciliation with deterministic recovery appends, the bounded heartbeat loop with fencing reactions, graceful shutdown, the second-host diagnostic, and ownership of the audit-exporter lifecycle.

**Architecture:** The host owns policy only — the lease mechanism, fencing predicate, and `session_heads` projection come from Slice 2; reconciliation reads canonical streams and appends terminal facts through the normal port. No domain rules and no storage authority live in the host package.

**Tech Stack:** Go 1.26, `testing/synctest` for deterministic time, standard concurrency primitives, race tooling.

## Global Constraints

- Normative specification: `docs/superpowers/specs/2026-08-16-runtime-host-recovery-design.md`; research evidence: the Slice 4 architecture gate.
- Package `internal/harness/runtime` imports Application, Domain, the SQLite adapter, and the standard library only; the architecture dependency test extends to it.
- The EventStore port, error algebra, domain events, and conformance suites must not change.
- Commands are unavailable until reconciliation completes; export lag never blocks readiness.
- Recovery appends terminal facts only, with deterministic `AppendID`s from the fixed namespace (`SessionID`, `TurnID`, `ItemID` or the `no_item` sentinel, `process_crash`); exact retry after a lost acknowledgement reuses the `AppendID`.
- No automatic model or tool replay of any kind.
- Heartbeat interval and deadline are bounded and configured; the deadline is strictly shorter than the lease duration.
- Every behavior is TDD. Every task ends with `gofmt`, focused tests, `go test ./... -count=1`, race when concurrency changes, review, and one small commit.
- English is normative; the Chinese plan is a synchronized reading copy committed together.
- Slice 3 must be merged before Task 4 (exporter wiring); Tasks 1–3 are independent of Slice 3.

## File map

| Path | Responsibility |
| --- | --- |
| `internal/harness/runtime/doc.go` | Package scope: one host, policy only |
| `internal/harness/runtime/reconcile.go` | Candidate enumeration, replay confirmation, recovery batch construction |
| `internal/harness/runtime/reconcile_test.go` | The reconciliation matrix |
| `internal/harness/runtime/host.go` | Startup order, admission gating, readiness, shutdown |
| `internal/harness/runtime/heartbeat.go` | Bounded renewal loop and fencing reaction |
| `internal/harness/runtime/heartbeat_test.go` | Deterministic-time heartbeat matrix |
| `internal/harness/runtime/host_test.go` | Lifecycle, diagnostics, exporter wiring |
| `internal/harness/architecture/dependencies_test.go` | Import rules for the new package |
| `docs/architecture/runtime-host.md` | Implemented contract |
| `docs/architecture/runtime-host-evidence.md` | Commits, verification, exclusions |

---

### Task 1 (PR 1): Reconciliation

**Files:**
- Create: `doc.go`, `reconcile.go`, `reconcile_test.go`

**Steps:**
- [ ] Failing test: a stream ending with an active Session, running Turn, and running Assistant Item produces exactly one recovery batch (`assistant.message.interrupted` + `turn.interrupted` with `process_crash`), the Session stays active, and the original `CommandID` lineage is preserved.
- [ ] Failing test: the recovery `AppendID` is a deterministic function of the fixed namespace inputs; two reconciliations derive the same ID.
- [ ] Failing test: replaying the same stream after recovery appends nothing (idempotence through exact-retry semantics); a lost recovery acknowledgement resolves to the original receipt.
- [ ] Failing test: a legacy running Turn with no active Item appends only `turn.interrupted` with the `no_item` sentinel `AppendID`.
- [ ] Failing test: a running Turn with a missing, terminal, mismatched, or multiple active Item reference returns `StoreCorrupt` and repairs nothing.
- [ ] Failing test: a clean stream is a no-op; candidates from `session_heads` that replay confirm no longer running are skipped.
- [ ] Commit `runtime: crash reconciliation with deterministic recovery appends`.

### Task 2 (PR 2): Host skeleton

**Files:**
- Create: `host.go` + tests in `host_test.go`

**Steps:**
- [ ] Failing test: startup executes the parent order — open, migrate, acquire lease, enumerate, reconcile, then readiness; commands before readiness fail with a stable classified error.
- [ ] Failing test: a second process that cannot acquire the lease exits startup with a stable diagnostic naming the owning runtime; it reconciles and exports nothing.
- [ ] Commit `runtime: host startup order, admission gating, and readiness`.

### Task 3 (PR 3): Heartbeat and fencing reaction

**Files:**
- Create: `heartbeat.go`, `heartbeat_test.go`

**Steps:**
- [ ] Deterministic-time failing test: renewal succeeds within the interval while owned; expiry fences appends via the existing store predicate.
- [ ] Failing test: failed renewal stops admission immediately, requests cancellation of in-flight work through the Application service, and stops the exporter; nothing is deleted; no takeover is attempted.
- [ ] Failing test: re-acquisition after quiescence takes the next token through the normal expired-takeover path.
- [ ] Failing test: heartbeat configuration bounds (deadline strictly shorter than lease duration) are validated.
- [ ] Commit `runtime: bounded heartbeat with fencing reaction`.

### Task 4 (PR 4): Shutdown, diagnostics, exporter wiring

**Files:**
- Modify: `host.go`; extend `host_test.go`

**Steps:**
- [ ] Failing test: graceful shutdown stops admission, cancels in-flight work with a bounded wait, stops the exporter at a segment boundary, and releases the lease by expiring it; a successor takes the next token.
- [ ] Failing test: the exporter starts only after readiness and its lag never blocks readiness.
- [ ] Extend the architecture dependency test to `internal/harness/runtime`.
- [ ] Commit `runtime: graceful shutdown, diagnostics, and exporter wiring`.

### Task 5 (PR 5): Docs and evidence

**Steps:**
- [ ] Publish the implemented contract and evidence ledger (bilingual); update the README index and milestone status.
- [ ] Final gates: `gofmt`, `go vet ./...`, `go test ./... -count=1`, `-race`, CGO-free three-OS builds; commit `runtime: contract and evidence`.

## Final completion gate

- Reconciliation matrix complete with deterministic IDs and idempotence; heartbeat matrix deterministic under `synctest`; startup order auditable; second-host diagnostic stable; shutdown ordering proven; exporter lifecycle owned; contract and evidence published with exclusions visible.
