# Runtime Host Completion Evidence

**Status:** Complete evidence ledger for Slice 4

**Contract:** [Runtime Host and Crash Recovery — Implemented Contract](runtime-host.md)

**Branch:** `agent/runtime-host-recovery`

## Commits

| Commit | Task | Content |
| --- | --- | --- |
| `056d0ff` | Docs | Bilingual spec and plan (with Slice 3 docs and both gates) |
| (runtime implementation) | Tasks 1–4 | Reconciliation, host skeleton and startup order, heartbeat with fencing reaction, shutdown with matching-pair release, exporter wiring |
| (this ledger) | Task 5 | Contract and evidence |

## Verification evidence

Commands and observed results (Apple M1, go 1.26.4):

- `go test ./... -count=1` — all 13 packages with tests pass.
- `go test ./internal/harness/runtime/ -count=1 -race` — pass (scripted
  lease outcomes are mutex-guarded).
- `CGO_ENABLED=0` builds for linux, darwin, windows.
- Reconciliation matrix: interrupted assistant item closed with the
  terminal pair and original lineage; idempotent second pass (deterministic
  `AppendID`); legacy no-item turn closed turn-only; clean stream no-op;
  `ActiveSessions` enumerates only projected-active sessions; mismatched
  item/turn and missing lineage refuse.
- Determinism: `recoveryAppendID` stable across derivations; session, item,
  and the `no_item` sentinel each change the ID.
- Heartbeat (deterministic time via `testing/synctest`, Go 1.26
  `synctest.Test`): fenced renewal stops admission and cancels the work
  context while the loop keeps polling; transient unavailability within the
  deadline continues; takeover after quiescence regains admission with an
  attempted re-acquisition; configuration bounds validated (deadline
  strictly between interval and lease duration).
- Lifecycle: launch reconciles a crashed database and becomes ready with
  the terminal facts durable; a second process receives a stable
  `ErrLeaseHeld` naming the owner; shutdown releases the lease so a
  successor takes the next token; the background exporter publishes a
  manifest only after readiness.
- Architecture: the runtime package is governed by the dependency rules
  (application, domain, and the sqlite adapter only; no engine, tools,
  policy, testkit, or other adapters).

## Deviations from the accepted design

None. The heartbeat deadline semantics distinguish fenced renewal
(immediate reaction) from transient unavailability (deadline-bounded
tolerance), which the design leaves to implementation and is recorded
here as the frozen behavior.

## Deferred GA blockers

- Process-level crash injection during reconciliation (kill -9 harness).
- Wall-clock soak of heartbeat cadence against real lease expiry.
- Clock-anomaly (jump) evidence beyond the store's safety-biased
  predicate.
- ACP client-driven restart flows (Slice 5 scope).
