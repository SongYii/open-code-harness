# Bounded DeepSeek Messages live acceptance

Opt-in experiment, not CI, a benchmark, a public injection API, or a stable
provider certification. See the dated observations in the
[evidence ledger](../../docs/architecture/provider-replay-evidence.md).

The adapter probe exercises a synthetic tool round trip through the production
adapter. The Composition probe uses actual assembly, a synthetic `nonce.txt`
workspace, read-only policy, SQLite, reopen, manual summary, continuation,
display export, and independent audit verification. Its overlay changes only
the assembly's HTTP client to install a **pre-send cost guard**. It does not
replace the model, adapter, tool loop, summarizer, storage, or lifecycle.

## Local rehearsal before spending

The same Composition scenario can run with a strictly in-process scripted
transport. It does not read the key, resolve DNS or contact a provider. The
transport is a Go test argument, not an environment switch that could quietly
turn a live result into a fixture result. Output is labeled `LOCAL_REHEARSAL`.

```bash
probe_overlay_dir="$(mktemp -d /tmp/och-deepseek-live.local-XXXXXX)"
go run experiments/deepseek-messages-live/prepare.go "$probe_overlay_dir"
go test -race -overlay="$probe_overlay_dir/composition-overlay.json" \
  -tags=livecomposition ./internal/harness/composition \
  -run '^TestMessages(CompositionLocalRehearsal|ReplayTailPreflight|CompactionMustLeaveReplayTail)$' \
  -count=1 -v
```

The rehearsal creates and removes its own synthetic workspace/database/ledger;
it never resets a live ledger. It exercises all eight requests, the real read
tool, both reopen boundaries, a nonempty signed assistant tail, exact wire
comparison and independent audit verification. A callback spy separately
requires **zero summary calls** for an open/failed/non-native newest turn.
After compaction, a second guard requires that turn's assistant state to remain
outside checkpoint coverage before any continuation request is sent.

This rehearsal closes a probe setup weakness found on September 14, **not by
itself a remote acceptance gate**. A subsequent authorized September 15 run
passed the actual live gate in eight calls; see the evidence ledger for the
combined budget and limits. Disposable numbered rows now supply source/retained
history instead of identical repeated sentences. No evidence establishes that
repetition caused the previously uncaptured stream failure.

## Cost and privacy boundary

- Only `POST https://api.deepseek.com/anthropic/v1/messages`, model
  `deepseek-flash`, streaming enabled.
- At most 12 transport calls **shared across both probes and diagnostic reruns**
  in the same artifact directory. A locked, fsynced ledger reserves each call
  before sending; failures and unknown outcomes count. Do not remove the ledger
  or create another directory to bypass a previously authorized budget.
- Each serialized request is at most 32 KiB; `max_tokens` is at most 4,096.
  SDK retries and redirects remain disabled by production code.
- At the [2026-09-14 official peak price](https://api-docs.deepseek.com/zh-cn/quick_start/pricing/)
  of CNY 2/M uncached input and 8/M output, allowing a conservative 65,536 input
  tokens per bounded request gives an estimated ceiling of CNY 1.97 for all 12.
  This is a conservative estimate, **not an account-level monetary lock** or
  invoice reconciliation. Check current prices before any future authorization.
- Read `/tmp/och-deepseek-key` only if regular, nonempty and mode 0600. No key,
  request header, message text or hidden thinking is printed by the probes.
  The model sees synthetic inputs, stock harness instructions and tool schemas,
  not the working repository. No write/exec tools are authorized by the policy.
- Artifact directory must be private (0700). SQLite and audit **do contain
  hidden thinking/signatures in plaintext**. Composition's bounded response
  captures (`response-NN.sse`, 0600) are also sensitive. Keep them local, outside
  Git; share only reviewed metadata. Deleting a credential file is not revoking
  the provider key or securely erasing all historical copies.

## Reproduce only with fresh, explicit spending permission

Run from the repository root in Bash. The credential prompt hides typed input;
paste the key there and press Enter. Do not paste it into a chat or command line.

```bash
umask 077
read -rsp 'DeepSeek API key: ' key
printf '%s' "$key" > /tmp/och-deepseek-key
unset key
echo
export OCH_MESSAGES_LIVE_ARTIFACTS="$(mktemp -d /tmp/och-deepseek-live.XXXXXX)"
go run experiments/deepseek-messages-live/prepare.go "$OCH_MESSAGES_LIVE_ARTIFACTS"
```

After checking and approving the price/bounds above:

```bash
export OCH_MESSAGES_LIVE_CONFIRM=I_UNDERSTAND
go test -overlay=experiments/deepseek-messages-live/overlay.json \
  -tags=liveprobe ./internal/harness/adapters/anthropic \
  -run '^TestLiveDeepSeekMessages$' -count=1 -v
go test -overlay="$OCH_MESSAGES_LIVE_ARTIFACTS/composition-overlay.json" \
  -tags=livecomposition ./internal/harness/composition \
  -run '^TestLiveMessagesComposition$' -count=1 -v
unset OCH_MESSAGES_LIVE_CONFIRM
rm -- /tmp/och-deepseek-key
```

Without the consent variable, both tests skip before reading credentials or
accessing the network. `prepare.go` only builds local overlays and does not
clear a request ledger. Refresh the snapshot if assembly changes. The current
Composition fixture declares a deliberately small 24,576-token context window
(4,096 maximum output, 10% tail), not the remote model's full context capacity.
It uses manual focus to request a compact eight-heading summary.

For offline stream diagnostics, no credential or consent variable is needed:

```bash
OCH_MESSAGES_CAPTURE=/tmp/och-deepseek-live.<suffix>/response-NN.sse \
  go test -overlay=experiments/deepseek-messages-live/overlay.json \
  -tags=liveprobe ./internal/harness/adapters/anthropic \
  -run '^TestInspectCapturedMessages$' -count=1 -v
```

This reports only event shapes, terminal reason and token counters. A rejected
capture intentionally fails the inspection test; it is not a passing acceptance
result. `OCH_MESSAGES_LIVE_RESUME_WORKSPACE` is a diagnostic entry point for a
single existing synthetic session **after the history-pressure turns have been
completed successfully**; it skips those turns and attempts compaction/continuation
only. Failed/open newest turns now stop at preflight without a summary request.
Inspect existing history before choosing it; it is not a general automatic
checkpoint/retry facility. `OCH_MESSAGES_LIVE_INSPECT=1` with that workspace
prints persisted failure codes and usage without calling a model.
Both inspection modes install a transport that rejects every HTTP request and
do not read the credential file. `OCH_MESSAGES_LIVE_VERIFY=1` verifies the
existing canonical history, display isolation and cold audit, separately from
the live nonempty-retained-wire assertion. It is not permission to mark that
live assertion as passed.

The September 14 run started before the native input-usage normalization fix.
Its early stored counters must not be rewritten: native uncached input is not
total input. Treat those rows as historical evidence of the bug, not comparable
normalized usage samples. Final billing belongs to the provider account ledger.
