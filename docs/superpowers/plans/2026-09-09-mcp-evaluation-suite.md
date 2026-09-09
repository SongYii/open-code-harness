# MCP Evaluation Suite Implementation Plan

**Design:** [MCP Evaluation Suite Design](../specs/2026-09-09-mcp-evaluation-suite-design.md)

1. Extend and validate frozen Subject MCP configuration; map it into
   Composition only for in-process execution. Add capability pairing tests.
2. Add evidence-only MCP verifiers and mutation-focused unit tests.
3. Add the real stdio fixture server, deterministic provider branches,
   checked-in Subject/Executor/Scenarios/EvalSet, and an end-to-end test.
4. Add the consent-gated DeepSeek-compatible live example without running it.
5. Update the evaluation/MCP contracts, reading copies, evidence ledgers,
   authority table, milestone status, and docsguard assertions.
6. Run targeted tests, full race tests, vet, tidy-diff, formatting, and diff
   checks; record actual evidence and open a reviewable PR.

