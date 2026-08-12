# Task 5 Implementation Report

## Scope

Defined the provider-neutral Engine model stream and runtime delivery contracts,
stable Engine errors, reusable Model adapter contract suite, and deterministic
`ScriptedModel` / `RecordingSink` fixtures.

## TDD evidence

### Cycle 1: Engine contract and adapters

RED command:

```sh
GOCACHE=/private/tmp/open-code-harness-gocache go test ./internal/harness/engine/... ./internal/harness/testkit -run 'Test(ScriptedModel|RecordingSink|ModelContract)' -count=1
```

Relevant failing output:

```text
internal/harness/engine/modeltest/suite.go:14:2: no required module provides package github.com/SongYii/open-code-harness/internal/harness/engine
FAIL github.com/SongYii/open-code-harness/internal/harness/engine/modeltest [setup failed]
```

Why expected: the test-first contract suite imported the intentionally absent
Engine package and its Model/Runtime types.

GREEN command:

```sh
gofmt -w internal/harness/engine internal/harness/testkit
GOCACHE=/private/tmp/open-code-harness-gocache go test ./internal/harness/engine/... ./internal/harness/testkit -run 'Test(ScriptedModel|RecordingSink|ModelContract)' -count=1
```

Output:

```text
ok github.com/SongYii/open-code-harness/internal/harness/testkit 1.047s
```

### Cycle 2: Error-tree traversal

RED command:

```sh
GOCACHE=/private/tmp/open-code-harness-gocache go test ./internal/harness/engine -run TestIsCodeTraversesJoinedTreesAndTypedNilBranches -count=1
```

Relevant failing output:

```text
internal/harness/engine/runtime.go:66:16: undefined: Error
internal/harness/engine/runtime.go:66:28: undefined: CodeInvalidRequest
FAIL github.com/SongYii/open-code-harness/internal/harness/engine [build failed]
```

Why expected: `errors.go` was deliberately absent while the new test specified
the required stable Error API, so consumers could not yet compile.

GREEN command:

```sh
gofmt -w internal/harness/engine/errors.go internal/harness/engine/errors_test.go
GOCACHE=/private/tmp/open-code-harness-gocache go test ./internal/harness/engine -run TestIsCodeTraversesJoinedTreesAndTypedNilBranches -count=1
```

Output:

```text
ok github.com/SongYii/open-code-harness/internal/harness/engine 0.868s
```

### Cycle 3: Complete runtime payload grammar

RED command:

```sh
GOCACHE=/private/tmp/open-code-harness-gocache go test ./internal/harness/testkit -run TestScriptedModel -count=1
```

Relevant failing output:

```text
internal/harness/engine/modeltest/suite.go:173:26: undefined: engine.NewEmitter
internal/harness/engine/modeltest/suite.go:177:24: undefined: engine.RuntimePayload
FAIL github.com/SongYii/open-code-harness/internal/harness/testkit [build failed]
```

Why expected: the expanded test-first suite exercised all six legal runtime
payloads while `runtime.go` was deliberately absent.

GREEN command:

```sh
gofmt -w internal/harness/engine internal/harness/testkit
GOCACHE=/private/tmp/open-code-harness-gocache go test ./internal/harness/engine/... ./internal/harness/testkit -run 'Test(ScriptedModel|RecordingSink|ModelContract)' -count=1
```

Output:

```text
ok github.com/SongYii/open-code-harness/internal/harness/engine 1.380s
ok github.com/SongYii/open-code-harness/internal/harness/testkit 0.915s
```

## Final verification

```sh
gofmt -w internal/harness/engine internal/harness/testkit
GOCACHE=/private/tmp/open-code-harness-gocache go test -race ./internal/harness/engine/... ./internal/harness/testkit -count=1
GOCACHE=/private/tmp/open-code-harness-gocache go test ./... -count=1
GOCACHE=/private/tmp/open-code-harness-gocache go vet ./...
git diff --check
```

Results: race suite passed (`engine` 1.546s, `testkit` 2.141s); normal full
suite passed (memory, application, domain, engine, and testkit); `go vet` and
`git diff --check` exited successfully. `gofmt` completed before verification.

## Files changed

- `internal/harness/engine/doc.go`
- `internal/harness/engine/model.go`
- `internal/harness/engine/runtime.go`
- `internal/harness/engine/errors.go`
- `internal/harness/engine/errors_test.go`
- `internal/harness/engine/modeltest/suite.go`
- `internal/harness/testkit/scripted_model.go`
- `internal/harness/testkit/scripted_model_test.go`
- `internal/harness/testkit/recording_sink.go`
- `internal/harness/testkit/recording_sink_test.go`

## Self-review

- `Emitter.Emit` accepts only `RuntimePayload`, preserving Emitter ownership of
  correlation and ordinal.
- Payload validation precedes cancellation/ordinal allocation; sink failures
  consume their ordinal while pre-attempt cancellation does not.
- Stable codes are ASCII lower-snake values with the specified one-to-64-byte
  grammar.
- `Error` methods and `IsCode` tolerate direct and joined typed-nil errors.
- `ScriptedModel` records requests under a mutex and returns defensive call
  snapshots. `RecordingSink` records attempts before deterministic one-shot
  failure and returns defensive snapshots.
- Public docs state the Model-stream and RuntimeSink concurrency ownership.

## Concerns

None identified. Task 6 remains responsible for enforcing ModelStream event
grammar and exactly-once Close ownership during a run; Task 5 intentionally
defines the ports and deterministic fixtures only.

## Fix round 1

### Cycle 4: ordinal exhaustion and declared error codes

Covering tests: `internal/harness/engine/runtime_test.go` and
`internal/harness/engine/errors_test.go`.

RED command:

```sh
GOCACHE=/private/tmp/open-code-harness-gocache go test ./internal/harness/engine -run 'Test(IsCodeAcceptsOnlyDeclaredCodes|EmitterDoesNotWrapExhaustedOrdinal|EmitterExhaustionFollowsValidationAndCancellation)' -count=1
```

Relevant failing output:

```text
--- FAIL: TestEmitterDoesNotWrapExhaustedOrdinal
sink saw []engine.RuntimeEvent{... Ordinal:0x0 ...}, want no exhaustion attempt
FAIL github.com/SongYii/open-code-harness/internal/harness/engine
```

Why expected: incrementing `math.MaxUint64` wrapped the local runtime ordinal to
zero and attempted sink delivery. The added error-code test also specified that
invented requested codes must never match.

GREEN command:

```sh
gofmt -w internal/harness/engine internal/harness/testkit
GOCACHE=/private/tmp/open-code-harness-gocache go test ./internal/harness/engine -run 'Test(IsCodeAcceptsOnlyDeclaredCodes|EmitterDoesNotWrapExhaustedOrdinal|EmitterExhaustionFollowsValidationAndCancellation)' -count=1
```

Output:

```text
ok github.com/SongYii/open-code-harness/internal/harness/engine 0.861s
```

Minimal fix: `Emitter.Emit` now returns `CodeDelivery` with the private
`errRuntimeOrdinalExhausted` sentinel before allocation or sink invocation;
`validErrorCode` limits Error matching to the seven declared codes.

### Cycle 5: reusable adapter/runtime contract coverage

Covering test file: `internal/harness/engine/modeltest/suite.go`, executed
through `internal/harness/testkit/scripted_model_test.go`.

The suite now covers all advertised Stream value/error pairs, nil-stream
precedence, CloseError and call counters, every illegal runtime field
combination, independent invalid correlation fields, sink-triggered cancellation
after an attempted delivery, and deterministic `Done()`-access cancellation
barrier behavior. Existing adapter behavior already satisfied these added port
cases, so the focused run was green on first execution; no production branch
was added for them. The impossible constructor branch and its unused dependency
were removed.

Focused command/output:

```sh
GOCACHE=/private/tmp/open-code-harness-gocache go test ./internal/harness/engine/... ./internal/harness/testkit -run 'Test(ScriptedModel|RecordingSink|ModelContract)' -count=1
ok github.com/SongYii/open-code-harness/internal/harness/testkit 0.984s
```

### Fix round 1 final verification

```sh
gofmt -w internal/harness/engine internal/harness/testkit
GOCACHE=/private/tmp/open-code-harness-gocache go test -race ./internal/harness/engine/... ./internal/harness/testkit -count=1
GOCACHE=/private/tmp/open-code-harness-gocache go test ./... -count=1
GOCACHE=/private/tmp/open-code-harness-gocache go vet ./...
git diff --check
```

Results: race suite passed (`engine` 2.148s, `testkit` 2.721s); full suite
passed (memory, application, domain, engine, testkit); vet and diff checks
passed. Files changed in this fix round: `runtime.go`, `runtime_test.go`,
`errors.go`, `errors_test.go`, `modeltest/suite.go`, `scripted_model.go`, and
this report. Self-review: exhaustion is checked only after payload validation
and pre-attempt cancellation, takes no ordinal and makes no sink call; requested
codes are whitelisted; the contract suite has no timing-based blocking inference.

## Fix round 2

Covering files: `internal/harness/testkit/scripted_model_test.go` and
`internal/harness/engine/modeltest/suite.go`.

New direct ScriptedStep tests use `Entered`/`Release` barriers, never a short
timing inference: they observe Entered, assert the buffered result is still
absent, then release or cancel. The runtime malformed-payload table now covers
the full Text/Code presence matrix for every event type and verifies one
subsequent valid event remains ordinal 1.

Mutation RED 1 command:

```sh
GOCACHE=/private/tmp/open-code-harness-gocache go test ./internal/harness/testkit -run TestScriptedModelStepSignalsBeforeReleaseAndCancellation -count=1
```

Relevant output after temporarily skipping `Entered`:

```text
Next() did not signal Entered
FAIL github.com/SongYii/open-code-harness/internal/harness/testkit
```

Mutation RED 2 command:

```sh
GOCACHE=/private/tmp/open-code-harness-gocache go test ./internal/harness/testkit -run 'TestScriptedModel$' -count=1
```

Relevant output after temporarily accepting terminal Text:

```text
Emit(engine.RuntimePayload{Type:"model.stream.started", Text:"text", Code:""}) error = <nil>, want invalid_request
delivered = []engine.RuntimeEvent{...}, want none
FAIL github.com/SongYii/open-code-harness/internal/harness/testkit
```

Both mutations were restored with `apply_patch`; GREEN/final commands:

```sh
gofmt -w internal/harness/engine internal/harness/testkit
GOCACHE=/private/tmp/open-code-harness-gocache go test ./internal/harness/engine/... ./internal/harness/testkit -count=1
GOCACHE=/private/tmp/open-code-harness-gocache go test -race ./internal/harness/engine/... ./internal/harness/testkit -count=1
GOCACHE=/private/tmp/open-code-harness-gocache go test ./... -count=1
GOCACHE=/private/tmp/open-code-harness-gocache go vet ./...
git diff --check
```

Results: focused passed (engine 0.624s, testkit 1.200s), race passed (engine
1.804s, testkit 1.543s), full suite, vet, gofmt, and diff check passed. No
mutation remains. Self-review: Entered is ordered before Release waiting,
cancellation returns `context.Canceled` with exact counters, and every
forbidden field-presence shape is rejected before attempt/ordinal allocation.
