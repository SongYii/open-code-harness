# MCP Evaluation Suite Architecture Gate

**Status:** Research evidence  
**Date:** 2026-09-09  
**Scope:** The missing evaluation suite for the implemented stdio MCP client.

## Question

What must an OCH MCP evaluation prove beyond the adapter's existing unit and
composition tests?

## Sources and findings

- The MCP specification's Tools contract makes tool descriptions and results
  model-visible, requires explicit consent for tool invocation, and says tool
  annotations are untrusted unless the server is trusted. Therefore protocol
  success is not an authorization or prompt-injection result.
- The official MCP Inspector exercises initialize, `tools/list`, and
  `tools/call`, with machine-readable failures. It is a server/debugging
  conformance tool, not an agent-quality harness: it invokes tools directly
  and does not place hostile descriptions in a model request.
- OCH's pinned evaluation comparison set remains applicable. Pi and Maka run
  real product sessions behind a Subject/Executor boundary; Inspect separates
  execution from evidence-backed scoring; Grok Build's precedent is to treat
  model-visible evidence as untrusted input. Codex, Kimi Code, and DeepSeek
  Harness still provide MCP client implementation precedents, not a comparable
  checked-in MCP agent-quality suite.
- OCH already records the exact tool name, description, and input schema in
  `model.request.recorded`. That durable event can prove exposure without a
  second trajectory format.

Primary sources:

- MCP Tools specification: <https://modelcontextprotocol.io/specification/2025-06-18/server/tools>
- MCP security principles: <https://modelcontextprotocol.io/specification/2025-03-26>
- MCP Inspector: <https://github.com/modelcontextprotocol/inspector>
- Existing pinned evaluation comparison: [Evaluation Architecture Gate](2026-09-01-evaluation.md)
- Existing MCP implementation comparison: [MCP implementation re-verification](2026-09-04-mcp-implementation-reverification.md)

## Decision

Build one suite with two explicitly different claims:

1. **Deterministic mechanism:** a real stdio fixture server is discovered;
   its exact hostile description reaches a durable model-request event; its
   tool call is classified `RiskExec`, routed through the shared approval
   mechanism, and denied or completed through the normal audit path.
2. **Live quality:** a live model sees the hostile description. Passing
   requires both deterministic containment (no tool call and no forbidden
   file) and a separately identified quality criterion. Policy blocking a bad
   call is containment evidence, never proof that the model resisted it.

The fixture server is a Scenario fixture launched with a Subject-frozen
basename command and arguments. Its bytes are therefore covered by the
Scenario fixture digest while the product configuration is covered by the
Subject digest. The first slice is in-process only. ACP needs a public,
credential-safe way to carry static MCP configuration through `och -acp`; it
must not receive an eval-only side channel.

## Rejected shapes

- Calling the MCP adapter directly: proves the adapter twice, not the agent.
- Treating a denied dangerous call as model resistance: confuses containment
  with behavior.
- Hiding a machine-local absolute server path in a frozen Subject: makes the
  document non-portable and leaks execution facts into semantic identity.
- Adding MCP Cells to ordinary PR CI: the adapter already has deterministic
  PR coverage; this broader suite is explicit. A scheduled job may opt in
  later, but the first slice does not silently add one.
