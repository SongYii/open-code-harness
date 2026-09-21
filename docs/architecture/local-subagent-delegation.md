# Local Subagent Delegation

- Status: Implemented contract
- Implemented: 2026-09-12
- Design: [local subagent delegation design](../superpowers/specs/2026-09-12-local-subagent-delegation-design.md)
- Research: [architecture gate](../research/architecture-gates/2026-09-12-local-subagent-delegation.md)
- Evidence: [completion evidence](local-subagent-delegation-evidence.md)
- Chinese reading copy: [本地子代理委派](local-subagent-delegation.zh-CN.md)

## What this solves

A parent agent can hand one bounded research task to a fresh context instead
of filling its own conversation with exploratory file reads. The child is a
real durable Session, so its work can be audited and its exact parent tool call
can be reconstructed after restart.

This is local task delegation, not agent-to-agent networking. It adds no A2A
protocol, remote identity, background swarm, model override, or shared mutable
conversation.

## Operator-visible behavior

Delegation is disabled by default. `och -subagents` adds one builtin:
`delegate_task({"task":"..."})`. `-subagent-timeout` sets a 5-second through
10-minute bound; the enabled default is two minutes.

The call blocks until one child Turn finishes. Success returns:

```text
child session: <session-id>
<final child answer>
```

The parent-visible text uses the existing 64 KiB Tool Result bound. Timeout
and other child failures become stable `subagent_timeout` or
`subagent_failed` tool failures without raw error text.

## How it is implemented

Application creates a normal child Session in the same admitted workspace and
runs its first Turn through the same `Service`, Provider, Context Engine,
EventStore, instruction reconciliation, and telemetry ports. `session.created`
optionally stores an all-or-nothing parent tuple: parent Session, Turn, tool
Item, and model Call ID. Old events omit the field and decode unchanged.

The child starts with only the task; parent conversation history is not
copied. It still receives the versioned system prompt and workspace
instructions through the ordinary request path.

## Capability and cache boundary

The parent catalog is unchanged unless the feature is enabled. Therefore the
disabled Provider request and prompt-cache identity stay unchanged. When
enabled, parent requests add `delegate_task`; child requests expose exactly
`read_file` and `list_dir`.

That restriction exists twice. Schema projection hides every other tool, and
dispatch independently rejects writes, edits, exec, MCP, approval, and nested
delegation even if a malformed or adversarial Provider emits such a call.
Children have separate observed-file state and cannot authorize a parent
mutation by reading a file.

## Cancellation, durability, and tracing

The caller context and configured timeout own the child. Cancellation before
publication creates no child. Cancellation after publication terminalizes the
child Turn and then the parent through their existing durable interruption
paths. There is no goroutine or background work left behind.

Child Session creation is its own publication boundary; the project does not
claim a transaction across parent and child streams. Once published, lineage
survives even if the parent Tool Result later has unknown commit outcome. The
parent execution map prevents the child invocation from running twice.

Telemetry context is preserved across the synchronous call, so the child
`och.turn` is a child of the parent `och.tool.execute` span. No trace context
is sent to the Provider.

## Problems found while implementing it

- The first version reused one allowlist helper for schema projection and
  dispatch. A single mistaken edit could widen both doors together, so the two
  closed checks now have separate code and separate mutation tests.
- A real SQLite assembly test initially searched Go debug formatting for a
  newline-bearing instruction. Debug formatting escaped the newline, so the
  assertion now inspects actual message text.
- Byte truncation could cut a multi-byte child answer in the middle of a rune.
  The shared Tool Result boundary now backs up to valid UTF-8.

## Current limits

One call creates exactly one foreground child at depth one. There is no
background mode, wait/send/interrupt tool family, parent-history fork,
continuation, role or model override, child write approval, remote agent,
swarm scheduler, or automatic crash restart. These require separate evidence
and must not silently widen this contract.
