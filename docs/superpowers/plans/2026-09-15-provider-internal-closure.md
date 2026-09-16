# Internal Provider Conformance and Ownership

- Status: Baseline committed as `b46b48e`; subsequent HTTP2 repair verified in the working tree
- Date: 2026-09-15
- Approved scope: steps 1–2 of the [Provider design](../specs/2026-09-15-provider-startup-extensibility-design.md)
- No public SDK, registration, configuration flags, durable schema or OTel API/schema changes
- Verification: [evidence and limits](../../architecture/provider-internal-closure-evidence.md)

## Implementation tasks

1. Replace exact chunk-count assertions in `engine/modeltest` with bounded
   semantic consumption: text/order/tools/completion, classified cancellation,
   and successful independent concurrent requests. Retain strict nil metadata
   for scripted/legacy fixtures; explicitly assert Messages usage/replay state.
   Run both real adapters through the shared suite and retain native wire tests.
2. Give builtin models once-owned close for their private HTTP transports.
   Never close injected nonstandard transports or a source transport that was
   cloned. Close is after request drain, not a replacement for cancellation.
3. Make Composition retain the Provider resource and include it in successful
   shutdown and constructor rollback. Reuse one bounded leaf teardown path;
   on failed/unproven cleanup abandon ownership rather than release the lease.
   Keep resource construction synchronous: existing constructors do no I/O.
4. Verify actual idle connections are closed, borrowed transports remain open,
   active calls drain before Provider close, Close is once-owned, and failed or
   blocked cleanup cannot announce successful shutdown/release. Exercise the
   same cleanup primitive on startup and shutdown; no global factory test hook.
5. Run focused race/native/recovery tests, architecture/document guards and the
   full suite as the environment permits. Mutate targeted semantic/ownership
   guards, record real results and any ineffective probes. Synchronize the
   existing contracts, evidence and bilingual implementation guide.

## Completion boundary

### Approved verification follow-up (2026-09-15; complete in the working tree)

The user approved three additional local gates: distinguish concurrent A/B
requests and cancel only A; exercise negotiated TLS/HTTP2 and owned versus
borrowed pools; run the real stock launcher through ACP EOF/SIGTERM during a
conversation or automatic summary, then inspect durable facts and restart.
Keep process-exit evidence separate from in-process Provider.Close evidence:
the OS closing sockets on process death does not prove the model close ran.
No paid calls, public API, registration or durable schema changes are included.

All 22 scenarios passed three repetitions under race detection. The final
Composition race regression, vet, architecture/document guards and formatting
checks passed; three negative controls failed at their intended assertions.
See the evidence ledger for commands, results and platform limits.

Internal cleanup/conformance only. A new public factory, credential lifecycle,
DTO bridge, implementation attribution and actual external consumer remain
separate gates. No paid model calls are required or authorized by this plan.
