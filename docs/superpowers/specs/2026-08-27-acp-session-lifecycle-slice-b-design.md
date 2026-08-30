# ACP Session Lifecycle (Slice B)

- **Date:** 2026-08-27
- **Status:** Draft (approved for specification; pending human review of this file)
- **Stability:** `experimental` ACP v1 session-management surface
- **Repository:** `open-code-harness` (`github.com/SongYii/open-code-harness`)
- **Normative language:** English
- **Chinese summary:** [ACP 会话生命周期（切片 B）](2026-08-27-acp-session-lifecycle-slice-b-design.zh-CN.md)
- **Prior slice:** [Conversation Surface and Session Transcript (Slices A / A′)](2026-08-23-conversation-and-session-transcript-design.md)
- **Protocol reference:** [ACP v1 schema](https://raw.githubusercontent.com/agentclientprotocol/agent-client-protocol/main/schema/v1/schema.json), read 2026-08-27

English is normative. The Chinese file is a synchronized summary rather than
a field-for-field translation of the API declarations.

---

## 1. Decision summary

Slice B adds capability-gated ACP v1 `session/list`, `session/resume`,
`session/close`, and `session/delete` to the existing persistent Session
model. ACP close and the existing durable `session.closed` fact are deliberately
different operations: ACP close cancels work and releases resources attached to
the current duplex, while preserving a resumable persistent Session. The
existing Application close remains the durable end-of-conversation command.

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
3. Make deletion durable, replayable, auditable, idempotent at the ACP boundary,
   and unavailable to ordinary session use without physical erasure.
4. Preserve the existing fencing, append identity, CAS, unknown-outcome, and
   pinned-read rules.
5. Make close/prompt/delete races and wire-session attachment explicit and
   testable.

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
- its status is `active` or `closed`;
- it has no active Turn or Item.

The decision emits exactly `[SessionDeleted{}]`. `domain.Apply` accepts that
event only from the same idle active-or-closed state and moves the aggregate to
`deleted`. No command other than the delete transition accepts a deleted
Session. A second domain delete is `session_deleted`; ACP maps that result, and
an absent Session, to idempotent success. A running Session remains ineligible
for deletion. Domain codec, clone, compact replay, historical oracle, and audit
serialization must all recognize the new event.

`session.closed` remains a distinct durable lifecycle fact produced by the
existing Application close command: it ends a normal conversation but does not
hide it or erase its history. ACP `session/close` does not emit that fact.
`session.deleted` is terminal and cannot be undone in this format.

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
type DeleteSessionRequest struct {
    SessionID     domain.SessionID
    WorkspaceRoot string
}

func (s *Service) ListSessions(context.Context, ListSessionsRequest) (ListSessionsResult, error)
func (s *Service) ResumeSession(context.Context, ResumeSessionRequest) (domain.Session, error)
func (s *Service) DeleteSession(context.Context, DeleteSessionRequest) error
```

`LoadSession` becomes the ordinary-use load boundary: it returns
`session_not_found` for a deleted aggregate. A private lifecycle loader may
replay deleted state only for `DeleteSession` so the second deletion gets the
deterministic domain error internally. `DeleteSession` treats an absent,
foreign-workspace, or already-deleted aggregate as `session_not_found` at its
public boundary so adapters can implement non-enumerating idempotence.
Transcript and audit export continue to read the authoritative stream directly
and therefore remain able to export deletion evidence.

`ResumeSession` performs no mutation and no replay notification. It validates
the canonical workspace and requires an active, idle Session. `DeleteSession`
validates the same workspace, loads lifecycle state, decides one
`SessionDeleted`, allocates normal append metadata, and calls the existing
`appendCompact`/resolution path. It does not bypass writer authority or invent
a special SQL mutation.

Workspace comparison has one lexical representation. A shared helper requires
an absolute path and returns `filepath.Clean` without resolving symlinks.
`CreateSession` persists that canonical value in `session.created`; list,
resume, delete, load, and prompt canonicalize request and assembly roots with
the same helper. Migration derives the catalog root by validating and cleaning
the recorded `session.created` value, never from an ACP request. Thus a legacy
`/repo/.` stream is catalogued as `/repo` and has the same admission behavior
as ordinary load.

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

ListSessionHeads(context.Context, ListSessionHeadsRequest) (SessionHeadPage, error)
```

All EventStore implementations and conformance fixtures must implement this
method. The port contract returns only visible, non-deleted heads; deleted is a
storage projection state, not a value exposed as `SessionHeadStatus`. The
service supplies canonical `WorkspaceRoot`, fixed `Limit = 50`, and treats an
invalid cursor as a validation error. The SQLite adapter is the production
implementation.

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
transaction. Because a populated SQLite table cannot gain a no-default
`NOT NULL` column in place, migration 4 creates a shadow `session_heads_v4`
table with the final constraints. It scans every `event_streams` row in stable
`session_id` order, decodes canonical events, compact-replays the stream, and
inserts the derived row into the shadow table.

Projection mapping is explicit: active with no Turn is `idle`, active with a
Turn is `running`, durable domain closed is `closed`, and deleted is `deleted`.
When verifying a version-3 head, legacy storage `active` is equivalent only to
derived `running`; `idle` and `closed` compare exactly. Active IDs and
`event_streams.last_append_commit_position` must also agree. Any other
disagreement or orphan head is `sqlite database corrupt`, while a missing
derived head is rebuilt. After every stream is verified, the migration drops
the old table, renames the shadow table, and creates this partial list index:

```sql
CREATE INDEX session_heads_visible_by_workspace
ON session_heads (
    workspace_root,
    updated_at_commit_position DESC,
    session_id DESC
)
WHERE status <> 'deleted';
```

`updateSessionHead` must derive canonical `workspace_root` from
`session.created`, carry it across later events, and transition to `deleted` on
`session.deleted` in the same `BEGIN IMMEDIATE` transaction that inserts the
canonical append. The audit-import head builder must use the same transition
and root derivation. `RebuildAndVerifySessionHeads` verifies root, status,
active IDs, and commit position, and runtime recovery enumerates `running`
heads instead of the legacy `active` spelling. Focused audit-import, rebuild,
and recovery tests cover these consumers. No projection writer may manufacture
the root from an ACP request.

`ListSessionHeads` opens one normal read transaction, filters deleted rows in
SQL before limiting, joins each selected head to `event_appends` on
`updated_at_commit_position` for `committed_at_unix`, and requests `Limit + 1`
rows in descending `(commit_position, session_id)` order. It converts the
committed time to UTC and the ACP adapter encodes `updatedAt` as RFC 3339 Nano.
The cursor is base64url JSON:

```json
{"v":1,"p":123,"s":"session-id"}
```

`p` is the last returned visible commit position and `s` its session ID. It is
bounded to 512 bytes, strictly decoded, and used only as bound SQL parameters.
An empty cursor starts at the newest row. The extra row determines whether a
next cursor exists and is not returned; the cursor is made from the last
visible row actually returned. Each page has one SQLite snapshot; the API
deliberately does not pin a snapshot across requests.

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
| `session/close` | required `sessionId` | `{}` after prompt settlement and release of duplex-owned resources; no domain append | unattached, foreign, domain-closed, or deleted → `-32602 invalid params` |
| `session/delete` | required `sessionId` | `{}` after durable `session.deleted` append, or idempotent no-op | same-workspace running/closing/deleting → `-32602 invalid params`; absent, foreign, or deleted → `{}` without mutation |

All persistence/internal failures in these methods use `-32603 session operation
failed`. Validation failures use fixed strings that do not include session IDs,
workspace roots, lifecycle state, or storage details. Delete returns the same
successful empty result for absent, foreign, and already-deleted sessions, so
it does not become an existence oracle. `session/list` always uses the assembly
workspace even when `cwd` is absent; it never emits deleted entries. It returns
no title, additional directories, or `_meta` fields.

`session/load` retains its existing compatibility request shape. If a caller
supplies a `cwd`, it must match the canonical assembly workspace; any non-empty
`mcpServers` or `additionalDirectories` is rejected. It now rejects a deleted
Session before replay. `session/prompt` performs the same deleted/workspace
admission before invoking `RunTurn` and requires an attached, idle wire entry.
`session/new`, successful active `session/load`, and `session/resume` attach the
entry. A load of a durable domain-closed Session may replay its history but
does not make it promptable.

### Close and delete concurrency

The ACP server's per-wire-session entry becomes a small state machine with a
prompt-completion channel:

```text
new / load / resume ───────────────────────────────────────> idle
idle ── prompt ──> running ── terminal response ──────────> idle
idle ── close ─> closing ───────────── release resources ─> detached
running ── close ─> closing ─> cancel + terminal response ─> detached
detached ── load / resume ─────────────────────────────────> idle
idle / detached / absent ─> deleting ─> append or no-op ──> absent
```

Close records `closing` while holding the server mutex, cancels a running
prompt, waits for its goroutine to publish the terminal response, releases
duplex-owned resources, and records `detached`. It never calls
`application.CloseSession` and never appends `session.closed`. Prompt, resume,
load, close, and delete are rejected while the entry is closing. Close uses its
own request context and never holds the mutex while waiting or releasing
resources. A detached Session must be loaded or resumed before another prompt.

Delete never cancels work. Under the mutex it rejects `running`, `closing`, or
`deleting`, then changes any other entry, including no entry, to `deleting`
before loading or
appending. This prevents a prompt from entering between deletion admission and
the CAS append. Prompt, resume, load, close, and another delete reject
`deleting`. On committed deletion or an idempotent absent/foreign/deleted
result, the server removes the entry; on an internal failure it restores the
prior idle/detached/absent state. The Application CAS remains authoritative
against work or deletion initiated outside this duplex.

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

1. Domain decision/apply/codec/clone/replay tests prove only idle active or
   closed Sessions delete, running Sessions do not, deletion is terminal, and
   historical events round-trip.
2. Application tests prove canonical workspace admission, deleted ordinary
   loads/prompts fail, export stream access survives, repeated delete has the
   deterministic internal result, and RunTurn/delete races have one CAS winner.
3. SQLite migration tests cover empty and populated version-3 databases, the
   shadow-table swap, legacy `active` to `running` mapping, exact backfill,
   malformed/orphan/mismatched head failure, canonical legacy roots, audit
   import, rebuild, recovery enumeration, visible ordering, `Limit + 1` cursor
   construction, workspace filtering, and deleted omission. Store conformance
   adds the catalog method to memory and SQLite factories.
4. ACP tests assert the exact initialize capabilities and JSON for list,
   resume, close, and delete; resume emits no replay; absent/foreign/deleted
   delete is an update-free success; close cancels, settles, releases, and emits
   no domain append; detached requires load/resume before prompt; and deleting
   prevents a prompt from entering before the CAS append.
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
| A foreign workspace can enumerate histories | list is catalog-filtered by canonical assembly root; selection failures contain no details, and delete makes absent/foreign/deleted indistinguishable successes. |
| ACP close accidentally ends persistent history | wire close only cancels, settles, and releases duplex resources; it never emits durable `session.closed`. |
| A close races a running prompt | ACP enters `closing`, cancels, waits for the terminal response, then records the entry as detached. |
| A delete races a prompt | ACP installs `deleting` under the mutex before load/append; the EventStore CAS remains the cross-runtime authority. |
| A derived catalog becomes silently stale | migration, append, audit import, rebuild, and recovery share and verify the explicit head mapping. |
| List pagination causes unbounded replay or post-filter gaps | fixed 50-row keyset query filters in SQL and uses the partial visible-head index. |
| Concurrent writes make a page cursor appear historical | contract promises a per-page snapshot only; it does not promise a multi-page snapshot. |
| New event breaks transcript export | transcript catalog and golden coverage add `session.deleted` in the same slice. |
