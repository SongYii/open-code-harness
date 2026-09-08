# Observed-State Safe File Mutation

**Status:** Implemented contract. Describes behaviour that exists in this
repository and is covered by tests. The accepted design is
[Observed-state safe file mutation](../superpowers/specs/2026-09-04-observed-file-mutation-design.md);
the implementation plan is
[here](../superpowers/plans/2026-09-05-observed-file-mutation.md); the evidence
is [observed-file-mutation-evidence.md](observed-file-mutation-evidence.md).

The synchronized Chinese reading copy is
[observed-file-mutation.zh-CN.md](observed-file-mutation.zh-CN.md). If the
copies diverge, this English document wins.

## The problem, stated plainly

An agent that writes a file it never read, or read ten minutes and three tool
calls ago, destroys whatever happened in between. The other writer might be a
human in an editor, a build step, a second agent, or the agent's own earlier
mistake. Nothing in the previous `write_file` could tell the difference between
"replace the file I just read" and "replace whatever is there now", because it
opened the destination with `O_TRUNC` and wrote.

Two things changed. Every destructive filesystem operation now carries a
**guard** — an explicit promise about the state it expects to find — and
publication is **atomic**, so the destination is never left half-written.

"Atomic" is not the same as "a reported failure means nothing happened", and
the difference is stated here rather than buried: verification runs *after*
the rename, so a `Write` can return an error over a destination that has
already changed. See [Failure after publication](#failure-after-publication).

## The port

`internal/harness/tools/ports.go`:

```go
type FileSystem interface {
	Resolve(ctx context.Context, workspace, requested string) (abs string, err error)
	Read(ctx context.Context, abs string, limit int) (read FileRead, err error)
	Write(ctx context.Context, abs string, data []byte, guard MutationGuard) (MutationResult, error)
	Edit(ctx context.Context, abs string, oldString, newString []byte, replaceAll bool, guard MutationGuard) (MutationResult, error)
	List(ctx context.Context, abs string, depth, limit int) (names []string, truncated bool, err error)
}
```

There is deliberately no unguarded write. A caller cannot forget to make a
promise, because the promise is a parameter.

`internal/harness/tools/files.go` holds the vocabulary and nothing else — no
I/O, no filesystem, no clock:

```go
type FileVersion string          // opaque; compared for equality, never parsed
type GuardKind string            // "create_if_absent" | "replace_if_version"
type MutationGuard struct { Kind GuardKind; Version FileVersion }
type FileRead struct { Data []byte; Truncated bool; Version FileVersion }
type MutationResult struct { Version FileVersion; Operation MutationOperation }
const MaxEditFileBytes = 1 << 20
```

`MutationGuard.Validate` accepts exactly four combinations: a create guard with
no version, and a replace guard with one. A create guard carrying a version
asserts something about a file it claims does not exist; a replace guard
without one is an unconditional overwrite wearing a guard's name. Both are
refused before any real file is touched.

`FileRead.Truncated` travels with the version on purpose. A guard derived from
a clipped read would let an agent replace a whole file on the strength of
having seen its first page.

## Versions

`internal/harness/adapters/workspacefs` computes a version as a SHA-256 over a
canonical fixed-width big-endian encoding of the target's identity fields:

| Platform | Fields |
| --- | --- |
| Linux (`version_linux.go`) | device, inode, size, mtime sec+nsec, ctime sec+nsec, mode |
| macOS (`version_darwin.go`) | the same, via `Mtimespec`/`Ctimespec` |
| everything else (`version_other.go`) | size, mode, mtime nsec |

Device and inode are included because publication works by rename, so a file's
identity changing under a reader is the ordinary case here rather than an
exotic one. ctime is included because it moves on metadata-only changes, such
as a `chmod`, that leave size and mtime alone.

`version_other.go` exists so the package cross-compiles — Windows in
particular — and **makes no runtime claim**. Without device and inode it
cannot notice a file being replaced by a different one of identical size,
mode, and timestamp. A platform that wants the real guarantee needs its own
file, not a weaker version silently standing in for one.

The token is hashed rather than concatenated so that it stays opaque: no
consumer can start depending on an inode number it was able to parse out.

## Reading is observing

`Read` opens a jailed regular file, takes the version from the open descriptor
before and after reading `limit+1` bytes, and reports `fs_stale_version` if the
two differ. Bytes paired with a version they were not actually read at would
make every guard built on them a false promise.

It returns at most `limit` bytes, sets `Truncated` when there was more, drops
an incomplete trailing rune left by the clip, and then refuses content that is
not valid UTF-8 with `fs_not_text`. The rune trim comes first so a cut in the
middle of a character is not blamed on the file. Directories and special files
are both refused as `fs_not_regular_file` before opening, so FIFO reads do not
block.

## Mutating

Both `Write` and `Edit` follow the same order, and the order is the contract:

1. context cancellation;
2. `guard.Validate()`;
3. workspace jail (`Resolve`/`jail`, unchanged by this work);
4. take the per-target lock, then **re-jail under the lock**, because the path
   could have become a symlink pointing outside the workspace in between;
5. check the guard against current state;
6. for `Edit` only, read and match;
7. stage, sync, publish.

The per-target lock is not a substitute for the guard. The guard defends
against writers this process does not control; the lock only stops one process
from racing itself between step 5 and step 7, where the guard alone would
leave a window. Lock entries are reference-counted and dropped at zero, so a
long-lived `FileSystem` does not accumulate one mutex per file ever touched.

### Publication

The destination is never opened for truncation. A replacement is written into
a private sibling `.och-stage` directory created `0700`, synced, given the
prior file's mode (or `0600` for a create), and closed. Then:

- a create is published with `os.Link`, which fails if the destination exists;
- a replace is published with `os.Rename`.

After publication, a verifier held on the staged descriptor checks that the
destination still names that staged identity and that its version is stable
while its bytes exactly equal the expected payload. A mismatch returns a zero
`MutationResult` and `fs_stale_version`; it never returns a version that could
describe an external writer's bytes. The payload comparison streams through one
fixed 32 KiB scratch buffer, so verification does not impose the edit size
limit on `Write`.

The parent directory is synced best-effort and the staging directory removed.
Every failure before the link or rename leaves the original exactly as it was.

#### Failure after publication

Verification runs after the rename, so this is a reachable and deliberate
outcome: `Write` returns `fs_stale_version` while the destination has already
been replaced. It happens when another writer touches the target between our
rename and our verification.

Returning an error there is the right answer — the version we would otherwise
return does not describe bytes we can vouch for, and an observation built on it
would license a later blind overwrite. But it means **a reported failure does
not promise the destination is unchanged.** What is promised is narrower and
still useful: the destination is never half-written, and the version this
session records is never one it cannot vouch for.

A caller that needs to know what is actually on disk after such a failure has
exactly one honest move, and it is the one the failure message already asks
for: read the file again. `TestAFailureAfterPublicationIsNotANonEvent` pins
this so it is a recorded fact rather than a surprise.

Staging has to be a sibling rather than a process temp directory because a
link and a rename only work within one filesystem.

### Editing

`Edit` is bounded UTF-8 literal replacement with no pattern language of any
kind. It reads at most `MaxEditFileBytes+1`, refusing anything larger with
`fs_too_large`. Before allocating either transformed output, it checks that
both the CRLF-normalized intermediate and the CRLF-restored final result are
at most 1 MiB; either excess is `fs_too_large`. The bound is a memory bound,
and a partial edit of a source file is worse than a refused one. `Write` has
no edit-size limit.

Matching normalizes CRLF to LF, because the caller is matching against text it
was shown. Publication restores the file's own dominant line ending, because
an edit that never claimed to touch line endings must not quietly rewrite
every line of a CRLF file.

`replace_all` is the only matching option. Without it, more than one match is
refused as `fs_ambiguous_edit` rather than silently editing an arbitrary one.

**The guard is checked before the literal is looked for.** A stale caller whose
literal happens to be absent would otherwise be told "your text is not there"
and sent looking for text, when the real answer is that the file changed
underneath it and needs re-reading.

## Observations

`internal/harness/application/file_observations.go` holds a per-session table
of what each session has read. Three states, and none may be collapsed:

| State | `write_file` guard | `edit_file` guard |
| --- | --- | --- |
| unseen (no entry) | `create_if_absent` — fails closed | refused, `fs_not_observed` |
| observed absent | `create_if_absent` | refused, `fs_not_found` |
| observed present | `replace_if_version` at the observed version | the same |

A write after unseen or observed-absent state uses `create_if_absent`, so an
existing file is refused rather than overwritten. Both states produce the same
guard and both come back as a raw `fs.ErrExist`, but they do not get the same
answer: a session that never looked is told to read, while a session that read
the target and found nothing is told the file changed since it looked. Only
the observation table can tell those apart, so the write path resolves the
conflict rather than the shared classifier. An edit has no such fallback:
unseen is `fs_not_observed`, while a file already observed absent is
`fs_not_found`.

The table is **process-local and never persisted**. A version is a fact about a
file on this machine at this moment; writing one into a Domain event would
dress it up as durable history, and a session resumed on another host would
carry guards describing files it has never seen. The consequence is stated
rather than hidden: a restart, or any second process holding the same durable
Session, begins having seen nothing.

Clearing happens on **successful resume, close, and delete**. It deliberately
does not happen on an ordinary load, or every Turn would begin unable to change
anything it read. Resume clears because the gap may be arbitrarily long and
what the session saw before it is no longer evidence about what is on disk now.

A failed mutation never advances an observation. The obvious well-meaning bug
is to refresh it after a refusal so the retry succeeds, which turns the guard
into a speed bump and lets the second attempt destroy work the agent never
looked at.

## What the model sees

The model never sees, supplies, or is asked about a version. Application
derives every guard after Policy and the Approver have run, immediately before
the adapter call. Freshness is not authorization.

`edit_file` joins the four existing builtins, `RiskWrite` and mutating, so it
inherits the Policy table `write_file` already sits in:

```json
{"type":"object","additionalProperties":false,
 "required":["path","old_string","new_string"],
 "properties":{
   "path":{"type":"string","minLength":1,"maxLength":4096},
   "old_string":{"type":"string","minLength":1,"maxLength":32768},
   "new_string":{"type":"string","maxLength":32768},
   "replace_all":{"type":"boolean"}}}
```

`old_string` and `new_string` share `write_file`'s own 32,768-byte argument
bound rather than inventing one. Two rules a JSON schema cannot express live in
Application: an edit needs a prior read, and `old_string` may not equal
`new_string` — a mutation that cannot change anything would still spend an
approval and a publication.

Supporting `replace_all` required a boolean leaf in the schema compiler. The
leaf rejects every keyword borrowed from another type, since a boolean has no
constraints of its own and a stated bound that does nothing is worse than no
bound. `compileSchema` now also requires the root to be an object: every
downstream guarantee is stated in terms of an object's properties.

A successful edit answers `edited file` or `replaced all occurrences`. It does
not copy the resulting file back, which would spend exactly the context budget
an edit tool exists to save.

### Failure vocabulary

Eight codes, because each calls for a different next step. They reach the model
as ordinary failed Tool Results inside a Turn; none renders a path, a version,
or file content.

| Code | Message |
| --- | --- |
| `fs_not_observed` | read the file before changing it |
| `fs_not_found` | file does not exist; create it or re-read after it appears |
| `fs_stale_version` | file changed since it was read; re-read it and retry |
| `fs_edit_not_found` | literal was not found |
| `fs_ambiguous_edit` | literal appears more than once; include more context or use replace_all |
| `fs_not_regular_file` | target is not a regular file |
| `fs_not_text` | file is not valid UTF-8 text |
| `fs_too_large` | file exceeds the edit size limit |

One translation happens in Application because the adapter cannot know better.
A create-if-absent guard reports raw `fs.ErrExist` when something is already
there, and that single adapter answer covers two situations the adapter cannot
distinguish: the session never read the target, or it read the target, found
nothing, and something appeared afterwards. Application holds the observation
table and resolves which one it is — `fs_not_observed` for the first,
`fs_stale_version` for the second. Telling the second to read the file would
be instructing it to repeat a read it remembers making, which is the failure
this mechanism spent Task 3 correcting in the other direction. `fs.ErrExist`
is deliberately not in the shared classifier's table, so an unresolved one
falls through to the generic failure rather than silently claiming a session
never looked. Adapter detail is never exposed either way.

## Bounds

| Bound | Value | Where |
| --- | --- | --- |
| edit source, normalized intermediate, and CRLF-restored final output | 1 MiB each | `tools/files.go` / `workspacefs` |
| `Write` payload | no edit-size limit | `workspacefs` |
| published-write verifier scratch | fixed 32 KiB | `workspacefs` |
| `old_string` / `new_string` | 32,768 bytes each | `edit_file` schema |
| `path` | 4,096 bytes | every file tool's schema |
| read limit | `MaxToolResultBytes` | Application's read path |

## What this does not cover

Stated as tests, not as caveats — see the evidence ledger.

- **`exec` is not mediated.** A command can rewrite anything in the workspace
  and this mechanism neither knows nor prevents it. What it does promise is
  that the damage is not compounded: the next structured write against a file
  `exec` changed is refused as stale rather than layered on top of it.
- **External writers between guard validation and rename.** An uncooperative
  writer can change the target after `checkGuard` but before our `os.Rename`;
  our rename can overwrite that competing revision. The verifier then sees our
  staged identity and expected bytes, not the overwritten revision, so it
  cannot detect that race. Closing it needs a kernel compare-and-swap primitive
  this project does not have.
- **External mutation after final verification.** The verifier detects a
  changed staged identity, payload, or version through its final destination
  check. A writer that changes the file after that final stable verification /
  return boundary remains outside the guarantee; only the next guarded
  operation can detect it as stale.
- **Windows runtime.** The package cross-compiles and `version_other.go` gives
  it a version function, but no runtime behaviour is claimed or tested there.
- **Cross-process observations.** Two `och` processes over one workspace each
  have their own table and neither sees the other's reads. The guard still
  refuses the second one's blind write, which is the property that matters.
- **Directories, devices, sockets.** Refused as `fs_not_regular_file` rather
  than handled.
