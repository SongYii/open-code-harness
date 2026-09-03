# Running and Operating Evaluations

**Audience:** anyone running `och-eval` locally or diagnosing a run's results

**See also:** [Evaluation System — Implemented Contract](../architecture/evaluation.md) for the underlying mechanism; [Authoring Evaluation Scenarios](evaluation-scenarios.md) if you also need to change what runs.

## The four commands

```text
och-eval run     -set PATH -artifacts PATH [-och-binary PATH] [-live]
och-eval regrade -attempt PATH -scorer ID
och-eval report  -set PATH [-artifacts PATH] [-output PATH]
och-eval judge   -attempt PATH -judge-config PATH [-price-table PATH] [-live]
```

`run` expands and executes an EvalSet's every Cell, publishing Attempt
evidence under `-artifacts`. `-och-binary` is required only when the EvalSet
references an `acp_subprocess` Executor — build one first
(`go build -o /tmp/och ./cmd/och`) and pass its path; `run` never builds it
for you. `regrade` scores one already-published Attempt directory against a
named scorer from `cmd/och-eval/scorer_catalog.go`'s own compiled table,
appending a new Score without touching any earlier one. `report` aggregates
every Attempt directory under an artifact root (classification state,
Outcome, every Score, and — new in Task 15 — any executor-parity pair it can
find) into one JSON document on stdout. `judge` runs one live quality
judgement against an already-published live Attempt and appends the Score it
produces; it is documented in full under
[Live quality judging](#live-quality-judging) below.

Every command's machine output is one versioned JSON document on stdout;
human-readable diagnostics go to stderr. Exit codes distinguish validation
failure, a deterministic gate failure, infrastructure failure, indeterminate
completion, and internal error (`cmd/och-eval/exit.go`) — a CI step checking
only "exit 0" already gets the gate signal it needs; a step that wants to
tell "policy said no" apart from "the harness itself broke" should switch on
the specific code.

## Resuming after an interruption

`run` does not resume on its own — if it was killed mid-EvalSet, some
Attempts under `-artifacts` may be in any of four states
(`internal/harness/eval/recovery.go`'s `ClassifyAttemptDirectory`):

| State | What it means | What to do |
| --- | --- | --- |
| `Uncommitted` | `attempt.json` never published | Nothing recoverable; this Cell never durably started |
| `InspectRequired` | Attempt published, no Outcome | A real crash mid-execution — inspect the workspace/process directories by hand before deciding whether to retry the Cell as a fresh Attempt |
| `ResumeCollectionOnly` | Attempt and Outcome exist, no Manifest | Evidence collection itself was interrupted after the Subject already stopped — `ResumeCollection` can finish this without re-running the Subject |
| `Terminal` | All three exist | Nothing to resume; `report`/`regrade` already work on it |

There is no CLI subcommand for `ResumeCollectionOnly` yet — it is a library
function (`eval.ResumeCollection`) a caller invokes directly today. `report`
still includes an `InspectRequired`/`ResumeCollectionOnly` Attempt in its
own output (with its `recoveryState` and no `status`/`scores`), so a report
always accounts for every published Attempt even when some are incomplete.

## Diagnosing an `indeterminate` Attempt

`indeterminate` means durable evidence cannot prove whether the Subject or
the infrastructure owns a failure — it is never "quality failed," and never
something a retry alone fixes without understanding why. Read the Outcome's
own `code` field first (`process/`, `evidence/diagnostics.json` when
present, and the Attempt's own `audit/` directory are the next places to
look):

- `acp_shutdown_unproven` / `acp_cancel_reap_unproven` / `acp_compact_relaunch_unproven`:
  a process reap could not be proven within its own grace period. Check
  whether the host was under unusual load (a starved scheduler can push a
  reap past a grace period that is normally generous) before assuming a real
  hang; if it reproduces reliably, it is a real bug, not a flaky timing
  assumption.
- `acp_cancel_escalated`: cancellation needed to reach at least SIGTERM to
  resolve — the writer was torn down, and the Scenario's own remaining
  actions never ran. Expected for a Scenario intentionally testing
  escalation; unexpected for anything else.

An `indeterminate` Outcome's own `evidence/` directory is still whatever
could be collected — read it before assuming nothing durable exists.

## Regrading, scoring, and cost

`regrade` never invokes the Subject and never needs a Provider credential —
it reads only already-committed evidence (`eval.RegradeAttempt` resolves
Scenario/Subject/Executor identity from the Attempt's own staged evidence
documents, never from a caller-supplied path). A regrade is safe to run
repeatedly and offline; each call appends a new, independent Score.

A deterministic verifier never costs anything beyond CPU/disk. A live
model-judge Score does, and its cost availability is explicit rather than
inferred: `ScorerUsage.costStatus` is either `computed` (with a
`costCurrency`, including a genuine zero for a free model) or `unavailable`
(no currency, zero microunits, and no claim that the run was free). A judge
Score's usage is kept independent of the Subject's own usage/cost recorded
on Outcome. Model judge quality signals are advisory and never gate an
ordinary PR (design §22) — only deterministic verifier failures and
configured deterministic floors may.

## Live quality judging

`och-eval judge` scores one already-published **live-lane** Attempt's
committed evidence with a real model, and appends the resulting Score
beside whatever deterministic Scores that Attempt already carries. It never
invokes the Subject, reopens its session, or touches Outcome.

```bash
export OCH_EVAL_LIVE_JUDGE_API_KEY=...        # the variable the JudgeConfig names
export OCH_EVAL_LIVE_CONFIRM=I_UNDERSTAND

go run ./cmd/och-eval judge \
  -attempt .eval-artifacts-context-quality-live/<attempt-id> \
  -judge-config eval/judges/context-quality-judge.example.json \
  -live
```

There is deliberately **no** endpoint, model, prompt, or credential-value
flag. Every one of those comes from the frozen JudgeConfig, and the command
refuses unless that document is byte-identical to the one the Attempt's own
evidence proves it was entitled to use — the runner staged `judge-config.json`
into the Attempt's evidence at collection time and the EvalSet named its
digest, so a Score's claimed configuration is verifiable offline, from the
Attempt alone, by anyone. A flag capable of overriding any of it would make
that guarantee meaningless.

The order of the gates is the point:

1. **Frozen evidence.** The Attempt must carry `eval-set.json` and
   `judge-config.json`, and the supplied config's canonical digest must match
   both the staged copy and the EvalSet's `judgeConfigDigest`.
2. **Dual consent.** `-live` **and** `OCH_EVAL_LIVE_CONFIRM=I_UNDERSTAND`,
   exactly as `run` requires them.
3. **Deterministic prerequisites.** Every verifier the Scenario declares
   runs. A non-Pass result publishes an Indeterminate Score *without calling
   the model at all* — quality judging asks "was this good", which is not
   worth paying for when the run did not meet its own invariants.

Only after all three does the provider call happen, and the credential the
JudgeConfig names is read only inside that call. Anything refused above
happens before a credential is ever looked up.

Two more refusals are worth knowing about:

- **Insufficient evidence never passes.** If a criterion declares an
  evidence role the manifest never collected, or the bundle's byte budget
  would drop an entire selected file, the run returns Indeterminate without
  calling the model and lists what was omitted in `missingEvidence`.
  Per-entry truncation is permitted — the contract supplies bounded excerpts
  — and every entry label records its original byte length, its excerpt byte
  length, and whether it was truncated.
- **An Attempt collected before this contract existed cannot be judged.**
  It has no staged EvalSet or JudgeConfig, so nothing in its evidence could
  prove which configuration it was entitled to use. Such an Attempt still
  regrades deterministically, forever; only live judging is closed to it.

**Exit codes are not the quality signal.** A published Score always exits
`0`, including a quality `fail`: live quality reports on work that already
cleared its deterministic gates, so it is a finding to read on stdout, not a
build to break. Exit `2` means the run was refused before any Score existed
(flags, JudgeConfig, price table, lane, frozen binding, or a legacy
Attempt); exit `1` means this command itself failed to encode or publish.

Pass `-price-table PATH` to get a computed cost. A JudgeConfig that pins a
`priceTableDigest` *requires* the flag and requires the table to match it —
a Score claiming a computed cost against a table nobody can identify would
be worse than one honestly reporting the price as unavailable.

## Credentials and privacy

A fixture-lane run never reads a real credential — `och-eval run` sets a
placeholder value (`fixture-placeholder-key`) for the duration of each
fixture-lane Subject's own credential environment variable and restores
whatever was there before on exit, whether the run succeeded or not.

A live-lane run requires **both** halves of design §24's dual consent before
touching a credential at all: the EvalSet's own `lane: "live"` **and** the
`-live` flag, **and** `OCH_EVAL_LIVE_CONFIRM=I_UNDERSTAND` in the
environment. Missing any one of these three refuses the run before creating
an artifact root — verify this yourself with the checked-in example rather
than trusting this sentence alone:

```bash
go run ./cmd/och-eval run -set eval/sets/context-quality-live.example.json -artifacts "$(mktemp -d)"
# refused: pass -live to confirm this EvalSet's own declared lane
```

No evidence is ever uploaded anywhere by this tooling, live or fixture —
every artifact stays on the local filesystem under whatever `-artifacts`
path was given. `internal/harness/redact`'s `Text` function strips
secret-shaped substrings (API keys, bearer tokens, PEM key blocks, and
similar) from tool-result content and error messages before they ever enter
committed evidence or a judge's own evidence bundle — but it is a small,
hardcoded, shape-specific pattern match, not a general secret scanner; treat
evidence directories as potentially still carrying real, unredacted
workspace content a live Subject wrote (a live run's own workspace is real
files a real model wrote, not a fixture double), and handle them with the
same care you would the credentials themselves.

## Artifact retention

Nothing in this milestone deletes an Attempt directory automatically —
`-artifacts` accumulates every Attempt from every `run` invocation pointed
at it until an operator cleans it up by hand. `.eval*` artifact roots are
gitignored by this repository's own convention (never commit one). There is
currently no built-in retention policy, size cap, or automatic pruning —
budget disk space accordingly for a long-running host that runs evaluations
repeatedly, and prune old artifact roots manually.

## Safely handling an orphaned runtime lease

If an `acp_subprocess` writer or `compact-session` process was killed
outside this tooling's own control (an operator's own `kill -9`, a host
reboot, an OOM kill) and its own database's runtime lease was never
released, **never signal a PID you read back from lease or database state**
— that PID may already have been reused by an unrelated process on the
host, and signaling it is signaling the wrong thing. This tooling's own
code never does this (every signal it ever sends targets a process handle
it spawned itself, in the same invocation, never a PID reconstructed from
persisted state) and no operator procedure should either.

The correct, safe recovery is to simply **wait**: the lease has a bounded
duration (`internal/harness/adapters/sqlite`'s own default, 30 seconds) and
expires on its own — a subsequent `run`/`compact-session` invocation
against the same database with a different runtime ID will succeed once it
does, without needing any manual intervention at all. If the orphaned
process is still actually running and holding host resources you need back
immediately, identify it through normal host tooling (`ps`, matching on the
workspace/database path in its own command line — every launch's argv is
visible in `ps`, not obscured) rather than trusting anything this
subsystem's own state recorded about "which PID" it was.
