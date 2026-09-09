# Current System Architecture Closure Implementation Plan

- Status: Implemented
- Date: 2026-09-09
- Design: [Current System Architecture and Boundary Closure](../specs/2026-09-09-current-system-architecture-design.md)

## Task 1: make production ownership fail closed

- Add explicit `contextengine` and `redact` owners.
- Replace the unowned-package import fallback with a direct classification
  failure.
- Exclude only named reusable test-support directories.
- Add focused classification regressions.

## Task 2: publish and enforce the dependency matrix

- Encode each owner's allowed internal package roots.
- Preserve the narrower host/network restrictions.
- Keep the deliberate Runtime-to-SQLite exception visible.
- Include MCP in the exhaustive adapter ownership test.
- Run the production source walk against the matrix.

## Task 3: close the whole client boundary

- Scan all of `internal/client`, not only `internal/client/acp`.
- Scan both independent client commands.
- Forbid Harness-to-client imports in the reverse direction.
- Prove the rule with a temporary mutation under `acpweb`.

## Task 4: publish the as-built architecture

- Add the English implemented contract and Chinese reading copy.
- Record layer/package ownership, dependency direction, process boundaries,
  control flow, data authority, and deliberate exceptions.
- Link detailed subsystem contracts instead of duplicating them.
- Publish the contract from the root README and documentation authority table.

## Task 5: verify and record evidence

- Run focused architecture and documentation tests.
- Mutate an unowned package and a client-to-Harness import; require both to
  fail for the intended reason, then remove the probes.
- Run `go vet ./...`, the full race suite, and `git diff --check`.
- Record commands and results in the completion evidence ledger.
