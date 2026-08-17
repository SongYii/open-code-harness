# Runtime Host and Crash Recovery — Implemented Contract

**Status:** Implemented; not GA

**Authority:** [Runtime Host and Crash Recovery (Slice 4) design](../superpowers/specs/2026-08-16-runtime-host-recovery-design.md)

**Base:** [SQLite canonical EventStore](sqlite-eventstore.md) — lease mechanism, fencing predicate, `session_heads` projection

**Package:** `internal/harness/runtime`

## Scope

The single Runtime Host over the Application service and SQLite store:
startup reconciliation with deterministic recovery appends, the bounded
heartbeat loop with fencing reactions, graceful shutdown with
matching-pair lease release, the second-host diagnostic, and ownership of
the background audit exporter. The host owns policy only — no domain
rules, no storage authority, no lease-mechanism changes.

## Startup order

`Launch` executes the parent sequence: open (format verification and
migrations run inside), acquire the Runtime lease and fencing token,
enumerate running candidates from `session_heads` (projection, confirmed
by replay), append recovery terminal facts, become ready, then start the
heartbeat loop and the background exporter. Commands are unavailable until
reconciliation completes (`ErrNotReady`); audit export lag never blocks
readiness. A second process that cannot acquire the lease fails with
`ErrLeaseHeld` naming the owner and reconciles nothing.

## Recovery transition

Replay ending with an active Session, a running Turn, and a running
Assistant Item appends one atomic batch: `assistant.message.interrupted
(code = process_crash)` then `turn.interrupted (reason = process_crash)`.
The Session stays active; the original `CommandID` remains the lineage.
The recovery `AppendID` is a deterministic hash in a fixed namespace of
Session, Turn, Item (or the `no_item` sentinel), and `process_crash`, so
a lost acknowledgement retries the exact append and duplicate
reconciliation resolves to the original receipt. A legacy running Turn
with no active Item closes the turn only, with the sentinel in its ID
namespace. An active Item referencing a different turn refuses; a missing
TurnStarted lineage refuses. No automatic model or tool replay of any
kind.

## Heartbeat and fencing reaction

Renewal runs on a bounded interval with a deadline strictly shorter than
the lease duration (validated at launch). A fenced renewal reacts
immediately: admission stops, local work is cancelled through the work
context, and the exporter stops — nothing is deleted and no takeover is
attempted while ownership is uncertain. Transient store unavailability
within the deadline does not revoke ownership: the per-append store
predicate is the authority, not the renewal round-trip. After quiescence
the loop may re-acquire through the normal expired-takeover path (next
monotonic token) and resume admission.

## Shutdown and exporter ownership

`Shutdown` stops admission, cancels in-flight work, waits for the loops
within the caller's bound, and releases the lease by expiring it — the
update matches the owning runtime ID and fencing token exactly, so a
stale host can never release a successor's lease (the Pi rule). The
background exporter starts only after readiness on a bounded cadence and
stops at shutdown.

## Exclusions

- Multiple hosts per database, leader election, clusters.
- Automatic model or tool retries; `retryOfTurnID` lineage recording
  belongs to the Application command layer.
- ACP and TUI surfaces.
- GA blockers: no process-level kill-during-reconcile harness; heartbeat
  evidence is deterministic-time (`testing/synctest`) plus scripted lease
  outcomes, not wall-clock soak; no multi-machine lease anomaly
  (clock-jump) evidence beyond the store's safety-biased predicate.
