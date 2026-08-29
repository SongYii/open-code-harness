# Session transcript completion evidence / 会话转录完成证据

**Status:** Complete evidence ledger

**Date:** 2026-08-23

**Contract:** [Session transcript — Implemented Contract](session-transcript.md)

**Combined ledger:** [Conversation and session transcript completion evidence](conversation-and-transcript-evidence.md)

This file is the stem ledger required for the session-transcript implemented
contract. The combined ledger lists PRs 1–8, mapping-table tests, golden
JSONL hashes, OpenReader vs live-lease tests, and exclusions. Verification
commands are repeated here so this file is itself auditable.

英文为规范记录。

## Verification commands

All keyless and network-free.

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test ./internal/harness/adapters/acp/ ./internal/harness/transcript/ \
  ./internal/harness/adapters/sqlite/ ./internal/harness/composition/ \
  ./internal/harness/architecture/ ./internal/docsguard/ ./cmd/och/ -count=1
go test -race ./internal/harness/adapters/acp/ ./internal/harness/transcript/ -count=1
```

## Remaining

Maze UI and verdicts; ACP v2; writing `transcript_entries`; changing the
audit JSONL codec; redacted export; subagent `origin`; `RuntimeEvent`
enrichment; import of transcript JSONL into EventStore. Surfaces remain
`experimental`; not GA.

**Update:** ACP session resume / list / delete, listed as excluded above at
this ledger's 2026-08-23 date, are implemented as of the [ACP session
lifecycle (Slice B) evidence ledger](acp-session-lifecycle-evidence.md),
which also adds the `session.deleted` transcript fact this contract's fact
catalog now documents.
