# Runtime Host and Crash Recovery (Slice 4)

**Status:** Draft pending architecture-gate evidence

**Date:** 2026-08-16

**Parent:** [Production Runtime Persistence, Recovery, and Client Boundary](2026-08-13-runtime-persistence-recovery-client-design.md)

**Gate:** `docs/research/architecture-gates/2026-08-16-runtime-host-recovery.md` (evidence pending; this spec freezes slice decisions against the parent design, sections 11 and 7.8)

**Implemented base:** [SQLite canonical EventStore](../../architecture/sqlite-eventstore.md) — the lease mechanism, fencing predicate, and `session_heads` projection are already delivered

## 1. Decision summary

Slice 4 builds the single Runtime Host over the implemented Application
service and SQLite store: startup reconciliation, the heartbeat loop with
fencing reactions, graceful shutdown, and the background audit-exporter
lifecycle. The store mechanism exists; this slice supplies the host policy.

Load-bearing slice decisions:

1. Package `internal/harness/runtime` composes the Application service, the
   EventStore, and the audit exporter. It owns no domain rules and adds no
   storage authority.
2. Commands are unavailable until startup reconciliation completes; audit
   export lag never blocks Runtime readiness.
3. Recovery appends terminal facts only, with deterministic `AppendID`s
   from a fixed namespace, and never replays a model or tool effect.
4. A host that cannot confirm lease ownership stops admitting executions
   and cancels local work; a fenced host cannot append because every
   transaction validates its token.
5. A second process that cannot acquire the lease exits with a stable
   diagnostic.

## 2. Goals

- The parent's startup order as one auditable sequence.
- Crash reconciliation that closes interrupted executions exactly once.
- A heartbeat loop with bounded deadlines and fail-stop semantics.
- Graceful shutdown that cancels work within bounds and releases the lease.
- Exporter lifecycle owned by the host (start after readiness, stop on
  shutdown).

## 3. Non-goals

- Multiple hosts per database; leader election across databases; clusters.
- Automatic model or tool retries of any kind.
- ACP, TUI, or any client surface (Slices 5–6).
- Changes to the lease store mechanism delivered in Slice 2.

## 4. Startup order

Exactly the parent section 11.2 sequence:

```text
open database → verify format and run migrations
→ acquire Runtime lease and fencing token
→ enumerate running candidates
→ ReadStream + replay each candidate
→ append recovery terminal facts
→ begin accepting commands
→ start background JSONL exporter
```

Candidates are enumerated from the `session_heads` projection (status
`active`) and confirmed only by authoritative stream replay — the
projection is never accepted as independent proof, per the Slice 2
contract.

## 5. Recovery transition

Authoritative replay that ends with an active Session, a running Turn, and
a running Assistant Item produces one atomic recovery batch:

```text
assistant.message.interrupted(code = "process_crash", message = "")
turn.interrupted(reason = "process_crash")
```

- The Session stays active; the original `CommandID` remains the
  correlation lineage.
- The recovery `AppendID` is derived deterministically in a fixed namespace
  from Session ID, Turn ID, Item ID, and `process_crash`; a lost recovery
  acknowledgement is retried as the exact same append and resolves to the
  original receipt.
- Duplicate reconciliation returns the receipt or observes the existing
  terminal state; it cannot add a second terminal pair.
- A legacy stream with a running Turn and no active Item appends only
  `turn.interrupted` with the same namespace plus an explicit `no_item`
  sentinel.
- A running Turn whose active Item reference is missing, terminal,
  mismatched, or multiple is not repaired: reconciliation reports
  `StoreCorrupt`.
- No long-lived `recovering` domain state is introduced.

## 6. No automatic replay

A running event cannot reveal whether the old process crashed before
sending a request, mid-stream, after provider completion, during terminal
commit, or after commit with a lost acknowledgement. Automatic repetition
can duplicate cost, answers, file edits, or remote effects. Recovery only
closes the uncertain execution; a new user intent creates new identities
and may record `retryOfTurnID` lineage.

## 7. Heartbeat and fencing reaction

- The host heartbeats through the store's `RenewLease` on a bounded
  interval with a deadline strictly shorter than the lease duration.
- Failure to confirm ownership: the host immediately stops admitting new
  executions, requests cancellation of in-flight local work through the
  Application service, and stops the exporter. It does not delete anything
  and does not attempt takeover.
- Renewal failing because the lease expired means the host was wedged or
  suspended; it re-acquires only through the normal expired-takeover path
  (token increments), and only after in-flight work has quiesced.
- Clock anomalies favor safety: an early revocation or delayed takeover is
  preferable to two live tokens.

## 8. Shutdown and second host

- Graceful shutdown: stop admission, cancel in-flight work with a bounded
  wait, stop the exporter at a segment boundary, then release the lease in
  one transaction by expiring it — matching the owning runtime ID and
  fencing token exactly, so a stale host can never release a successor's
  lease (the Pi rule confirmed by the gate). Successor takeover takes the
  next token.
- A second process that cannot acquire the lease exits with a stable,
  classified diagnostic naming the owning runtime; it performs no
  reconciliation and no export.

## 9. Testing evidence

1. Startup reconciliation matrix: interrupted assistant item, `no_item`
   legacy turn, already-recovered stream (idempotence), malformed item
   reference (`StoreCorrupt`), clean stream (no-op).
2. Deterministic `AppendID` derivation and exact-retry after a lost
   recovery acknowledgement.
3. Heartbeat: renewal success, expiry fencing, stop-admission reaction,
   cancel propagation to a running execution.
4. Graceful shutdown ordering and lease release; successor takeover token
   increment.
5. Second-process diagnostic.
6. Exporter lifecycle: starts after readiness, stops at shutdown, lag does
   not block readiness.
7. Deterministic-time tests where possible (`testing/synctest`), plus
   concurrency race gates.

## 10. Delivery plan

1. **Reconciliation** — candidate enumeration, replay confirmation,
   deterministic recovery appends, the full matrix.
2. **Host skeleton** — startup order, admission gating, readiness.
3. **Heartbeat and fencing reaction** — bounded loop, stop-admission,
   cancellation propagation.
4. **Shutdown, diagnostics, exporter wiring** — release, second-host
   diagnostic, exporter start/stop.
5. **Docs and evidence** — implemented contract, evidence ledger,
   bilingual, index updates.

## 11. Exclusions

No multi-host coordination; no model/tool replay; no lease-mechanism
changes; no client protocol; no new domain events beyond the recovery
terminal facts already defined by the Domain.
