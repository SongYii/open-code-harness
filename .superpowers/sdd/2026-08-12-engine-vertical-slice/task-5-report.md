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
