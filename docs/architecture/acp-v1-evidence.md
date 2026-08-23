# ACP v1 Adapter completion evidence / ACP v1 Adapter 完成证据

**Status:** Complete evidence ledger

**Date:** 2026-08-22

**Design:** [ACP v1 Adapter (Milestone 6)](../superpowers/specs/2026-08-22-acp-v1-adapter-design.md)

**Contract:** [ACP v1 Adapter — Implemented Contract](acp-v1.md)

This ledger records what was done. Completion is claimed from the evidence
below, not from checkbox state.

英文为规范记录。

## Delivered

| Piece | Location |
| --- | --- |
| Focused spec + zh-CN | `docs/superpowers/specs/2026-08-22-acp-v1-adapter-design.md` |
| Approver slot | `internal/harness/tools/slot.go` |
| ACP adapter | `internal/harness/adapters/acp` |
| Composition `ServeACP` | `internal/harness/composition/assembly.go` |
| `cmd/och -acp` | `cmd/och/main.go` |
| Architecture guard `ownerACP` | `internal/harness/architecture/dependencies_test.go` |

## Verification commands

```bash
gofmt -l .
go vet ./...
go test ./internal/harness/adapters/acp/ ./internal/harness/tools/ ./internal/harness/composition/ ./internal/harness/architecture/ ./internal/docsguard/ -count=1
```

## Remaining

- No v2, resume, or authenticate. Catalog-backed `RunTurn` prefixes prior turns from the event log; there is no independent Context Engine or token-aware compaction.
- Default gate does not spawn `cmd/och`.
