# Plain-Language Implementation Guide and Documentation Standard

- Status: Accepted
- Date: 2026-09-10
- Scope: every implemented subsystem and every future implementation slice
- Depends on: [Current system architecture](../../architecture/current-system.md)

## 1. Problem

The repository is unusually strong at preserving technical contracts, commit
identities, test commands, mutation results, and exclusions. It is much less
consistent at telling a reader the development story in ordinary language.

Recent evidence ledgers explain incorrect assumptions and failed tests in
detail. Earlier ledgers often contain only a commit table, command output, and
the list of work that was still missing on that date. Several of those dated
lists are now false as descriptions of the current repository. They are valid
historical evidence, but they are easy to mistake for current status.

A Chinese translation of a dense contract does not by itself solve the
readability problem. New contributors need a stable entry point that answers
the same small set of practical questions for every subsystem.

## 2. Decision

Publish one English guide and one synchronized Chinese reading copy covering
every row that `docs/README.md` labels `Implemented contract`.

Each subsystem entry must use these five headings:

1. **Problem** — what real failure or limitation this subsystem prevents;
2. **Visible result** — what a user or integrator can actually observe;
3. **Implementation** — the real packages/entrypoints and the mechanism in
   plain language;
4. **Problems found and fixes** — at least one concrete development finding,
   or an explicit statement that the dated evidence recorded none; and
5. **Still missing** — the important current limit, without implying GA.

Each entry links the implemented contract and its evidence ledger. Design and
plan links may be included when they help, but the guide does not replace
either document.

## 3. Historical evidence

Evidence ledgers remain append-only historical records. Do not rewrite an old
“remaining work” section to match the present, because that would change what
the evidence said when captured. Instead, a ledger containing a now-stale
status statement receives a prominent notice near the top:

- the document is a dated snapshot;
- its old remaining-work list is not current status; and
- the current architecture/guide is the status pointer.

New evidence ledgers must distinguish “remaining at the time of this slice”
from “current project status.” Later corrections append a dated update rather
than silently editing the original claim.

## 4. Executable coupling

`internal/docsguard` must derive the implemented-contract list from the
authority table and require both guides to link every one. It must also require
the five standard headings for each subsystem entry. This prevents a new
technical contract from landing without a readable explanation.

The gate checks structure and coverage, not prose quality. Review remains
responsible for judging whether an explanation is genuinely understandable.

## 5. Writing rules

- Lead with the practical outcome, not type names.
- Introduce a technical term only after explaining the idea it names.
- Prefer one concrete failure example over a list of interfaces.
- Name real code locations, but do not turn the guide into an API catalog.
- Say what a test proves and what it does not prove.
- Record a failed assumption or ineffective test honestly; do not present the
  final implementation as if it was obvious from the start.
- Keep current status separate from historical evidence.
- Avoid unexplained acronyms. The first use expands ACP, MCP, CAS, SSE, and
  similar project terms.
- Keep each subsystem entry short enough to read without opening its detailed
  contract.

## 6. Project implementation rule

An implementation slice is not documentation-complete until it has:

- an accepted design before its implementation plan;
- production code and an implemented contract;
- reproducible evidence tied to commits and tests;
- a plain-language five-question guide entry in both languages;
- honest findings and deviations; and
- current limitations separated from dated historical status.

These requirements are added to the project documentation rules and the
standard completion checklist.

## 7. Non-goals

- No simplification of the technical contracts themselves.
- No deletion or rewriting of historical evidence.
- No claim that structural checks can measure writing quality.
- No new product feature or runtime behavior.

## 8. Acceptance

All current implemented contracts have bilingual five-question entries; stale
historical ledgers are visibly marked; the authority map and root README expose
the guide; docsguard rejects a missing subsystem or heading; and all repository
tests remain green.
