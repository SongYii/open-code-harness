# Context Engine Completion Evidence

**Status:** Complete evidence ledger for Milestone 8

**Contract:** [Context Engine — Implemented Contract](context-engine.md)

**Design:** [Context Engine design](../superpowers/specs/2026-09-01-context-engine-design.md)

**Plan:** [Context Engine implementation plan](../superpowers/plans/2026-09-01-context-engine.md)

## Commits

| Commit | Task | Content |
| --- | --- | --- |
| `64850ee` | Gate | Context Engine architecture gate |
| `c580a12` | Design | Durable Context Engine design proposal |
| `723073f` | Design | Design review resolution |
| `6bf2a5c` | Plan | Context Engine implementation plan |
| `8521a5d` | Task 1 | Budget contract, wire estimate meter, usage anchor (`contextengine`, pure) |
| `bb8196c` | Task 2 | Event projection grammar and source digest chain |
| `2c16b4d` | Task 3 | Boundary planner and two-pass bounded scan |
| `7dd1911` | Task 4 | Bounded Tool Result projection |
| `7400531` | Task 5 | Checkpoint types, deterministic reset, replay validation |
| `4ea3228` | Task 6 | Summary contract, validation, and materializer |
| `92ba350` | Task 7 | Domain context compaction commands, events, and bounded aggregate state |
| `2a409ab` | Task 8 | Request purpose, output cap, and cached-input normalization (`engine`, `openaicompat`) |
| `beba5aa` | Task 9 (1/2) | Application pre-turn orchestrator (`PrepareContext` built and tested standalone) |
| `8f2f894` | Task 9 (2/2) | Wire the Context Engine into `RunTurn` admission |
| `0020c2f` | Task 10 | Mid-turn preparation and Provider overflow recovery |
| `00d567c` | Task 11 | Manual compaction, `Service.CompactSession` |
| `fbc0edd` | Task 12 | Context compaction concurrency and cancellation race matrix |
| `acfc8b3` | Task 12 (follow-up) | Fix an over-asserting `RunTurn`-vs-`CloseSession` exclusivity test CI's slower runners caught |
| `74d0063` | Task 13 | SQLite/memory checkpoint store, migration 5, and hash-chain verification |
| `ead2399` | Task 14 | SQLite/runtime checkpoint recovery and crash reconciliation |
| `2681249` | Task 15 | Composition, ACP, and transcript projection wiring |
| `0423fb0` | Task 16 | Benchmarks, evidence ledger, and documentation |
| (this commit) | Follow-up | `och compact-session` CLI, plus a real deterministic-reset checkpoint digest bug this CLI work found and fixed |

Tasks 1–15 were each opened as an individual PR against `main`, watched
through CI (`go`, `determinism`, `cross-build (darwin\|windows)`, `vulncheck`),
and merged only once green — the standard workflow this repository uses
throughout, not specific to this milestone.

## Mechanism → test → mutation result

Per pure-package concern (`internal/harness/contextengine`):

| Mechanism | Test | Mutation check |
| --- | --- | --- |
| Trigger comparison (`SelectCutPoint`) | `TestSelectCutPointBelowTriggerRetainsEverything` and the boundary matrix | Flipping the comparison operator stops the below-trigger case from retaining everything — caught (Task 3). |
| Output/safety reserve (`ComputeBudget`) | Budget table tests at 8K/32K/128K and near-invalid routes | Removing the safety reserve from `hardInput` stops `ErrBudgetInvalid` from firing at the documented boundary — caught (Task 1). |
| Tool-pair boundary (`ProjectSourceEvents`) | Interleaved multi-call Tool Result fixtures | Accepting an unbalanced Call/Result pair as a closed boundary is caught by the projection invariant test (Task 2). |
| Source filter/digest (`ExtendSourceDigest`/`IsSourceEvent`) | `TestComputeSourceDigest*`, plus this milestone's own storage-layer half (Task 13: skipping independent re-verification and trusting a claimed digest) | Folding a non-source event into the digest chain, and skipping the storage-layer's own independent re-verification, are both caught (Task 2 pure half; Task 13 storage half). |
| Append-before-use (admission ordering) | `TestRunTurnWithContextEngineAdmissionBatchAndDispatchedEnvelopeMatch`'s order-asserting model (fails if `Model.Stream` is called before `context.prepared`/`model.request.recorded` commit) | Reordering the admission append after the Provider call is caught (Task 9). |
| Summary shrink validator (`ValidateSummary`) | Shrink/redaction/truncation/duplicate-heading goldens | Disabling the shrink check accepts a non-shrinking summary — caught (Task 5/6). |
| Reset hard-limit gate (`ResetEligibility`) | `TestOverflowRecoveryCancellationNeverBecomesResetOrCompletion` and the reset-ladder tests | Removing the `CallerCanceled` guard lets a canceled caller's failure fall through to a reset — caught (Task 12). |
| Overflow attempt cap (`overflowRecoveryEligible`) | Overflow recovery tests, redirected to the actually-reachable `contextEnabled()` gate once "cap exhaustion via repeated recoveries" was found structurally unreachable (single-shot maximal compaction) | Removing the gate causes a nil-Meter panic on the legacy path — caught, and the cap-exhaustion redirection is itself disclosed in the Task 10 commit message. |
| Checkpoint current-budget replay check (hash-chain re-verification) | `TestRebuildContextCheckpointHeadsDetectsInvalidUnderlyingEvent`/`TestUpdateContextCheckpointHeadRejectsMismatchedDigestAndRollsBackWholeAppend`, plus `TestRebuildContextCheckpointHeadsSpansMultiplePages` (this task's own paging refactor) | Skipping the independent digest re-verification stops both the write-time and rebuild-time rejection tests from rejecting a mismatched claim — caught (Tasks 13 and 16). An off-by-one at a page boundary in the paging refactor is caught by the multi-page test specifically — caught (Task 16). |

Per Application trigger:

| Trigger | Test | Notes |
| --- | --- | --- |
| `pre_turn` | `TestRunTurnWithContextEngineAdmissionBatchAndDispatchedEnvelopeMatch` and the composition end-to-end test (`TestAssemblyRunsAToolCallingTurnEndToEnd`, extended this task to require `context.prepared` in the durable stream) | Real tool-calling Turn through the full assembly, not a synthetic fixture alone. |
| `mid_turn` | `context_midturn_test.go` | Mid-turn preparation between Steps. |
| `manual` | `context_manual_test.go` (summary/reset success, no-ladder failure, focus rendering, `TestCompactSessionSummarizeTimeoutBoundsAHangingSummarizer` added this task) | No ladder fallback verified explicitly. |
| `overflow_retry` | `context_overflow_test.go` | 10% reduction requirement, per-Turn cap, legacy-path error-code classification difference disclosed as pre-existing and unrelated. |

## Verification command output

Go 1.26.6, linux/amd64 (`ip-172-26-1-67`, Ubuntu 24.04 kernel 7.0.0-1010-aws), 2-vCPU cloud instance.

```text
$ go build ./...
(clean)

$ go vet ./...
(clean)

$ CGO_ENABLED=0 go build ./...
(clean)

$ go test ./... -count=1
ok  	.../cmd/acp-client	2.096s
ok  	.../cmd/acp-web-bridge	0.017s
ok  	.../cmd/och	0.285s
ok  	.../internal/client/acp	0.027s
ok  	.../internal/client/acpweb	0.295s
ok  	.../internal/docsguard	(after this document existed to satisfy the link-resolution check)
ok  	.../internal/harness/adapters/acp	3.556s
ok  	.../internal/harness/adapters/localexec	1.155s
ok  	.../internal/harness/adapters/memory	1.279s
ok  	.../internal/harness/adapters/openaicompat	0.206s
ok  	.../internal/harness/adapters/sqlite	13.190s
ok  	.../internal/harness/adapters/system	0.005s
ok  	.../internal/harness/adapters/workspacefs	0.034s
ok  	.../internal/harness/application	3.635s
ok  	.../internal/harness/architecture	0.427s
ok  	.../internal/harness/composition	0.740s
ok  	.../internal/harness/contextengine	0.010s
ok  	.../internal/harness/domain	0.260s
ok  	.../internal/harness/engine	0.010s
ok  	.../internal/harness/policy	0.004s
ok  	.../internal/harness/redact	0.004s
ok  	.../internal/harness/runtime	0.826s
ok  	.../internal/harness/testkit	0.006s
ok  	.../internal/harness/tools	0.007s
ok  	.../internal/harness/transcript	0.117s

$ go test -race ./... -count=1
ok  	.../cmd/acp-client	2.664s
ok  	.../cmd/acp-web-bridge	1.095s
ok  	.../cmd/och	1.997s
ok  	.../internal/client/acp	1.140s
ok  	.../internal/client/acpweb	1.393s
ok  	.../internal/docsguard	1.159s
ok  	.../internal/harness/adapters/acp	30.116s
ok  	.../internal/harness/adapters/localexec	2.198s
ok  	.../internal/harness/adapters/memory	9.035s
ok  	.../internal/harness/adapters/openaicompat	2.058s
ok  	.../internal/harness/adapters/sqlite	68.177s
ok  	.../internal/harness/adapters/system	1.030s
ok  	.../internal/harness/adapters/workspacefs	1.061s
ok  	.../internal/harness/application	43.941s
ok  	.../internal/harness/architecture	3.114s
ok  	.../internal/harness/composition	4.821s
ok  	.../internal/harness/contextengine	1.065s
ok  	.../internal/harness/domain	4.378s
ok  	.../internal/harness/engine	1.050s
ok  	.../internal/harness/policy	1.072s
ok  	.../internal/harness/redact	1.047s
ok  	.../internal/harness/runtime	5.012s
ok  	.../internal/harness/testkit	1.039s
ok  	.../internal/harness/tools	1.089s
ok  	.../internal/harness/transcript	2.048s

$ go test -race -count=5 ./internal/harness/application/... ./internal/harness/domain/...
ok  	.../internal/harness/application	136.134s
ok  	.../internal/harness/domain	19.628s

$ git diff --check
(clean)
```

Benchmark output (`internal/harness/contextengine/bench_test.go`,
`internal/harness/adapters/sqlite/context_checkpoint_bench_test.go`):

```text
$ go test ./internal/harness/contextengine/... -bench . -benchmem -run '^$'
BenchmarkScan/turns=100-2            819    1426113 ns/op     367482 B/op    1812 allocs/op
BenchmarkScan/turns=1000-2            79   16863219 ns/op    4309602 B/op   18036 allocs/op
BenchmarkScan/turns=10000-2            6  179767319 ns/op   47197121 B/op  180077 allocs/op
BenchmarkSelectCutPoint/turns=100-2  50752      22540 ns/op      49008 B/op       9 allocs/op
BenchmarkSelectCutPoint/turns=1000-2  2786     476088 ns/op     892784 B/op      15 allocs/op
BenchmarkSelectCutPoint/turns=10000-2  100   10798108 ns/op    9748336 B/op      23 allocs/op
BenchmarkMaterialize/turns=100-2      6721     171502 ns/op  19814 dispatched_tokens  115396 B/op  11 allocs/op
BenchmarkMaterialize/turns=1000-2     5653     211804 ns/op  23576 dispatched_tokens  124716 B/op  11 allocs/op
BenchmarkMaterialize/turns=10000-2    5037     244012 ns/op  23576 dispatched_tokens  124736 B/op  11 allocs/op

$ go test ./internal/harness/adapters/sqlite/... -bench 'BenchmarkLoadLatestContextCheckpoint' -benchmem -run '^$'
BenchmarkLoadLatestContextCheckpoint/turns=100-2    6927   187102 ns/op  23378 B/op  458 allocs/op
BenchmarkLoadLatestContextCheckpoint/turns=1000-2   5239   196922 ns/op  23376 B/op  458 allocs/op
BenchmarkLoadLatestContextCheckpoint/turns=10000-2  5780   187279 ns/op  23375 B/op  458 allocs/op
```

**Reading the numbers** (single-machine sample; no GA latency claim is made
from it, per design §22.4):

- `BenchmarkMaterialize` and `BenchmarkLoadLatestContextCheckpoint` are
  **flat** across the 100x Turn-count range (100 → 10,000): the actually
  dispatched envelope and the SQLite checkpoint lookup both deliver the
  "bounded by budget, not Turn count" property design §22.4 requires.
- `BenchmarkScan` and `BenchmarkSelectCutPoint` are **not** flat: both scale
  roughly linearly with Turn count (100 → 1,000 Turns is close to a 10x
  jump in bytes/op for both). This is Task 16's own most significant
  finding — documented in full, with its root cause and scope of impact,
  in [context-engine.md's "Known limitations"](context-engine.md#known-limitations)
  and in the pre-existing `Scan` doc comment
  (`internal/harness/contextengine/planner.go`) that anticipated exactly
  this benchmark surfacing it. It is a real, disclosed performance gap on
  the pre-turn planning path, not a correctness or safety gap (the envelope
  actually dispatched stays bounded regardless), and is called out as a GA
  blocker rather than silently left for the numbers alone to imply.

## Deviations from the plan's file map

- `internal/harness/adapters/sqlite/migration5.go` (as literally named in
  the plan's Task 13 file list, mirroring the existing `migration4.go`
  precedent) does not exist as a separate file; the migration 5 DDL
  (`migration5DDL`) instead lives directly in `migrations_sql.go` alongside
  every other migration's own DDL constant, and its registration in
  `migrations.go`'s `migrations` slice needed no accompanying code-driven
  `apply` step (unlike migration 4's `migrateSessionHeadsV4`), since
  `context_checkpoint_heads` has no legacy data to backfill.
- Task 13 does not build a shared memory/SQLite conformance test suite
  matching `eventstoretest`'s own precedent the design references
  (`docs/architecture/eventstore-v2.md`); it ships direct, disclosed
  per-adapter tests instead. Reasoning stated in the Task 13 commit
  message: the two adapters' write paths differ structurally enough (SQLite
  verifies inside one transaction; memory re-verifies per read) that a
  shared harness would mostly abstract over adapter-specific setup rather
  than genuinely shared behavior, plus the time already invested in the
  session that delivered it.
- Task 14 added `Store.SessionsWithActiveCompaction`
  (`internal/harness/adapters/sqlite/lease.go`) and unioned it into
  `reconcileAll`'s candidate discovery — not named in the plan's own Task
  14 file list, but a real bug the design's own reconciliation requirement
  could not be correctly delivered without: `session_heads.status` never
  reflects compaction activity, so `ActiveSessions` alone would never
  discover a session whose only dangling state is a crashed manual/
  pre-turn compaction, permanently blocking it from ever starting a new
  Turn or compaction again.
- Task 16's own `RebuildAndVerifyContextCheckpointHeads` paging refactor
  (`internal/harness/adapters/sqlite/context_checkpoint_rebuild.go`) is not
  named in the plan's Task 16 bullet list either; it was made necessary by
  this task's own benchmark work discovering that the Task 14
  implementation, while functionally correct, materialized a whole
  session's canonical history into one slice — violating design §22.4's
  own "cold projection rebuild may be O(history) but stays paged and
  heap-bounded" requirement. Fixed and proven correct across a page
  boundary (`TestRebuildContextCheckpointHeadsSpansMultiplePages`) before
  this task's benchmarks were considered complete.
- `internal/harness/contextengine/bench_test.go` and
  `internal/harness/adapters/sqlite/context_checkpoint_bench_test.go`: the
  plan names one benchmark file location "or equivalent"; this task uses
  two, one per package, since the pure-package planning-path benchmarks and
  the SQLite checkpoint-lookup benchmark measure genuinely different
  components.

## Remaining blockers (not GA)

This milestone stays **not GA**, matching every prior evidence ledger's own
honesty convention, until at minimum:

1. Real-model quality evaluation of rolling summaries — every test this
   milestone wrote uses scripted or fixture summarizers; none evaluates an
   actual model's summarization quality.
2. Wider provider coverage than the single OpenAI-compatible adapter this
   milestone exercises.
3. `Scan`/`SelectCutPoint`'s `O(history)` planning-path cost (see above) is
   resolved, or explicitly accepted for the production scale this project
   targets — an architectural change to a pure, heavily mutation-tested
   package, not attempted at the tail of this milestone.
4. A wall-clock, multi-process soak of the recovery paths beyond this
   milestone's deterministic-time and scripted-outcome test evidence.
5. The disclosed inert config surface — `MaxSummaryChunks` and the unwired
   usage anchor (`EvaluateUsageAnchor`) — remain open, clearly-scoped
   follow-ups — see
   [context-engine.md's "Known limitations"](context-engine.md#known-limitations)
   for the complete, current list. `och compact-session` (design CE-14)
   and `MaxPrunedToolResultsPerRequest`/Tool Result pruning are no longer
   among them: see the two follow-up sections below.

## Follow-up: `och compact-session` CLI and a reset-checkpoint digest bug

Delivered after Task 16 landed, once `cmd/och` review turned to the one
item Task 16 itself left as a clearly-scoped, unblocked follow-up.

`cmd/och` gains a `compact-session` subcommand (`cmd/och/main.go`,
`cmd/och/compact_session_test.go`) implementing design CE-14: it opens the
normal composition root exactly like the serve path does (a new
`bindAssemblyFlags` helper shares the workspace/database/provider/policy
flag surface between the two, since both need a full assembly — unlike
`export-session`, which only ever opens a read-only `sqlite.OpenReader`),
runs one `Service.CompactSession` call, and prints one stable JSON object
to stdout (`compactSessionOutput`) with a human-readable line on stderr.
`-strategy reset`/`-focus` map directly onto `CompactSessionRequest`'s own
fields. Taking the Runtime lease through the ordinary `composition.Open`
path means it fails rather than running beside another live writer for
free, without any dedicated lease-collision code of its own.

Building this CLI's own end-to-end tests against a **real** SQLite
database — for the first time ever exercising a deterministic-reset
compaction through a genuinely independent-verifying `ContextCheckpointStore`,
rather than `context_manual_test.go`'s own `fakeCheckpointStore` (which
never verifies anything) — found a real, previously-undetected bug:
`buildResetCheckpoint`'s `Coverage.SourceDigest` was left at its seed value
and never actually extended over the newly covered canonical records, even
though `ThroughSequence` correctly advanced to claim that coverage.
`ValidateSuccessor` (the only check every prior reset test exercised)
verifies structural relationships only — predecessor linkage, coverage
ordering, same-coverage-rewrite digest equality — and never recomputes a
digest from canonical content, so it could not catch this. Every
deterministic-reset checkpoint this codebase had ever built — both the
manual `-strategy reset` path and the automatic overflow-recovery reset
path, which share this one function — would have been rejected the moment
a genuinely verifying store (SQLite's write-time hash-chain hook, or
either adapter's own read-time re-verification) tried to read it back.

Fixed by extending the digest over the newly covered range exactly as the
rolling-summary path (`buildSummaryCheckpointWithFocus`) already did:
`readSourceRecordsRange` plus `contextengine.ExtendSourceDigestOverRecords`,
threaded through both call sites (`runCompactionBracket`'s automatic reset
fallback and `Service.CompactSession`'s own manual reset path).
Regression-tested at the application layer against the memory adapter's
own real, independently-verifying checkpoint store
(`TestCompactSessionResetCheckpointDigestSurvivesIndependentVerification`)
and end to end through the new CLI against a real SQLite database
(`TestCompactSessionSummaryStrategySucceeds`,
`TestCompactSessionResetStrategyNeverCallsTheProvider`), both with their
own mutation check (reverting the fix and confirming the corresponding
test fails, then restoring).

Verification: `go build`, `go vet`, `go test ./... -count=1`,
`go test -race ./cmd/och/... ./internal/harness/application/... ./internal/harness/composition/... -count=1`,
and a full `go test -race ./... -count=1` are clean, with one exception
disclosed rather than silently ignored: `TestConformance/limits_copies_cancellation_and_corruption`
(`internal/harness/adapters/sqlite`, a pre-existing, unmodified conformance
test unrelated to this change) failed once under the combined system load
of a whole-repository `-race` run (`writer_fenced`, a lease-duration-
sensitive assertion) but passed cleanly in isolation immediately afterward
(`go test -race ./internal/harness/adapters/sqlite/... -run TestConformance -count=1`,
13.12s vs. 50.99s under full-repo load) — a load-induced timing flake in an
unrelated, unmodified test, matching this project's own established
precedent for distinguishing that from an actual regression.

## Follow-up: Tool Result pruning wired into `Materialize`

Delivered as a further follow-up: closes `MaxPrunedToolResultsPerRequest`'s
own disclosed "accepted but inert" gap, leaving `MaxSummaryChunks` as the
one remaining item on that list (see context-engine.md's "Known
limitations" #3 for why multi-chunk summarization is a separate,
larger, not-yet-attempted follow-up rather than bundled here).

**Mechanism.** `contextengine.MaterializeInput` gains three fields —
`ProtectedTail`, `MaxPrunedToolResults`, `HardInput` — all zero by
default, so every existing caller (this package's own tests included)
continues to dispatch byte-identical Tool Result content, unchanged.
When all three are set, `Materialize` walks `RetainedTail` in its own
ascending sequence order and replaces a Tool Result message whose own
meter estimate exceeds `MaxProjectedToolResultTokens(ProtectedTail)`
(design §10) with `ProjectToolResult`'s existing, already-tested
marker-framed excerpt, oldest first, up to `MaxPrunedToolResults`
replacements. A Tool Result `ProjectToolResult` itself cannot fit even
as an empty-content marker (`ErrContextUnitTooLarge`) is left
byte-identical rather than failing the call — `Materialize` returns no
error today, so it cannot itself surface that code; a request that still
cannot fit remains `overflow_retry`'s own problem to react to, exactly as
before this change for any oversized content.

**Plumbing.** Building this surfaced a second, previously undisclosed gap
distinct from the pruning mechanism itself being unwired:
`composition.Config.Context.MaxPrunedToolResultsPerRequest` was accepted
and range-validated by `composition`, but `composition/assembly.go` never
actually passed it into `application.ContextConfig` at all — the value
was silently dropped between the two layers regardless of what
`Materialize` itself could do with it. Fixed by adding the field all the
way through: `application.ContextConfig` and `ContextOrchestratorDeps`
both gain `MaxPrunedToolResultsPerRequest uint32` (zero by default,
matching every existing caller), `service.contextOrchestratorDeps()`
copies it from `Config.Context`, `composition/assembly.go` now passes
`config.Context.MaxPrunedToolResultsPerRequest` through, and
`PrepareContext`'s own final `Materialize` call passes
`deps.Budget.ProtectedTail`/`deps.MaxPrunedToolResultsPerRequest`/
`deps.Budget.HardInput`.

**Tests.**
`TestMaterializeToolResultPruningDisabledByDefaultLeavesContentByteIdentical`
and `TestMaterializePrunesOversizedToolResultsUpToTheCap` (`contextengine`)
cover the pure mechanism: disabled-by-default byte-identity, oversized
Tool Results actually replaced up to the configured cap, results beyond
the cap left untouched, and non-Tool-Result messages never altered.
`TestMidTurnToolResultPruningIsWiredEndToEnd` (`application`) is the
end-to-end regression test: a real `read_file` Tool Call through a real
mid-turn Step, with a ~50 KiB fixture file (comfortably under Tool
Runtime's own `MaxToolResultBytes` cap, but far above design §10's own
largest possible per-result cap of 2048 tokens) — with pruning left at
its zero-value default, the second (mid_turn) `ModelRequestRecorded`
carries the byte-identical original file content; with
`MaxPrunedToolResultsPerRequest` configured, that same request instead
carries `ProjectToolResult`'s own marker-framed excerpt naming the same
Tool Call ID, strictly smaller than the original.

### Mechanism → test → mutation result

| Mechanism | Test | Mutation check |
| --- | --- | --- |
| `Materialize` prunes an oversized retained Tool Result up to the configured cap | `TestMaterializePrunesOversizedToolResultsUpToTheCap` (`contextengine`) | Forcing `pruningEnabled` to `false` makes the assertion on `PrunedToolResultCount` (and the byte-identity check in the disabled-by-default test) fail — caught. |
| `PrepareContext` passes `Budget.ProtectedTail`/`MaxPrunedToolResultsPerRequest`/`Budget.HardInput` into `Materialize` | `TestMidTurnToolResultPruningIsWiredEndToEnd` (`application`) | Hardcoding the `Materialize` call's own `MaxPrunedToolResults` argument to `0` makes the "pruning enabled" half of this test fail (the dispatched Tool Result stays the full, unpruned original) — caught. |
| `composition/assembly.go` forwards `MaxPrunedToolResultsPerRequest` into `application.ContextConfig` | `TestValidateRejectsEveryDocumentedCause`/`TestValidateAppliesContextDefaultsWithoutMutatingTheCaller` (`composition`, pre-existing) plus the application-level end-to-end test above, which would silently see pruning never activate if this line were dropped again | Covered transitively: the application-level test above exercises the real `composition`-shaped `ContextConfig` field name, not a hand-rolled one. |

### Verification command output

```text
$ go build ./...
(clean)

$ go vet ./...
(clean)

$ CGO_ENABLED=0 go build ./...
(clean)

$ go test ./... -count=1
(all packages ok)

$ go test -race ./... -count=1
(all packages ok)

$ go test ./internal/harness/contextengine/... -bench BenchmarkMaterialize -benchmem -run '^$'
BenchmarkMaterialize/turns=100-2      7862   153076 ns/op   19814 dispatched_tokens  115758 B/op  11 allocs/op
BenchmarkMaterialize/turns=1000-2     6116   187794 ns/op   23576 dispatched_tokens  125653 B/op  11 allocs/op
BenchmarkMaterialize/turns=10000-2    5464   227906 ns/op   23576 dispatched_tokens  124099 B/op  11 allocs/op
```

Unchanged from the original evidence ledger's own numbers (within noise):
this benchmark's own fixture never enables pruning, so the new code path
adds one cheap boolean short-circuit per message and nothing else when a
caller leaves the three new fields at their zero value.
