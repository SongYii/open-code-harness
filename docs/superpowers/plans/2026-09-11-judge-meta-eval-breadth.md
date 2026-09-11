# Judge Meta-evaluation Breadth Implementation Plan

**Design:** [Judge Meta-evaluation Breadth Design](../specs/2026-09-11-judge-meta-eval-breadth-design.md)

1. Add adversarial fixtures for missing declared-role citations and an empty
   determinate rationale; observe both being wrongly accepted before the fix.
2. Preserve each shown evidence path's manifest-role membership in the bundle.
3. Fail closed after strict reference and contradiction handling when a
   determinate response lacks declared-role coverage or a non-empty rationale.
4. Correct the old known-fail fixture so it cites the audit evidence used by its
   continuity result.
5. Synchronize English/Chinese implemented contracts, authority map, GA status,
   and evidence ledger in plain language.
6. Run targeted tests, mutation checks, full race tests, vet, formatting, and
   diff checks before opening the PR.
