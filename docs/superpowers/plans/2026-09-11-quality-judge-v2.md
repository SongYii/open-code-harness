# Quality Judge v2 implementation plan

1. Preserve the frozen v1 prompt and every existing configuration/report.
2. Add a separately frozen v2 prompt and resolve prompt bytes by exact ID and
   digest before any provider call.
3. Strictly decode `unresolvedContradictoryEvidence` for v2 while continuing to
   decode the historical field only for v1.
4. Prove resolved contradictions remain Fail, unresolved conflicts become
   Indeterminate, v1 fields are rejected under v2, and the caller receives the
   selected frozen prompt.
5. Freeze six fresh calibration and six fresh holdout cases, bind DeepSeek and
   OpenAI configs to v2, and mechanically prevent overlap with all earlier
   semantic cases or drift between provider holdouts.
6. Synchronize architecture/evidence/operations documentation and run focused,
   package, static, and mutation verification.
7. When a new temporary DeepSeek credential is available, run 18 calibration
   calls, freeze the policy, then run the predeclared 18-call holdout. Run the
   identical OpenAI holdout only when its credential is available.

## Execution state (2026-09-11)

Steps 1–6 are complete. The authorized DeepSeek step completed: calibration
was 17/18 and the disjoint holdout was 15/18, so the frozen gate correctly
failed. The former contradiction ambiguity did not recur; all holdout misses
were one sparsely evidenced Pass label, which is preserved rather than edited
after observation. OpenAI remains unrun because its credential is unavailable.
