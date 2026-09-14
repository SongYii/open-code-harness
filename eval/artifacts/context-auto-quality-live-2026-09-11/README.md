# context-auto-quality live run artifacts, 2026-09-11

Four consecutive live runs of the `context-auto-quality-live-example` EvalSet
against a real DeepSeek endpoint. These are the complete Attempt trees the
runner wrote — evidence manifests, transcripts, audit exports, and the SQLite
event store of each run — not a summary.

They are committed because the repository's own status documents name
real-model sample size as an outstanding blocker, and because they had
survived only as untracked directories inside one developer worktree. Deleting
that worktree would have destroyed them. Provenance for the four
`eval/reports/context-auto-quality-live-deepseek-2026-09-11-run-N.json`
documents is exactly these trees: each report was regenerated from the tree
beside it with `och-eval report`, not transcribed.

| Run | Attempts | Judge verdict |
| --- | --- | --- |
| `run-1` | 2 | one `indeterminate`; the second Attempt is `inspect_required` and was never scored |
| `run-2` | 1 | `indeterminate` |
| `run-3` | 1 | `indeterminate` |
| `run-4` | 1 | `pass` |

Read that table as what it is. One `pass` out of four runs of the same frozen
Cell, with three `indeterminate` verdicts and one Attempt that never reached a
verdict at all, is not a quality result. It is a sample of how this judge
behaves on this Scenario against this provider on one day, and the honest
reading is that the judge did not reach a definite answer most of the time.
No claim about context quality rests on these runs, and none should until the
`indeterminate` outcomes are explained.

## Reproducing a report

```bash
go run ./cmd/och-eval report \
  -set eval/sets/context-auto-quality-live.example.json \
  -artifacts eval/artifacts/context-auto-quality-live-2026-09-11/run-4
```

Reporting is offline and reads only the committed trees; it contacts no
provider and needs no credential. `run-1` through `run-3` exit non-zero,
which is the runner correctly refusing to call an indeterminate outcome a
pass.

## What is not here

No credential is present: the trees were scanned for key and bearer-token
shapes, in the JSON and in the SQLite files, before they were committed.
Subject documents name a credential *environment variable*, never its value.
