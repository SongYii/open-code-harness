# Minimal ACP-Native Client Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a small, standalone ACP v1 client that spawns an agent
subprocess over stdio, sends `session/prompt`, renders a live trajectory
from `session/update`, answers `session/request_permission`
interactively, and — as its acceptance proof — actually interoperates
with this repository's own `och -acp` agent, closing the one seam the ACP
v1 adapter has never had exercised by anything but its own scripted test
fixtures.

**Architecture:** A new top-level `internal/client/acp` package (wire
transport, session lifecycle, trajectory reducer, permission-prompt
logic — all parameterized over `io.Reader`/`io.Writer` pairs, none of it
assuming a real terminal or a real subprocess, so it is unit-testable
without either) and a new `cmd/acp-client` binary (flag parsing, real
subprocess spawn, real stdio wiring, the main loop). Neither package sits
under `internal/harness/`, imports anything from it, or is imported by
it — this client is a consumer of the ACP wire protocol, not a harness
adapter, exactly as `docs/superpowers/specs/2026-08-30-acp-native-client-design.md`
§3 requires.

**Tech Stack:** Go 1.26, standard library only for the wire/session/reducer
package (`encoding/json`, `bufio`, `os/exec`, `context`, `sync`); `cmd/acp-client`
additionally uses `golang.org/x/term` (already permitted by this
project's existing `go.mod` policy of narrowly-scoped `golang.org/x/*`
packages, same tier as `golang.org/x/sys`) for the one TTY check §5 of
the design allows, with a no-dependency plain-append fallback if that
check is ever judged not worth even that. No new non-test *module*
dependency beyond that one package, and none at all in
`internal/client/acp` itself.

**Spec:** `docs/superpowers/specs/2026-08-30-acp-native-client-design.md`
(English normative, Accepted); synchronized Chinese summary at
`docs/superpowers/specs/2026-08-30-acp-native-client-design.zh-CN.md`.
Research: `docs/research/architecture-gates/2026-08-30-acp-native-client.md`.

## Global Constraints

- `internal/client/acp` never imports anything under `internal/harness/`,
  and nothing under `internal/harness/` may import `internal/client/acp`
  or `cmd/acp-client`. `internal/harness/architecture`'s existing
  dependency-boundary tests must report zero new imports crossing that
  line after every task — this is checked explicitly at the end of every
  task, not assumed.
- `internal/client/acp` takes every I/O dependency (the agent connection,
  the operator's prompt/permission input and output, the render sink) as
  an interface or an `io.Reader`/`io.Writer` parameter. No file in that
  package may reference `os.Stdin`, `os.Stdout`, or `os.Exec` directly —
  those are `cmd/acp-client`'s job. This is what makes every task below
  testable with an in-process fake instead of a real subprocess or a real
  terminal.
- Every task's reducer or session-state logic is scoped to exactly what
  this repository's own agent (`internal/harness/adapters/acp`) actually
  emits and expects, verified by reading that package's source directly
  — not re-derived from the ACP specification in the abstract — per the
  design's own §4-§6. An unrecognized shape from a future or different
  agent degrades to a labeled fallback; it never panics or is silently
  dropped.
- No sleep-based concurrency tests. Use channels and context deadlines;
  `t.Skip` only where a task's own acceptance criterion genuinely
  requires a real subprocess this environment might not have (there is
  exactly one such case, Task 5's real integration test using this
  repository's own `och` binary, which this repository can always build
  from source — so `t.Skip` there is not expected to fire in this
  repository's own CI, only in an unusual environment that cannot build
  Go at all).
- Every task follows red-green-refactor: write the focused test, observe
  it fail for the right reason, then implement, then run the focused
  package tests green before moving on.
- `CGO_ENABLED=0 go build ./...` stays clean after every task (this
  project's standing constraint; nothing in this plan has a reason to
  need CGO, but the check is explicit per task regardless).

## File Map

| Path | Responsibility |
| --- | --- |
| `internal/client/acp/wire.go` (+`_test.go`) | JSON-RPC 2.0 envelope types, NDJSON line framing (encode/decode), frame-size and newline-safety guards |
| `internal/client/acp/connection.go` (+`_test.go`) | `Connection`: outbound call/notify over a `Handler`-dispatching read loop, response-waiter keyed by request id |
| `internal/client/acp/client.go` (+`_test.go`) | ACP-specific session lifecycle: `Initialize`, `NewSession`, `LoadSession`, `Prompt`, `Cancel` |
| `internal/client/acp/trajectory.go` (+`_test.go`) | The `toolCallId`-keyed reducer; the four known `sessionUpdate` variants plus a labeled fallback |
| `internal/client/acp/permission.go` (+`_test.go`) | Permission-prompt logic parameterized over `io.Reader`/`io.Writer`; the real two-option shape, a generic N-option fallback, EOF-while-pending fail-closed default |
| `cmd/acp-client/main.go` (+`_test.go`) | Flags, subprocess spawn, real stdio wiring, signal handling, the prompt-read-eval-render main loop |
| `internal/harness/architecture/dependencies_test.go` | Confirm no import crosses the `internal/client/acp` / `internal/harness/` boundary in either direction |
| `docs/architecture/acp-native-client.md`, `.zh-CN.md` | New implemented-contract doc for this client |
| `docs/architecture/acp-native-client-evidence.md` | New evidence ledger |
| `docs/README.md`, `README.md` | Authority-table rows, milestone prose |

---

### Task 1: NDJSON JSON-RPC wire transport

**Files:**

- Add: `internal/client/acp/wire.go`
- Add: `internal/client/acp/wire_test.go`
- Add: `internal/client/acp/connection.go`
- Add: `internal/client/acp/connection_test.go`

- [ ] Define the JSON-RPC 2.0 envelope (a single struct with optional
  `id`/`method`/`params`/`result`/`error` fields covering request,
  response, and notification, matching the shape and quality bar of this
  repository's own `internal/harness/adapters/acp/codec.go` — read
  directly for reference, not imported or copied verbatim since this
  package owns its own copy per the design's §1.1 decision).
- [ ] NDJSON framing: one JSON value per line, a `bufio.Scanner` with a
  bounded max line size (mirror the existing 1 MiB frame cap), a writer
  that rejects an encoded payload containing a literal newline or
  exceeding the cap rather than silently truncating or corrupting the
  stream.
- [ ] Define `type Handler interface { HandleSessionUpdate(params
  json.RawMessage); HandleRequestPermission(ctx context.Context, params
  json.RawMessage) (result json.RawMessage, err error) }` — the seam
  later tasks implement; Task 1's own tests use a stub recording what it
  received.
- [ ] `Connection` wraps an `io.Reader`/`io.Writer` pair (not necessarily
  a subprocess — a plain `io.Pipe` pair is enough for every test in this
  package), runs one background read-loop goroutine dispatching inbound
  notifications and the one inbound call to a `Handler`, and exposes
  `Call(ctx, method, params) (json.RawMessage, error)` and
  `Notify(method, params) error` for outbound traffic, matching an
  outbound-response-waiter-keyed-by-id pattern (`acp-go-sdk`'s own
  Connection, read directly at the architecture gate, keeps concurrent
  inbound requests separate from a strictly ordered inbound-notification
  stream for exactly this reason — mirror that separation, do not import
  the package).
- [ ] `Connection.Close` stops the read loop and fails every outstanding
  waiter with a named "connection closed" error rather than leaking a
  blocked goroutine.
- [ ] Add unit tests: a well-formed request/response/notification round
  trip over an `io.Pipe`; a line exceeding the frame cap is rejected, not
  truncated; a call whose context is cancelled before a response arrives
  returns promptly with the context's error, not a hang; `Close` unblocks
  every pending `Call`.
- [ ] Run:

```bash
go test ./internal/client/acp/... -run 'Wire|Connection|Frame' -count=1 -v
go test -race ./internal/client/acp/... -count=1
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(client/acp): NDJSON JSON-RPC wire transport`.

### Task 2: ACP session client

**Files:**

- Add: `internal/client/acp/client.go`
- Add: `internal/client/acp/client_test.go`

- [ ] `Client` wraps a `*Connection` and exposes the ACP method surface
  this slice needs: `Initialize(ctx) (AgentInfo, Capabilities, error)`,
  `NewSession(ctx, cwd) (sessionID string, error)`,
  `LoadSession(ctx, sessionID, cwd) error`, `Prompt(ctx, sessionID, text)
  (stopReason string, error)`, `Cancel(ctx, sessionID) error`.
- [ ] `Initialize` sends `protocolVersion: 1` and an explicit, empty
  `clientCapabilities` object — no `fs`, no `terminal` key present at
  all, not present-but-false — per the design's §1.3 decision, verified
  against this repository's own agent by reading
  `internal/harness/adapters/acp/protocol.go`'s `initializeParams` (which
  parses no client capabilities today, confirmed at design time; this
  task does not re-verify that fact, only relies on it as recorded).
- [ ] `LoadSession` and `NewSession` both attach a `Handler` (Task 1's
  interface) before sending their request, since a `session/load`'s
  replayed `session/update` notifications can arrive before its own
  response per the design's §4 sequence diagram — a `Client` constructed
  without a handler wired first must not be able to call either method
  (return an error, not silently drop replayed updates).
- [ ] `Prompt` sends `session/prompt` and blocks on the connection's
  response for that call specifically; it does not itself inspect
  `session/update` traffic (that is the `Handler`'s job, wired by
  whatever owns the `Client`) — `Prompt`'s only contract is "block until
  this specific prompt's terminal response or `ctx` is done, and return
  the stop reason".
- [ ] `Cancel` sends `session/cancel` as a notification (matching
  `acp-v1.md`'s existing agent-side cancellation semantics: fire-and-
  forget, the in-flight `Prompt` call observes the resulting `cancelled`
  stop reason on its own pending response, not a separate signal).
- [ ] Add unit tests against an in-process fake agent (a second
  `Connection` over an `io.Pipe`, driven by the test — not a real
  subprocess, that is Task 5's job): `Initialize` sends capabilities
  exactly as specified above; `LoadSession` delivers replayed
  `session/update`s to the handler before returning; `Prompt` blocks
  until the matching response arrives and returns its stop reason;
  `Cancel` during an in-flight `Prompt` unblocks it with `cancelled`;
  `Prompt`'s `ctx` cancellation returns promptly without waiting for the
  agent.
- [ ] Run:

```bash
go test ./internal/client/acp/... -run 'Client|Initialize|Session|Prompt|Cancel' -count=1 -v
go test -race ./internal/client/acp/... -count=1
```

- [ ] Commit: `feat(client/acp): ACP session lifecycle client`.

### Task 3: Trajectory reducer

**Files:**

- Add: `internal/client/acp/trajectory.go`
- Add: `internal/client/acp/trajectory_test.go`

- [ ] A `Trajectory` type holding tool-call state keyed by `toolCallId`
  plus an ordered index for rendering, and an `Apply(update
  json.RawMessage) []RenderEvent` method — pure, no I/O, no dependency on
  `Connection` or `Client` — matching the reducer pattern all three gate
  sources converged on independently (design §5).
- [ ] Handle exactly the four `sessionUpdate` values this repository's
  own agent emits, verified by reading
  `internal/harness/adapters/acp/project.go` directly for this task (not
  assumed from Task 2's earlier reading, since that file may have moved):
  `user_message_chunk`, `agent_message_chunk` (both streamed as text),
  `tool_call` (creates an entry), `tool_call_update` (mutates an existing
  entry's status/content, matched by `toolCallId`).
- [ ] Any other `sessionUpdate` value produces exactly one `RenderEvent`
  carrying its raw `sessionUpdate` string and raw JSON, labeled
  distinctly from the four known kinds, so a caller can render it as
  "unrecognized: ..." rather than treat it as one of the four — the
  forward-compatibility behavior the design's §5 and §8 risk table both
  require, tested explicitly (a made-up fifth variant name must not
  panic, must not be silently dropped, and must be distinguishable in
  the returned `RenderEvent` from a known kind).
- [ ] A `tool_call_update` naming a `toolCallId` `Apply` has not seen
  before is treated the same as the unrecognized-variant case (a labeled
  anomaly, not a panic or a silently-created phantom entry), since this
  is a real possible agent bug this client must survive, not assume away.
- [ ] Add unit tests: each of the four variants applied in isolation;
  a `tool_call` followed by two `tool_call_update`s reaching a terminal
  status; interleaved chunks from two different tool calls do not cross-
  contaminate each other's state; the two anomaly cases above.
- [ ] Run:

```bash
go test ./internal/client/acp/... -run 'Trajectory|Reducer|Apply' -count=1 -v
```

- [ ] Commit: `feat(client/acp): trajectory reducer`.

### Task 4: Permission-prompt logic

**Files:**

- Add: `internal/client/acp/permission.go`
- Add: `internal/client/acp/permission_test.go`

- [ ] A `PermissionPrompter` type constructed with an `io.Reader` (for
  the operator's answer) and an `io.Writer` (for the prompt text) —
  never `os.Stdin`/`os.Stdout` directly, per the Global Constraints — with
  one method, `Decide(ctx, params json.RawMessage) (optionID string, err
  error)`, matching `Handler.HandleRequestPermission`'s shape from
  Task 1 (a thin adapter in `client.go` or here converts between the two
  once both exist; whichever file it lands in, it must not duplicate the
  option-rendering logic).
- [ ] The real, verified shape (read directly from
  `internal/harness/adapters/acp/{protocol,server}.go`, per the design's
  §6): a `toolCall` with title/kind/status and an `options` array. When
  `options` has exactly the two entries this repository's own agent
  always sends (`optionId` `"allow-once"` / `"reject-once"`), print the
  tool call's title and kind, then a `y`/`n` prompt.
- [ ] For any other `options` shape (a different count, different
  `optionId`s, or an agent this client was not specifically tuned
  against), print each option with a number and its `name`, and read a
  numeric choice — the generic fallback the design's §6 requires so this
  client does not hardcode the two-option case as the only shape it can
  answer.
- [ ] An `io.EOF` (or any read error) while an answer is pending resolves
  to whichever offered option's `kind` is `reject_once`/`reject` if one
  exists, else the last-listed option, and never returns an error that
  would leave the underlying ACP call unanswered — a fail-closed default
  per the design's §6, not a hang.
- [ ] Add unit tests: the real two-option shape answered `y` and `n`; a
  three-option generic case answered by number, including an
  out-of-range or non-numeric input being re-prompted rather than
  crashing or defaulting silently; the EOF-while-pending case resolves to
  a reject option deterministically.
- [ ] Run:

```bash
go test ./internal/client/acp/... -run 'Permission|Decide' -count=1 -v
```

- [ ] Commit: `feat(client/acp): interactive permission-request handling`.

### Task 5: `cmd/acp-client` binary and real interoperability proof

**Files:**

- Add: `cmd/acp-client/main.go`
- Add: `cmd/acp-client/main_test.go`
- Modify: `internal/harness/architecture/dependencies_test.go` (add the
  new boundary case from the Global Constraints)

- [ ] Flags: `-agent <path>` and `-agent-args <string...>` (the
  subprocess to spawn and its argv — this client never hardcodes `och
  -acp`, per the design's §2 "not a general ACP client by data coupling,
  but not agent-specific by construction" distinction), `-cwd <path>`
  (required, the session workspace), `-resume <sessionId>` (optional;
  absent means `session/new`, present means `session/load`).
  `-agent`/`-agent-args` split mirrors how `cmd/och` itself already
  parses flags with `flag.NewFlagSet`, for consistency of style within
  this repository even though this is a separate binary.
- [ ] Spawn the agent via `exec.Command`, wire its `Stdin`/`Stdout` to a
  `Connection` (Task 1) over the subprocess's pipes, pass its `Stderr`
  through to the client's own `os.Stderr` unchanged (matching `cmd/och
  -acp`'s own stderr-for-diagnostics precedent).
- [ ] Wire a `Trajectory` (Task 3) and a `PermissionPrompter` (Task 4,
  constructed over real `os.Stdin`/`os.Stdout`) as the `Handler` (Task 1)
  the `Client` (Task 2) dispatches to; render each `RenderEvent` to
  `os.Stdout` — a TTY check (`golang.org/x/term.IsTerminal` on
  `os.Stdout`'s fd, checked once at startup) selects incremental in-place
  status-line reprinting for `tool_call_update`s versus a plain appended
  line for a non-TTY (piped output, CI) per the design's §5 and §8 risk
  table; text chunks always stream as plain appended output regardless.
- [ ] Main loop: `Initialize`, then `NewSession`/`LoadSession` per the
  `-resume` flag, then read one line at a time from `os.Stdin` as the
  next prompt, calling `Client.Prompt` and blocking (rendering as
  notifications and permission requests arrive) until it returns, then
  read the next line; `SIGINT` during an in-flight `Prompt` calls
  `Client.Cancel`; a second `SIGINT` with nothing in flight exits without
  sending `session/cancel` first, closing the subprocess's stdin before
  exiting.
- [ ] Add the `internal/harness/architecture` boundary case: neither
  `internal/client/acp` nor `cmd/acp-client` is importable from anything
  under `internal/harness/`, and neither imports anything under
  `internal/harness/` itself.
- [ ] Add one real, gated integration test in `cmd/acp-client/main_test.go`:
  build (or reuse an already-built) `och` binary from this repository,
  spawn it as `och -acp` with a real temporary workspace and database,
  drive one full `session/new` → prompt (containing a request this
  project's default policy mode routes through `write_file`, requiring
  approval) → answer the permission request `allow-once` → observe the
  turn reach a terminal stop reason. This is the acceptance proof for
  this whole plan's stated primary goal (design §1's "closing the last
  unverified seam"), not merely another unit test — name it accordingly
  (for example `TestInteropRealAgentCompletesAnApprovedWriteFile`) so its
  role is unambiguous in a test list.
- [ ] Run:

```bash
go build ./cmd/acp-client/...
go test ./cmd/acp-client/... -count=1 -v
go test -race ./internal/client/acp/... ./cmd/acp-client/... -count=1
go test ./internal/harness/architecture/... -count=1 -v
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(acp-client): standalone ACP client binary`.

### Task 6: Publish the implemented-contract documentation and evidence

**Files:**

- Add: `docs/architecture/acp-native-client.md`, `.zh-CN.md`
- Add: `docs/architecture/acp-native-client-evidence.md`
- Modify: `docs/README.md`, `README.md`

- [ ] Write `docs/architecture/acp-native-client.md` following this
  project's established implemented-contract format (status/stability/
  maturity header, scope, delivered capability, package boundaries,
  exclusions) — the client-side mirror of `docs/architecture/acp-v1.md`,
  cross-linking it. Cite the exact test names Tasks 1–5 added, matching
  this repository's own documentation style.
- [ ] Add the synchronized Chinese reading copy.
- [ ] Add `docs/architecture/acp-native-client-evidence.md`: a commit
  table for Tasks 1–6, a mapping-table of tests per surface (wire
  transport, session lifecycle, reducer, permission handling, the real
  interoperability test), the actual verification command output, a
  "Deviations from this plan's file map" section if any arose during
  implementation (matching the exec-sandboxing evidence ledger's own
  precedent of disclosing such things rather than silently absorbing
  them), and a "Remaining" section naming milestone 7's fuller TUI,
  wire-level debug logging, and general (agent-agnostic) compatibility
  as excluded by design, not deferred without a stated reason.
- [ ] Add authority-table rows to `docs/README.md` for the new
  implemented contract, its reading copy, and the evidence ledger.
  Update the design and plan rows' status if not already `Accepted` /
  `Implemented plan`. Update `README.md`'s summary to mention the client
  now exists, matching how the ACP v1 adapter's own summary line was
  added when that slice shipped.
- [ ] Run:

```bash
go test ./internal/docsguard/... -v
git diff --check
```

- [ ] Commit: `docs: publish the ACP-native client contract and evidence`.

## Final Completion Gate

- [ ] Run `gofmt -w` on changed Go files and verify `gofmt -l` prints
  nothing for them.
- [ ] Run `go vet ./...`.
- [ ] Run `CGO_ENABLED=0 go build ./...`.
- [ ] Run `go test ./... -count=1`.
- [ ] Run `go test -race ./... -count=1`.
- [ ] Run `go test -race -count=5 ./internal/client/acp/...` to exercise
  the connection read-loop and reducer paths repeatedly.
- [ ] Confirm `internal/harness/architecture` reports zero imports
  crossing the `internal/client/acp` / `internal/harness/` boundary in
  either direction (Global Constraints).
- [ ] Confirm `TestInteropRealAgentCompletesAnApprovedWriteFile` (Task 5)
  actually ran and passed in this run, not merely compiled — this is the
  one test in this plan whose entire purpose is proving real
  interoperability, and a green suite that skipped it silently would
  misrepresent this plan's central claim.
- [ ] Request code review, address findings with focused regression
  tests, then create a final implementation/evidence commit if review
  changes are needed.
