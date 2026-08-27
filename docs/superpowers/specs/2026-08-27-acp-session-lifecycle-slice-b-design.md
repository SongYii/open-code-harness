# ACP Session Lifecycle (Slice B)

- **Date:** 2026-08-27
- **Status:** Draft (approved for specification; pending human review of this file)
- **Stability:** `experimental` ACP v1 session-management surface
- **Repository:** `open-code-harness` (`github.com/SongYii/open-code-harness`)
- **Normative language:** English
- **Reading copy:** [ACP 会话生命周期（切片 B）](2026-08-27-acp-session-lifecycle-slice-b-design.zh-CN.md)
- **Prior slice:** [Conversation Surface and Session Transcript (Slices A / A′)](2026-08-23-conversation-and-session-transcript-design.md)
- **Protocol reference:** [ACP v1 schema](https://raw.githubusercontent.com/agentclientprotocol/agent-client-protocol/main/schema/v1/schema.json), read 2026-08-25

English is normative. The Chinese file is a synchronized reading copy.

---

## 1. Decision summary

Slice B adds capability-gated ACP v1 `session/list`, `session/resume`,
`session/close`, and `session/delete` to the existing persistent Session
model. `close` is included even though the original follow-on named only
list/resume/delete: delete is intentionally legal only after close, so an ACP
client needs a supported route into that state.

Deletion is a durable, irreversible **logical deletion**. It appends the new
canonical domain fact `session.deleted`; it never removes rows from `events`,
the audit chain, or session transcript exports. A deleted Session is absent
from ACP list results and unavailable to normal load, resume, or prompt
operations. This preserves EventStore and audit evidence while giving users a
real session-history cleanup operation.

The existing synchronous `session_heads` projection becomes the indexed
catalog. A format migration gives every head its workspace root, validates and
backfills it by replaying canonical streams, and keeps it current in the same
transaction as every append. ACP never reads SQLite directly.

## 2. Goals and exclusions

### Goals

1. Advertise and implement ACP v1 list, resume, close, and delete capabilities.
2. Give `session/list` deterministic keyset pagination over sessions belonging
   only to the assembly workspace.
3. Make deletion durable, replayable, auditable, and unavailable to ordinary
   session use without physical erasure.
4. Preserve the existing fencing, append identity, CAS, unknown-outcome, and
   pinned-read rules.
5. Make close/prompt/delete races explicit and testable.

### Exclusions

- ACP v2, protocol-version negotiation, auth, `session/set_mode`,
  `session/set_config_option`, session fork, batch deletion, undelete, and
  physical retention/garbage collection.
- `additionalDirectories`, session-scoped MCP configuration, and MCP client
  construction. Slice B advertises none of those capabilities.
- A new `och` session-management subcommand. This slice is an ACP surface.
- Titles generated from user prompts, search, tags, status metadata, or any
  data beyond ACP's required SessionInfo fields.
- A multi-page historical snapshot guarantee for lists. Each page is one read
  transaction; concurrent writes can move a session between pages.
- Changing Slice A's conversation projector, the audit JSONL codec, or the
  token-aware-context/compaction boundary.

## 3. State and durable facts

### 3.1 Domain lifecycle

Add `SessionStatusDeleted = "deleted"`, `CommandDeleteSession =
"session.delete"`, `DeleteSession{SessionID}`, `EventSessionDeleted =
"session.deleted"`, and `SessionDeleted{}`.

`domain.Decide` accepts `DeleteSession` only when all of these are true:

- the aggregate exists and its ID matches the command target;
- its status is `closed`;
- it has no active Turn or Item.

The decision emits exactly `[SessionDeleted{}]`. `domain.Apply` accepts that
event only from the same closed, idle state and moves the aggregate to
`deleted`. No command other than the delete transition accepts a deleted
Session. A second delete is `session_deleted`; an active Session remains
`session_not_active` or the established close eligibility error. Domain codec,
clone, compact replay, historical oracle, and audit serialization must all
recognize the new event.

`session.closed` remains a distinct, reversible-in-storage lifecycle fact: it
ends a normal Session but does not hide it or erase its history. `session.deleted`
is terminal and cannot be undone in this format.

### 3.2 Public Application behavior

`application.Service` gains these use cases:

```go
type ListSessionsRequest struct {
    WorkspaceRoot string
    Cursor        string
}
type ListedSession struct {
    SessionID     domain.SessionID
    WorkspaceRoot string
    UpdatedAt     time.Time
}
type ListSessionsResult struct {
    Sessions   []ListedSession
    NextCursor string
}
type ResumeSessionRequest struct {
    SessionID     domain.SessionID
    WorkspaceRoot string
}
type DeleteSessionRequest struct { SessionID domain.SessionID }

func (s *Service) ListSessions(context.Context, ListSessionsRequest) (ListSessionsResult, error)
func (s *Service) ResumeSession(context.Context, ResumeSessionRequest) (domain.Session, error)
func (s *Service) DeleteSession(context.Context, DeleteSessionRequest) error
```

`LoadSession` becomes the ordinary-use load boundary: it returns
`session_not_found` for a deleted aggregate. A private lifecycle loader may
replay deleted state only for `DeleteSession` so the second deletion gets the
deterministic domain error. Transcript and audit export continue to read the
authoritative stream directly and therefore remain able to export deletion
evidence.

`ResumeSession` performs no mutation and no replay notification. It validates
the exact workspace and requires an active, idle Session. `DeleteSession`
loads its state, decides one `SessionDeleted`, allocates normal append metadata,
and calls the existing `appendCompact`/resolution path. It does not bypass
writer authority or invent a special SQL mutation.

The EventStore port gains a catalog method rather than leaking SQL to ACP:

```go
type ListSessionHeadsRequest struct {
    WorkspaceRoot string
    Cursor        string
    Limit         uint32
}
type SessionHeadPage struct {
    Sessions []SessionHead
    NextCursor string
}
type SessionHead struct {
    SessionID     domain.SessionID
    WorkspaceRoot string
    Status        domain.SessionStatus
    UpdatedAt     time.Time
}

ListSessionHeads(context.Context, ListSessionHeadsRequest) (SessionHeadPage, error)
```

All EventStore implementations and conformance fixtures must implement this
method. The service supplies `WorkspaceRoot`, fixed `Limit = 50`, filters out
deleted heads before exposing them, and treats an invalid cursor as a validation
error. The SQLite adapter is the production implementation.

## 4. SQLite catalog projection and migration

`session_heads` already updates synchronously in `Store.Append`; it is derived
state, not a second authority. Migration 4 changes it to contain:

```text
session_id TEXT PRIMARY KEY
workspace_root TEXT NOT NULL
status TEXT NOT NULL                 -- idle | running | closed | deleted
active_turn_id TEXT NULL
active_item_id TEXT NULL
updated_at_commit_position INTEGER NOT NULL
```

The migration runs on the writer connection inside the existing migration
transaction. It adds `workspace_root`, then scans every `event_streams` row in
stable `session_id` order, decodes canonical events, and compact-replays each
stream. The derived workspace, status, active IDs, and last commit position
must equal an existing head row when one exists; disagreement is
`sqlite database corrupt`, not silent repair. It inserts a missing head and
fills `workspace_root` for verified rows. A new index supports the list query:
`(workspace_root, status, updated_at_commit_position DESC, session_id DESC)`.

`updateSessionHead` must derive `workspace_root` from `session.created`, carry
it across later events, and transition to `deleted` on `session.deleted` in the
same `BEGIN IMMEDIATE` transaction that inserts the canonical append. It must
never manufacture the root from an ACP request.

`ListSessionHeads` opens one normal read transaction, joins each selected head
to `event_appends` on `updated_at_commit_position` for `committed_at_unix`, and
returns non-deleted heads in descending `(commit_position, session_id)` order.
The cursor is base64url JSON:

```json
{"v":1,"p":123,"s":"session-id"}
```

`p` is the last returned commit position and `s` its session ID. It is bounded
to 512 bytes, strictly decoded, and used only as bound SQL parameters. An empty
cursor starts at the newest row. A next cursor is returned only when an
additional row exists. Each page has one SQLite snapshot; the API deliberately
does not pin a snapshot across requests.

`sqlite.Reader` remains `ReadStream`-only. It receives the format-version gate
but no session-management mutation or catalog API.

## 5. ACP v1 contract

The initialize result keeps `loadSession: true` and advertises:

```json
"sessionCapabilities": {"list": {}, "resume": {}, "close": {}, "delete": {}}
```

This matches the current ACP v1 capability model. The adapter does not advertise
additional directories, MCP, modes, or configuration options.

| Method | Request accepted | Success | Rejection behavior |
| --- | --- | --- | --- |
| `session/list` | optional `cwd`, optional opaque `cursor` | `{sessions:[{sessionId,cwd,updatedAt}],nextCursor?}` | non-empty foreign `cwd` or bad cursor → `-32602 invalid params` |
| `session/resume` | required `sessionId`, required `cwd`; empty MCP/additional-directory lists tolerated only when supplied | `{}` and no history updates | absent, foreign, closed, running, or deleted → `-32602 invalid params` |
| `session/close` | required `sessionId` | `{}` after prompt settlement and durable close | absent, foreign, closed, or deleted → `-32602 invalid params` |
| `session/delete` | required `sessionId` | `{}` after durable `session.deleted` append | absent, foreign, active, running, or deleted → `-32602 invalid params` |

All persistence/internal failures in these methods use `-32603 session operation
failed`. These fixed strings deliberately do not reveal session existence,
workspace roots, lifecycle state, or storage details. `session/list` always
uses the assembly workspace even when `cwd` is absent; it never emits deleted
entries. It returns no title, additional directories, or `_meta` fields.

`session/load` retains its existing compatibility request shape. If a caller
supplies a `cwd`, it must match the assembly workspace; any non-empty
`mcpServers` or `additionalDirectories` is rejected. It now rejects a deleted
Session before replay. `session/prompt` performs the same deleted/workspace
admission before invoking `RunTurn`.

### Close concurrency

The ACP server's per-wire-session entry becomes a small state machine with a
completion channel:

```text
idle ── prompt ──> running ── terminal ──> idle
idle ── close ──> closing ── close append ──> absent
running ── close ──> closing ── cancel + terminal + close append ──> absent
```

The close request records `closing` while holding the server mutex, cancels a
running prompt, waits for its goroutine to publish its terminal result, then
calls `application.CloseSession`. A new prompt, resume, close, or delete while
the entry is closing is rejected without starting work. The close request uses
its own request context and never holds the mutex while waiting or appending.
Delete never cancels work; it is legal only after the close has committed.

## 6. Projection, export, and documentation effects

`internal/harness/transcript.ProjectRecord` adds a `session.deleted` fact with
an empty object payload. Its catalog, goldens, strict codec tests, and English
and Chinese schema docs add the same fact. ACP conversation replay does not
project deletion because it is rejected before replay.

Implemented-contract documentation updates are additive:

- `docs/architecture/acp-v1.md` and `.zh-CN.md`: capabilities, wire tables,
  close ordering, errors, workspace isolation, and exclusions.
- `docs/architecture/session-transcript.md` and `.zh-CN.md`: deleted fact and
  exportability after logical deletion.
- `docs/architecture/sqlite-eventstore.md` and `.zh-CN.md`: migration 4 and
  synchronous session catalog projection.
- `docs/architecture/conversation-and-transcript-evidence.md`: Slice B PRs,
  migration/backfill checks, and ACP lifecycle tests.

## 7. Verification and acceptance

The implementation must add focused tests before each behavior:

1. Domain decision/apply/codec/clone/replay tests prove only closed idle
   Sessions delete, deletion is terminal, and historical events round-trip.
2. Application tests prove deleted ordinary loads/prompts fail, export stream
   access survives, and close/delete races have one CAS winner.
3. SQLite migration tests cover empty and populated version-3 databases,
   exact backfill, malformed/mismatched head failure, list ordering, cursor
   validation, workspace filtering, and deleted omission. Store conformance
   adds the catalog method to memory and SQLite factories.
4. ACP tests assert the exact initialize capabilities and JSON for list,
   resume, close, and delete; resume emits no replay; foreign/deleted state
   leaks no update; close cancels then settles before close; and deletion
   cannot race a prompt into a usable Session.
5. Transcript tests add a deleted golden line and a deleted-session export.

Before completion, run:

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./internal/harness/domain/ ./internal/harness/application/ \
  ./internal/harness/adapters/acp/ ./internal/harness/adapters/sqlite/ \
  ./internal/harness/transcript/ ./internal/harness/composition/
GOOS=windows GOARCH=amd64 go test ./...
GOOS=darwin GOARCH=arm64 go test ./...
```

## 8. Risks and mitigations

| Risk | Mitigation |
| --- | --- |
| A delete erases audit evidence | `session.deleted` is append-only; no data rows are deleted. |
| A foreign workspace can enumerate histories | list is catalog-filtered by assembly root; all selection failures use the same invalid-params response. |
| A close races a running prompt | ACP enters `closing`, cancels, waits for settlement, then invokes normal `CloseSession` CAS. |
| A derived catalog becomes silently stale | migration replays and verifies heads; every append updates the same transaction. |
| List pagination causes unbounded replay | fixed 50-row keyset query over an indexed projection. |
| Concurrent writes make a page cursor appear historical | contract promises a per-page snapshot only; it does not promise a multi-page snapshot. |
| New event breaks transcript export | transcript catalog and golden coverage add `session.deleted` in the same slice. |

