# Provider HTTP2 shutdown repair

- Status: Implemented and verified in the working tree; not yet committed
- Authorized: user requested the repair on 2026-09-16
- Scope: private HTTP transport connection ownership; preserve pending MCP work
- Evidence: [repair ledger](../../architecture/provider-http2-shutdown-evidence.md)

The previous H2 cancellation/socket-close test failed in four of twenty runs.
Canceled body Close can return before net/http retires its H2 stream, while
CloseIdleConnections skips a connection with streams still present. Changing
the test timeout, retrying the idle sweep or downgrading to HTTP1 is not a fix.

1. Both builtin adapters attach a private connection owner only AFTER transport
   creation/cloning. A small shared adapter-internal helper handles connection
   lifetime, not protocol, auth, routing, state, tools or SDK registration.
2. After application drain, reject new dials, cancel/account for pending dials,
   close owned sockets regardless of transport idle classification, and cache
   errors. A late dial cannot reopen the model; its cleanup is accounted for
   before Close succeeds. Composition retains its existing shared timeout and
   lease-abandonment policy for unproven cleanup.
3. Preserve concrete custom TLS connections for actual H2 negotiation. Own
   their underlying sockets; optional GC cleanup reclaims discarded TLS
   bookkeeping, never substitutes for explicit owner Close.
4. Retain original H2 assertions. Add deterministic non-idle/late-dial/error
   tests, custom TLS/source-pool checks, architecture allowlist tests and source
   mutation controls. Run affected-package and full-repository race regressions.
5. Synchronize bilingual contracts and resolve the MCP acceptance warning only
   after actual regression success. No paid calls, public SDK or remote writes.
