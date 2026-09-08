# System Prompt and Workspace Instructions Completion Evidence

**Status:** Evidence ledger for the implemented
[Versioned System Prompt and Workspace Instructions](system-prompt-workspace-instructions.md)
contract. Commands below report observed results; a command not run is named
as such rather than counted as a pass.

## Research, design, and plan

- Research gate:
  `docs/research/architecture-gates/2026-09-04-agent-instructions-and-file-mutation.md`
- Accepted English design and Chinese reading copy:
  `docs/superpowers/specs/2026-09-04-system-prompt-workspace-instructions-design.md`
  and `.zh-CN.md`
- Implementation plan:
  `docs/superpowers/plans/2026-09-08-system-prompt-workspace-instructions.md`

The research compared established agent-instruction behavior and selected a
fixed harness prompt plus hierarchical `AGENTS.md` with append-only deltas.
The implementation keeps `FileVersion` as a fast hint and content digest as
authority, matching the observed-state design used by databases: optimistic
observation may avoid work, but committed identity is validated before state
advances.

## Task commits

| Commit | Content |
| --- | --- |
| `ee0d649` | Embedded, versioned coding-agent prompt and golden digest |
| `35439ae` | Canonical workspace instruction event vocabulary and validation |
| `9c9935e` | Pure reconcile/replay and bounded deterministic rendering |
| `440355a` | Durable filesystem reconciliation and failure retention |
| `a23e40d` | Cache-stable conversation request assembly and tool-touch discovery |
| `da11dd2` | Summary/reset instruction snapshots, recovery validation, and adapter ownership |
| `e40c38b` | Transcript mapping, restart scenario, and ACP/in-process durable request parity |

## Observed verification before documentation

Task 6 required race command:

```text
$ go test -race ./internal/harness/contextengine ./internal/harness/application \
    ./internal/harness/adapters/memory ./internal/harness/adapters/sqlite -count=1 -timeout=10m
ok  contextengine  1.116s
ok  application    46.936s
ok  memory          8.457s
ok  sqlite         63.790s
```

Task 7 required race command:

```text
$ go test -race ./internal/harness/application ./internal/harness/adapters/acp \
    ./internal/harness/composition ./internal/harness/transcript -count=1 -timeout=10m
ok  application  47.665s
ok  acp          34.018s
ok  composition  10.871s
ok  transcript    2.501s
```

The first full `go test ./... -count=1` run did not pass. Real eval attempts
recorded root absence as `workspace.instructions.recorded`, while transcript
projection still rejected that new canonical type as `unsupported_event_type`.
That made transcript and audit evidence missing and caused downstream regrade,
judge, parity, and variance tests to fail. `e40c38b` added the mapping. The
representative evidence and judge tests then passed, followed by:

```text
$ go test ./internal/harness/eval ./cmd/och-eval -count=1 -timeout=10m
ok  internal/harness/eval  64.252s
ok  cmd/och-eval           11.305s
```

This failure is retained because it shows the transcript test was not merely
decorative: without the mapping the real evidence pipeline was broken.

## Mechanism-to-test evidence

| Mechanism | Principal evidence |
| --- | --- |
| Prompt identity and stable bytes | `TestSystemPromptIdentityAndGoldenDigest`, `TestSystemPromptCarriesStableSafetyAndWorkflowGuidance` |
| Version hint, digest authority, same-digest suppression | `TestWorkspaceInstructionsReconcileUsesVersionAsHintAndDigestAsIdentity` |
| Confirmed absence vs transient failure | `TestWorkspaceInstructionsReconcileRecordsConfirmedRootAbsenceOnce`, `TestWorkspaceInstructionsReconcileRetainsStateAndDeduplicatesFailureEpisode` |
| Marker escaping and specific-first bounds | `TestRenderBatchEscapesARepositorySuppliedClosingMarker`, `TestRenderBatchPreservesMostSpecificSourceAndNamesBroadOmission` |
| 256-path cap | `TestReconcileRefusesSource257BeforeReadingItsContent`, `TestReconcileRefusesBatchThatWouldCrossSourceLimit` |
| Event-before-request and nested discovery | `TestSuccessfulStructuredFileToolDiscoversNestedInstructionsBeforeNextRequest` |
| Stable request prefix | `TestConversationRequestsKeepStablePrefixAndAppendInstructionChanges` |
| Restart recheck | `TestWorkspaceInstructionsRestartReplaysStateAndRechecksDisk` |
| Summary/reset rebase and summarizer exclusion | `TestPrepareContextRebasesInstructionsWithoutSendingThemToSummarizer`, `TestPrepareContextResetRebasesTheSameEffectiveInstructionSnapshot` |
| Snapshot digest/render agreement and ownership | Domain snapshot tests, `TestInstructionSnapshotCheckpointConversionRejectsSemanticDrift`, Memory/SQLite checkpoint tests |
| ACP/in-process parity | `TestAssemblyRunsAToolCallingTurnEndToEnd`, `TestAssemblyServesACPTurnEndToEnd` |
| Transcript facts and frozen JSONL | `TestProjectRecordCarriesWorkspaceInstructionsAndCheckpointSnapshot`, `TestProjectRecordFrozenPayloads`, `TestGoldenFixturesRoundTrip` |

## Live DeepSeek validation

Status: **partial**. An explicitly authorized run against the
OpenAI-compatible DeepSeek endpoint using `deepseek-chat` completed on
2026-09-08. The credential was supplied through a non-echoing stdin path and
was not placed in the repository, configuration, evidence events, or this
ledger.

Subject provider calls: **completed**. Attempt
`30f1d1728e0e1e21572c80c697a30360` completed with evidence collection marked
complete. The first turn reported 1,028 input, 45 output, and zero cached input
tokens in 1,115 ms. The second turn reported 1,090 input, 61 output, and 1,024
cached input tokens in 1,322 ms. Thus 93.9% of the second request's input tokens
were reported as cached, evidence that the stable request prefix works on this
route. The endpoint did not return a provider request ID and the repository has
no price entry for this route, so cost remains unavailable. The model retained
the first-turn prohibition and refused the conflicting second-turn request;
the forbidden `secrets.txt` file was not created.

Judge provider call: **not attempted**. Score
`f8aa8fa8fc9bd8b484083c743a82fd45` was `indeterminate` before any model judge
request because `manifest-complete-v1` was indeterminate. The checked-in
scenario requires a `workspace` evidence role but contains no `collect` action,
so its otherwise complete eleven-entry manifest cannot satisfy that
prerequisite.

This run also did not validate preservation across compaction. The explicit
compact action produced no checkpoint, summary, or compaction event: the short
first turn had no safe covered cut point, and the eval executor currently
treats `CompactSession` returning `Ran == false` as successful action
completion. The observed refusal therefore proves ordinary two-turn retention
and live cache reuse only. Fixing the scenario's evidence contract and making a
requested no-op compaction visible are separate evaluation changes; a paid
rerun is required before claiming post-compaction quality or a live judge pass.

## Final verification and mutation status

All twelve minimum deliberate mutations were made one at a time in production
code, observed failing through the named test, and restored before the green
regression. These were real source mutations, not tests that merely simulated
an alternative implementation.

| Mutated mechanism | Failure that was observed |
| --- | --- |
| Prompt digest enforcement | `TestSystemPromptIdentityAndGoldenDigest`: changed `PromptDigest` differed from the golden digest |
| Version-only fast path | `TestWorkspaceInstructionsReconcileUsesVersionAsHintAndDigestAsIdentity`: unchanged version performed the forbidden 1 MiB read |
| Same-digest suppression | The same test rejected/appended the manufactured same-content replace instead of remaining at version 2 |
| Confirmed absence/remove | `TestReconcileSetReplaceRemoveAndReappear`: the prior source remained effective |
| Transient-failure retention | `TestWorkspaceInstructionsReconcileRetainsStateAndDeduplicatesFailureEpisode`: the effective-set digest changed on permission failure |
| Closing-marker escaping | `TestRenderBatchEscapesARepositorySuppliedClosingMarker`: repository text regained an unescaped closing delimiter |
| Specific-first budgeting | `TestRenderBatchPreservesMostSpecificSourceAndNamesBroadOmission`: the nested source was omitted and the broad source retained |
| 256-path cap | `TestReconcileRefusesSource257BeforeReadingItsContent`: source 257 was admitted to validation |
| Event before consuming request | `TestSuccessfulStructuredFileToolDiscoversNestedInstructionsBeforeNextRequest`: the second request omitted the nested delta |
| Summarizer exclusion | `TestPrepareContextRebasesInstructionsWithoutSendingThemToSummarizer`: unique repository prose appeared in summarizer input |
| Snapshot digest verification | `TestInstructionSnapshotCheckpointConversionRejectsSemanticDrift`: a changed digest was accepted when the digest comparison was removed |
| ACP/in-process parity | `TestAssemblyServesACPTurnEndToEnd`: an ACP-only input suffix changed the durable request shape |

The snapshot mutation exposed that the semantic-drift table covered prompt ID
and rendered text but did not directly mutate `Digest`; the digest case was
added before the production comparison was removed and observed failing. The
parity mutation similarly strengthened the common composition assertion to
require the exact original user input on every durable provider request, so a
transport-only rewrite can no longer pass merely because both paths carry the
same system prompt and repository instruction.

After restoring every mutation, the focused green regression reported:

```text
ok  internal/harness/agentinstructions  0.019s
ok  internal/harness/application        0.018s
ok  internal/harness/composition        0.287s
ok  internal/docsguard                   0.167s
```

The pre-commit full verification on 2026-09-08 reported:

```text
$ go test -race ./... -count=1 -timeout=20m
PASS (all packages)
internal/harness/eval        206.236s
internal/harness/adapters/sqlite  74.071s
internal/harness/application  45.232s
internal/harness/composition  10.209s

$ go vet ./...
PASS
$ go mod tidy -diff
PASS (no diff)
$ gofmt -l .
PASS (no output)
$ git diff --check
PASS
$ npm run typecheck
PASS
$ npm test
4 files and 18 tests passed
$ npm run build
PASS; Vite built 7 modules
```

This is pre-commit evidence. A separate final section will record the exact
post-commit rerun before PR creation; this ledger does not substitute the
earlier output for that required final check.

## Post-commit verification

Commit `e1ea342` (`docs: publish workspace instructions contract`) contains
the completed implementation contract, reading copy, evidence unit,
authority/root-README wiring, docsguard rule, and the two test-strengthening
changes found by mutation. With that commit checked out and a clean tracked
tree, the complete command set was rerun on 2026-09-08:

```text
$ go test -race ./... -count=1 -timeout=20m
PASS (all packages)
internal/harness/eval             205.297s
internal/harness/adapters/sqlite   73.521s
internal/harness/application       49.241s
internal/harness/composition       11.127s

$ go vet ./...
PASS
$ go mod tidy -diff
PASS (no diff)
$ gofmt -l .
PASS (no output)
$ git diff --check HEAD
PASS
$ npm run typecheck
PASS
$ npm test
4 files and 18 tests passed in 5.83s
$ npm run build
PASS; Vite built 7 modules in 70ms
```

The evidence-only commit that adds this section necessarily follows the commit
whose output it records. Before PR creation it is checked with docsguard,
format/diff cleanliness, and the same final branch verification; this ledger
does not pretend a Git commit can contain its own not-yet-created object ID.

## Exclusions

- Windows-specific runtime behavior is outside this module.
- No post-compaction live-model instruction-retention claim is made.
- No live judge verdict has been obtained.
- `exec` and MCP do not drive nested instruction discovery.
