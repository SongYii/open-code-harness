# Pinned Anthropic SDK conformance probe

Status: historical local conformance experiment. Date: 2026-09-14.

Follow-up: v1.72.0 has now been adopted by the main module's experimental
`deepseek-messages` route. See the [current contract](../../docs/architecture/provider-replay.md).
The isolation and verification observations below describe the preceding probe
stage. This characterization helper remains test-only; production uses its own
bounded admission and never exposes this helper's partial SDK results.

This answers whether the official Go SDK can replace hand-written native Messages
plumbing without treating SDK success as an OCH durable-completion guarantee.
The selected endpoint is DeepSeek's official Anthropic-compatible interface;
every HTTP call here terminates at an injected in-memory transport. No sockets,
real credentials, provider tools, or paid API calls are used.

## Dependency and execution isolation

- SDK: `github.com/anthropics/anthropic-sdk-go v1.72.0`.
- Upstream commit: `03d7ef5861e6db02581610bf60bf0abe57bcb52a`.
- Module checksum: `h1:T0qQWWfygiL24lqEQaffRXCvEeno3yiv/mFzUdY1E88=`.
- License: MIT; no upstream source was copied into this repository.
- `go.mod`/`go.sum` here are an **alternate manifest**, not a runnable standalone
  application and not the production dependency graph.
- `overlay.json` adds `sdk_probe_test.go` virtually to the adapter's test package.
  That reuses the exact [existing fixture helpers](../../internal/harness/adapters/anthropic/decode_test.go)
  without duplicating the corpus. At the probe stage, the root had no SDK dependency.
  A build tag alone would not isolate it: `go mod tidy` considers test build tags.

Run **from the repository root**, with Go 1.26 as used by the repository:

```sh
go test -mod=readonly \
  -modfile=experiments/anthropic-sdk/go.mod \
  -overlay=experiments/anthropic-sdk/overlay.json \
  -tags=sdkprobe ./internal/harness/adapters/anthropic -count=1 -v
```

First use downloads public modules; subsequent runs need no network when they
are cached. Add `-race` for the race run. To use the isolated cache from this
investigation, prefix the command with:

```sh
GOCACHE=/tmp/och-architecture-review-gocache GOMODCACHE=/tmp/och-sdk-probe-modcache
```

After downloading, `GOPROXY=off GOSUMDB=off` was used for the offline test runs;
the committed checksums still bind the downloaded modules. Ordinary root
`go test ./...` does **not** execute this explicit experiment. No CI coverage or
production SDK adoption is claimed by its presence.

## What passed, and what must remain ours

| Case | Observed v1.72.0 behavior | Integration implication |
| --- | --- | --- |
| Existing normal fixtures | Fragmented tool input, omitted thinking, Unicode, cumulative usage and native message conversion work | Reuse SDK types and protocol facilities |
| Indexed open blocks | Multiple blocks can remain open; deltas/stops update the correct index | Wider than the current sequential-open prototype |
| Two-request tool continuation | Signed block order, empty thinking, large integer input and grouped results survive native request serialization | SDK replay conversion is useful; no tool executor or durable history is provided by this test |
| Missing message_stop | EOF can return no SDK error while retaining partial content | Track explicit protocol completion, not just `stream.Err()` |
| Truncated tool input | Accumulator replaces malformed input with `{}` | Validate before repair; never commit fabricated arguments |
| Duplicate fields | SDK accepts an ambiguous JSON payload | Keep bounded raw JSON validation before lossy decoding |
| Usage-only message_delta | Updates usage but clears the SDK object's stop reason | Track finish-reason presence independently |
| Redirect-safe client + HTTP 307 | No redirect is followed, but streaming can appear empty with no SDK error | Check HTTP status/content type before stream decoding |
| HTTP 429/503 | No retries when explicitly disabled; errors retain response bodies and HTTP objects | Own retry accounting and project errors into safe fields |
| Ambient credentials | `WithoutEnvironmentDefaults` prevents inherited bearer/profile configuration | Resolve credentials in OCH; don't let the SDK add a second credential source |
| Native redacted_thinking | SDK preserves the opaque block | Native-format evidence only; not a DeepSeek compatibility claim |

The hostile-fixture matrix runs all existing rejection fixtures through both
implementations. Its metadata-only logs characterize SDK acceptance; they are
not claims that every accepted shape is forbidden by the remote API. Specific
cases above have explicit assertions, so dependency upgrades force review of
assumptions rather than silently changing them.

The probe's client controls are test configuration, not yet a production wrapper.
The helper intentionally returns the SDK's raw observations, including partial
messages and errors, to expose their behavior. It must not be reused as an engine
completion gate. Actual cancellation/idle/header timeouts, response limits,
DeepSeek signature semantics, SQLite restart, audit export and compaction remain
integration work.

## Mutation evidence

Three separate temporary mutations to `probeClient` failed at the intended
assertions and were restored:

1. Remove `WithoutEnvironmentDefaults`: the explicit API key coexists with an
   unexpected ambient bearer header. The test deliberately leaves the ambient
   API-key variable empty, so explicit-key precedence cannot mask this mutation.
2. Change `WithMaxRetries(0)` to `WithMaxRetries(1)`: the 503 test observes two
   requests instead of one.
3. Permit redirects: the 307 test observes a second request. Its injected
   transport rejects that follow-up, so a guard regression cannot escape to
   a real network or loop indefinitely.

These are assertion failures, not compile errors or fixture-startup failures.

## Final local verification

Passed on the restored tree:

- The full overlaid adapter suite with `-race`, including both the original
  parser tests and the pinned SDK characterization tests.
- `go vet` for the overlaid SDK package and separately for the root module.
- Root adapter, architecture and documentation tests.
- `go mod verify -modfile=experiments/anthropic-sdk/go.mod` (all modules verified).
- Root `go mod tidy -diff`, `git diff --check`, and an empty diff for root
  `go.mod`/`go.sum`.

No full-repository test-suite run, live DeepSeek acceptance, or production
adapter completion is claimed by these results.

## Adoption decision

Proceed with SDK-assisted native requests/types and indexed block assembly, but
only behind bounded raw-input/HTTP/grammar admission and safe error projection.
Do not import its tool runner, use its repaired output as a canonical event,
or expose SDK structs through Domain/public APIs. Whether to reuse the SDK's SSE
decoder or keep a small bounded raw-frame reader depends on enforcing limits and
validation **before** decode; the SDK's default decoder alone is insufficient.
Do not maintain two complete production parsers to check one another.

The subsequent replacement adopted the SDK for request encoding, HTTP execution
and indexed content accumulation, retaining bounded raw framing/tool validation.
Root dependencies, durable state and CLI routes now include that implementation;
OTel remains unchanged. This isolated probe is retained as SDK upgrade evidence.
