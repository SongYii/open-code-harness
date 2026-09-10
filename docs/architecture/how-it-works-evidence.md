# Plain-Language Implementation Guide Completion Evidence

- Status: Complete
- Date: 2026-09-10
- Design commit: `db11cfd` (`docs: define plain-language implementation standard`)
- Implementation commit: `701ee0c` (`docs: explain every implemented subsystem plainly`)
- Guides: [English](how-it-works.md) · [中文](how-it-works.zh-CN.md)

## What changed

- The authority table now registers Evaluation as an implemented contract;
  before this correction, code and milestone prose said it was implemented but
  documentation gates did not include it in the implemented-module inventory.
- Both guides cover all 20 implemented contracts and answer the same five
  practical questions for each.
- The root README links the guide beside the foundational and current-system
  architecture documents.
- Five early evidence ledgers now carry a prominent historical-snapshot notice.
  Their original status text remains unchanged.
- The foundational delivery principles and `docs/README.md` rules now make the
  bilingual five-question entry part of implementation completion.
- `internal/docsguard` derives its expected modules from the authority table and
  rejects a missing guide entry, an extra stale entry, or any missing standard
  heading.

## Real documentation findings

The audit did not assume that existing documentation was uniformly readable.
It found three distinct generations:

1. early ledgers that mostly list commits and test commands;
2. middle ledgers with mapping/mutation tables but limited plain-language
   narrative; and
3. recent ledgers that explicitly record wrong assumptions, ineffective tests,
   corrections, and honest live-validation limits.

The guide preserves those differences. Where an early ledger recorded no
development problem, the guide says so rather than manufacturing a story.

The same audit found stale historical claims in the Engine, EventStore,
Provider, Tool Runtime, and base ACP ledgers. For example, EventStore's ledger
correctly said on its completion date that SQLite had not started, while SQLite
is now implemented. Notices resolve the ambiguity without rewriting history.

## Mutation evidence

The Evaluation marker was temporarily removed from the Chinese guide, then the
focused gate was run:

```text
go test ./internal/docsguard \
  -run TestPlainLanguageGuidesCoverImplementedContracts -count=1

guide has no entry for implemented contract docs/architecture/evaluation.md
FAIL
```

The marker was restored and the same test passed. This proves the gate consumes
the authority-table inventory rather than a duplicated hard-coded module list.

## Verification

The final tree passes:

```text
go test ./internal/docsguard ./internal/harness/architecture -count=1
go vet ./...
PATH=/tmp/och-no-bwrap:$PATH go test -race ./... -count=1
go mod tidy -diff
git diff --check
```

The PATH prefix matches ordinary CI by reporting `bwrap` unavailable; this
machine has a working system `bwrap`, while two pre-existing localexec tests
assert the no-backend branch.

## Limit

The gate can prove that every module and heading exists. It cannot prove that
the prose is genuinely understandable. Reviewers remain responsible for
rejecting unexplained jargon, vague claims, or a “problems found” section that
does not match the evidence.
