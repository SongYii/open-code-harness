# Observed-File-Mutation Completion Evidence

**Status:** Evidence ledger for the
[Observed-State Safe File Mutation](observed-file-mutation.md) contract,
including final-review Fixes A and B. Every command below was run in this
repository and its result is reported as observed, including the ones that went
differently than planned.

## Commits

| Commit | Task | Content |
| --- | --- | --- |
| `148004e` | 1 | Guard, version, read, and result values plus seven filesystem error codes (`tools/files.go`, `tools/errors.go`) |
| `bae2919` | 2 | Guarded atomic mutation: final port, opaque versions, staged publication, literal edit (`workspacefs`) |
| `f059612` | 3 | Per-session observation table, guard derivation, and the seven Tool Result mappings (`application`) |
| `0619cf8` | 4 | `edit_file` in the catalog, boolean schema leaf, parse and dispatch |
| `8ea5d63` | 5 | Pre-publication fault seam, boundary scenarios, race and repetition matrices |

## Latest-main integration commits

These are the reviewed fixes as integrated on the current branch after the
`origin/main` base. Implementation claims below use these integrated SHAs, not
the review-source SHAs.

| Review source | Integrated commit | Role |
| --- | --- | --- |
| `4a44822` | `a74309e8314035fc4d14d11428ce6ab25542ace0` | Fix A RED |
| `0d98071` | `4b6dbd24dc1829c0171946067fb759bfc0d5e425` | Fix A GREEN |
| `86d9dc2` | `3367a5b34a7313198fc6082f0ee9f5e69da9dd28` | Fix B RED |
| `bbaf36f` | `0714c68aea0a16f0b45344f1896402971b2285bd` | Fix B GREEN |
| `102d706` | `ef27018efda1731aff085d59dd7a8fcfabe9e81d` | synchronized documentation |
| `c5b893b` | `c5b893bd1d31bad856eadda2aef1f6b12c8f20a3` | RED coverage for guarded writes above the edit limit |
| `335e273` | `335e273c2cae2f95984d6e41a570ee927bd6cecd` | GREEN streaming published-write verifier |

The historical `f661140` latest-main integration report recorded these
successful integration gates: focused repeated workspacefs/application race
tests, `go test ./internal/docsguard ./internal/harness/architecture -count=1`,
`go test ./... -count=1`, `go vet ./...`, Windows and Darwin builds, the
ACP-web build, and both `git diff --check` forms. It also recorded that the
scheduled Context matrix remains gated by
`OCH_EVAL_SCHEDULED_CONTEXT_MATRIX`. That report artifact was reverted by
`eb6e48c` and is absent from the net diff; this ledger preserves the relevant
historical gate record instead of treating the reverted artifact as present.

## Final-review source provenance

The final-review fixes were originally reviewed as the following source
commits. This table is provenance only; implementation claims use the
latest-main integration SHAs above.

| Source commit | Role | Finding covered |
| --- | --- | --- |
| `4a44822` | Fix A RED | edit-expansion and post-publication interference regressions |
| `0d98071` | Fix A GREEN | bounded result sizing and verification of the published revision |
| `86d9dc2` | Fix B RED | recovery/wire-code, observed-absent, create-conflict, and FIFO regressions |
| `bbaf36f` | Fix B GREEN | eight-code recovery mapping and uniform non-regular target handling |
| `102d706` | documentation | synchronized plan, English, Chinese, and evidence corrections |

## Final broad-review findings and corrections

The final broad review found four issues: edit expansion could exceed the
whole-file bound; a returned mutation version could adopt an unobserved
post-publication change; observed-absent and create-conflict recovery was
incomplete; and three stable error names plus special-file behavior diverged
from the accepted plan. Fix A closes the first two findings. Fix B closes the
last two.

The accepted observation table requires an edit after an observed-absent read
to return `fs_not_found`; the earlier seven-code lists omitted that eighth
wire code. The final stable names are `fs_edit_not_found`,
`fs_ambiguous_edit`, and `fs_not_regular_file`. Directories and special files
share `fs_not_regular_file`, while raw `fs.ErrExist` from create-if-absent
maps to the bounded `fs_not_observed` read-before-change recovery result.

## Mechanism → test → mutation result

Every row is a mutation actually applied and observed, then reverted.

| Mechanism | Test | Mutation result |
| --- | --- | --- |
| Guard combinations (`MutationGuard.Validate`) | `TestMutationGuardValidate` | Accepting a replace guard with no version fails the test — caught, restored. |
| Error vocabulary (`validErrorCode`) | `TestFilesystemErrorCodes`, `TestFilesystemErrorCodesAreDistinct` | Dropping `fs_not_observed` fails both — caught, restored. |
| Guard-before-match ordering (`Edit`) | `TestEditChecksTheGuardBeforeTheMatch` | Matching first reports `fs_edit_not_found` where the guard was stale — caught, restored. |
| Dominant newline restoration (`applyLiteralEdit`) | `TestEditPreservesTheDominantNewline` | Dropping the restoration rewrites a CRLF file to LF — caught, restored. |
| Atomic create publication (`os.Link`) | `TestConcurrentCreatorsAcrossFileSystemsLeaveExactlyOneWinner` | Publishing a create by rename yields 2 then 8 winners — caught, restored. |
| Stale-version equality (`checkGuard`) | ten tests across `workspacefs` | Inverting the comparison fails `TestReadThenGuardedWriteThenStaleWrite`, both fault tests, and every edit test — caught, restored. |
| Unique-match cardinality, adapter (`applyLiteralEdit`) | `TestEditMissingAndAmbiguousAreDistinctRefusals` | Bypassing it silently edits one of several matches — caught, restored. |
| Unique-match cardinality, double (`memEdit`) | `TestEditFileMissingAmbiguousAndReplaceAll` | Bypassing it in MemFS fails the Application test — caught, restored. |
| Resume clears observations (`ResumeSession`) | `TestResumeForgetsWhatTheSessionObserved` | Keeping them lets a post-resume write succeed — caught, restored. |
| Absent vs. unseen distinction (`guardForEdit`) | `TestFileObservationsTransitions` | Collapsing them reports `fs_not_observed` for a file known absent — caught, restored. |
| A failed write does not advance state (write path) | `TestARefusedWriteDoesNotLicenseTheNextOne` | Refreshing the observation after a refusal lets the retry through — caught, restored. |
| Edit refuses an unobserved target (`guardForEdit` in dispatch) | `TestEditFileRequiresAReadFirst` | Falling back to a write guard reports "file changed" for a file never read — caught, restored. |
| Identical replacement refusal (`parseToolArgs`) | `TestEditFileRejectsAnIdenticalReplacement` | Allowing it runs a mutation that cannot change anything — caught, restored. |
| Object-only root schema (`compileSchema`) | `TestABooleanLeafIsAllowedButABooleanToolSchemaIsNot`, `TestNewCatalogRejectsInvalidSpecs/type_boolean` | Removing the root check accepts a tool whose entire argument schema is a boolean — caught, restored. |

## Two mutations that initially caught nothing

Both are recorded because a mutation that changes no test result is a fact
about the tests, not a formality to skip.

**Publication by rename instead of link.** The first concurrency test drove
eight goroutines through one `FileSystem`, whose per-target lock had already
serialized them before publication could matter. Replacing `os.Link` with
`os.Rename` left it green, so it proved the lock and not the atomicity its name
claimed. It is renamed
`TestConcurrentCreatorsThroughOneFileSystemAreSerialized`, and a second test
drives eight independent `FileSystem` instances — which is also the real
situation the guard exists for, where the other writer is another process. The
re-aimed mutation then reported 2 and 8 winners.

**A failed write recording a bogus version.** Recording a made-up version after
a refusal changes nothing, because the very next re-read overwrites it. The
dangerous version is the well-meaning one: the write was refused, so refresh
the observation from disk and let the retry through. That turns the guard into
a speed bump and lets the second attempt destroy work the agent never looked
at. `TestARefusedWriteDoesNotLicenseTheNextOne` drives two writes with no read
between them, and the re-aimed mutation fails it.

**A related finding, recorded for the same reason.** Bypassing the adapter's
unique-match rule left the Application-level ambiguity test green, because that
test runs through MemFS and never reaches `workspacefs`. Two implementations of
one rule need two mutations, or one of the tests is proving nothing about the
code a reader assumes it covers. Both are now listed above.

## Contracts the implementation corrected

**A message corrected at the observation boundary.** Both an unseen and an
observed-absent write produce a create-if-absent guard. When something is
already there, the adapter preserves raw `fs.ErrExist`; Application resolves
that conflict where the observation table is visible. An unseen target maps to
`fs_not_observed` and the bounded read-before-change instruction, while an
observed-absent target maps to `fs_stale_version` and a re-read instruction.
An edit after an authoritative missing read is different: it returns
`fs_not_found` without calling the filesystem mutation port or advancing the
observation.

**An accidental guarantee made explicit.** Adding a boolean leaf to the schema
compiler broke a pre-existing test that expected `{"type":"boolean"}` to be
refused as a whole tool schema. The test had been passing because boolean was
not a compilable kind at all, not because a root check existed. `compileSchema`
now requires the root to be an object, which is what every downstream
guarantee — required fields, rejected unknown keys, per-field bounds — was
already assuming.

**A test double that would have proved the opposite.** `MemFS.AddFile` set a
fixed version, so reseeding a file to stand in for an external writer left the
version unchanged and the guard blind to it. Any "external change detected"
test built on that would have passed while proving nothing. `AddFile` now
advances the version.

## A boundary asserted at the table rather than end to end

The process-restart property — observations do not survive a process, so a
restart begins having seen nothing — is asserted by
`TestEachServiceGetsItsOwnObservationTable` rather than by running two Services
over one store. The end-to-end version was written first and failed with
`conflict/append_identity_mismatch`: this package's sequence ID generator
restarts from the same values, so the second Service collides on append
identity before it can reach a tool. The property itself is that no two
Services share a table, so it is asserted directly and the reason is recorded
in the test rather than left as a weaker end-to-end test that appears to prove
more.

## Historical source-branch verification output

Go 1.26.6, linux/amd64. Run against the historical source-branch working tree.

This block is historical source-branch evidence, not the current integrated
code head. Its first exact full-race run failed only in SQLite:
`TestConformance/limits_copies_cancellation_and_corruption` reported
`rejected over-limit request leaked identities: store/writer_fenced
(session=session-request-plus-one expected=0 actual=0 identity_kind=may_have_committed=false)`.
The focused command once and then at `-count=3` had 4/4 non-reproductions; a
subsequent historical full-race rerun exited 0 without a SQLite change.
```text
$ go build ./...
(clean)

$ go vet ./...
(clean)

$ CGO_ENABLED=0 go build ./...
(clean)

$ GOOS=windows go build ./...
(clean)

$ GOOS=darwin go build ./...
(clean)

$ go mod tidy -diff
(clean)

$ go test -race ./... -count=1
(historical successful source-branch run, elapsed 369.95s; not a final-tree or current-head claim)

$ cd cmd/acp-web-bridge/web && npm ci && npm run build
(ok)

$ go test ./internal/docsguard ./internal/harness/architecture -count=1
ok  	.../internal/docsguard	0.057s
ok  	.../internal/harness/architecture	0.395s

$ git diff --check
(clean)

$ go test -race ./internal/harness/adapters/workspacefs ./internal/harness/application \
      -run 'Test.*(Mutation|Observation|Stale|Concurrent|Resume|Edit)' -count=10
ok  	.../internal/harness/adapters/workspacefs	2.142s
ok  	.../internal/harness/application	6.245s

$ go test ./internal/harness/tools ./internal/harness/adapters/workspacefs \
      ./internal/harness/application ./internal/harness/composition -count=1
ok  	.../internal/harness/tools	0.012s
ok  	.../internal/harness/adapters/workspacefs	0.142s
ok  	.../internal/harness/application	4.346s
ok  	.../internal/harness/composition	2.896s
```

The scheduled Context matrix is unaffected by this work and was not run: it is
gated by `OCH_EVAL_SCHEDULED_CONTEXT_MATRIX`, which only a schedule-triggered
CI job sets.

## Latest-main code-head controller race pass

At code head `335e273`, the controller ran exactly:

```text
go test -race ./... -count=1
```

It exited 0 with no race report. Key package outputs were `cmd/och-eval`
56.357s, MCP 22.947s, SQLite 72.552s, workspacefs 1.587s, application
46.776s, and eval 199.642s. Every other package reported `ok` or `[no test
files]`. This is the latest-main code-head pass; it is distinct from every
historical source-branch full-race entry above.

## Known limitations

These are the contract's own exclusions, restated here so a reader of the
ledger does not have to infer them from what is absent.

- `exec` is not mediated. `TestExecIsNotMediatedAndTheNextEditDetectsIt`
  proves the part that is true — the next structured write against a file
  `exec` changed is refused as stale — and does not claim the part that is
  not.
- An uncooperative external writer can change the target after `checkGuard`
  but before our `os.Rename`; our rename can overwrite that competing revision.
  The verifier then sees our staged identity and expected bytes, not the
  overwritten revision, so it cannot detect this accepted lack of kernel CAS.
- A verifier detects staged-identity, expected-byte, and version mismatches
  through its final destination check. External mutation after the final stable
  verification / return boundary remains outside the guarantee and is detected
  only by the next guarded operation.
- Windows cross-compiles and has a version function; no runtime behaviour is
  claimed or tested there.
- Two `och` processes over one workspace do not share observations. The guard
  still refuses the second one's blind write.


## Update: a create conflict has two answers, not one (2026-09-07)

The hardening pass that bounded edit expansion also moved the create-conflict
answer into the shared classifier, mapping every raw `fs.ErrExist` to
`fs_not_observed`. That is right for one of the two situations it covers and
wrong for the other.

Both a session that never read the target and a session that read it, found
nothing, and then lost a race to an external creator produce a
`create_if_absent` guard, and both come back from the adapter as `fs.ErrExist`.
The adapter cannot distinguish them, because the difference is not on disk —
it is in the observation table. Telling the second one to "read the file before
changing it" instructs it to repeat a read it remembers making, which is the
failure Task 3 corrected once already in the other direction.

The gap was in the tests, not only in the change: nothing covered
observed-absent-then-created, so the regression passed CI.
`TestObservedAbsentThenExternallyCreatedSaysReRead` closes that, and it landed
on `main` first so the classifier change had to answer it rather than be
argued about in review.

The write path now resolves `fs.ErrExist` where the observation table is
visible — `fs_not_observed` when the session never looked, `fs_stale_version`
when it did — and `fs.ErrExist` is deliberately absent from
`classifyFilesystemError`, so an unresolved one falls through to the generic
failure rather than silently claiming a session never looked.

Mutations, both observed red:
  - classify every create conflict as never-observed ->
    TestObservedAbsentThenExternallyCreatedSaysReRead.
  - classify every create conflict as stale ->
    TestWriteFileRefusesToOverwriteAnUnobservedFile.
