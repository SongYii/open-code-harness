# Web Trajectory UI and Browser Transport Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a browser-reachable trajectory viewer for ACP v1 sessions:
a new `cmd/acp-web-bridge` binary that spawns an ACP v1 agent and relays
its wire bytes, unparsed, to exactly one browser WebSocket connection at a
time; a genuinely independent TypeScript ACP v1 client running in that
browser, rendering a turn-grouped ledger with a per-record inspector and
composer-position permission approval, exactly as the accepted design
specifies.

**Architecture:** The bridge (`internal/client/acpweb` + `cmd/acp-web-bridge`)
never parses JSON-RPC: it pumps `subprocess stdout line ↔ WebSocket text
frame` in both directions, guarded by an Origin allowlist and a
per-invocation random token on every WebSocket upgrade. The browser
implements ACP v1 itself — `initialize`, `session/new`/`load`,
`session/prompt`, `session/request_permission`, `session/cancel` — and
derives turn boundaries and a local, disclosed-as-approximate timing view
entirely client-side, since `toolCallId` already encodes `turnID` and ACP's
"Never projected on ACP" boundary is accepted as-is (design §1, §5).
Frontend assets are embedded into the bridge binary at build time so the
shipped artifact stays one self-contained binary.

**Tech Stack:** Go 1.26, `github.com/coder/websocket` (new direct
dependency: pure Go, no cgo, context-first API, actively maintained —
chosen over `gorilla/websocket` for its smaller surface and this
project's existing familiarity with the `coder` org's Go tooling via
`.reference/acp-go-sdk`). Frontend: TypeScript, bundled with Vite, no UI
framework (vanilla DOM APIs) — matching this project's own minimalism
discipline (no plugin kernel, no unnecessary dependency) for what is a
single-session, single-viewer page, not a general application shell.
`chromedp` (pure-Go headless-Chrome control) for the required real
end-to-end frontend interoperability proof, so that test stays a normal
`go test` like every other real-interop test in this repository, rather
than introducing a Node-based test runner into CI.

**Spec:** `docs/superpowers/specs/2026-08-31-web-trajectory-ui-design.md`
(English normative, Accepted); Chinese summary at
`docs/superpowers/specs/2026-08-31-web-trajectory-ui-design.zh-CN.md`.
Research: `docs/research/architecture-gates/2026-08-31-web-trajectory-ui.md`.
No Chinese reading copy for this plan, matching recent plans' precedent.

## Global Constraints

- **The bridge never parses JSON-RPC.** No task may add method-name
  inspection, field extraction, or any ACP-semantic logic to
  `internal/client/acpweb` or `cmd/acp-web-bridge`. If a task's tests seem
  to need the bridge to understand a message, the test is testing the
  wrong layer — ACP semantics belong exclusively to the frontend (Tasks
  4–6).
- **Every WebSocket upgrade passes both the Origin allowlist and the
  token check, independently.** Neither task may special-case the other
  away; a test host or CI environment must set up a real matching pair,
  never bypass one check to make the other's test pass.
- **Loopback-only, hardcoded.** No task adds a `-listen` host component
  or any flag that could bind beyond `127.0.0.1`. `-listen`, if present at
  all, only ever selects the port.
- **No sleep-based assertions for timing-sensitive tests.** Where a real
  timed check is unavoidable (the reconnection test's subprocess-liveness
  assertion, the chromedp end-to-end run), bound it by an event or a
  generous timeout with a stated reason, matching this project's existing
  precedent (`TestCgroupMemoryQuotaKillsAMemoryGrowingCommand`'s own
  capability-gated style) rather than an exact duration.
- **Every environment-dependent test skips cleanly with a stated reason**
  when its dependency is missing (no headless Chrome for chromedp, no
  network loopback binding permission in a locked-down sandbox) — never
  asserts behavior no test actually observed.
- **Every task follows red-green-refactor and adds a mutation check**
  where the task introduces new security- or correctness-load-bearing
  logic (Origin/token checks, the relay's line/frame boundary handling,
  the frontend's own wire-contract conformance): disable the new check or
  logic, confirm the task's own new test fails for the right reason, then
  restore.
- `gofmt`, `go vet ./...`, and `CGO_ENABLED=0 go build ./...` stay clean
  after every Go task. The frontend's own type-checker and its test
  runner stay clean after every frontend task.

## File Map

| Path | Responsibility |
| --- | --- |
| `internal/client/acpweb/relay.go` | Subprocess spawn/lifecycle, bidirectional line↔frame pump, reconnection rewiring |
| `internal/client/acpweb/security.go` | Origin allowlist and per-invocation token generation/validation |
| `internal/client/acpweb/server.go` | HTTP server: WebSocket upgrade wiring, static asset serving from an injected `fs.FS` |
| `internal/client/acpweb/*_test.go` | New tests per task |
| `cmd/acp-web-bridge/main.go` | Flags (`-agent`, `-cwd`, `-resume`, `-listen`), token generation, ready-URL printing, signal handling, `//go:embed web/dist` |
| `cmd/acp-web-bridge/web/` | Frontend source (TypeScript, Vite config, `package.json`) and its build output (`web/dist/`, gitignored, built before `go build`) |
| `cmd/acp-web-bridge/web/src/acp-client.ts` | Independent TypeScript ACP v1 client: wire framing, JSON-RPC dispatch, session/protocol calls |
| `cmd/acp-web-bridge/web/src/ledger.ts` | Turn-grouped ledger data model, reduced from raw `session/update` notifications |
| `cmd/acp-web-bridge/web/src/ui.ts` | DOM rendering: ledger, per-record inspector, composer-position permission approval |
| `cmd/acp-web-bridge/web/tests/` | Frontend unit tests (Vitest, against a mock WebSocket) |
| `cmd/acp-web-bridge/interop_test.go` | Real end-to-end proof: real `och` + real bridge + chromedp-driven real browser |
| `go.mod`, `go.sum` | New direct dependency: `github.com/coder/websocket` |
| `docs/architecture/web-trajectory-ui.md`, `.zh-CN.md` (new) | Implemented contract for this new subsystem |
| `docs/architecture/web-trajectory-ui-evidence.md` (new) | Evidence ledger |
| `SECURITY.md` | New bullet: the bridge's network-reachable surface, Origin/token defenses, loopback-only scope |
| `docs/README.md` | New authority-table rows; milestone 7 entry updated to record implementation |

---

### Task 1: `internal/client/acpweb` relay core

**Files:**

- Create: `internal/client/acpweb/relay.go`, `internal/client/acpweb/relay_test.go`
- Modify: `go.mod`, `go.sum` (add `github.com/coder/websocket`)

- [ ] Define `Relay` (or similarly named type) wrapping one spawned
  subprocess's `stdin`/`stdout` pipes, mirroring `cmd/acp-client/main.go`'s
  own `exec.Command` + `StdoutPipe`/`StdinPipe` + fixed cleanup order
  (close subprocess stdin, close the active connection, wait for exit) —
  reuse that shape directly rather than re-deriving it.
- [ ] Implement the bidirectional pump against an abstract
  `io.Reader`/`io.Writer` pair representing "the current browser
  connection" (not a concrete `*websocket.Conn` yet — Task 3 wires the
  real one): one subprocess stdout line (trailing `\n` stripped) becomes
  one write to that pair; one read from that pair becomes one subprocess
  stdin line (`\n` appended). No JSON parsing anywhere in this path.
- [ ] Implement reconnection rewiring: a method that swaps the active
  connection pair without touching the subprocess. The previous pair's
  pump goroutines exit cleanly (no panic, no double-close) when swapped
  out mid-flight.
- [ ] Add `go get github.com/coder/websocket` and run `go mod tidy`.
- [ ] Unit tests: a fake subprocess (a tiny helper script or an
  in-process `io.Pipe`-backed stand-in) proves multi-line bursts translate
  correctly in both directions, including a line at exactly the
  configured max-message-size boundary; a mid-stream reconnect (swap the
  active pair) does not drop or duplicate bytes already in flight to the
  old pair and does not restart or signal the subprocess.
- [ ] Mutation check: break the trailing-newline handling in one
  direction, confirm the multi-line test fails for the right reason
  (concatenated or malformed lines), restore.
- [ ] Run:

```bash
go test ./internal/client/acpweb/... -run Relay -count=1 -v
go test -race ./internal/client/acpweb/... -count=1
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(acpweb): subprocess-to-WebSocket relay core`.

### Task 2: Origin allowlist and per-invocation token

**Files:**

- Create: `internal/client/acpweb/security.go`, `internal/client/acpweb/security_test.go`

- [ ] `GenerateToken() string`: 32 bytes from `crypto/rand`, hex-encoded.
  Never derived from a predictable seed (time, PID).
- [ ] `ValidateToken(want, got string) bool`: `crypto/subtle.ConstantTimeCompare`,
  never a `==` string comparison.
- [ ] `CheckOrigin(selfOrigin, requestOrigin string) bool`: exact match
  only; an empty `requestOrigin` (header absent) returns `true` from this
  function specifically — the token check is what still gates a
  no-Origin request, per design §4, so this function's contract is "does
  a *present* Origin match," not "is this request safe."
- [ ] Wire both checks into one upgrade-gate function taking the incoming
  request's `Origin` header and token query parameter, returning a single
  allow/deny with no distinguishing detail exposed on a deny (matching
  the design's stated no-leak requirement).
- [ ] Unit tests: matching origin + matching token → allow; mismatched
  origin (any non-empty, non-matching value) → deny regardless of token;
  absent origin + matching token → allow; absent origin + wrong/missing
  token → deny; matching origin + wrong/missing token → deny.
- [ ] Mutation check: change `ValidateToken` to use `==` instead of
  `ConstantTimeCompare`, confirm this does not itself break any
  allow/deny test outcome (expected — correctness is unaffected by timing
  safety) but note in the commit that this specific mutation cannot be
  caught by a functional test, only by the code-review requirement that
  `crypto/subtle` is actually used — record this explicitly rather than
  claiming a mutation test covers it when it cannot.
- [ ] Run:

```bash
go test ./internal/client/acpweb/... -run 'Token|Origin' -count=1 -v
go test -race ./internal/client/acpweb/... -count=1
```

- [ ] Commit: `feat(acpweb): Origin allowlist and per-invocation token checks`.

### Task 3: `cmd/acp-web-bridge` binary and HTTP/WebSocket server

**Files:**

- Create: `internal/client/acpweb/server.go`, `internal/client/acpweb/server_test.go`
- Create: `cmd/acp-web-bridge/main.go`, `cmd/acp-web-bridge/main_test.go`
- Create: `cmd/acp-web-bridge/web/dist/.gitkeep` (placeholder until Task 7)

- [ ] `server.go`: an `http.Server` serving static assets from an injected
  `fs.FS` at `/`, and upgrading to WebSocket at `/ws` using
  `github.com/coder/websocket`, applying Task 2's upgrade-gate function
  before `Accept`, and setting the WebSocket connection's max message
  size comfortably above ACP's own 1 MiB outgoing-frame bound (design §3)
  — a documented constant, not a magic number. On a successful upgrade,
  wires the connection into Task 1's `Relay` as the new active pair.
- [ ] `main.go`: flags `-agent` (required), `-cwd` (required), `-resume`
  (optional), `-listen` (default `127.0.0.1:0`, port-only as the Global
  Constraints require). Spawns the agent via Task 1's `Relay`. Generates
  a token (Task 2), computes `selfOrigin` from the listener's actual
  bound address (`net.Listener.Addr()`, since the default port is
  OS-assigned), and prints the ready URL,
  `http://127.0.0.1:<port>/?token=<token>`, to stderr — the same
  logged-escape-hatch precedent this project already uses. `-resume`
  behavior mirrors `cmd/acp-client`'s own: the browser's own `initialize`/
  `session/load` calls (Task 4) still decide new-vs-resume, so this flag
  only needs to reach the frontend, e.g. embedded into the served index
  page or exposed via a small same-origin `/config` endpoint the frontend
  reads once at startup — pick whichever keeps `main.go` and `server.go`
  simplest; either satisfies the design.
- [ ] Signal handling: `signal.NotifyContext` (`os.Interrupt`,
  `syscall.SIGTERM`), same fixed cleanup order as `cmd/acp-client`
  (subprocess stdin closed, then waited on) on shutdown.
- [ ] The placeholder `web/dist/.gitkeep` plus a minimal
  `//go:embed web/dist` in `main.go` proves the embed mechanism and the
  binary builds end-to-end before the real frontend exists (Task 7
  replaces the placeholder's contents, not this wiring).
- [ ] Tests: an httptest-based (or real, ephemeral-port) server accepts
  an upgrade with a valid Origin+token pair and relays a scripted
  subprocess's output to a real WebSocket test client end to end; a
  second connection attempt while the first is still open successfully
  takes over (first connection's read loop exits cleanly, no panic); an
  upgrade attempt with a bad token or mismatched Origin is rejected with
  `403` and the connection is never wired into the `Relay`.
- [ ] Mutation check: skip the upgrade-gate call entirely, confirm the
  bad-token/mismatched-Origin rejection test fails (upgrade succeeds when
  it must not), restore.
- [ ] Run:

```bash
go test ./internal/client/acpweb/... ./cmd/acp-web-bridge/... -count=1 -v
go test -race ./internal/client/acpweb/... ./cmd/acp-web-bridge/... -count=1
CGO_ENABLED=0 go build ./...
```

- [ ] Commit: `feat(acp-web-bridge): HTTP/WebSocket server and CLI binary`.

### Task 4: Frontend ACP v1 client (protocol layer)

**Files:**

- Create: `cmd/acp-web-bridge/web/package.json`, `tsconfig.json`, `vite.config.ts`
- Create: `cmd/acp-web-bridge/web/src/acp-client.ts`
- Create: `cmd/acp-web-bridge/web/tests/acp-client.test.ts`

- [ ] Scaffold the frontend project (Vite + TypeScript, no framework,
  Vitest for unit tests) under `cmd/acp-web-bridge/web/`, `.gitignore`ing
  `node_modules/` and `dist/` (Task 7 covers the actual build/embed
  pipeline; this task's tests run via the frontend's own test runner, not
  `go build`).
- [ ] `acp-client.ts`: a genuinely independent JSON-RPC 2.0 client over a
  `WebSocket`, implementing exactly the wire shape `acp-v1.md` and
  `internal/client/acp` already satisfy: request/response correlation by
  id, notification dispatch, `initialize`, `session/new`, `session/load`,
  `session/prompt`, `session/cancel` calls, and inbound
  `session/request_permission` handling via a caller-supplied callback
  (mirroring `internal/client/acp`'s own `Handler` interface split, so
  Task 6 can plug in the real UI without touching this file).
- [ ] Reads the token from the page's own URL and appends it to the
  WebSocket URL's query string, per design §4 — this file does not
  attempt to set custom headers (browsers cannot).
- [ ] Unit tests (Vitest, against a mock/fake `WebSocket`): request/response
  correlation with out-of-order responses; a notification dispatched
  without a pending request; a rejected/errored response surfaces as a
  rejected promise with the JSON-RPC error's message; a
  `session/request_permission` inbound request invokes the supplied
  callback and sends its return value back as the RPC result.
- [ ] Mutation check: break id correlation (always match the first
  pending request regardless of id), confirm the out-of-order-response
  test fails for the right reason, restore.
- [ ] Run whatever the scaffolded `package.json` defines for typecheck
  and test (document the exact commands here once chosen, e.g. `npm run
  typecheck && npm test`).
- [ ] Commit: `feat(acp-web-bridge/web): independent TypeScript ACP v1 client`.

### Task 5: Turn-grouped ledger and per-record inspector

**Files:**

- Create: `cmd/acp-web-bridge/web/src/ledger.ts`, `cmd/acp-web-bridge/web/src/ui.ts`
- Create: `cmd/acp-web-bridge/web/tests/ledger.test.ts`
- Modify: `cmd/acp-web-bridge/web/index.html` (created if not already scaffolded in Task 4)

- [ ] `ledger.ts`: pure reduction functions from raw `session/update`
  notifications (as delivered by Task 4's client) into a turn-grouped
  view model. A turn boundary opens when the frontend itself calls
  `session/prompt` (live) and closes when that call's response arrives;
  on a future `session/load`-driven replay, `user_message_chunk` opens a
  turn instead (design §1.5) — implement both boundary sources behind one
  shared reducer so live and replay produce the same shape, even though
  this slice's UI only drives the live path (§2's single-session scope).
  Tool records within a turn are keyed by `toolCallId`
  (`turnID + "/" + callID`), never re-derived by guessing.
- [ ] Local timing: stamp each notification/response at receipt with
  `performance.now()` (or `Date.now()`, whichever the chosen approach
  uses consistently) and compute a per-turn/per-tool-call wall-clock span
  from those stamps alone — explicitly labeled in the resulting view
  model as a local approximation (a boolean or a documented field name
  making this unambiguous to `ui.ts`), never conflated with a
  provider-reported value ACP does not send (design §5).
- [ ] `ui.ts`: renders the ledger (turn separators, tool/assistant/user
  records) and a per-record inspector opened on selection, showing
  `rawInput`, content, status, and the local timing approximation from
  above with its approximate-timing label visibly rendered, not hidden in
  a tooltip only. Virtualized/windowed rendering for very long sessions
  is out of scope for this task and this plan (a plan-level narrowing of
  the design's own open scope, stated here rather than left implicit):
  render all loaded records directly; a future slice may add
  virtualization if a real session length makes it necessary.
- [ ] Unit tests (`ledger.test.ts`): a scripted sequence of raw
  `session/update` notifications (covering tool_call, tool_call_update
  completed/failed, and agent_message_chunk) reduces to the expected
  turn-grouped shape; two turns in sequence do not bleed records into
  each other; a tool call whose `toolCallId` cannot be parsed is placed
  in an "unassigned" bucket rather than crashing the reducer or silently
  dropping it.
- [ ] Mutation check: break turn-boundary detection (never close a turn),
  confirm the two-turn test fails for the right reason (records bleed
  together), restore.
- [ ] Run the frontend's typecheck and test commands.
- [ ] Commit: `feat(acp-web-bridge/web): turn-grouped ledger and record inspector`.

### Task 6: Composer-position permission approval

**Files:**

- Modify: `cmd/acp-web-bridge/web/src/ui.ts`
- Create: `cmd/acp-web-bridge/web/tests/permission-ui.test.ts`

- [ ] Wire Task 4's `session/request_permission` callback to a UI state
  that replaces the composer (the text-input area an operator would
  otherwise type a prompt into) with the pending request: the correlated
  tool call's title/kind/`rawInput` shown inline, and allow-once/reject
  controls, per design §6. The composer returns to its normal state
  immediately after the decision is sent.
- [ ] While a permission request is pending, prompt submission is
  disabled (mirroring `cmd/acp-client`'s own "concurrent prompts on one
  session" rule being enforced server-side, but giving the operator an
  immediate, local reason rather than waiting for a `-32600` round trip).
- [ ] Unit tests: a `session/request_permission` callback invocation
  transitions the UI state as expected; choosing allow-once or reject
  resolves the callback with the corresponding outcome and restores the
  composer; a second permission request arriving while the first is still
  pending (should not happen per ACP's own one-prompt-in-flight rule, but
  defend the UI layer anyway) does not corrupt the pending state — assert
  a specific, deliberate behavior (e.g., the second is queued or logged
  as unexpected) rather than leaving it undefined.
- [ ] Mutation check: skip restoring the composer after a decision,
  confirm the "returns to normal state" test fails for the right reason,
  restore.
- [ ] Run the frontend's typecheck and test commands.
- [ ] Commit: `feat(acp-web-bridge/web): composer-position permission approval`.

### Task 7: Build pipeline and binary embedding

**Files:**

- Modify: `cmd/acp-web-bridge/main.go` (embed directive, if the path changes)
- Modify: `cmd/acp-web-bridge/web/package.json` (build script)
- Create/modify: root or `cmd/acp-web-bridge/`-scoped build documentation (a `Makefile` target or a documented two-step build command — whichever this project's existing `README.md` "Development" section style favors)

- [ ] Wire Vite's production build (`vite build`) to emit into
  `cmd/acp-web-bridge/web/dist/`, replacing Task 3's placeholder content.
- [ ] Confirm `//go:embed web/dist` in `main.go` serves the real built
  `index.html`/JS/CSS correctly (a Go test starting the server and
  fetching `/` over real HTTP, asserting the built entry point is
  returned, not the Task 3 placeholder).
- [ ] Document the two-step build (`cd cmd/acp-web-bridge/web && npm ci &&
  npm run build`, then `go build ./cmd/acp-web-bridge`) in this
  project's own `README.md` "Development" section, alongside the existing
  `gofmt`/`go vet`/`go test`/`go run` block — state plainly that
  `go build ./...` alone does not produce a working `acp-web-bridge`
  binary with real assets unless the frontend was already built at least
  once (a real, disclosed limitation of embedding a separately-built
  frontend, not glossed over).
- [ ] Run:

```bash
cd cmd/acp-web-bridge/web && npm ci && npm run build && cd -
go build ./cmd/acp-web-bridge
go test ./cmd/acp-web-bridge/... -count=1
```

- [ ] Commit: `build(acp-web-bridge): wire Vite build output into the embedded binary`.

### Task 8: Real end-to-end interoperability proof, contract, and evidence

**Files:**

- Create: `cmd/acp-web-bridge/interop_test.go`
- Create: `docs/architecture/web-trajectory-ui.md`, `.zh-CN.md`
- Create: `docs/architecture/web-trajectory-ui-evidence.md`
- Modify: `SECURITY.md`, `docs/README.md`, `README.md`

- [ ] `interop_test.go`, matching `TestInteropRealAgentCompletesAnApprovedWriteFile`'s
  own standard: build the real `och` binary and the real
  `cmd/acp-web-bridge` binary from source, start the bridge against a
  local scripted HTTP fixture standing in for the model provider (same
  fixture-server precedent the ACP-native client's own interop test
  uses), drive a real headless Chrome instance via `chromedp` to the
  printed ready URL (including its token), submit one prompt that
  triggers a `write_file` tool call, approve the resulting permission
  request through the real rendered UI (Task 6), and assert the turn
  reaches `end_turn` and the file actually exists in the workspace
  afterward. Gate this test on `chromedp` being able to find a Chrome/
  Chromium binary on the host (skip cleanly with a stated reason if not,
  per the Global Constraints — do not fail CI hosts that lack a browser).
- [ ] Write `docs/architecture/web-trajectory-ui.md` (full English
  normative implemented contract; this is a new subsystem, not an
  extension of an existing contract, unlike the exec CPU quota slice) and
  its Chinese reading copy: scope, the relay's exact framing rule
  (line↔frame, newline handling), the Origin/token upgrade gate, the
  frontend's wire-contract conformance and its independence from
  `internal/client/acp`, the local-timing-approximation disclosure, and
  every non-goal from the design (§2) restated as a stated exclusion, not
  silently absent.
- [ ] Write `docs/architecture/web-trajectory-ui-evidence.md`: a commit
  table for this plan's gate/design/plan/Tasks 1–8, the mapping table
  (mechanism → test → mutation result) for the relay, the security
  checks, and the frontend reducer/UI, real verification command output
  for both the Go and frontend test suites, and the `interop_test.go`
  run's own output (or a clear note if it was skipped on this evidence-
  gathering host for lacking a browser, with instructions for reproducing
  it on a host that has one).
- [ ] Update `SECURITY.md` with a new bullet describing this surface:
  loopback-only by default, Origin-allowlist and per-invocation-token
  defenses, and the honest scope limit that this is a **local development
  tool**, not hardened for exposure beyond loopback (matching this
  project's own established candor about disclosed limitations elsewhere
  in the same file).
- [ ] Update `docs/README.md`'s authority table (new "Implemented
  contract" and "Evidence" rows) and milestone 7's entry to state this
  slice is implemented and verified, not merely designed.
- [ ] Update the root `README.md`'s "Current status" bullets, matching
  the style of the exec CPU quota and secret redaction entries there.
- [ ] Run:

```bash
go test ./... -count=1
go test -race ./... -count=1
cd cmd/acp-web-bridge/web && npm run typecheck && npm test && cd -
go test ./cmd/acp-web-bridge/... -run Interop -count=1 -v
go test ./internal/docsguard/... -v
git diff --check
```

- [ ] Commit: `docs: publish the web trajectory UI contract and evidence`.

## Final Completion Gate

- [ ] Run `gofmt -w` on changed Go files and verify `gofmt -l` prints
  nothing for them.
- [ ] Run `go vet ./...`.
- [ ] Run `CGO_ENABLED=0 go build ./...` (after a frontend build, per
  Task 7's disclosed build-order dependency).
- [ ] Run `go test ./... -count=1` and `go test -race ./... -count=1`.
- [ ] Run the frontend's own typecheck and test commands clean.
- [ ] Confirm every task's mutation check was actually performed and
  recorded in the evidence ledger, not merely planned, including Task 2's
  explicitly-uncoverable-by-a-functional-test note about
  `crypto/subtle.ConstantTimeCompare`.
- [ ] Confirm `interop_test.go` actually ran (not skipped) on at least
  one host during this plan's execution, with its real output captured in
  the evidence ledger — a design that requires a real end-to-end proof is
  not satisfied by a test that only ever skips.
- [ ] Confirm no task added any ACP-semantic logic to
  `internal/client/acpweb` or `cmd/acp-web-bridge` (Global Constraints) —
  a final read of both packages' diffs against this plan, not just a
  test-passing check.
- [ ] Confirm `SECURITY.md`'s new bullet states no broader claim than
  this plan actually delivers — in particular, it must not imply any
  hardening for non-loopback exposure, which remains explicitly out of
  scope.
- [ ] Request code review, address findings with focused regression
  tests, then create a final implementation/evidence commit if review
  changes are needed.
