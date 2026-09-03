# Context Mechanism Evaluation Suite Design

**Status:** Implementation-ready handoff; proposed normative supplement

**Date:** 2026-09-03

**Authority:** This document specializes the accepted milestone 10 design in
[`2026-09-02-evaluation-design.md`](2026-09-02-evaluation-design.md) for the
Context Engine mechanism suite. The parent design wins if these documents
conflict. An implementation must update this document, rather than silently
deviate, if repository facts invalidate a contract below.

**Current baseline:** At the time of writing, `main` includes the Context
Engine, both eval executors, evidence collection and offline regrading, tool /
workspace fixtures, executor parity, and the consent-gated live judge. The
previously disclosed steady-state history rescan, inert multi-chunk summary,
inert Tool Result pruning, and unwired usage-anchor gaps have already been
closed. This suite evaluates those production paths; it does not replace them.

## 1. Decision

The next evaluation slice is a deterministic Context Engine mechanism suite.
It drives real OCH Sessions through both supported execution surfaces, saves
canonical evidence, and grades the evidence offline. It does not add an
eval-only Context Engine, Provider, checkpoint store, or Session path.

The implementation uses a thin extension of the existing system:

1. add one missing per-request pruning fact to `context.prepared`;
2. add a stateless Context-aware loopback fixture script;
3. build a typed, fail-closed Context trace over canonical audit evidence;
4. add focused verifier IDs and checked-in Scenarios;
5. split cross-surface, specialized-budget, and ACP-only matrices rather than
   expanding the EvalSet schema or creating a fixture scripting language.

Real-model summary quality remains a live lane. This delivery may refine the
checked-in live Scenario and judge criteria, but it must not call a live model
in ordinary or scheduled deterministic CI.

## 2. Why this slice exists

The current checked-in `context-compaction` Scenario proves only one fact: a
manual `reset` action produced a matching `context.compaction.started` and a
completed `source_tail_reset_v1` checkpoint. That smoke test is useful but does
not prove automatic admission, mid-turn preparation, summary chunking,
overflow retry, request-time Tool Result projection, or checkpoint reuse after
a process restart.

The current `smart` fixture also selects behavior by searching the entire raw
request body. Earlier prompt markers therefore remain visible in later turns,
and the handler cannot safely express ordered Context behavior. It always
returns plain `ok` to summarizer requests, which is not a valid
`och_context_summary_v1` document, and it cannot deterministically inject one
pre-delta `context_overflow` followed by success without mutable global state.

Finally, `contextengine.PreparedContext.PrunedToolResultCount` records how many
Tool Results were actually projected for a request, but
`domain.ContextPreparedRecorded` does not persist that value. A verifier can
currently infer projection from `ModelRequestRecorded.Messages`, but cannot
cross-check the Context Engine's own decision evidence. This is an
observability contract gap, not a reason to inspect SQLite from eval.

## 3. Goals

The suite must prove, from saved evidence:

- manual summary and reset behavior;
- automatic pre-turn compaction before dispatch;
- mid-turn preparation after a Tool Result and before the next model request;
- bounded, actually effective Tool Result projection;
- provider-overflow recovery with a smaller, separately recorded retry;
- rolling multi-chunk summarization rather than an accepted-but-inert setting;
- non-lowering provider usage anchoring when a plain wire estimate alone would
  stay below the trigger;
- checkpoint reuse and chain continuity after clean restart;
- checkpoint reuse after ACP `interrupt` and `kill` restarts;
- transcript and audit projection of public Context lifecycle evidence;
- Context budget invariants for every dispatched conversation attempt;
- semantic parity where both executors promise the same behavior;
- fail-closed offline regrading from the manifest, without rerunning OCH.

The suite must remain deterministic, bounded, credential-free, and runnable
against an isolated fixture/workspace/store/Session/artifact root per Attempt.

## 4. Non-goals and explicit limits

This slice does not:

- evaluate arbitrary external agents;
- introduce a Scenario-authored program, shell verifier, fixture DSL, or new
  Provider abstraction;
- make live-model quality a PR or deterministic scheduled gate;
- claim that in-process abandonment simulates an OS process crash;
- inspect or copy a live SQLite database or live audit replica;
- record eval Attempt or Score facts in the Session event stream;
- require identical lifecycle event sequences from in-process and ACP manual
  compaction; only declared semantic facts are compared;
- prove a crash at the exact instant a compaction bracket is open.

The last item is intentionally explicit. The current Scenario language can
restart between actions, but cannot wait on a live `context.compaction.started`
barrier while a prompt or compact action is still running. Adding a reliable
mid-compaction crash test requires a separate accepted contract for an
attempt-local synchronization primitive; sleeps, PID polling, and copying a
live audit replica are forbidden substitutes. Existing runtime/store tests
remain authoritative for unmatched-bracket reconciliation. This suite proves
process restart and checkpoint recovery after a completed checkpoint.

## 5. Architecture and ownership

```text
checked-in Scenario + Subject + Executor
                 |
                 v
          cmd/och-eval runner
          /                 \
 in-process Composition      real och -acp subprocess
          \                 /
           production Application/Session
                       |
              fixture://context-mechanism
                       |
       cold transcript + verified audit export
                       |
             typed Context trace index
                       |
         deterministic verifier Score
```

Ownership remains unchanged:

- `internal/harness/domain` owns the durable Context event fields.
- `internal/harness/application` maps real Context preparation results into
  domain events.
- `internal/harness/transcript` owns the public transcript projection.
- `internal/harness/eval` owns evidence parsing and deterministic verdicts.
- `cmd/och-eval` owns the embedded loopback fixture implementation and CLI
  wiring, not Scenario semantics.
- Composition remains the only owner of concrete adapter construction and cold
  evaluation evidence export.

Domain, Application, Context Engine, transcript, adapters, and Composition
must not import eval. Eval must not import the SQLite adapter.

## 6. Evidence contract amendment

Add this optional, additive field to `domain.ContextPreparedRecorded`:

```go
PrunedToolResultCount uint32 `json:"prunedToolResultCount,omitempty"`
```

`application.ContextPreparedRecordedFromResult` must copy the value from
`result.Prepared.PrunedToolResultCount`. The domain codec and transcript codec
must accept and project the field. Zero means no Tool Result was projected for
that request; it does not mean pruning was disabled.

No database migration or event version change is required because the event
payload is JSON and the field is optional. Old records decode with zero. New
writers must not populate the field from configuration; it is an observed
result of `Materialize`.

For a pruning Scenario to pass, the verifier must observe all of:

1. `context.prepared.prunedToolResultCount > 0`;
2. the paired `model.request.recorded`, matched by `contextDecisionID`, contains
   the fixed projected Tool Result framing;
3. the projected frame preserves the original Tool Call ID;
4. `original_bytes` and `sha256` match the collected fixture file;
5. the projected text is smaller than the original and its framing closes;
6. the request remains within its recorded hard-input budget.

Configuration such as `maxPrunedToolResultsPerRequest > 0` is never proof.

## 7. Stateless fixture protocol

Add a separate embedded fixture script named `context-mechanism` rather than
making the existing smoke handler accumulate more unrelated branches. A
fixture Subject refers to it as `fixture://context-mechanism`; runtime endpoint
resolution remains in memory and must not mutate the frozen Subject document or
its digest.

The handler parses the OpenAI-compatible JSON request. It must not select a
branch by `bytes.Contains` over the whole body and must not use cross-request
mutable counters. Classification order is:

1. **Summarizer request.** The sole current input begins with the versioned
   `och_context_summary_v1` prompt marker and contains the required output
   contract. Return a structurally valid eight-section summary.
2. **Tool continuation.** The latest request contains a Tool Result associated
   with the current Scenario marker. Return the declared continuation sentinel.
3. **Latest-user Scenario marker.** Select behavior from the latest real user
   message only, never an earlier historical message.
4. **Default.** Return a bounded plain completion.

Scenario markers are checked-in, non-secret strings with an
`OCH_EVAL_CONTEXT_` prefix. They are fixture protocol coordinates, not user
identity and not verifier success by themselves.

### 7.1 Summary response

The summary response contains exactly the required headings, in the required
order. Its `Established Facts` section contains:

```text
fixture_chunk_depth: N
```

For a request without a rendered `PREVIOUS CHECKPOINT`, `N` is `1`. Otherwise
the handler extracts the prior fixture depth and returns `N + 1`. It rejects a
malformed or non-numeric prior depth with a bounded fixture-contract failure.
This makes rolling behavior observable without server state: a final checkpoint
whose `SummaryChunks == N >= 2` proves each later summarizer request received
the previous chunk's output.

The response must be emitted through the same real streaming protocol used by
conversation requests. It may include fixed usage numbers when a Scenario
needs usage-anchor coverage; those numbers are fixture data, not inferred
cost.

### 7.2 Overflow response

For `OCH_EVAL_CONTEXT_OVERFLOW`, return HTTP 400 with the
OpenAI-compatible `context_length_exceeded` shape when the request contains no
rolling-summary or source-tail-reset checkpoint message. Return success once a
checkpoint message is present. This produces exactly one recoverable overflow
without a request counter: the retry differs by its actual prepared envelope.

The success body contains a unique bounded sentinel so transcript evidence can
prove that the post-compaction request reached the fixture.

### 7.3 Tool Result response

For `OCH_EVAL_CONTEXT_PRUNE`, first request a real `read_file` of the checked-in
large fixture file. On the continuation request, return success only when the
received Tool Result contains the complete projected-result framing and the
original Tool Call ID. Otherwise return a deterministic failure sentinel that
the verifier rejects.

The fixture is an external observer of the actual HTTP envelope. Audit evidence
remains the canonical explanation of how OCH prepared that envelope.

### 7.4 Usage-anchor response

For `OCH_EVAL_CONTEXT_USAGE_ANCHOR`, the first conversation response reports
a fixed, deliberately high provider input-token count while returning a short
assistant message. A later response stays ordinary. Classification is derived
from the latest user marker and the request history, not a mutable request
counter. The fixture numbers and Subject profile must make the plain next-request
wire estimate remain below trigger while the eligible non-lowering anchor
crosses it.

The verifier correlates the prior `model.usage.recorded` with the later
`context.prepared`; a high fixture usage number or enabled configuration alone
cannot pass.

### 7.5 Fixture safety

The handler must:

- bound request reads;
- never log Authorization or the complete raw request body;
- return bounded, stable error text;
- be concurrency-safe even if the runner later executes Attempts in parallel;
- have table-driven tests for malformed JSON, historical-marker confusion,
  summary precedence, overflow before/after checkpoint, and pruning framing;
- leave the existing `smart` fixture behavior backward-compatible.

## 8. Typed Context trace

Replace ad-hoc repeated JSON loops with a reusable internal trace builder in
`internal/harness/eval`. It reads only manifest-declared `audit` entries through
`ArtifactReader`, preserves canonical append/event order, and indexes:

- compaction starts, completions, and failures by compaction ID;
- prepared decisions and model requests by `contextDecisionID`;
- turn ID, item ID, attempt index, trigger, checkpoint ID/kind, budget fields,
  usage anchor, and pruning count;
- provider usage records needed to establish the preceding eligible anchor;
- checkpoint predecessor, coverage, digest, token, chunk, and pruning facts;
- terminal Turn/assistant outcomes required for correlation.

The builder validates before any criterion runs:

- every completed/failed compaction has one earlier matching start;
- no compaction ID has multiple terminal events;
- every conversation `model.request.recorded` with a Context decision has one
  earlier matching `context.prepared` for the same turn/item/attempt;
- decision IDs are unique;
- attempt indices for a single assistant item are positive and ordered;
- checkpoint IDs are unique and predecessor references do not fork or cycle;
- recorded budget ordering is `target < trigger < hardInput` (protected-tail is
  not a `context.prepared` field and must not be invented by the verifier);
- required numeric fields are non-zero where the production contract requires
  them.

Malformed JSON, unreadable audit evidence, broken correlation, or contradictory
events makes dependent criteria `indeterminate`; it must never degrade to a
simple behavioral `fail` or pass by ignoring the malformed record.

## 9. Deterministic verifier catalog

Add focused IDs rather than one Scenario-ID-aware mega-verifier:

| Verifier ID | Required observed behavior |
| --- | --- |
| `context-manual-reset-v1` | manual/reset start and matching completed `source_tail_reset_v1` checkpoint with advancing coverage |
| `context-manual-summary-v1` | manual/summary start and matching completed `rolling_summary_v1` checkpoint with valid version fields and non-empty summary |
| `context-pre-turn-summary-v1` | pre-turn/summary completes before the paired request, and that request's prepared evidence names the new checkpoint |
| `context-mid-turn-v1` | a second preparation occurs after a Tool Result on the same Turn, uses trigger `mid_turn`, and pairs with attempt index 2 |
| `context-tool-result-pruned-v1` | all six evidence checks from section 6 plus the fixture success sentinel |
| `context-overflow-recovered-v1` | initial attempt 1, overflow-retry attempt 2 on the same turn/item, one `overflow_retry` compaction, smaller retry estimate, and completed Turn |
| `context-multi-chunk-summary-v1` | completed rolling checkpoint has `summaryChunks >= 2`, fixture depth equals chunk count, and no partial-summary checkpoint was accepted |
| `context-usage-anchor-v1` | an eligible prior provider-usage record exceeds the plain next-request estimate, the next preparation records `usageAnchorApplied=true` with the expected non-lowering value, and pre-turn compaction occurs |
| `context-checkpoint-reused-v1` | a post-restart request names the pre-restart checkpoint or a valid successor and does not replay pre-checkpoint raw history |
| `context-budget-bounds-v1` | every prepared conversation request has ordered budgets, positive serialized size, and `estimatedTotalTokens <= budgetHardInput` |
| `context-projection-present-v1` | transcript and audit both contain the required public Context lifecycle facts; transcript intentionally need not expose `model.request.recorded` |

The existing `context-compaction-observed-v1` may remain as a compatibility
alias for the old smoke Scenario, but new Scenarios use the focused IDs. Do not
silently broaden its semantics.

### 9.1 Verdict rules

- Missing/unreadable/malformed required evidence: `indeterminate`.
- Well-formed evidence showing the behavior did not happen: `fail`.
- Infrastructure outcome: existing outcome criterion applies independently;
  no Context criterion turns infrastructure failure into subject failure.
- A fixture-contract failure sentinel: `fail` when transcript/audit are intact;
  `indeterminate` when the evidence needed to establish it is missing.
- A configuration field, Scenario action, or executor capability declaration
  alone cannot satisfy a criterion.
- Regrading the same manifest with the same verifier build must produce the
  same criterion statuses and details.

Verifier detail text must be stable, bounded, and evidence-oriented. It may
name event kinds, action coordinates, and counts; it must not dump complete
prompts, Tool Results, or model responses.

## 10. Scenario matrix

Every Scenario starts from a fresh Session and fixture copy.

| Scenario ID | Key actions | Subject profile | Verifiers | Surfaces |
| --- | --- | --- | --- | --- |
| `context-manual-reset` | padded prompts, manual reset | core | manual reset, bounds, projection | in-process + ACP |
| `context-manual-summary` | padded prompts, manual summary | core | manual summary, bounds, projection | in-process + ACP |
| `context-pre-turn-summary` | build history, pressure prompt | core | pre-turn summary, bounds, projection | in-process + ACP |
| `context-mid-turn-pruning` | prompt causes large `read_file`, continuation | prune | mid-turn, pruning, bounds | in-process + ACP |
| `context-overflow-retry` | build compactable history, overflow marker prompt | overflow | overflow recovered, bounds, projection | in-process + ACP |
| `context-multi-chunk-summary` | large history, summary-triggering prompt | chunk | multi-chunk, pre-turn summary, bounds | in-process + ACP |
| `context-usage-anchor` | first response reports high fixed input usage, then a small appended prompt | anchor | usage anchor, pre-turn summary, bounds | in-process + ACP |
| `context-checkpoint-clean-restart` | history, summary, clean restart, prompt | core | checkpoint reused, bounds, projection | in-process + ACP |
| `context-checkpoint-interrupt-restart` | history, summary, interrupt, prompt | recovery | checkpoint reused, bounds | ACP only |
| `context-checkpoint-kill-restart` | history, summary, kill, prompt | recovery | checkpoint reused, bounds | ACP only |

`context-mid-turn-pruning` combines two mechanisms because the production
projection occurs while materializing the second request after a real Tool
Result. Splitting them would either duplicate the same expensive trace or test
mid-turn preparation without the mechanism whose request boundary matters.

The manual-reset Scenario may reuse the existing fixture directory, but use a
new ID or update every frozen digest atomically. Never edit checked-in frozen
documents without regenerating and reviewing all referencing digests.

## 11. Subject profiles and matrix partitioning

EvalSet expansion is a Cartesian product. Different budget profiles must not be
placed as multiple Subjects in one broad set, because each Scenario would run
against every profile and create meaningless Cells. Do not add include/exclude
syntax to EvalSet v1 for this suite.

Use one Subject document per semantic profile and paired in-process/ACP
EvalSets, following the existing parity baseline/candidate pattern:

- **core:** small enough to trigger compaction from bounded checked-in prompts,
  but large enough for ordinary fixture responses;
- **prune:** protected-tail budget forces the large Tool Result above
  `MaxProjectedToolResultTokens`, with pruning count capped above zero;
- **overflow:** ordinary estimate stays below local hard input so the fixture's
  provider rejection, not admission planning, triggers recovery;
- **chunk:** covered source fits only through at least two summarizer calls and
  `maxSummaryChunks` is high enough to complete;
- **anchor:** plain next-request estimate remains below trigger while the prior
  fixture response's fixed provider input usage raises the eligible anchor
  above trigger;
- **recovery:** same semantic Context configuration as core where possible,
  used only with the ACP executor.

Each Subject remains secret-free and names `fixture://context-mechanism`.
Profile numbers must be derived in tests from the actual wire meter and fixture
content, then frozen; do not guess percentages until a real end-to-end test
demonstrates the intended branch with margin.

Suggested sets:

```text
eval/sets/context-core-inprocess.json
eval/sets/context-core-acp.json
eval/sets/context-prune-inprocess.json
eval/sets/context-prune-acp.json
eval/sets/context-overflow-inprocess.json
eval/sets/context-overflow-acp.json
eval/sets/context-chunk-inprocess.json
eval/sets/context-chunk-acp.json
eval/sets/context-anchor-inprocess.json
eval/sets/context-anchor-acp.json
eval/sets/context-recovery-acp.json
```

Paired sets use identical Scenario order, repetition count, pairing seed, and
pairing tags. They differ only where the accepted executor contract requires
it. `interrupt` and `kill` never appear in an in-process set.

## 12. CI and execution lanes

### Ordinary PR CI

Keep the accepted PR matrix tiny. Strengthen or replace its existing one
Context smoke Cell with `context-pre-turn-summary` or
`context-mid-turn-pruning`; do not add the full matrix to every PR. Existing
tool/workspace and parity Cells remain in place.

The representative Context Cell must be offline, deterministic, and complete
well inside the existing PR timeout. A second executor is already represented
by the parity Cell; ordinary PR CI need not multiply every Context Scenario by
both executors.

### Explicit/scheduled deterministic lane

Run all paired core/prune/overflow/chunk/anchor sets and the ACP recovery set
on the supported Unix environment. Publish manifests, scores, reports, and timing
artifacts. No credential or `--live` flag is allowed.

The steady-state scan-cost fix is not observable from an artifact-only verifier
without adding forbidden store instrumentation to eval. The scheduled lane must
therefore also run
`TestPrepareContextResumesScanFromCheckpointRatherThanStreamStart` as a hard
structural regression and retain `BenchmarkScanFromCheckpoint` alongside
`BenchmarkScan` as performance evidence. Benchmark values are reported, not a
machine-independent hard threshold; re-reading pre-checkpoint history is a hard
test failure.

Windows continues to cross-build. It does not run ACP subprocess recovery until
the parent design's Job Object contract exists.

### Live lane

The existing `context-quality-live.example.json` remains consent-gated by both
`lane=live` and `--live`. A follow-up may add repeated real-model tasks for
constraint retention, decision continuity, Tool Result attribution, summary
fidelity, latency, token use, and stability. Those Scores are evidence for GA
decisions, not ordinary PR gates.

## 13. File map

Expected implementation touch points:

| Path | Change |
| --- | --- |
| `internal/harness/domain/events.go` | add per-request pruning count |
| `internal/harness/domain/codec.go` and tests | additive field validation/round trip |
| `internal/harness/application/context_orchestrator.go` and tests | copy observed pruning count |
| `internal/harness/transcript/codec.go` and tests | project pruning count on `context.prepared` |
| `cmd/och-eval/fixture.go` and tests | add stateless `context-mechanism` fixture protocol |
| `internal/harness/eval/context_trace.go` and tests | typed audit trace and correlation validation |
| `internal/harness/eval/context_verifier.go` and tests | focused deterministic verifier catalog |
| `internal/harness/eval/verifier.go` | register IDs |
| `eval/scenarios/context-*/**` | checked-in Context fixtures and Scenario documents |
| `eval/subjects/context-*.json` | frozen profile Subjects |
| `eval/sets/context-*.json` | paired and ACP-only matrices |
| `docs/architecture/evaluation*.md` | operator commands and implemented boundary |
| `docs/architecture/evaluation-evidence.md` | commit/test/mutation/runtime evidence and remaining gaps |

Do not add a new concrete adapter or import SQLite from eval.

## 14. Implementation sequence

Each slice follows red-green-refactor and lands independently green.

### Slice 1: Close the pruning evidence gap

1. Add failing domain codec and transcript round-trip tests.
2. Add a failing Application mapping test that starts with a prepared result
   whose pruning count is non-zero.
3. Add the optional field and mappings.
4. Run domain, Application, transcript, and architecture tests.

### Slice 2: Build the typed Context trace

1. Add table-driven tests using minimal canonical audit fixtures.
2. Prove duplicate decisions, orphan terminal compactions, broken pairings,
   malformed JSON, and missing audit fail closed.
3. Implement one parse/index pass shared by all Context verifiers.
4. Keep the existing audit helper compatible or migrate it without changing
   the old verifier's public result.

### Slice 3: Add the stateless fixture protocol

1. Add request-classification tests first.
2. Prove a marker in old history cannot select the latest request's branch.
3. Add valid summary/depth behavior and overflow before/after checkpoint.
4. Add large-file tool call and projected-continuation validation.
5. Run fixture tests with the race detector.

### Slice 4: Land core Scenarios and verifiers

1. Add manual summary/reset, pre-turn, and clean-restart verifier tests,
   including tampered and missing evidence.
2. Add checked-in fixtures and Subjects.
3. Run each Scenario end-to-end through in-process first.
4. Freeze digests only after behavior and evidence are stable.
5. Run the paired ACP sets and generate parity reports.

### Slice 5: Land pruning, overflow, chunk, and usage-anchor Scenarios

1. Add one failing end-to-end test per production mechanism before its verifier
   implementation.
2. Assert behavior from canonical audit plus fixture-observed success, not
   config.
3. Add mutation tests that remove the pruning count, reduce chunk count to one,
   remove the overflow retry, make retry size non-decreasing, or clear the
   applied usage-anchor fact; each mutation must stop the relevant criterion
   from passing.
4. Freeze profile values and document their measured margin.

### Slice 6: ACP abrupt restart and suite publication

1. Add completed-checkpoint interrupt/kill Scenarios only to the ACP set.
2. Prove owned process group termination/reap and same-Session `session/load`
   through existing executor evidence and a successful post-restart prompt.
3. Update architecture docs and evidence ledger.
4. Record the still-deferred active-compaction crash barrier explicitly.

## 15. Test and acceptance contract

Targeted package checks during development:

```bash
go test ./internal/harness/domain ./internal/harness/application ./internal/harness/transcript -count=1
go test ./cmd/och-eval ./internal/harness/eval -count=1
go test -race ./cmd/och-eval ./internal/harness/eval -count=1
go test ./internal/harness/application -run TestPrepareContextResumesScanFromCheckpointRatherThanStreamStart -count=1
go test ./internal/harness/contextengine -run '^$' -bench 'BenchmarkScan' -benchmem
```

End-to-end acceptance uses fresh artifact roots and the checked-in sets:

```bash
go run ./cmd/och-eval run -set eval/sets/context-core-inprocess.json -artifacts "$(mktemp -d)"
go run ./cmd/och-eval run -set eval/sets/context-core-acp.json -artifacts "$(mktemp -d)"
go run ./cmd/och-eval run -set eval/sets/context-prune-inprocess.json -artifacts "$(mktemp -d)"
go run ./cmd/och-eval run -set eval/sets/context-overflow-inprocess.json -artifacts "$(mktemp -d)"
go run ./cmd/och-eval run -set eval/sets/context-chunk-inprocess.json -artifacts "$(mktemp -d)"
go run ./cmd/och-eval run -set eval/sets/context-anchor-inprocess.json -artifacts "$(mktemp -d)"
go run ./cmd/och-eval run -set eval/sets/context-recovery-acp.json -artifacts "$(mktemp -d)"
```

The exact ACP binary flag required by the current CLI must be included where
applicable; the implementer should copy the checked-in parity command rather
than inventing a second launch path.

Before completion:

```bash
gofmt -w <changed-go-files>
go test ./... -count=1
go test -race ./internal/harness/eval ./cmd/och-eval -count=1
go vet ./...
CGO_ENABLED=0 go build ./...
git diff --check origin/main...HEAD
```

Acceptance requires:

- every intended Cell publishes an Outcome, complete manifest, and Score;
- all deterministic Context criteria pass on untampered fixtures;
- missing, malformed, truncated, and contradictory evidence never passes;
- offline regrade produces the same verdicts without starting OCH or the
  fixture Provider;
- baseline/candidate pairing reports no undeclared semantic difference;
- no test relies on sleep for process or compaction synchronization;
- the ordinary PR matrix remains within its documented Cell count and lane;
- live evaluation remains impossible without explicit consent;
- docs and evidence ledger do not claim active-compaction crash coverage.

## 16. Review checklist

Before implementation begins, the assignee should answer these from repository
evidence:

- Does the current audit artifact include `model.request.recorded` messages in
  the form the pruning verifier expects?
- Does the fixture parser identify the summary prompt by its version marker,
  not by a fragile incidental sentence?
- Do frozen core profile prompts trigger with comfortable margin on both
  executor surfaces?
- Can one semantic Subject be used by both executor sets, or does current ACP
  argv resolution require the established mirrored Subject document? Preserve
  current parity behavior if so.
- Do interrupt/kill tests prove process reap before relaunch and avoid Windows
  claims?
- Are all Scenario, Subject, Executor, and EvalSet digest changes reviewed as
  exact canonical-byte changes?
- Does every verifier distinguish unavailable evidence from observed mismatch?
- Does the evidence ledger name both achieved coverage and the
  active-compaction crash barrier still missing?

If any answer changes the architecture boundary, stop and amend this document
before code is written.
