# Local Subagent Delegation Implementation Plan

**Design:** [accepted design](../specs/2026-09-12-local-subagent-delegation-design.md)

## Task 1: Durable lineage

1. Add failing Domain tests for optional all-or-nothing parent lineage.
2. Extend `SessionCreated`, `Session`, Decide/Apply, event codec, and clone paths.
3. Prove old event JSON is unchanged and malformed lineage fails closed.

## Task 2: Static tool and configuration

1. Add failing schema/catalog tests for `delegate_task` and its 16 KiB task.
2. Add opt-in Application and Composition configuration with bounded timeout.
3. Prove disabled catalog/Provider wire equality and inconsistent construction
   fails closed.

## Task 3: Child capability projection

1. Add failing tests showing a child request sees exactly `read_file` and
   `list_dir`.
2. Centralize per-Session schema selection.
3. Add a second dispatch-side allowlist test for hidden write, exec, MCP, and
   recursive calls.

## Task 4: Synchronous delegation

1. Add a failing Application scenario for parent tool call → child Session →
   child Turn → bounded result → parent continuation.
2. Implement child creation and recursive `RunTurn` behind the existing tool
   lifecycle, using the caller context and configured timeout.
3. Prove the child gets fresh history plus ordinary workspace instructions,
   and the durable lineage points to the exact parent call.

## Task 5: Failure, cancellation, and concurrency

1. Cover pre-cancel, mid-child cancel, timeout, child Provider failure, and raw
   error canaries.
2. Race two independent parent Sessions and prove lineage/result isolation.
3. Cover unknown parent-result append without a second child execution.

## Task 6: Composition and observability

1. Wire CLI/config and exact catalog behavior.
2. Run a fixture Provider end to end through a real SQLite Assembly.
3. Assert child Turn trace parentage beneath parent tool execution.

## Task 7: Documentation and evidence

1. Publish bilingual implementation contracts using the project five-question
   format.
2. Update current architecture, security, README authority/status tables, and
   docsguard ownership.
3. Run focused race, full tests, vet, cross-build, mutation checks, and record
   every real deviation or unresolved blocker.
