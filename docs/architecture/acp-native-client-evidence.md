# ACP-native client completion evidence

**Status:** Complete evidence ledger

**Date:** 2026-08-30

**Design:** [Minimal ACP-native client](../superpowers/specs/2026-08-30-acp-native-client-design.md)

**Plan:** [Minimal ACP-native client implementation plan](../superpowers/plans/2026-08-30-acp-native-client.md)

**Contracts:** [ACP-native client](acp-native-client.md) (new); [ACP v1 adapter](acp-v1.md) (unchanged — this slice adds the client role as a separate, non-importing package, per the design's own package-boundary decision)

This ledger records step 2 of the [client-surface-and-security
sequencing decision](../research/architecture-gates/2026-08-30-client-surface-and-security-sequencing.md):
a minimal, standalone ACP v1 client, closing the one seam the ACP v1
adapter had never had exercised by anything but this repository's own
scripted test fixtures. Completion is claimed from the evidence below,
not from checkbox state.

## Commits

| Item | Commit | Subject |
| --- | --- | --- |
| Gate | `953738b` | docs: acp-native client architecture gate |
| Gate addendum | `e608303` | docs: rule out DeepSeek Harness frontend extraction for the ACP client gate |
| Design (draft) | `f113940` | docs: minimal ACP-native client design (draft, pending review) |
| Design (accepted) | `1b62121` | docs: accept minimal ACP-native client design |
| Plan | `8782c6b` | docs: minimal ACP-native client implementation plan |
| Task 1 | `11f0578` | feat(client/acp): NDJSON JSON-RPC wire transport |
| Task 2 | `4785b65` | feat(client/acp): ACP session lifecycle client |
| Task 3 | `dc199a9` | feat(client/acp): trajectory reducer |
| Task 4 | `d105c1f` | feat(client/acp): interactive permission-request handling |
| Task 5 | `abd4b51` | feat(acp-client): standalone ACP client binary |
| Task 6 | this commit | docs: publish the ACP-native client contract and evidence |

`45e19a3` (`fix: harden session lifecycle contracts`), interleaved in this
range, is unrelated work from a different, concurrent session sharing
this repository's working directory at the time (ACP session
lifecycle/SQLite append-path hardening) — recorded here only so a reader
of `git log` does not mistake it for part of this plan.

Each of Tasks 1–5 shipped as its own reviewed, merged pull request (PRs
61–65), each with its own full verification suite run before merge, not
only the aggregate run below. The architecture gate shipped as PRs 56–57;
the design and plan as PRs 58–60.

## Mapping-table tests

| Surface | Tests |
| --- | --- |
| Wire transport (framing) | `TestFrameWriterRoundTripsOneMessagePerLine`, `TestFrameWriterRejectsAnInvalidRawMessagePayload`, `TestFrameWriterCompactsInsignificantWhitespaceInAValidPayload`, `TestFrameWriterRejectsAnOversizedPayload`, `TestDecodeFramesFailsHardOnAMalformedLine`, `TestDecodeFramesSkipsBlankLines`, `TestMessageClassification` |
| Wire transport (Connection) | `TestConnectionCallRoundTrip`, `TestConnectionNotifyDoesNotWaitForAResponse`, `TestConnectionDeliversSessionUpdateToHandler`, `TestConnectionAnswersRequestPermissionThroughHandler`, `TestConnectionAnswersAnUnknownInboundMethodWithMethodNotFound`, `TestConnectionCallReturnsPromptlyOnContextCancellation`, `TestConnectionCloseUnblocksAPendingCall`, `TestConnectionCloseIsIdempotent` |
| Session lifecycle client | `TestNewClientRejectsANilHandler`, `TestClientInitializeDeclinesFsAndTerminalCapabilities`, `TestClientLoadSessionDeliversReplayedUpdatesBeforeReturning`, `TestClientPromptReturnsTheStopReason`, `TestClientCancelDuringAnInFlightPromptUnblocksItWithCancelled`, `TestClientPromptReturnsPromptlyOnContextCancellation` |
| Trajectory reducer | `TestTrajectoryAppliesAgentMessageChunk`, `TestTrajectoryAppliesUserMessageChunk`, `TestTrajectoryAppliesToolCall`, `TestTrajectoryToolCallThenTwoUpdatesReachesATerminalStatus`, `TestTrajectoryInterleavedToolCallsDoNotCrossContaminate`, `TestTrajectoryUnrecognizedVariantIsALabeledAnomalyNotAPanicOrDrop`, `TestTrajectoryToolCallUpdateForAnUnknownIDIsALabeledAnomalyNotAPhantomEntry`, `TestTrajectoryMalformedParamsIsALabeledAnomaly` |
| Permission-prompt handling | `TestDecideRealTwoOptionShapeAnswersYes`, `TestDecideRealTwoOptionShapeAnswersNo`, `TestDecideReplaysThePromptOnAnUnrecognizedYesNoAnswer`, `TestDecideGenericShapeAnswersByNumber`, `TestDecideGenericShapeRePromptsOnOutOfRangeAndNonNumericInput`, `TestDecideEOFWhilePendingResolvesToTheRejectOptionForTheRealShape`, `TestDecideEOFWhilePendingResolvesToTheRejectOptionForTheGenericShape`, `TestDecideRejectsMalformedParams`, `TestDecideRejectsZeroOptions`, `TestHandleRequestPermissionWrapsTheChoiceInTheAgentsResultShape` |
| `cmd/acp-client` flags | `TestRunRequiresAgentFlag`, `TestRunRequiresCwdFlag` |
| Real interoperability proof | `TestInteropRealAgentCompletesAnApprovedWriteFile` |
| Package boundary | `TestClientPackagesAreIsolatedFromInternalHarness` (`internal/harness/architecture`) |

## Verification commands and output

All keyless and network-free (the one HTTP call the interoperability test
makes is to a local `httptest.Server`, not a live provider).

```text
$ test -z "$(gofmt -l .)"
(clean)

$ go vet ./...
(clean)

$ CGO_ENABLED=0 go build ./...
(clean)

$ go test -count=1 ./...
ok   github.com/SongYii/open-code-harness/cmd/acp-client
ok   github.com/SongYii/open-code-harness/cmd/och
ok   github.com/SongYii/open-code-harness/internal/client/acp
ok   github.com/SongYii/open-code-harness/internal/docsguard
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/acp
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/localexec
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/memory
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/openaicompat
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/system
ok   github.com/SongYii/open-code-harness/internal/harness/adapters/workspacefs
ok   github.com/SongYii/open-code-harness/internal/harness/application
ok   github.com/SongYii/open-code-harness/internal/harness/architecture
ok   github.com/SongYii/open-code-harness/internal/harness/composition
ok   github.com/SongYii/open-code-harness/internal/harness/domain
ok   github.com/SongYii/open-code-harness/internal/harness/engine
ok   github.com/SongYii/open-code-harness/internal/harness/policy
ok   github.com/SongYii/open-code-harness/internal/harness/runtime
ok   github.com/SongYii/open-code-harness/internal/harness/testkit
ok   github.com/SongYii/open-code-harness/internal/harness/tools
ok   github.com/SongYii/open-code-harness/internal/harness/transcript

$ go test -race -count=1 ./...
ok   (every package listed above)

$ go test -race -count=5 ./internal/client/acp/...
ok   github.com/SongYii/open-code-harness/internal/client/acp

$ GOOS=windows GOARCH=amd64 go build ./...
(clean)

$ GOOS=darwin GOARCH=arm64 go build ./...
(clean)

$ git diff --check
(clean)
```

The real interoperability test, run individually with verbose output to
confirm it is not silently skipped:

```text
$ go test ./cmd/acp-client/... -run TestInteropRealAgentCompletesAnApprovedWriteFile -v
=== RUN   TestInteropRealAgentCompletesAnApprovedWriteFile
--- PASS: TestInteropRealAgentCompletesAnApprovedWriteFile (1.26s)
```

Its actual rendered output, captured once for this ledger (not asserted
verbatim by the test itself, which checks for the load-bearing substrings
and the real written file — capturing it here is for a human reader):

```text
session session-e839fa261a825235b06c8b46d6a9d835 ready
>
[tool] write_file (edit): pending
[tool] write_file: in_progress
write_file (edit) — allow? [y/n] [tool] write_file: completed
Done
[end_turn]
>
```

## Mutation checks

Each was reverted immediately after confirming the RED result, before the
GREEN commit:

- **Task 1** (`frameWriter`'s originally-planned newline guard): removing
  it did not fail any test — because it was unreachable dead code.
  `encoding/json.Marshal` never produces a literal newline in output for
  this package's `message` struct: it fails outright on a `RawMessage`
  containing one inside a string (verified directly with a throwaway
  program), and compacts away insignificant whitespace in an otherwise-
  valid nested value. The guard was removed rather than kept as
  false-confidence dead code; the frame-size guard, mutated separately,
  is real and is what the surviving test protects.
- **Task 1** (`Connection.Close`'s reader-close call): removing it left
  `Close()` hanging forever, since nothing else unblocks the read loop's
  blocked read.
- **Task 1** (the unknown-inbound-method rejection): removing it made the
  test hang waiting on a channel nothing sends to, rather than failing
  fast with a visible assertion mismatch — still a correctly-caught RED,
  just a different failure shape than expected.
- **Task 2** (`NewClient`'s nil-handler guard): removing it compiled and
  ran fine right up until the test that specifically exercises a nil
  handler failed - confirming the guard, not incidental behavior, is what
  the test protects.
- **Task 2** (`clientCapabilities`'s emptiness): adding one field changed
  the wire bytes and the test caught it immediately, confirming the test
  checks actual wire content, not merely that the struct happens to be
  empty today.
- **Task 3** (the unrecognized-`sessionUpdate`-variant anomaly): the first
  mutation attempt (rerouting the unrecognized case through
  `applyToolCallUpdate`) still passed by coincidence, since the malformed
  shape it hit failed to parse into a tool call either and produced an
  anomaly through a different path. The mutation that actually caught the
  bug was making the default case fall through to a normal, non-anomaly
  `RenderEvent`, which the test rejected immediately - a reminder that a
  passing mutation test does not by itself prove the right code path was
  exercised.
- **Task 3** (the unknown-`toolCallId` phantom-entry guard): mutating it
  to silently create a phantom entry instead of returning an anomaly was
  caught immediately.
- **Task 4** (the EOF fail-closed default): flipping it to resolve to the
  *allow* option instead of *reject* — exactly the class of bug this
  design exists to prevent — was caught immediately.
- **Task 4** (the two-option-vs-generic routing): forcing every request
  through the generic numbered-choice path broke both real-shape y/n
  tests immediately, confirming the routing is load-bearing, not
  incidental.
- **Task 5** (the new architecture boundary test): adding a real import
  from `internal/client/acp` into `internal/harness/domain` was caught
  immediately, then reverted.

## Deviations from the plan's stated file map

- **Task 5**: added `-provider-allow-insecure-loopback` to `cmd/och/main.go`,
  not named in the plan's Task 5 file map. Without it there is no way to
  point the real `och` binary at a local, keyless HTTP fixture instead of
  a live provider, which the real interoperability test requires —
  mirroring how Task 5 of the earlier exec-sandboxing plan added
  `-allow-unsandboxed-exec` to the same file for the same class of
  reason: an escape hatch that exists in `Config` but is unreachable from
  the actual CLI is not done.
- **Task 5**: `cmd/acp-client` gained two extra small files
  (`handler.go`, `render.go`) beyond the plan's stated `main.go` — a
  natural split of "wire the Handler interface" and "render one
  RenderEvent" out of `main.go` rather than an architecture change.
- **Task 5**: the plan's own sketched signal-handling design (a
  background goroutine continuously scanning stdin for the next prompt
  line, watching for signals while idle) was replaced during
  implementation, before any test caught it, because reasoning through it
  surfaced a real concurrency bug: it would have required
  `PermissionPrompter` to read the operator's answer through its own
  separate `bufio.Reader` over the same stdin, and two independent
  `bufio.Reader`s wrapping one source can each read ahead and steal bytes
  meant for the other. The shipped design shares one `bufio.Reader`
  between prompt-line reading and permission-answer reading (safe because
  the two never run concurrently) and scopes signal interception to only
  the duration of an in-flight prompt, needing no code at all for the
  idle case.

## Exclusions

Recorded as out of this slice by design, not as deferred bugs inside it:

- milestone 7's fuller `TypeScript TUI client` — this slice delivers a
  smaller, Go-native artifact milestone 7 may treat as evidence, or may
  ignore entirely, not that milestone itself.
- A general (agent-agnostic) ACP v1 client — built and tested against
  this repository's own agent's actual, observed behavior, not a
  from-the-specification-alone implementation.
- `fs`/`terminal` client-capability implementation — declined entirely at
  `initialize`.
- Wire-level observability (a raw request/response/notification debug
  log).
- Consuming `och export-session`'s JSONL output as an alternate history
  source.
- Multi-session, multi-agent, or split-pane UI.
- Any new non-test module dependency beyond `golang.org/x/term`.

## Remaining

- No macOS or Windows host has run any part of this slice for real; this
  entire plan was implemented and verified from one Linux x86_64 host.
  Nothing in this slice is platform-specific by construction (unlike the
  exec-sandboxing slice), but it has not been observed running anywhere
  else.
- The interoperability test exercises exactly one scripted turn (one
  `write_file` tool call, one approval, one final message). It does not
  exercise `session/load`/resume, `session/cancel`, or a rejected
  permission answer against the real agent — those paths are covered by
  Tasks 1-4's fake-agent unit tests, not by a second real-subprocess test,
  a deliberate scope cut for this slice's single stated acceptance
  criterion.
- Surfaces remain `experimental`; not GA.
