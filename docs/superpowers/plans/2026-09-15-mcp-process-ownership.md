# MCP process ownership: internal execution slice

- Status: Implemented and verified in the working tree; Provider HTTP2 blocker resolved on 2026-09-16
- Baseline: Provider closure committed locally as `b46b48e`
- Scope: local stdio process ownership, not an execution SDK or remote backend
- Evidence: [verification and regression history](../../architecture/mcp-process-ownership-evidence.md), [HTTP2 repair](../../architecture/provider-http2-shutdown-evidence.md)

## Ownership and implementation

| Resource | Owner after this slice |
| --- | --- |
| One-shot command execution and bounded capture | Existing localexec Runner; unchanged |
| Long-lived stdin/stdout, Start bracket, quota, sole Wait, process-group shutdown, temporary directory | localexec managed stdio process |
| MCP framing, handshake, discovery, calls and session | MCP adapter using the pinned SDK IOTransport |
| Workspace file operations | Existing workspacefs adapter; unchanged |
| Admission, draining, cleanup deadline, lease abandonment/release | Existing Composition/runtime |

1. Replace the consumer's raw exec.Cmd port with Start plus byte I/O and
   idempotent Close. Keep the port internal and consumer-owned. Composition
   supplies the local implementation; neither adapter imports its sibling.
2. Retain shared command admission/confinement. The managed process owns one
   Wait, closes stdin before escalation, proves leader and process group gone,
   closes pipes, and releases command resources. Cache cleanup errors. Startup
   context does not become the successfully started server's lifetime context.
3. Route SDK connection closure and failed handshake through that same Close.
   Propagate unproven cleanup to Composition, including startup failure, so it
   cannot release the lease as though teardown succeeded.
4. Remove MCP's os/exec exception and prohibit OS process dependencies there.
   Verify protocol behavior, actual process trees, cancellation/start failure,
   concurrent/idempotent close, safe confinement and lease behavior under race.
5. Update authoritative MCP contracts and bilingual guides; record commands,
   negative controls and limitations. No paid model calls or remote git writes.

## Boundaries

Only local POSIX process supervision is implemented. Unsupported platforms
must fail before spawning a managed server, not claim process-tree cleanup.
No container/SSH backend, hot loading, registration, public DTO, OTel change,
new durable schema or generic file/command mega-interface is included.
