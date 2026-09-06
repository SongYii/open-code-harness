# Implemented Observed File Mutation Contract

- Status: implemented internal contract; pre-v0, not GA
- Normative design: [Observed-state safe file mutation](../superpowers/specs/2026-09-04-observed-file-mutation-design.md)
- Implementation plan: [six-task plan](../superpowers/plans/2026-09-05-observed-file-mutation.md)
- Completion evidence: [evidence ledger](observed-file-mutation-evidence.md)
- Chinese reading copy: [已实现观察式文件修改合同](observed-file-mutation.zh-CN.md)

This is the English implemented authority. The Chinese document is a synchronized
reading copy. All types, ports, codes, and schemas below are internal before v1.

## Port and values

`tools.FileSystem` is the consumer-owned workspace I/O port. Its exact methods
are:

```go
Resolve(ctx context.Context, workspace, requested string) (abs string, err error)
Read(ctx context.Context, abs string, limit int) (FileRead, error)
Write(ctx context.Context, abs string, data []byte, guard MutationGuard) (MutationResult, error)
Edit(ctx context.Context, abs string, old, replacement []byte, replaceAll bool, guard MutationGuard) (MutationResult, error)
List(ctx context.Context, abs string, depth, limit int) (names []string, truncated bool, err error)
```

`FileVersion string` is opaque and comparable; callers never parse it.
`FileRead{Data, Truncated, Version}` reports the whole target's version even
when `Data` is truncated. `MutationGuard` is either
`{Kind: GuardCreateIfAbsent, Version: ""}` or
`{Kind: GuardReplaceIfVersion, Version: non-empty}`. `MutationResult` returns
the next opaque version and `MutationCreate` or `MutationUpdate`. Any other
guard shape is invalid arguments. `MaxEditFileBytes` is exactly `1 << 20`
(1 MiB).

`workspacefs` derives Linux/Darwin versions from target identity and
high-resolution metadata; its other-platform implementation exists only to
compile. It re-jails on execution and uses per-canonical-target locks within
one adapter instance.

## Five closed builtin schemas

The default order is unchanged for the original four, followed by `edit_file`:

| Tool | Required / optional fields | Risk and result |
| --- | --- | --- |
| `read_file` | `path` string, 1–4096 bytes | read; UTF-8 content, capped at 64 KiB plus the existing truncation marker |
| `write_file` | `path` 1–4096; `content` string ≤32768 bytes | write; `wrote <n> bytes`; existing targets need an observation and a matching version |
| `list_dir` | `path` 1–4096; optional integer `depth` 1–2 (default 1) | read; up to 256 entries |
| `exec` | `argv` array 1–64 of strings ≤4096 bytes; optional `cwd` 1–4096 | exec; separate CommandRunner contract |
| `edit_file` | `path` 1–4096, non-empty `old_string` ≤32768, `new_string` ≤32768; optional JSON boolean `replace_all` default false | write; `edited file` or `replaced all occurrences` |

Every schema is an object with `additionalProperties:false`; model arguments
cannot supply a version or mutation guard. Boolean schemas are accepted only
as leaves, so a root boolean schema remains invalid. `old_string` must differ
from `new_string`. Matching is literal and non-overlapping: zero matches,
or more than one match without `replace_all`, fail. The adapter owns the 1 MiB
whole-file edit bound, UTF-8 validation, dominant LF/CRLF preservation, and
existing regular-file mode preservation.

## Observation and authorization

Application owns a synchronized, process-local table keyed by Session ID and
resolved canonical target. A state is `unseen`, `absent`, or `present(version)`:

| Event or call | State/result |
| --- | --- |
| successful `read_file` | record `present(returned version)` |
| missing `read_file` | record `absent`; return ordinary not-found behavior |
| `write_file` after unseen or absent | guarded `create_if_absent`; `fs.ErrExist` maps to `fs_not_observed` |
| `write_file` after present(v) | guarded `replace_if_version(v)` |
| `edit_file` after unseen | `fs_not_observed` |
| `edit_file` after absent | `fs_not_found` |
| `edit_file` after present(v) | guarded `replace_if_version(v)` |
| successful write or edit | replace state with returned version |
| failed mutation | retain the prior state |

`LoadSession` and ordinary later turns retain this runtime state. A successful
`ResumeSession`, `CloseSession`, or `DeleteSession` clears that Session's state;
failed or unresolved lifecycle operations do not. A newly constructed Service
also starts unseen, even over the same durable Session: observations are never
persisted or reconstructed.

Freshness is not authorization. The fixed execution order is schema validation
→ lexical scope (no I/O) → `Resolve` probe → `policy.Engine.Decide` → required
`Approver` grant → observation guard and filesystem mutation. Policy or
approval denial therefore produces no edit/write effect; `ModeAllowWrites`
can authorize a write but cannot remove its guard.

## Stable recovery and privacy

The complete new code/message mapping is fixed and lower-case on the wire:

| Code | Exact model-visible message |
| --- | --- |
| `fs_not_observed` | `read the file before changing it` |
| `fs_not_found` | `file does not exist; create it or re-read after it appears` |
| `fs_stale_version` | `file changed since it was read; re-read it and retry` |
| `fs_edit_not_found` | `literal was not found` |
| `fs_ambiguous_edit` | `literal appears more than once; include more context or use replace_all` |
| `fs_not_regular_file` | `target is not a regular file` |
| `fs_not_text` | `file is not valid UTF-8 text` |
| `fs_too_large` | `file exceeds the edit size limit` |

The mapping emits only these bounded literals and codes. Paths, contents,
opaque versions, and adapter causes are not rendered into tool results,
records, runtime events, model messages, or approval requests.

Directories and special targets are both `fs_not_regular_file`; the adapter
checks target mode before opening a non-regular target, avoiding FIFO reads.

## Publication and scope boundary

For a guarded mutation, `workspacefs` re-jails/re-identifies and validates the
guard under the target lock; an edit then reads bounded UTF-8, checks the guard
before matching, and calculates replacement bytes. It creates an exclusive
0600 file inside a private 0700 sibling staging directory, writes all bytes,
syncs, applies the intended mode, closes, re-jails/revalidates, and publishes
with link-without-replacement for create or atomic rename for replace. It then
best-effort syncs the parent and removes staging residue. No destination is
truncated in place; pre-publication failure preserves it. Publication is the
commit point even if post-publication cleanup/metadata reporting fails.

This is deliberately not a filesystem transaction. It serializes structured
writes sharing one `workspacefs` instance and detects changes at guarded
checks, but provides no kernel/external-process CAS and no guarantee against
an external final check-to-rename race. `exec` is not mediated by the guard;
an exec-side change is detected by a later structured read/edit only when the
version differs. Observations are not durable. Windows runtime behavior is
not claimed: Windows/Darwin are compile-only on this Linux evidence host.
The workspace jail is retained, not upgraded into a hostile-parent-path
security claim.

No live model, provider API call, or API key is needed or claimed for this
slice; all evidence uses local, scripted, or filesystem fixtures.
