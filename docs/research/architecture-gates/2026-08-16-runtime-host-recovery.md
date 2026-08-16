# Runtime Host and Recovery Architecture Gate

**Status:** Complete research evidence

**Date:** 2026-08-16

**Scope:** Slice 4 (Runtime Host and crash recovery) primary-source
re-verification. Records the then-public host, lease, crash-detection, and
reconciliation contracts of the required comparison set and the adopt/reject
boundary for the host gate.

This document is research evidence. It does not change the lease mechanism
delivered in Slice 2 and does not authorize copying reference-project types.

English is the normative research record. The Chinese file is a synchronized
reading copy.

## Questions

1. Do the reference implementations confirm the accepted host design —
   single host per database, fencing on every append, reconcile-before-
   command startup, terminal-fact-only recovery?
2. Which observed crash-detection and liveness mechanisms should Slice 4
   adopt, and which are insufficient?
3. Is there primary-source evidence that advisory locks alone are unsafe
   (beyond the Slice 2 gate's citation)?

## Re-verified primary sources

| Source | Observed state |
| --- | --- |
| [Grok Build](https://github.com/xai-org/grok-build) | `5163763`, 2026-08-15 |
| [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) | `47f9438`, 2026-08-13 |
| [Maka](https://github.com/maka-agent/maka-agent) | `2e3c82e`, 2026-08-16 |
| [OpenAI Codex](https://github.com/openai/codex) | `9ded177`, 2026-08-16 |
| [Pi agent core](https://github.com/badlogic/pi-mono) | `d3ab2af`, 2026-08-16 |
| [Kleppmann, "How to do distributed locking"](https://martin.kleppmann.com/2016/02/08/how-to-do-distributed-locking.html) | 2016-02-08, theory authority |

## Observed contracts and boundary

| Source | Observed contract | Slice 4 decision | Boundary |
| --- | --- | --- | --- |
| Pi | `writer_lease(owner_id, fence, expires_at_ms)` in SQLite: "`writer_lease` enforces the single-writer rule... `open()` acquires the claim, storage renews it on appends and while idle, and close stops renewal after the queue drains and deletes only its matching `(owner_id, fence)` pair — so a stale owner cannot release the replacement that succeeded it." | Independent confirmation of the Slice 2 lease shape by the only reference that implements fencing. Adopt the close-releases-only-matching-pair rule for graceful shutdown. | Do not copy their renewal-on-append policy; our heartbeat owns renewal. |
| Pi | "a JSONL session opened twice is corrupt and undetected" — the failure mode their lease exists to prevent. | Confirms the charter's single-writer stance. | — |
| Kleppmann | "the GC pause lasts longer than the lease expiry period, and the client doesn't realise that it has expired"; the fix is "a fencing token is simply a number that increases" and "the storage server... rejects the request" with a stale token; "provided that the lock service generates strictly monotonically increasing tokens." | Theory authority for the already-implemented per-append token predicate. Lease expiry alone never revokes an in-flight write; only token-checked storage rejects it. | — |
| Grok Build | Crash markers: "Tracks open TUI sessions in `~/.grok/active_sessions.json` for crash recovery. Clean exit removes the entry; crash leaves it behind. On next launch, `collect_crashed` finds orphaned entries (dead PIDs)." | Adopt the clean-exit-releases shape (our graceful shutdown expires the lease). | PID liveness is point-in-time with "no heartbeat, no lease, no fencing token" — insufficient as our authority. |
| DeepSeek Harness | "A backend that reloads a log crashed mid-turn finds an open `turn/start` with no `turn/end`. It does not truncate… it closes the orphaned turn with a synthetic `turn/end { reason: { kind: 'interrupted' } }`"; "`interrupted` is the one TurnEndReason no loop emits." | Confirms synthetic closure of orphaned turns exactly matches our `process_crash` recovery facts. | — |
| DeepSeek Harness | "Repair applies only to cold sessions... an open live turn rejects rather than receiving synthetic interruption boundaries"; the end-seed marker is "NOT a liveness signal about other writers." | Adopt cold-only repair: reconciliation runs at startup after lease acquisition, never against a live owner. | Their multi-wrier deferral ("needs a signal beyond the log") is solved by our lease; do not adopt log-shape liveness. |
| Maka | "Resume Is Not Retry—How Maka Continues Safely from Crash Facts"; "Resume never resurrects the old process or disguises 'try again' as recovery."; "Desktop startup repairs state before it invokes a model"; parked executions are a "Permanent v1 stop; no second attempt." | Confirms reconcile-before-command ordering and terminal-fact-only recovery with no automatic retry. | Their parked-state machinery is unnecessary for us: our recovery is one terminal batch, not a resumable operation park. |
| Codex | Advisory `flock` writer locks with one-shot stale cleanup; open issue [#36869](https://github.com/openai/codex/issues/36869): "Thread metadata updates and unarchive can bypass the per-thread writer lock." | Second direct evidence (after the Slice 2 gate) that advisory locks without fencing are incomplete enforcement. | — |

## Rejected shapes

1. **PID liveness as the liveness authority** (Grok Build): point-in-time
   checks with no heartbeat cannot detect a wedged owner.
2. **Log-shape liveness inference** (DeepSeek Harness's explicit gap): an
   unmatched marker in the log is lifecycle evidence, never a live-writer
   signal.
3. **Resumable parked operations** (Maka's current stopgap): our recovery
   closes the execution; it does not park it for continuation.
4. **Renewal-on-append instead of a heartbeat** (Pi): append quiescence
   must not extend a lease silently; the host owns renewal explicitly.

## Findings

### F1. The accepted host design is confirmed on every axis

Single host per database, fencing tokens validated by storage on every
append, reconcile-before-command startup, synthetic `process_crash` closure
of orphaned executions, and no automatic replay all have direct
primary-source or theory support.

### F2. Slice 2's lease is the only fenced lease among the references except Pi

Pi's `writer_lease` independently matches our shape (owner, fence,
expiry, matching-pair release). Every other reference uses advisory locks,
PID checks, or defers multi-writer entirely.

### F3. Adopt list

Cold-only repair after lease acquisition; clean-exit lease release
(matching-owner rule); reconcile-before-model startup order;
terminal-fact-only recovery; no-retry parking of any kind.

### F4. Reject list

PID liveness as authority; log-shape liveness inference; parked resumable
operations; renewal-on-append.

## Evidence limits

- Observations are point-in-time at the listed commits; Maka's reconciler
  is documented future work, not implementation.
- No runtime testing of reference projects was performed.
- This gate implements nothing; the Slice 4 specification and plan cite it.
