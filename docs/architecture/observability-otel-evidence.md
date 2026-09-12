# Trace-only Observability Completion Evidence

**Status:** complete

**Contract:** [Trace-only observability](observability-otel.md)

**Design:** [accepted design](../superpowers/specs/2026-09-12-observability-otel-design.md)

**Plan:** [implementation plan](../superpowers/plans/2026-09-12-observability-otel.md)

## Commits

| Commit | Content |
| --- | --- |
| `c5743fd` | Typed telemetry port, bounded OTLP/HTTP adapter, Composition lifecycle, all first-slice instrumentation, topology/privacy/wire tests |

## What is proven

| Claim | Executable evidence |
| --- | --- |
| Business packages do not depend on OTel | `TestProductionDependencyBoundaries` classifies `telemetry` and `adapters/otel`; only the latter may import `go.opentelemetry.io/otel...` |
| Stable parent tree | memory-recorder tests cover Turn/model/logical append parentage and Runtime reconcile/recovery-append parentage |
| Real OTLP format | `TestAdapterExportsParentedMetadataOnlyOTLP` decodes OTLP protobuf, verifies resource/span parent IDs, and rejects a content canary |
| Content stays out | `TestTelemetryTurnTopologyContainsOnlyMetadata` and `TestTelemetryDoesNotChangeProviderWire` drive prompt, output, path, key, and endpoint canaries through real paths and search raw records/OTLP bytes |
| Prompt cache neutrality | `TestTelemetryDoesNotChangeProviderWire` requires byte-identical method, request URI, OCH-owned headers, and body with tracing off/on |
| Export cannot block work | `TestBoundedProcessorDropsWithoutBlockingAndHidesExporterError` blocks an exporter, overfills the fixed queue, requires counted drops and bounded `OnEnd`, and rejects raw error text in diagnostics |
| Invalid metadata fails safe | port tests reject arbitrary keys/content and close an invalid End as `dropped/invalid_telemetry_metadata` |
| Configuration fails closed | adapter and Composition tables reject missing trace paths, userinfo/query/fragment, remote plaintext, invalid ratios, and options without an endpoint |
| Shutdown is assembly-owned | enabled Composition test receives OTLP on `Assembly.Close`; normal Open/Close and idempotence suites remain green |
| Runtime recovery is visible | `TestLaunchTracesRecoveryAppendAsReconciliationChild` proves the deterministic recovery append is a child of startup reconciliation |
| Every context trigger is visible | `TestTelemetryAutomaticContextTriggersHaveStableTopology` covers pre-turn compaction, mid-turn preparation, and overflow-retry compaction; `TestTelemetryManualCompactionIsARootWithAppendChildren` covers manual compaction |
| A real Collector accepts the output | official `otelcol` v0.160.0 accepted one real ACP Turn as 8 spans, including `och.turn`, `och.context.prepare`, `och.model.request`, `och.store.append`, and startup `och.runtime.reconcile` |

The checked-in memory tree covers normal, failure, cancellation, tool,
approval, policy denial, duplicate join, replay, manual compaction, and
startup-recovery paths. The automatic context matrix separately covers all
three production triggers: pre-turn, mid-turn, and overflow-retry.

## External Collector proof

On 2026-09-12, the official OpenTelemetry Collector release `v0.160.0` Linux
amd64 archive was pinned by its GitHub Release API SHA-256 digest
`5415b8daf782f17cc463c3e46816abf181a68a04b7bcf98c273c3c204096c743`;
the downloaded archive matched that digest. The Collector used only an OTLP
HTTP receiver on `127.0.0.1:14318` and its detailed debug exporter.

A locally built `acp-client` drove one prompt through a separately running,
locally bound fixture Provider and a real `och -acp` process with:

```text
-otel-traces-endpoint http://127.0.0.1:14318/v1/traces
-otel-allow-insecure-loopback
```

The client completed normally with `collector proof` and `end_turn`. The
Collector decoded one startup reconciliation trace plus one complete Turn
trace: **8 spans total**. The Turn root had direct context-prepare,
model-request, and logical store-append children; every span ended `ok`. This
proves interoperability independently of the in-process protobuf decoder.

## Size and dependency cost

Measured against implementation base `b37e488`, excluding tests and docs:

- production Go additions: **1,199 lines**, under the accepted 1,200-line cap;
- exact production module-version graph for `cmd/och`: **17 → 36** entries;
- new/replaced production entries include the four OTel modules, OTLP proto,
  protobuf/gRPC/gateway, `backoff/v5`, `go-logr`, and upgraded `x/net`,
  `x/oauth2`, `x/sync`, and `x/text` selected by minimal-version selection;
- `go list -deps ./internal/harness/adapters/otel`: **380 packages** including
  the standard library; and
- Linux amd64 `och`, `go1.26.6`, `-buildvcs=false -trimpath`: **20,706,339 →
  31,841,654 bytes**, a **11,135,315-byte / 53.78%** increase.

This is a large cost. The feature remains runtime opt-in but cannot be compiled
out: the accepted design deliberately chose one binary for operational
simplicity. A future size-sensitive distribution should reconsider a separate
binary/build, not pretend the disabled runtime removes linked code.

## Benchmarks

Linux amd64, Intel Xeon Platinum 8259CL, Go 1.26.6, fixed 100 iterations:

```text
BenchmarkRunTurnTelemetry/disabled-2          1139967 ns/op  511799 B/op  2591 allocs/op
BenchmarkRunTurnTelemetry/sampled_otel-2      1332430 ns/op  536714 B/op  2768 allocs/op
BenchmarkRunTurnTelemetry/forced_drop_otel-2  1169643 ns/op  532075 B/op  2727 allocs/op
```

These are local comparison numbers, not an SLO. The sampled delta in this run
is about 0.19 ms/Turn; forced-drop remains close to disabled because enqueue is
non-blocking. The dedicated overload test, not timing noise, proves the bound.

## Verification record

Passed:

```text
go test -race ./internal/harness/telemetry ./internal/harness/adapters/memory \
  ./internal/harness/adapters/otel ./internal/harness/application \
  ./internal/harness/runtime ./internal/harness/architecture -count=1
go test -race ./internal/harness/application \
  -run '^TestTelemetryAutomaticContextTriggersHaveStableTopology$' -count=1 -v
go test ./internal/harness/composition ./cmd/och ./internal/harness/architecture
go vet ./...
GOOS=windows go build ./... && GOOS=windows go vet ./...
GOOS=darwin go build ./... && GOOS=darwin go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./... # No vulnerabilities found.
go mod tidy -diff
git diff --check
```

`go test ./... -count=1` passed every package except two `localexec` host
capability tests during the concurrent full run. Both passed immediately with:

```text
go test ./internal/harness/adapters/localexec \
  -run 'TestRunKillsOnResourceLimitSignal|TestEnforcementReportsNoneWithoutAPlatformBackend' \
  -count=1 -v
```

No telemetry code is imported by `localexec`; the transient result is recorded
rather than rewritten as a clean full-suite pass. All trace-only observability
acceptance evidence is now complete; the unrelated host-capability flake remains
documented instead of being hidden.
