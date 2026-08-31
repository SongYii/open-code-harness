# Web trajectory UI completion evidence

Auditable results for the [Web Trajectory UI — Implemented
Contract](web-trajectory-ui.md), the [design](../superpowers/specs/2026-08-31-web-trajectory-ui-design.md)
it fixes, and the [eight-task implementation plan](../superpowers/plans/2026-08-31-web-trajectory-ui.md)
that delivered it.

## Commits

| PR | Commit | Summary |
| --- | --- | --- |
| #84 | `edb7325` | Research: web trajectory UI and browser transport architecture gate |
| #85 | `c32cefb` | Design: web trajectory UI and browser transport (Accepted) |
| #86 | `64b2531` | Plan: eight-task implementation sequence |
| #87 | `1afa66d` (`96a0fc8`) | Task 1: `internal/client/acpweb.Relay` — subprocess-to-WebSocket relay core |
| #88 | `fc6c3ee` (`907c596`) | Task 2: Origin allowlist and per-invocation token checks |
| #89 | `8a9df9c` (`0afc3a2`) | Task 3: HTTP/WebSocket server and `cmd/acp-web-bridge` CLI binary |
| #90 | `e182311` (`9fe188c`) | Task 4: independent TypeScript ACP v1 client |
| #91 | `766d2eb` (`ae5ed9e`) | Task 5: turn-grouped ledger and record inspector |
| #92 | `cfec2cf` (`b61bfa8`) | Task 6: composer-position permission approval |
| #93 | `c3cf8b7` (`49aa175`) | Task 7: Vite build pipeline and binary embedding, incl. `main.ts`/`index.html` app wiring |
| #94 | (this PR) | Task 8: real chromedp interoperability proof, this document, `SECURITY.md`, `docs/README.md`, root `README.md` |

## Mapping table

| Mechanism | Test | Mutation result |
| --- | --- | --- |
| `Relay` stdout → active `Conn`, newline stripped | `TestRelayPumpsSubprocessStdoutToActiveConn` | Removing the trailing-newline append on the reverse (conn→stdin) path hung the newline-specific test on a blocking read until Go's own test timeout — a genuine failure for the right reason, manifesting as a timeout rather than a fast mismatch |
| `Relay` drops stdout with no active `Conn` | `TestRelayDropsStdoutWhenNoConnActive` | — (behavioral test, not separately mutated; covered by the reconnect mutation below) |
| `Relay.SetConn` rewires without restarting the subprocess | `TestRelayReconnectRewiresActiveConnWithoutTouchingSubprocess` | — |
| Large line near `MaxRelayFrameBytes` round-trips intact | `TestRelayHandlesLargeLineNearMaxFrameBytes` | — |
| `CheckOrigin` exact-match | `TestCheckOrigin` | — |
| `ValidateToken` constant-time compare | `TestValidateTokenExactMatchOnly` | Swapping `ConstantTimeCompare` for `==` cannot be caught by any functional test (correctness is unaffected by timing safety) — documented as an explicitly-uncoverable-by-test mutation, not silently claimed covered |
| `UpgradeAllowed` requires both Origin and token | `TestUpgradeAllowedRequiresBothChecksIndependently` | Removing the `CheckOrigin` call made the mismatched-origin subtest fail for the exact right reason (`UpgradeAllowed` returned `true` when it must return `false`) |
| `Server.handleWebSocket` gates before `websocket.Accept` | `TestServerRejectsUpgradeWithBadTokenOrMismatchedOrigin` | Skipping the upgrade-gate call made all three subtests (wrong token, missing token, mismatched origin) fail for the exact right reason |
| `-listen` host must be `127.0.0.1` | `TestRunRejectsNonLoopbackListenHost` | Removing the check made `run()` proceed to actually bind the given host and block in the server loop — a genuine failure surfacing as a hang/timeout, same class as the Task 1 mutation above |
| `AcpClient` id-based request/response correlation | `TestAcpClient...resolves the matching pending request even when responses arrive out of order` | Replacing id-keyed lookup with "first pending in the map" made the out-of-order test fail for the exact right reason (wrong session id resolved) |
| `WebSocketTransport` queues `send()` until the socket opens | `WebSocketTransport > queues send() calls made before the socket opens...` | Removing the queue (send unconditionally) made the test fail for the exact right reason — this is the same defect the real interop proof (below) found independently against a real browser `WebSocket`, before this dedicated unit test existed |
| `Ledger` attributes tool records by parsing `toolCallId`, not the currently-open turn | `does not bleed records from one turn into the next` | Making `turnFor` always return the first turn ever created made the two-turn test fail for the exact right reason (turn 2's tool record appeared inside turn 1) |
| `Ledger` places a malformed `toolCallId` in `unassigned` | `places a tool call whose toolCallId has no separator in the unassigned bucket instead of crashing` | — |
| `TrajectoryView.select` opens the inspector | `opens the inspector with the record's fields when a row is clicked` | Removing the `renderInspector` call made the test fail for the exact right reason (inspector stayed hidden) |
| `TrajectoryView.resolvePermission` restores the composer | `choosing allow-once resolves with allow-once and restores the composer` | Removing the `restoreComposer()` call on the no-more-queued path made the test fail for the exact right reason (the input element never reappeared) |
| `go:embed web/dist` serves the real build, not the Task 3 placeholder | `TestRunServesTheRealBuiltFrontendNotThePlaceholder` | Temporarily replacing the real build output with the Task 3 placeholder content made the test fail for the exact right reason |
| End-to-end: real `och`, real bridge, real browser, real permission click, real file write | `TestInteropRealBrowserCompletesAnApprovedWriteFile` | Not mutated (an acceptance proof, not a unit test); its first run failed for a real reason — see below |

## Verification commands and output

Go side, run from the repository root after `main` included through PR
#93:

```
$ gofmt -l .   # (excluding cmd/acp-web-bridge/web/node_modules)
$ go vet ./...
$ CGO_ENABLED=0 go build ./...
$ go test ./... -count=1
ok  	github.com/SongYii/open-code-harness/... (all packages)
$ go test -race ./... -count=1
ok  	github.com/SongYii/open-code-harness/... (all packages)
```

All four produced no output beyond the expected `[no test files]` lines
for packages that own none — no failures, no `gofmt` diffs, no `vet`
findings.

Frontend side, run from `cmd/acp-web-bridge/web`:

```
$ npm run typecheck
> tsc --noEmit
(no output; exit 0)

$ npm test
 Test Files  4 passed (4)
      Tests  18 passed (18)

$ npm run build
dist/index.html                 0.32 kB │ gzip: 0.23 kB
dist/assets/index-DzmvavwK.js  10.82 kB │ gzip: 3.59 kB
✓ built in 48ms
```

Real end-to-end interoperability proof, run with a Playwright-downloaded
Chromium (`npx playwright install chromium`, since no system Chrome/
Chromium was preinstalled on this evidence-gathering host) pointed at via
`CHROME_EXECUTABLE`:

```
$ export CHROME_EXECUTABLE=$(...)/chromium-1234/chrome-linux64/chrome
$ go test ./cmd/acp-web-bridge/... -run TestInteropRealBrowserCompletesAnApprovedWriteFile -count=1 -v
=== RUN   TestInteropRealBrowserCompletesAnApprovedWriteFile
2026/08/31 composition: AllowUnsandboxedExec is true - proceeding without OS-level exec confinement: bwrap probe failed: exit status 1
och: acp v1 on stdin/stdout; workspace=/tmp/TestInteropRealBrowserCompletesAnApprovedWriteFile.../002
--- PASS: TestInteropRealBrowserCompletesAnApprovedWriteFile (2.71s)
PASS
```

This test **did run for real** on this evidence-gathering host, not
merely skip — reproducible on any host with `CHROME_EXECUTABLE` set or a
`google-chrome`/`chromium`/`chromium-browser` binary on `PATH`; it skips
cleanly with a stated reason otherwise.

**Its first run failed**, before the `WebSocketTransport` fix documented
in the implemented contract: `chromedp.Run: context deadline exceeded`.
Debugging with a throwaway diagnostic test (console-log/exception
capture, not kept) showed the page's own error: `Failed to execute
'send' on 'WebSocket': Still in CONNECTING state.` — `AcpClient`'s
constructor calls `initialize()` before the browser's WebSocket
handshake completes. Fixed by queuing `send()` calls until the socket's
`open` event fires (`acp-client.ts`), covered by a new dedicated unit
test, and reverified end to end after the fix (output above).

## Mutation checks

Every task performed at least one real mutation check (break the new
logic, confirm the right test fails, restore) per the plan's Global
Constraints; each is recorded in its own task's commit message, cross-
referenced in the mapping table above. Two are worth calling out
specifically:

- **Task 1 and Task 3 both produced a hang/timeout rather than a fast
  assertion failure** when their respective guards were removed
  (newline handling; `-listen` host validation). Both were confirmed as
  genuine failures for the right reason before being killed and
  reverted — a slow failure is still a failure, and this is disclosed
  rather than treated as an inconclusive mutation check.
- **Task 2's `ConstantTimeCompare` swap is explicitly uncoverable by any
  functional test.** Recorded as such rather than silently assumed
  covered by the surrounding `UpgradeAllowed` mutation check, which
  covers a different line.

## Deviations from the plan

- **The plan's Task 6/Task 7 boundary did not name an explicit task for
  writing `index.html`/`main.ts`** (the actual application wiring of
  `AcpClient`, `Ledger`, and `TrajectoryView`). This was a real gap in
  the plan itself, not a silent scope expansion: it was closed in Task 7
  (PR #93), disclosed in that task's own commit message, since Task 7 is
  where a real Vite build first needed a real entry point to build
  against.
- **`web/dist/`'s tracked placeholder used a real `index.html` file, not
  the plan's originally suggested `.gitkeep`**, because `go:embed`
  refuses to embed a directory containing no embeddable files (a
  dotfile-only directory does not count) — discovered directly while
  implementing Task 3, disclosed in that task's commit message, and
  superseded by the real, gitignored build output once Task 7 landed.
- **Task 3 also fixed a pre-existing, unrelated `docsguard` failure**
  found while running the full suite before that task's own commit: the
  web trajectory UI gate's Chinese file (from PR #84) cited its
  normative source in prose rather than by the literal filename
  `TestReadingCopiesNameTheirNormativeSource` requires. Corrected to
  match every other gate's citation format.
- **The `WebSocketTransport` connection-race fix** (above) was not
  anticipated by the design or plan; it is exactly the kind of defect
  the plan's own required real end-to-end proof exists to catch, and it
  did.

## Exclusions

Every exclusion in the [implemented contract](web-trajectory-ui.md)'s own
"Explicit exclusions" section is a plan-level or design-level non-goal
with its own stated reason, not an oversight discovered after the fact.

## Remaining

- **Live token usage, latency, `finishReason`, and a timing overview**
  remain unavailable in the live browser view, per ACP v1's own "Never
  projected on ACP" boundary. A later slice could layer the session-
  transcript export's richer data onto a *finished* session's view; this
  slice does not attempt it.
- **Multi-viewer fan-out, in-browser session list/resume/delete, and
  non-loopback exposure** are all named, reasoned non-goals for a later
  slice if one is ever needed, not gaps discovered late.
- **No system Chrome/Chromium was available on this evidence-gathering
  host**; the real interoperability proof ran against a Playwright-
  downloaded Chromium instead, discovered and installed specifically to
  satisfy this plan's own requirement that the proof actually run rather
  than merely skip. A future host with a system browser installed needs
  no such workaround — the test's own discovery (`CHROME_EXECUTABLE`,
  then `PATH` lookup) covers both.
- **Virtualized ledger rendering** for very long sessions is not
  implemented; every loaded record is mounted directly, an explicit
  plan-level narrowing of the design's own open scope.
