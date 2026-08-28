# ACP Session Lifecycle Slice B Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement capability-gated ACP v1 session list, resume, close, and logical delete while preserving append-only evidence, writer fencing, CAS behavior, workspace isolation, and deterministic catalog pagination.

**Architecture:** Add `session.deleted` to the bounded Session aggregate, expose lifecycle use cases through `application.Service`, and extend `application.EventStore` with a workspace-scoped head catalog. Memory and SQLite implement the same port; SQLite migration 4 rebuilds the head projection through a verified shadow table. ACP owns only duplex attachment/concurrency state, while durable close and delete remain Application/domain concerns. Transcript and implemented-contract documentation recognize the new durable fact.

**Tech Stack:** Go 1.26, standard library (`context`, `database/sql`, `encoding/base64`, `encoding/json`, `path/filepath`, `sync`, `time`), `modernc.org/sqlite`, newline-delimited JSON-RPC, table-driven `testing`, race and cross-build verification.

**Spec:** `docs/superpowers/specs/2026-08-27-acp-session-lifecycle-slice-b-design.md` (English normative); synchronized Chinese summary at `docs/superpowers/specs/2026-08-27-acp-session-lifecycle-slice-b-design.zh-CN.md`.

## Global Constraints

- ACP `session/close` is duplex detach only. It must never call `application.CloseSession` or append `session.closed`.
- `session.deleted` is the only new durable fact. It is terminal, logically hides a Session from ordinary use, and never physically removes canonical or audit rows.
- Domain deletion accepts active-idle and durable-closed Sessions, rejects running Sessions, and keeps second deletion as deterministic `session_deleted`.
- ACP delete is non-enumerating and idempotent for absent, foreign-workspace, and already-deleted Sessions; those cases return `{}` without mutation.
- Workspace roots use one lexical form: absolute plus `filepath.Clean`, with no symlink resolution. No adapter request may manufacture a stored root.
- `SessionHeadStatus` is independent of `domain.SessionStatus`: visible values are `idle`, `running`, and `closed`; SQLite alone also stores `deleted`.
- Catalog filtering happens before limiting. Pagination is descending `(updated_at_commit_position, session_id)` with `Limit + 1`, a maximum 512-byte strict base64url JSON cursor, bound SQL parameters, and one snapshot per page.
- Migration 4 must verify canonical replay and version-3 heads before swapping the shadow table. Legacy `active` is equivalent only to derived `running`; missing heads may be rebuilt, but mismatches and orphan heads are corruption.
- Projection behavior must stay aligned across append, migration, audit import, rebuild verification, and runtime recovery.
- No sleep-based concurrency tests. Use channels and the repository's bounded test timeouts.
- Every task follows red-green-refactor: run the focused test and observe the expected failure before production edits, then run focused package tests after implementation.
- Preserve current append identity, unknown-outcome resolution, writer authority, pinned reads, audit-chain bytes, and `sqlite.Reader`'s read-only surface.

## File Map

| Path | Responsibility |
| --- | --- |
| `internal/harness/domain/{state,commands,events,errors,decide,apply,record,codec}.go` | Deleted status, command, event, decision/application, clone, strict codec |
| `internal/harness/domain/*_test.go` | Decision/apply/codec/replay/compact/historical-oracle coverage |
| `internal/harness/application/store.go` | Catalog port types and `ListSessionHeads` method |
| `internal/harness/application/session.go` | Canonical workspace helper and create/load/list/resume/delete use cases |
| `internal/harness/application/{session,concurrency,store}_test.go` | Application lifecycle, cursor mapping, and CAS race tests |
| `internal/harness/application/eventstoretest/{suite,cases}.go` | Shared catalog conformance for every EventStore |
| `internal/harness/adapters/memory/event_store.go` | Deterministic in-memory head catalog and cursor implementation |
| `internal/harness/testkit/v2_store.go` and test doubles implementing `EventStore` | New catalog method stubs/delegation |
| `internal/harness/adapters/sqlite/{migrations,migrations_sql,session_catalog}.go` | Migration 4 and production list query/cursor codec |
| `internal/harness/adapters/sqlite/{append,rebuild,auditimport,lease}.go` | Shared head transition consumers and running recovery spelling |
| `internal/harness/adapters/sqlite/{migrations,session_catalog,append,rebuild,auditimport,lease}_test.go` | Shadow migration, projection, list, and recovery coverage |
| `internal/harness/adapters/acp/{protocol,server}.go` | Capabilities, RPC shapes, attachment state machine and handlers |
| `internal/harness/adapters/acp/server_test.go` | Exact wire frames and close/delete race coverage |
| `internal/harness/transcript/codec.go` and testdata | `session.deleted` fact projection and strict/golden codec |
| `docs/architecture/{acp-v1,session-transcript,sqlite-eventstore}.{md,zh-CN.md}` | Implemented contracts after behavior is green |
| `docs/architecture/{acp-v1,conversation-and-transcript,session-transcript,sqlite-eventstore}-evidence*.md` | Commands, test evidence, and implementation commits |

---

### Task 1: Add the terminal domain deletion fact

**Files:**

- Modify: `internal/harness/domain/state.go`
- Modify: `internal/harness/domain/commands.go`
- Modify: `internal/harness/domain/events.go`
- Modify: `internal/harness/domain/errors.go`
- Modify: `internal/harness/domain/decide.go`
- Modify: `internal/harness/domain/apply.go`
- Modify: `internal/harness/domain/record.go`
- Modify: `internal/harness/domain/codec.go`
- Test: `internal/harness/domain/decide_test.go`
- Test: `internal/harness/domain/apply_test.go`
- Test: `internal/harness/domain/codec_test.go`
- Test: `internal/harness/domain/compact_test.go`
- Test: `internal/harness/domain/compact_equivalence_test.go`
- Test: `internal/harness/domain/replay_test.go`
- Test: `internal/harness/domain/historical_oracle_test.go`
- Testdata: `internal/harness/domain/testdata/session_lifecycle.jsonl`

- [x] Add failing decision tests proving active-idle and durable-closed states emit exactly one `SessionDeleted{}`, while pristine, wrong-ID, running, and already-deleted states return their stable domain codes.
- [x] Add failing apply/replay tests proving `SessionDeleted` is accepted only from idle active-or-closed state, sets `Status = SessionStatusDeleted`, advances the version, and makes every later event fail closed.
- [x] Add failing codec/clone fixtures for the exact canonical envelope type `session.deleted` and `{}` payload, including strict rejection of null/unknown payload fields.
- [x] Extend the historical oracle independently so compact/historical equivalence covers both active-idle deletion and durable-close-then-delete.
- [x] Add these exact public declarations and route them through `Decide`, `Apply`, codec, and clone switches:

```go
const SessionStatusDeleted SessionStatus = "deleted"
const CommandDeleteSession = "session.delete"
const EventSessionDeleted = "session.deleted"
const CodeSessionDeleted ErrorCode = "session_deleted"

type DeleteSession struct{ SessionID SessionID }
type SessionDeleted struct{}
```

- [x] Implement `decideDeleteSession` and `applySessionDeleted` without weakening existing close/start eligibility. Deleted-state rejection must be explicit before generic status validation so callers receive `session_deleted`.
- [x] Run the focused tests before and after implementation:

```bash
go test ./internal/harness/domain -run 'Test(DecideDeleteSession|ApplySessionDeleted|RecordedEventCodec|Compact.*Delete|Historical.*Delete)' -count=1
go test ./internal/harness/domain -count=1
```

- [x] Commit: `feat(domain): add logical session deletion fact`.

### Task 2: Add canonical workspace and Application lifecycle use cases

**Files:**

- Modify: `internal/harness/application/store.go`
- Modify: `internal/harness/application/session.go`
- Modify: `internal/harness/application/read_stream.go`
- Modify: `internal/harness/application/session_test.go`
- Modify: `internal/harness/application/concurrency_test.go`
- Modify: `internal/harness/application/store_test.go`
- Modify: every test double that implements `application.EventStore`

- [x] Add failing table tests for `CanonicalWorkspaceRoot`: reject blank, padded/invalid UTF-8, and relative paths; return `filepath.Clean` for absolute paths without evaluating symlinks.
- [ ] Add the catalog port types exactly as specified and extend `EventStore`:

```go
type ListSessionHeadsRequest struct {
    WorkspaceRoot string
    Cursor        string
    Limit         uint32
}

type SessionHeadStatus string

const (
    SessionHeadStatusIdle    SessionHeadStatus = "idle"
    SessionHeadStatusRunning SessionHeadStatus = "running"
    SessionHeadStatusClosed  SessionHeadStatus = "closed"
)

type SessionHead struct {
    SessionID     domain.SessionID
    WorkspaceRoot string
    Status        SessionHeadStatus
    UpdatedAt     time.Time
}

type SessionHeadPage struct {
    Sessions  []SessionHead
    NextCursor string
}
```

- [x] Add failing service tests for fixed `Limit = 50`, defensive result slices, catalog-store error mapping, bad cursor as `CategoryValidation`, and canonical root forwarding.
- [x] Add failing resume tests for active-idle success without append, and absent/foreign/closed/running/deleted rejection.
- [x] Add failing delete tests for the normal append path, canonical workspace admission, absent/foreign/already-deleted public `session_not_found`, running rejection, unknown-outcome resolution, and second-delete internal replay.
- [x] Add a channel-driven race test where `RunTurn` admission and `DeleteSession` start from the same head and exactly one CAS append wins.
- [x] Implement:

```go
func CanonicalWorkspaceRoot(root string) (string, error)
func (s *Service) ListSessions(context.Context, ListSessionsRequest) (ListSessionsResult, error)
func (s *Service) ResumeSession(context.Context, ResumeSessionRequest) (domain.Session, error)
func (s *Service) DeleteSession(context.Context, DeleteSessionRequest) error
```

- [x] Make `CreateSession` persist the canonical root. Split loading into a private lifecycle replay and the public `LoadSession`; public load maps deleted state to `session_not_found`, while delete can inspect deleted state before returning the same public code.
- [x] Keep `DeleteSession` on the existing `BuildAppendIntent` / `CommitAppendIntent` / `ResolveAppendIntent` path so ID generation, authority lookup, digesting, CAS, and unknown-outcome resolution remain unchanged.
- [ ] Run:

```bash
go test ./internal/harness/application -run 'Test(CanonicalWorkspaceRoot|ListSessions|ResumeSession|DeleteSession|RunTurnDeleteRace|CreateSessionCanonical)' -count=1
go test -race ./internal/harness/application -count=1
```

- [ ] Commit: `feat(application): add session catalog resume and delete`.

### Task 3: Implement the catalog port in memory and shared conformance

**Files:**

- Modify: `internal/harness/application/eventstoretest/suite.go`
- Modify: `internal/harness/application/eventstoretest/cases.go`
- Modify: `internal/harness/adapters/memory/event_store.go`
- Modify: `internal/harness/adapters/memory/event_store_test.go`
- Modify: `internal/harness/testkit/v2_store.go`

- [ ] Add shared failing cases for workspace filtering, deleted omission, idle/running/closed mapping, descending commit-position/session-ID tie order, `Limit + 1`, next cursor, invalid/oversize cursor, UTC timestamps, and defensive returned slices.
- [x] Add a private strict cursor value matching `{"v":1,"p":123,"s":"session-id"}`. Encode with `base64.RawURLEncoding`; reject decoded payloads over 512 bytes, padding, unknown/missing fields, non-positive positions, and invalid Session IDs.
- [x] Extend in-memory state with the minimum immutable metadata needed to list heads at one mutex-protected snapshot. Update it only at the same single publish point as append.
- [x] Derive status through canonical domain replay or a shared memory projection: active with no turn is idle, active with a turn is running, closed is closed, and deleted is omitted before limiting.
- [x] Keep cursor comparison on `(commitPosition, sessionID)` and use the last returned visible row for `NextCursor`.
- [ ] Run:

```bash
go test ./internal/harness/adapters/memory ./internal/harness/application/eventstoretest -count=1
go test -race ./internal/harness/adapters/memory ./internal/harness/application -count=1
```

- [ ] Commit: `feat(memory): implement session head catalog`.

### Task 4: Migrate and maintain SQLite session heads v4

**Files:**

- Modify: `internal/harness/adapters/sqlite/migrations.go`
- Modify: `internal/harness/adapters/sqlite/migrations_sql.go`
- Modify: `internal/harness/adapters/sqlite/append.go`
- Modify: `internal/harness/adapters/sqlite/rebuild.go`
- Modify: `internal/harness/adapters/sqlite/auditimport.go`
- Modify: `internal/harness/adapters/sqlite/lease.go`
- Test: `internal/harness/adapters/sqlite/migrations_test.go`
- Test: `internal/harness/adapters/sqlite/append_test.go`
- Test: `internal/harness/adapters/sqlite/rebuild_test.go`
- Test: `internal/harness/adapters/sqlite/auditimport_test.go`
- Test: `internal/harness/adapters/sqlite/lease_test.go`

- [ ] Build version-3 database fixtures without opening them through the latest migrator. Add failing migration tests for empty and populated databases, `/repo/.` canonicalization, legacy active-to-running verification, missing-head rebuild, malformed event, mismatch, and orphan-head rollback.
- [ ] Register migration 4 and create `session_heads_v4` with final `workspace_root NOT NULL`, status check, foreign key, active ID, and commit-position constraints.
- [ ] In the migration code step, iterate `event_streams` in Session ID order, strict-decode and compact-replay each stream, derive canonical root/status/active IDs/last position, compare the version-3 head using only the approved legacy equivalence, insert the verified row, reject orphans, then drop/rename and create the partial visible index.
- [ ] Replace `applyHeadTransition` with a fallible transition that derives canonical root from `SessionCreated`, stores `running` rather than `active`, clears active IDs on terminal transitions, and stores `deleted` for `SessionDeleted`.
- [ ] Update append and audit import SQL to write `workspace_root`; no call site accepts an adapter request root.
- [ ] Extend rebuild verification to compare root, status, active IDs, and `event_streams.last_append_commit_position`, and to reject orphan head rows.
- [ ] Change runtime recovery enumeration from storage status `active` to `running`, preserving replay as the authority.
- [ ] Run:

```bash
go test ./internal/harness/adapters/sqlite -run 'Test(Migration4|AppendSessionHead|Rebuild|Import.*Head|ActiveSessions)' -count=1
go test -race ./internal/harness/adapters/sqlite ./internal/harness/runtime -count=1
```

- [ ] Commit: `feat(sqlite): migrate and maintain session catalog heads`.

### Task 5: Add SQLite keyset catalog queries

**Files:**

- Add: `internal/harness/adapters/sqlite/session_catalog.go`
- Add: `internal/harness/adapters/sqlite/session_catalog_test.go`
- Modify: `internal/harness/adapters/sqlite/conformance_test.go`
- Modify: `internal/harness/adapters/sqlite/reader_test.go`

- [ ] Add failing SQLite tests for deterministic ordering, Session ID tie-breaking, page boundaries, `Limit + 1`, invalid/oversize cursor, injection-shaped cursor content, workspace filtering, deleted omission before limit, and one-page read snapshot under a concurrent append.
- [ ] Implement `Store.ListSessionHeads` in one read transaction. Validate canonical root, cursor, and `1 <= Limit <= 256`; query only `status <> 'deleted'`, use bound keyset predicates, order descending, and join `event_appends` at `updated_at_commit_position`.
- [ ] Convert `committed_at_unix` to a UTC `time.Time`, validate every scanned status against the visible enum, keep the extra row out of `Sessions`, and derive the cursor from the last returned row.
- [ ] Run the shared EventStore conformance against SQLite and memory.
- [ ] Prove `sqlite.Reader` still has no `ListSessionHeads` method with the existing reflection/surface test style.
- [ ] Run:

```bash
go test ./internal/harness/adapters/sqlite -run 'Test(ListSessionHeads|Conformance|ReaderTypeHasNo)' -count=1
go test -race ./internal/harness/adapters/sqlite ./internal/harness/adapters/memory -count=1
```

- [ ] Commit: `feat(sqlite): add workspace session catalog pagination`.

### Task 6: Implement ACP lifecycle capabilities and duplex state machine

**Files:**

- Modify: `internal/harness/adapters/acp/protocol.go`
- Modify: `internal/harness/adapters/acp/server.go`
- Modify: `internal/harness/adapters/acp/server_test.go`
- Modify: `internal/harness/composition/end_to_end_test.go`

- [ ] Add exact initialize golden assertions for `listSessions`, `resumeSession`, `closeSession`, and `deleteSession`, while retaining `loadSession` and prompt capabilities.
- [ ] Add strict request/result structs for list/resume/close/delete, including optional `cwd`/`cursor`, required `sessionId`/`cwd`, tolerated empty MCP/additional-directory lists, and RFC3339Nano UTC `updatedAt`.
- [ ] Extend the adapter `Sessions` interface with Application list/resume/delete use cases.
- [ ] Replace the cancel-only entry with explicit states and a prompt completion channel:

```go
type wireSessionState uint8

const (
    wireIdle wireSessionState = iota
    wireRunning
    wireClosing
    wireDetached
    wireDeleting
)

type sessionState struct {
    state wireSessionState
    cancel context.CancelFunc
    promptDone chan struct{}
}
```

- [ ] Make successful new, active load, and resume attach an idle entry. Durable-closed load may replay but remains detached/unpromptable. Prompt requires an attached idle entry and moves it to running before starting `RunTurn`.
- [ ] Make prompt completion preserve `closing` when close won admission; otherwise return running to idle. Publish the terminal JSON-RPC response before closing `promptDone` so close settlement ordering is observable.
- [ ] Implement close as `idle/running -> closing -> detached`: cancel running work, wait without holding the mutex, release duplex-owned resources, return `{}`, and never call durable Application close.
- [ ] Implement delete admission under the mutex for idle/detached/absent only, install `deleting` before Application load/append, remove the entry after committed/idempotent success, and restore the exact prior state after an internal failure.
- [ ] Add channel-driven tests for: close-cancel-terminal-order; no `session.closed` append; detached prompt rejection and reattach; delete blocking prompt entry; running/closing/deleting rejections; absent/foreign/deleted idempotent `{}`; internal-error restoration; duplicate close/delete; and fixed non-leaking error strings.
- [ ] Canonicalize assembly and request roots using `application.CanonicalWorkspaceRoot`; reject invalid configuration before serving.
- [ ] Extend the composition ACP end-to-end path through list, close, resume, and delete, and assert transcript export still contains deletion evidence after ordinary load fails.
- [ ] Run:

```bash
go test ./internal/harness/adapters/acp -run 'TestServe.*(List|Resume|Close|Delete|Detached|Race|Capabilities)' -count=1
go test -race ./internal/harness/adapters/acp ./internal/harness/composition -count=1
```

- [ ] Commit: `feat(acp): implement session lifecycle management`.

### Task 7: Project deletion and publish implemented contracts

**Files:**

- Modify: `internal/harness/transcript/codec.go`
- Modify: `internal/harness/transcript/codec_test.go`
- Modify: `internal/harness/transcript/export_test.go`
- Modify: `internal/harness/transcript/testdata/facts.jsonl`
- Modify: `internal/harness/transcript/testdata/complete.jsonl`
- Modify: `docs/architecture/acp-v1.md`
- Modify: `docs/architecture/acp-v1.zh-CN.md`
- Modify: `docs/architecture/session-transcript.md`
- Modify: `docs/architecture/session-transcript.zh-CN.md`
- Modify: `docs/architecture/sqlite-eventstore.md`
- Modify: `docs/architecture/sqlite-eventstore.zh-CN.md`
- Modify: relevant evidence ledgers under `docs/architecture/`
- Modify: `docs/README.md` if its status/authority table references the prior ACP surface

- [ ] Add failing frozen-payload and golden tests for a `session.deleted` transcript fact with `{}` payload; prove deleted streams still export with `Open = false`, `Running = false`, correct fact count, and a complete trailer.
- [ ] Add `session.deleted` to the strict known-fact catalog and `ProjectRecord` switch without making unknown canonical events skippable.
- [ ] Update English and Chinese implemented contracts only with behavior proven by tests: capability JSON, non-enumerating delete, duplex detach semantics, workspace canonicalization, v4 migration, head states, keyset cursor, recovery spelling, and transcript deletion fact.
- [ ] Record task commit hashes and exact verification output in the existing evidence ledgers; do not claim unexecuted platforms or race runs.
- [ ] Run:

```bash
go test ./internal/harness/transcript ./internal/harness/composition -count=1
git diff --check
```

- [ ] Commit: `docs: publish ACP lifecycle slice B contracts`.

## Final Completion Gate

- [ ] Run `gofmt -w` on changed Go files and verify `gofmt -l` prints nothing for them.
- [ ] Run `go vet ./...`.
- [ ] Run `go test ./... -count=1`.
- [ ] Run `go test -race ./... -count=1`.
- [ ] Run `go test -race ./internal/harness/adapters/sqlite -count=5` to exercise migration/catalog concurrency repeatedly.
- [ ] Run `GOOS=windows GOARCH=amd64 go test ./...`.
- [ ] Run `GOOS=darwin GOARCH=arm64 go test ./...`.
- [ ] Run `git diff --check` and inspect `git status --short` so only Slice B artifacts are included.
- [ ] Confirm ACP close emits no `session.closed`, delete emits exactly one `session.deleted`, deleted streams remain exportable, and list never exposes deleted heads.
- [ ] Confirm migration rollback leaves a version-3 database unchanged for malformed, mismatched, and orphan-head fixtures.
- [ ] Confirm `sqlite.Reader` remains read-only and no adapter imports another adapter.
- [ ] Request code review, address findings with focused regression tests, then create a final implementation/evidence commit if review changes are needed.
