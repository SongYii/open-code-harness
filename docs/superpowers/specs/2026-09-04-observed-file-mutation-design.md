# Observed-State Safe File Mutation Design

**Status:** Accepted on 2026-09-05

**Date:** 2026-09-04

**Stability:** All new Go types, ports, errors, and tools remain `internal` before v1.0.

**Research basis:** [Agent instructions and safe file mutation architecture gate](../../research/architecture-gates/2026-09-04-agent-instructions-and-file-mutation.md)

English is normative. The
[Chinese file](2026-09-04-observed-file-mutation-design.zh-CN.md) is a
synchronized reading copy.

## Problem

`read_file` does not return an internal revision, `write_file` unconditionally
truncates its destination, and no targeted edit tool exists. The runtime can
therefore overwrite a change made after its last read, and a failed write can
leave a partial file. Approval decides whether a mutation is allowed; it does
not prove that the mutation still applies to the version the agent inspected.

## Goals

- Prevent lost updates among structured filesystem tools in one active session.
- Detect ordinary external changes between observation and publication and
  return an actionable retry error.
- Keep versions and guards out of model-controlled JSON arguments.
- Add a bounded literal `edit_file` with unique-match-by-default semantics.
- Publish whole-file replacements atomically and preserve an existing regular
  file's permissions.
- Preserve the current workspace jail, Policy/Approver routing, replayable tool
  events, cancellation, and resource bounds.

## Non-goals

- A transactional filesystem or a kernel-atomic CAS against every external process.
- Mediating writes performed by `exec` or by software outside this process.
- Persisting observations across process restart or session resume.
- Fuzzy matching, a patch language, regex replacement, binary editing,
  recursive directory mutation, or Windows-specific publication work.
- Replacing the existing write approval with freshness checks; both apply.

## Chosen architecture

The feature has three explicit owners:

1. `tools.FileSystem` owns opaque observation and guarded-mutation value types.
2. Application owns a per-active-session observation table and converts model
   calls into guards. The model never supplies a version.
3. `workspacefs` owns target identity, version calculation, per-target mutation
   serialization, literal edit, and staged atomic publication.

No general plugin/event framework is added. The observation policy is a small
Application collaborator because Application already owns Session identity,
tool ordering, and the Policy/Approver pipeline.

## Port contract

The exact names may be refined in the implementation plan, but the semantic
shape is fixed:

```go
type FileVersion string // opaque outside the adapter

type FileObservation struct {
    Data      []byte
    Truncated bool
    Version   FileVersion
}

type MutationGuard struct {
    Kind    GuardKind // create_if_absent or replace_if_version
    Version FileVersion
}

type MutationResult struct {
    Version   FileVersion
    Operation MutationOperation // create or update
}

Read(ctx, abs, limit) (FileObservation, error)
Write(ctx, abs, data, guard) (MutationResult, error)
Edit(ctx, abs, old, replacement []byte, replaceAll bool, guard MutationGuard) (MutationResult, error)
```

`FileVersion` is comparable but otherwise opaque. The local adapter derives it
from canonical target identity and high-resolution metadata, including device,
inode, size, modification time, and change time where the platform exposes
them. Tests must not parse it. A storage platform unable to supply a trustworthy
token makes guarded replacement unavailable rather than silently unconditional.

## Observation policy

The table key is `(SessionID, canonical target identity)`. Its state is one of
`unseen`, `absent`, or `present(version)`.

| Operation | Prior observation | Guard / result |
| --- | --- | --- |
| read existing | any | record returned `present(version)` |
| read missing | any | record `absent`, return normal not-found result |
| write | unseen or absent | `create_if_absent`; an existing target fails without overwrite |
| write | present(v) | `replace_if_version(v)` |
| edit | unseen | `FS_NOT_OBSERVED` |
| edit | absent | `FS_NOT_FOUND` |
| edit | present(v) | `replace_if_version(v)` |

This permits creating a new path without a ceremonial failed read while still
protecting a concurrent creator. It requires reading an existing file before
overwriting it. Any failed mutation leaves the prior observation unchanged; a
stale error must be repaired by `read_file`, not by blind retry. Observation
state is cleared when the active session runtime is discarded, so a resumed
session starts `unseen` and fails closed.

A truncated/windowed read still observes the whole target version. Freshness
means “the same file revision,” not “the model saw every byte.” Whole-file
overwrite after a partial read is allowed but remains subject to approval; the
tool guidance tells the model to prefer edit for focused changes.

## `edit_file`

The model schema is:

```json
{"path":"path","old_string":"non-empty literal","new_string":"replacement","replace_all":false}
```

The existing path and content bounds apply; an explicit edit-file byte bound is
configured and enforced before holding both old and new copies in memory.
`old_string` must be non-empty and differ from `new_string`. Matching is
literal. One match is required by default; zero yields `FS_EDIT_NOT_FOUND`, and
multiple matches yield `FS_AMBIGUOUS_EDIT`. `replace_all=true` replaces every
non-overlapping match.

The adapter checks the version guard before matching, so a stale edit reports
`FS_STALE_VERSION`, never a misleading match error against newer content. UTF-8
text only is accepted. The dominant LF/CRLF style and an existing regular
file's mode are preserved.

## Atomic mutation

Within one `workspacefs` instance, write and edit acquire a lock keyed by the
canonical target. Under that lock the adapter:

1. re-jails and re-identifies the target;
2. validates `create_if_absent` or `replace_if_version`;
3. for edit, reads and validates the current bounded UTF-8 text and computes the replacement;
4. creates an exclusive, owner-only temporary file in a private staging directory beside the destination;
5. writes all bytes, syncs the file, applies the intended mode, and closes it;
6. publishes without replacement for create, or atomically renames over the validated regular target for replace;
7. best-effort syncs the parent directory, removes staging residue, and returns the new version.

No destination is truncated in place. Pre-publication failure preserves the
old target. Successful publication is the commit point even if later cleanup
fails.

The replace sequence cannot be claimed as a universal external-writer CAS:
portable filesystems do not expose “rename only if destination still has this
metadata version.” The adapter detects changes at its guarded check, and all
in-process structured writers share the target lock, but an uncooperative
external writer can race in the final check-to-rename window. The implemented
contract and documentation must retain this limitation.

## Errors and model recovery

New stable codes are `FS_NOT_OBSERVED`, `FS_STALE_VERSION`,
`FS_EDIT_NOT_FOUND`, `FS_AMBIGUOUS_EDIT`, `FS_NOT_REGULAR_FILE`, `FS_NOT_TEXT`,
and `FS_TOO_LARGE`. Human/model-facing text includes the remedy but never
exposes the opaque version. Policy denial, approval denial, scope denial,
cancellation, and adapter errors remain distinct classifications.

## Request caching and persistence impact

The new `edit_file` schema has a fixed prompt-token and KV-cache cost while the
tool catalog is unchanged. Observation state adds no prompt tokens. Read and
mutation results continue to append after the reusable request prefix. The
canonical event log records calls and results as today; opaque versions are
execution-control state and must not become model-authored fields.

## Verification and acceptance

Acceptance requires tests for:

- port conformance for versioned read, guarded create/replace, and literal edit;
- read A, external replace B, attempted mutation rejection, re-read, then safe retry;
- two concurrent structured writers from one observation: exactly one commits;
- unseen create racing another creator never overwrites the winner;
- unique, missing, ambiguous, and `replace_all` edit cases;
- invalid UTF-8, directory/symlink jail, size bounds, cancellation, mode and line-ending preservation;
- injected pre-publication failures leave the destination byte-identical;
- Policy and Approver still run before a mutating effect;
- restart/resume drops observations and requires a re-read;
- race tests for the observation table and target-lock registry;
- explicit evidence that `exec` writes are outside this guarantee.

Completion also requires synchronized implemented-contract docs and an evidence
ledger. A live model or API key is not needed for this module.
