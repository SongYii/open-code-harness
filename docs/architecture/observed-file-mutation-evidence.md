# Observed-File-Mutation Completion Evidence

**Status:** Evidence ledger for the
[Observed-State Safe File Mutation](observed-file-mutation.md) contract. Every
command below was run in this repository and its result is reported as
observed, including the ones that went differently than planned.

## Commits

| Commit | Task | Content |
| --- | --- | --- |
| `148004e` | 1 | Guard, version, read, and result values plus seven filesystem error codes (`tools/files.go`, `tools/errors.go`) |
| `bae2919` | 2 | Guarded atomic mutation: final port, opaque versions, staged publication, literal edit (`workspacefs`) |
| `f059612` | 3 | Per-session observation table, guard derivation, and the seven Tool Result mappings (`application`) |
| `0619cf8` | 4 | `edit_file` in the catalog, boolean schema leaf, parse and dispatch |
| `8ea5d63` | 5 | Pre-publication fault seam, boundary scenarios, race and repetition matrices |

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

**A message that was wrong the day after it was written.** Task 3's plan mapped
`fs_stale_version` to "file changed since it was read; re-read it and retry".
Wiring it revealed that an unseen target produces a create-if-absent guard, and
the adapter refuses that as stale when something is in fact there — so a model
was told a file had changed since it read it, about a file it had never read.
That sends it to re-read something it has no memory of and calls that a retry.
Application knows whether it had an observation and the adapter does not, so
the translation lives there and reports `fs_not_observed`.

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

## Verification command output

Go 1.26.6, linux/amd64. Run against this branch's own working tree.

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
(all packages ok; elapsed 369.95s on the final tree)

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

## Known limitations

These are the contract's own exclusions, restated here so a reader of the
ledger does not have to infer them from what is absent.

- `exec` is not mediated. `TestExecIsNotMediatedAndTheNextEditDetectsIt`
  proves the part that is true — the next structured write against a file
  `exec` changed is refused as stale — and does not claim the part that is
  not.
- The window between `checkGuard` and `os.Rename` is not closed. An external
  writer landing there loses its change silently.
- Windows cross-compiles and has a version function; no runtime behaviour is
  claimed or tested there.
- Two `och` processes over one workspace do not share observations. The guard
  still refuses the second one's blind write.
