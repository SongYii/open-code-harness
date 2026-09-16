# Provider HTTP2 shutdown repair evidence (2026-09-16)

- Status: Repair verified in the working tree; not yet committed
- Plan: [HTTP2 shutdown repair](../superpowers/plans/2026-09-16-provider-http2-shutdown.md)
- Prior failure: [MCP ownership regression](mcp-process-ownership-evidence.md)

## Cause and repair

The previous direct Provider test reproduced four failures in twenty runs,
waiting for the private H2 socket to close after Model.Close. This path does
not execute MCP code. Local Go 1.26.6 source confirms the relevant ordering:
`http2transportResponseBody.Close` can return when request context is canceled,
before its stream's done channel closes; `http2ClientConn.closeIfIdle` skips
connections with streams or reservations. One idle sweep therefore cannot
establish private connection teardown after application-level stream drain.

Both adapters now attach `adapters/internal/httpresource` to an already-private
standard transport. The helper tracks actual dialed sockets. Model.Close still
runs AFTER application drain, but now rejects new dials, cancels pending dials,
closes owned connections independently of H2 idle bookkeeping and waits for
late dial results to be cleaned up. Failures are cached, including late socket
cleanup errors. Composition's existing leaf timeout bounds uncooperative custom
dials/closes and abandons the lease rather than claiming successful teardown.

Source transports are never modified: ownership is installed only on their
clones. Nonstandard RoundTrippers remain borrowed. Standard TCP connections
remove their records when closed. Custom TLS dialers keep their concrete
`*tls.Conn` visible to net/http (otherwise H2 negotiation breaks); the owner
retains the underlying socket, not the TLS object. Runtime cleanup can reclaim
discarded TLS records, but explicit Close synchronously releases owned sockets
whether or not GC runs. There is no background idle-sweep retry or production
sleep. Request bodies, event bytes, SDK surfaces and OTel are unchanged.

The helper is a stdlib-only resource leaf, not a new Provider adapter or public
extension. Architecture gates allow only the two Provider adapters to import
it; it cannot import Engine or other harness layers, or execute commands.

## Verification observed so far

- The unchanged complete isolation/borrowed-H2-pool matrix passed twenty race
  repetitions (4.057s).
- The exact previously failing `chat/http2/cancel=true` case passed one hundred
  race repetitions (4.824s). This was before the final pending-dial accounting
  tightening; subsequent regression covers that final change.
- Deterministic ownership and custom TLS cases passed three repetitions
  (helper 1.055s, Composition 3.251s); pending-dial cancellation/accounting was
  then added and its focused rerun passed (1.228s / 2.594s).
- Final helper tests passed ten repetitions (1.569s), including cached late
  cleanup failure. `go vet` for both adapters, helper and Composition passed.

Real TLS tests cover both adapters and both DialTLSContext/legacy DialTLS,
require actual HTTP2, and prove the source client's warmed pool remains reusable
after its cloned model closes. Unit checks cover sockets not in an idle pool,
late dials, forgotten ordinary closed sockets, concurrent/idempotent Close,
cached failure and borrowed RoundTrippers. A GC test checks discarded TLS
objects are not retained; it does not stand in for explicit shutdown evidence.

## Negative controls on the final repair

Source overlays under `/tmp/och-http2-close.Or5RAN` leave repository production
files intact. All four controls failed at the intended assertions:

| Omitted safeguard | Observed rejection |
| --- | --- |
| Explicit socket close, leaving only CloseIdleConnections | `TestCloseOwnsConnectionsNotYetIdle`: owned socket survived Close |
| Close the socket returned by a canceled late dial | `TestLateDialCannotRepopulateClosedOwner`: owned socket survived Close (both cleanup-result variants) |
| Propagate connection Close error | `TestCloseFailureIsCachedEvenAfterTransportClose`: cleanup failure was lost |
| Wait for pending dials | `TestLateDialCannotRepopulateClosedOwner`: owner returned before its pending dial |

The late-dial test's 20ms observation asserts that shutdown stays BLOCKED while
the fixture deliberately withholds a dial result. It is not a production delay
or a longer timeout added to the original H2 test. Ordinary socket cleanup,
late cleanup and error propagation have independent guards.

## Final regression results

`go test -race ./... -count=1` passed, including eval (380.521s), Composition
(219.805s), localexec, MCP, runtime, SQLite, both adapters, launcher and SDK.
During that run, pending-dial accounting and malformed custom-dial cleanup
were tightened. After the final production change, the complete affected
packages were rerun:

```sh
go test -race ./internal/harness/adapters/openaicompat ./internal/harness/adapters/anthropic ./internal/harness/adapters/internal/httpresource ./internal/harness/composition -count=1
```

Passed: 2.747s / 39.671s / 1.107s / 227.173s respectively. The full run plus
this final affected-package rerun cover the final production tree; this is not
a claim that the earlier full run alone included the last tightening.

The original failing case was also rerun ONE HUNDRED times on the final code:

```sh
go test -race ./internal/harness/composition -run '^TestProviderDistinctRequestsAndHTTP2Isolation$/chat/http2/cancel=true$' -count=100
```

Passed (5.896s), with its original 15s deadline and assertions unchanged.
Architecture/document guards, affected-package vet, formatting and diff checks
passed. Durations are verification observations, not performance benchmarks.
The MCP slice's Provider regression blocker is resolved; both changes remain
uncommitted in the working tree, with no public SDK or remote integration claim.

No live provider endpoints, credentials, paid calls or remote git writes used.
