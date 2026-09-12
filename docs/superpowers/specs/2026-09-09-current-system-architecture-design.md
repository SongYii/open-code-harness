# Current System Architecture and Boundary Closure

- Status: Accepted
- Date: 2026-09-09
- Scope: an as-built architecture map and fail-closed package boundaries
- Charter: [Foundational architecture](2026-08-11-open-code-harness-architecture-design.md)

## 1. Problem

The foundational charter still states the intended shape of the product, and
each delivered slice has an implemented contract. The repository does not,
however, have one authoritative description of the system that exists now.
Readers must reconstruct control flow, state authority, and package ownership
from many subsystem documents.

The executable dependency gate has the same gap. Most `internal/harness`
directories have an explicit owner, but an unknown production directory falls
back to a permissive rule. `contextengine` and `redact` currently use that
fallback. Client isolation is also named for, and limited to,
`internal/client/acp`; the independently implemented `acpweb` client is not
covered by that exhaustive boundary.

This makes architectural drift possible without a failing test.

## 2. Decision

Add one **implemented contract** that describes the current system rather than
future milestones. It must contain:

1. a package/layer ownership table;
2. the dependency direction and deliberate exceptions;
3. end-to-end command, model, tool, event, and recovery flows;
4. the authority for durable facts, derived projections, workspace state, and
   process lifecycle;
5. the public protocol and process boundaries; and
6. links to the detailed subsystem contracts that remain authoritative for
   local behavior.

The document is an index and boundary contract, not a duplicate specification.
If a subsystem contract and the overview differ on local behavior, the
subsystem contract wins. If code changes the topology or ownership table, the
overview and the executable boundary registry change in the same pull request.

## 3. Fail-closed ownership

Every production Go directory below `internal/harness` must match an explicit
owner root. A directory containing only tests is not a production directory.
The reusable `testkit` and named contract-test packages are explicit test
support exclusions.

An unclassified production directory is itself an architecture violation,
regardless of what it imports. There is no permissive production fallback.
`contextengine` and `redact` become explicit owners in this slice.

Each owner has a stated lower-level dependency set. Existing host/network
restrictions remain in force. The dependency test is the executable source for
the detailed import policy; the architecture overview publishes the readable
matrix.

## 4. Client boundary

`internal/client` is a separate ACP-client side of the process boundary. No
production package anywhere below `internal/client` may import
`internal/harness`, and no production package below `internal/harness` may
import `internal/client`. The rule applies to future client packages without
needing another hand-written directory entry.

Entrypoints retain narrow roles:

- `cmd/och` and `cmd/och-eval` may enter the harness through composition and
  evaluation surfaces;
- `cmd/acp-client` and `cmd/acp-web-bridge` remain independent client-side
  programs and may not import the harness implementation.

## 5. Mechanical documentation coupling

The documentation authority map publishes the new contract, synchronized
Chinese reading copy, and evidence ledger. `docsguard` verifies their presence
and links using the repository's existing rules. The root README links the
current architecture next to the foundational charter so a new contributor
can distinguish “where the project intends to go” from “what exists now.”

The architecture dependency test must also pin these properties with focused
regressions:

- `contextengine` and `redact` classify explicitly;
- a synthetic unknown production package is rejected even with harmless
  imports;
- test-support exclusions remain narrow and path-aware;
- both `acp` and `acpweb`, plus future `internal/client` descendants, are
  isolated from the harness; and
- the actual repository contains no unclassified production harness package.

## 6. Non-goals

- No package moves or public API changes.
- No new abstraction layer or dependency-injection framework.
- No attempt to make the overview generate source code.
- No claim that import rules prove runtime authority, data consistency, or
  protocol conformance; those remain covered by subsystem tests.
- No Windows runtime expansion or OpenTelemetry design.

## 7. Acceptance

The slice is complete when the current architecture contract is published,
all production harness packages are explicitly classified, the whole client
tree is isolated, mutation-oriented regression tests prove the new gates can
fail, `docsguard` passes, and the full race suite remains green.
