# MCP process ownership evidence (2026-09-15)

- Status: Implemented and verified in the working tree; HTTP2 blocker resolved on 2026-09-16
- Plan: [internal execution slice](../superpowers/plans/2026-09-15-mcp-process-ownership.md)
- Contract: [MCP client](mcp-client.md), [中文合同](mcp-client.zh-CN.md)
- Provider baseline committed locally as `b46b48e`; this slice is not committed

The failure records below describe the 2026-09-15 checkpoint and are preserved.
The 2026-09-16 [Provider repair and regression results](provider-http2-shutdown-evidence.md)
resolve that blocker: full-repository race and final affected-package regression
passed, including the MCP slice. This is working-tree evidence, not a merge.

## Implemented boundary

Composition supplies localexec.StdioProcess to MCP's internal Start/Read/Write/
Close port. No exec.Cmd, PID, OS signal, startup bracket or quota operation
crosses it. SDK IOTransport owns protocol framing/handshake; localexec owns
the sole Wait, pipes, shared confinement and process-resource cleanup.

Close sends EOF, escalates through bounded group TERM/KILL, checks Wait and
absence of leader AND group, releases pipes/temp/quota, and caches its result.
MCP preserves cleanup uncertainty on failed startup/handshake and SDK-initiated
closure. Host drain/deadline/fencing/lease policy is unchanged. Non-POSIX
construction rejects before spawning instead of using a parent-only fallback.

One-shot commands and workspace files retain their existing ports. No public
SDK, remote backend, registry, durable schema, OTel change or paid call.

## Verification

```sh
go test -race ./internal/harness/adapters/localexec ./internal/harness/adapters/mcp -count=1
go test -race ./internal/harness/composition ./internal/harness/adapters/localexec -run 'TestAssemblyMCPCleanupUncertaintyRetainsLease|TestStdioProcessCachesCleanupFailure' -count=3
```

Both passed: first command 16.897s / 10.242s; second 4.514s / 1.319s.
The final localexec/MCP suites also passed with `-count=3` (49.866s / 28.296s).
An additional independent live-leader proof test passed three race repetitions
(1.031s) after that run; no production code changed after the full regression.
Coverage includes real handshake/discovery/calls, startup-context separation,
failed construction, whole-group cleanup, stubborn leader, unread output,
quota/temp cleanup, concurrent Close and preservation of cleanup failure.
The second command uses real SDK byte-channel sessions and actual SQLite:
failed/timed-out MCP cleanup retains the lease, rejects a successor with
ErrLeaseHeld, and late cleanup cannot erase the original failure. The channel
is a contract fixture, not remote execution or external adoption evidence.

`go test -race ./internal/harness/composition -run 'MCP|Managed|Lifecycle|Fenc|Lease' -count=3`
also passed (48.716s), covering MCP integration and selected existing lifecycle/
fencing/lease regressions separately from the unresolved Provider HTTP2 test.

The first subprocess fixture attempt failed because Go test flags lacked a
`--` separator; that fixture was corrected. The old MCP process-ladder tests
were replaced by execution-owner tests. A missing child fails the tree test,
not skips it. Only that fixture disables sandbox wrapping to avoid bwrap's
PID namespace/die-with-parent masking missing group signals; other process/MCP
and existing confinement tests use normal backend selection.

Five isolated Go source overlays under `/tmp/och-mcp-ownership.KyM8s6` tested
negative controls without modifying repository production files:

| Mutation | Rejection |
| --- | --- |
| Skip quota registration | `TestStdioProcessEOFReapsAndReleasesOnce`: quota enrollment was skipped |
| Discard startup cleanup error | `TestConnectRetainsCleanupFailureOnStartupOrHandshakeError/start`: cleanup uncertainty was lost |
| Ignore Wait completion | `TestStdioReapProofRequiresWaitAndLeader`: missing Wait reported reaped |
| Omit leader liveness check when its group is missing | `TestStdioReapProofRejectsMissingGroupWithLiveLeader`: missing group hid a live leader |
| Replace group TERM/KILL with liveness probes | `TestStdioProcessClosesWholeTree`: process teardown could not be proven |

All failed at assertions, not compilation. Failure-only tree cleanup runs
AFTER assertions, terminates the child and lets its parent reap it.

`go vet` for localexec, MCP and Composition passed. Architecture/document
guards passed after classifying the existing build-tagged fixture executable
as test support: only its exact directory is excluded; neighboring testdata
remains inspected and production imports of the fixture are forbidden. MCP's
former os/exec exception is gone; syscall/x/sys dependencies are forbidden too.

Windows/amd64 and Darwin/arm64 package builds passed. These are compile checks,
not runtime certification. Linux evidence does not certify macOS RLIMIT or
Windows supervision. Process-group proof is not containment of descendants
that deliberately escape the group; confinement remains the sandbox's job.

Final architecture/document guards passed again (0.845s / 0.288s). One sandbox
attempt could not write Go's build cache; rerunning with local test permissions
passed. Formatting and diff whitespace checks passed.

## Full regression: failures retained, not waived

`go test -race ./... -count=1` FAILED. All packages except architecture and
Composition passed, including eval (338.155s), localexec (17.452s), MCP
(13.031s), runtime and existing persistence/security suites.

1. The stricter architecture scan initially treated the independent fixture
   executable as production. Classification was corrected as described above;
   the production restriction remains in force.
2. The Provider isolation gate timed out at `provider_isolation_test.go:192`,
   waiting for the H2 socket to close after Model.Close in
   `chat/http2/cancel=true`. Twenty focused race repetitions reproduced FOUR
   failures (60.700s total), so this is not dismissed as incidental load.

The Provider test constructs adapters directly, without exercising this slice's
MCP code. No Provider code or assertion was changed here. Source inspection
identifies a relevant race window: Chat Close cancels before body Close;
Go HTTP2 body Close may return on cancellation before internal stream cleanup,
while CloseIdleConnections skips connections with streams still present.
This is not proof every cancellation failure has one cause, but the earlier
passing sample no longer establishes reliable immediate H2 pool cleanup.

A separate bounded Provider fix and regression rerun are needed before claiming
the working tree is fully verified or ready to merge. Do not hide this with
sleeps, retries or a weakened socket-close assertion.
