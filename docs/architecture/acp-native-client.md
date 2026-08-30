# ACP-Native Client — Implemented Contract

**Status:** Implemented; not GA

**Stability:** `experimental` until v1.0

**Maturity:** pre-v0; not a general availability release

**Authority:** [Minimal ACP-native client design](../superpowers/specs/2026-08-30-acp-native-client-design.md)

**Evidence:** [ACP-native client completion evidence](acp-native-client-evidence.md)

**Packages:** `internal/client/acp` (wire transport, session lifecycle, trajectory reducer, permission-prompt logic); `cmd/acp-client` (the binary: flags, subprocess spawn, real stdio, main loop)

This document records behavior enforced by the current code and tests. It
is an internal Go contract, not a stable public protocol.

## Scope

This is the **client** role of ACP v1: the process that spawns an agent
over stdio, sends `session/prompt`, renders a live trajectory from
`session/update`, and answers `session/request_permission`. It is the
mirror of [ACP v1 adapter](acp-v1.md), which implements the **agent**
role; the two share no code and do not import each other in either
direction (`TestClientPackagesAreIsolatedFromInternalHarness`,
`internal/harness/architecture`).

`internal/client/acp` is not a harness adapter: it implements no
`tools.*` port and sits entirely outside `internal/harness/`, the same
way `cmd/och` itself sits outside `internal/harness/` as a consumer of
the composition root rather than a part of it. It is not general-purpose
either: it is built and tested against this repository's own agent's
actual, observed behavior (the exact four `sessionUpdate` variants that
agent emits, the exact two-option permission shape it sends), not
re-derived from the ACP specification in the abstract, though nothing in
it is intentionally tied to this repository's own agent by construction —
the `-agent` flag names any ACP v1 agent binary.

## Wire transport

`internal/client/acp/wire.go` and `connection.go` implement NDJSON-framed
JSON-RPC 2.0: one JSON value per line, a bounded frame size
(`TestFrameWriterRejectsAnOversizedPayload`), and a `Connection` running
one background read-loop goroutine that dispatches inbound notifications
and the one inbound call this client answers
(`session/request_permission`) to a `Handler` interface
(`TestConnectionDeliversSessionUpdateToHandler`,
`TestConnectionAnswersRequestPermissionThroughHandler`). An unrecognized
inbound method gets an immediate method-not-found response rather than
leaving the agent waiting for one that never comes
(`TestConnectionAnswersAnUnknownInboundMethodWithMethodNotFound`).
`Connection.Close` closes the underlying reader to unblock a pending read
and fails every outstanding call with a named error
(`TestConnectionCloseUnblocksAPendingCall`); it is idempotent
(`TestConnectionCloseIsIdempotent`). A `Call`'s context cancellation
returns promptly without waiting on the agent
(`TestConnectionCallReturnsPromptlyOnContextCancellation`). This is a
second, independent implementation of the same wire shape
`internal/harness/adapters/acp/codec.go` implements for the agent side —
deliberately not shared, per the same "the framing contract is small
enough to own" reasoning the 2026-08-22 ACP v1 gate already applied to
that side.

## Session lifecycle client

`internal/client/acp/client.go`'s `Client` wraps a `Connection` and
exposes `Initialize`, `NewSession`, `LoadSession`, `Prompt`, and `Cancel`
— wire field names verified directly against
`internal/harness/adapters/acp/protocol.go` (`sessionId`, `cwd`,
`prompt: [{type,text}]`, `stopReason`). `NewClient` builds its own
`Connection` and rejects a nil `Handler` outright
(`TestNewClientRejectsANilHandler`), since a `session/load`'s replayed
`session/update` notifications can arrive before `session/load`'s own
response resolves and must never have nowhere to go
(`TestClientLoadSessionDeliversReplayedUpdatesBeforeReturning`).
`Initialize` sends an empty `clientCapabilities` object — no `fs` key, no
`terminal` key, present or absent-but-false — an explicit decline of
both, verified against this repository's own agent's `initializeParams`,
which parses no client capabilities today
(`TestClientInitializeDeclinesFsAndTerminalCapabilities`). `Prompt` waits
only on its own call's matching response
(`TestClientPromptReturnsTheStopReason`,
`TestClientPromptReturnsPromptlyOnContextCancellation`); `Cancel` is
fire-and-forget, matching the agent's own cancellation semantics (the
in-flight `Prompt` observes `cancelled` on its own pending response,
`TestClientCancelDuringAnInFlightPromptUnblocksItWithCancelled`).

## Trajectory reducer

`internal/client/acp/trajectory.go`'s `Trajectory.Apply` reduces one
`session/update` notification into exactly one `RenderEvent`, keyed by
`toolCallId` — pure, no I/O
(`TestTrajectoryAppliesAgentMessageChunk`,
`TestTrajectoryAppliesUserMessageChunk`, `TestTrajectoryAppliesToolCall`,
`TestTrajectoryToolCallThenTwoUpdatesReachesATerminalStatus`,
`TestTrajectoryInterleavedToolCallsDoNotCrossContaminate`). Scoped to
exactly the four `sessionUpdate` variants this repository's own agent
emits (verified directly against
`internal/harness/adapters/acp/project.go`):
`user_message_chunk`/`agent_message_chunk` (text), `tool_call` (creates),
`tool_call_update` (mutates). Anything else — an unrecognized variant, or
a `tool_call_update` naming a `toolCallId` never created — produces a
`RenderAnomaly` carrying the raw payload, never a panic, a silent drop,
or a phantom entry (`TestTrajectoryUnrecognizedVariantIsALabeledAnomalyNotAPanicOrDrop`,
`TestTrajectoryToolCallUpdateForAnUnknownIDIsALabeledAnomalyNotAPhantomEntry`,
`TestTrajectoryMalformedParamsIsALabeledAnomaly`).

## Permission-prompt handling

`internal/client/acp/permission.go`'s `PermissionPrompter` asks an
operator to answer `session/request_permission` over an injected
`io.Reader`/`io.Writer` pair, never `os.Stdin`/`os.Stdout` directly. This
repository's own agent's real, verified shape — exactly two options,
`allow-once`/`reject-once` — gets a plain y/n prompt
(`TestDecideRealTwoOptionShapeAnswersYes`,
`TestDecideRealTwoOptionShapeAnswersNo`,
`TestDecideReplaysThePromptOnAnUnrecognizedYesNoAnswer`); any other shape
falls through to a generic numbered-choice prompt
(`TestDecideGenericShapeAnswersByNumber`,
`TestDecideGenericShapeRePromptsOnOutOfRangeAndNonNumericInput`). An
exhausted input stream while an answer is pending resolves
deterministically to a reject-flavored option, or the last-listed one,
and never returns an error that would leave the ACP call unanswered
(`TestDecideEOFWhilePendingResolvesToTheRejectOptionForTheRealShape`,
`TestDecideEOFWhilePendingResolvesToTheRejectOptionForTheGenericShape`).
`HandleRequestPermission` wraps the chosen option into
`{"outcome":{"outcome":"selected","optionId":...}}`, this repository's
own agent's real result shape
(`TestHandleRequestPermissionWrapsTheChoiceInTheAgentsResultShape`).

## `cmd/acp-client`

Flags: `-agent <path>` (required), `-cwd <path>` (required), `-resume
<sessionId>` (optional; absent means `session/new`, present means
`session/load`). Everything after a literal `--` is the agent's own
argv, using `flag.FlagSet`'s own parsing-terminator convention rather
than a custom multi-value flag type
(`TestRunRequiresAgentFlag`, `TestRunRequiresCwdFlag`). The agent's
stderr passes through to this client's own stderr unchanged, matching
`cmd/och -acp`'s own stderr-for-diagnostics precedent.

One shared `*bufio.Reader` over the operator's stdin serves both
prompt-line reading and `PermissionPrompter`'s answer reading. Two
independent `bufio.Reader`s each wrapping the same stdin would each read
ahead into their own buffer and could steal bytes meant for the other; a
shared reader is safe because the two never run concurrently — a
permission request only arrives while a prompt is already in flight,
exactly when prompt-line reading is not itself blocked. `SIGINT`/`SIGTERM`
interception is scoped to only the duration of an in-flight prompt: the
first signal cancels it via `session/cancel`, a second arriving before
that cancellation settles exits immediately; an idle `Ctrl-C` between
prompts is not intercepted at all and gets Go's own default termination.

Rendering (`cmd/acp-client/render.go`) is plain and line-oriented: a
`RenderToolCallUpdate` reprints its status in place only on a real
terminal (one `golang.org/x/term.IsTerminal` check at startup); a
non-terminal (piped output, CI) always appends a plain new line, with no
terminal-control-sequence dependency at all. Text chunks stream as
appended output; a `RenderAnomaly` prints a labeled, raw fallback line.

## Real interoperability proof

`TestInteropRealAgentCompletesAnApprovedWriteFile`
(`cmd/acp-client/main_test.go`) builds this repository's own real `och`
binary from source, spawns it as a genuine OS subprocess, and drives it
end to end through this package's own real `run()` — the exact code
`main()` calls — against a local, scripted, keyless HTTP fixture standing
in for the model provider. Everything else is real: the agent subprocess,
the ACP wire handshake, `session/new`, live trajectory rendering, the
interactive permission answer, the `write_file` tool call actually
executing against a real workspace directory, and the turn reaching
`end_turn`. This is the first time anything has driven the ACP v1 adapter
with something other than this repository's own scripted test fixtures.

## Explicit exclusions

This implemented contract does not provide:

- milestone 7's fuller `TypeScript TUI client` (`docs/README.md`) — this
  is a smaller, Go-native artifact milestone 7 may treat as evidence of
  what a fuller client needs, or may ignore entirely, not that milestone
  itself;
- a general (agent-agnostic) ACP v1 client — built and tested against
  this repository's own agent's actual, observed behavior, not a
  from-the-spec-alone implementation claiming compatibility with every
  agent in the wild;
- `fs` or `terminal` client-capability implementation — declined
  entirely at `initialize`, since this repository's own agent already
  confines workspace filesystem access and `exec` as its own jailed tools
  and parses no client capabilities today;
- wire-level observability (a raw request/response/notification debug
  log, à la Zed's separate `acp_tools` view) — this repository's own
  transcript/audit surfaces already give an operator visibility this
  client's trajectory rendering does not need to duplicate;
- consuming `och export-session`'s JSONL output — that tool remains a
  separate, already-implemented offline/audit surface; this client
  renders only from live `session/update`, reusing `session/load` for
  resume;
- multi-session, multi-agent, or split-pane UI — one client process
  drives exactly one session against exactly one agent process for its
  whole lifetime;
- any new non-test module dependency beyond `golang.org/x/term`, used for
  exactly one TTY check.
