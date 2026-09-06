# Agent Instructions and Safe File Mutation Architecture Gate

**Status:** Complete research evidence

**Date:** 2026-09-04

**Scope:** Re-verify how the required six reference projects expose coding
instructions and filesystem tools, whether their request shape preserves
provider prefix caching, and whether a read followed by a later mutation is
protected against a lost update. This gate informs two separate designs; it
does not itself define an implementation plan.

English is the research authority. The
[Chinese file](2026-09-04-agent-instructions-and-file-mutation.zh-CN.md) is a
synchronized reading copy.

## Comparison set and pinned commits

The repositories were read as primary sources and are pinned per Documentation
rules 7 and 8. Product documentation linked below was observed on 2026-09-04.
Nothing from a reference repository is copied into this project.

| Project | Repository | Commit | Relevant surface |
| --- | --- | --- | --- |
| Codex | `openai/codex` | `89a4eec` | `apply_patch`, instruction discovery, prompt assembly |
| Kimi CLI | `MoonshotAI/kimi-cli` | `86f1364` | Read/Write/StrReplace tools, agent customization |
| Grok Build | `xai-org/grok-build` | `72a6125` | `read_file`, `search_replace`, LSP/tool filtering |
| Pi | `badlogic/pi-mono` | `92d8e2d` | read/write/edit tools, per-file mutation queue |
| Maka | `maka-agent/maka-agent` | `7f7843e` | runtime-owned prompt/tool composition; no stronger file CAS precedent found |
| DeepSeek Harness | `deepseek-ai/deepseek-harness` | `b150a55` | system-prompt fragments, `AGENTS.md`, filesystem observation policy and guarded atomic mutation |

Additional primary sources were used where the fixed six cannot establish the
concurrency principle: PostgreSQL's current transaction-isolation documentation
and RFC 9110 `If-Match`. Claude Code and OpenCode were also inspected as widely
used coding-tool implementations, but they do not replace the mandatory set.

## Current Open Code Harness gap

The current workspace port returns only bytes and a truncation flag from
`Read`; `Write` has no precondition. The local adapter opens the destination
with `O_CREATE|O_TRUNC`, writes directly, and closes it. Consequently:

- a failed or interrupted write may expose partial content;
- a model can read revision A, an external actor can publish revision B, and a
  later `write_file` silently overwrites B;
- the model has no targeted edit tool, so small changes require a whole-file
  rewrite;
- there is no stable stale-observation error that tells the model to re-read.

The request side has exact `model.request.recorded` evidence and a Context
Engine, but no versioned coding-agent system prompt or hierarchical workspace
instruction contract. Adding mutable text near the beginning of every request
would make the earliest changed token move and reduce provider prefix-cache
reuse.

## Filesystem-tool findings

| Project | Model-facing mutation | Useful protections | Lost-update protection across turns |
| --- | --- | --- | --- |
| Codex | `apply_patch` custom patch grammar | add/update/delete/move, contextual hunks, mismatch failure | Implicit old-content precondition for edited hunks; not a file-version CAS, and a multi-file patch can commit earlier files before a later failure |
| Kimi CLI | Read, Write, StrReplace | exact replacement, `replace_all`, diff/approval | Replacement text is an implicit content precondition; whole-file Write is still overwrite-oriented |
| Grok Build | `read_file`, `search_replace` | focused replacement, LSP feedback, tool filtering | No database-style version token was found |
| Pi | read, write, edit | multiple exact non-overlapping edits, per-file in-process mutation queue, BOM/newline preservation, diff | Serializes its own mutations but does not reject a later mutation because an earlier read became stale |
| Maka | runtime tool composition | policy/runtime ownership | No stronger version-guarded file mutation contract was found |
| DeepSeek Harness | read, write, edit | internal observation record, opaque version, atomic create/replace, per-target lock, structured repair errors | Yes for the structured tool path: observed-present becomes replace-if-version; observed-absent becomes create-if-absent; stale state is rejected |

Two additional implementations reinforce, rather than change, this result.
Claude Code's Edit uses `old_string`/`new_string`, while Write replaces a whole
file; its own documentation warns that Bash can mutate files outside built-in
editing/checkpoint controls. OpenCode performs exact and fuzzy replacement,
diff approval, formatting, and LSP diagnostics under a per-file semaphore, but
its Write path still reads for presentation and then writes without a durable
version precondition.

These tools are deliberately designed. Their common center is a small schema,
bounded read output, exact or narrowly fuzzy replacement, a reviewable diff,
approval routing, and actionable errors. The uncommon feature is optimistic
concurrency between a prior read and a later write.

### DeepSeek Harness's four-layer precedent

DeepSeek Harness separates:

1. model-facing read/write/edit schemas and rendering;
2. an observation policy that remembers `unseen`, `absent`, or
   `present(version)` per actor and target;
3. a filesystem port with optional mutation guards;
4. a local provider implementing staged, fsynced publication.

The model never receives or echoes the version. A successful read records it
internally. A later write/edit automatically supplies the corresponding guard.
`FS_NOT_OBSERVED` and `FS_STALE_VERSION` direct the model to read again. This is
the closest fit to Open Code Harness because it keeps a simple tool schema and
puts freshness enforcement below model-controlled arguments.

Its limits matter. The version is filesystem metadata
(`dev:ino:size:mtimeNs:ctimeNs`), its target lock is in-process, and an
uncooperative external writer is not serialized. It is optimistic conflict
detection, not a transactional filesystem.

### Database and HTTP analogy

PostgreSQL prevents lost updates by validating a transaction's assumptions at
commit and requiring retry after a serialization conflict. HTTP `If-Match`
does the same with an entity tag: mutate only if the resource still denotes the
observed representation. The transferable rule is not to expose a timestamp to
the model; it is to bind a mutation to an opaque observation and reject a stale
commit.

This project cannot honestly promise a kernel-atomic compare-and-swap against
every external filesystem writer. It can guarantee the invariant among its own
structured file tools, use atomic publication to eliminate partial files, and
detect ordinary external replacements before publication. Commands run through
`exec` remain an explicit unmediated mutation path.

## Instruction and cache findings

The comparison projects vary in filenames and layering, but converge on a
stable prompt prefix plus workspace-owned instructions:

- Codex discovers `AGENTS.md` by scope and gives closer files precedence. Its
  patch tool remains separate from instruction discovery.
- Claude Code loads hierarchical `CLAUDE.md`/rules into session context and
  documents prompt-cache behavior explicitly; repository edits are appended to
  the conversation rather than rewriting already-sent turns.
- DeepSeek Harness composes registered system-prompt fragments in stable order,
  documents each fragment's KV-cache effect, and treats read/tool results as
  append-only request suffixes. Its filesystem guidance is fixed while the
  plugin scope is unchanged.
- Kimi CLI and the remaining reference projects also keep tool definitions and
  core agent policy in controlled prompt/tool composition rather than asking
  the model to manufacture policy text.

Provider prefix caches reuse only an unchanged prefix. Rebuilding an early
system message whenever `AGENTS.md` changes moves the first changed token near
the request start. Appending a bounded instruction delta after existing history
keeps the earlier prefix byte-stable. Compaction must then rebase those deltas
into one durable effective snapshot; otherwise old and superseded instructions
consume the context window indefinitely.

## Adopt / reject / defer

Adopt:

- DeepSeek Harness's hidden observation state and guarded mutation shape;
- targeted literal `edit_file`, unique-match by default;
- same-directory staged atomic publication, mode preservation, and per-target
  serialization for in-process mutations;
- stable, structured stale/not-observed/ambiguous/not-found errors;
- a versioned, fixed coding-agent system prompt followed by append-only
  workspace-instruction deltas;
- exact durable recording before provider dispatch and deterministic
  compaction rebasing.

Reject:

- model-supplied timestamps, hashes, or version tokens;
- unconditional overwrite of an existing file that the session has not read;
- claiming `mtime` alone is a transaction or claiming external commands are
  covered by the structured-tool guard;
- rewriting the first system message on every turn;
- importing a general plugin kernel just to express either policy.

Defer:

- all-process workspace transactions or an overlay/versioned workspace;
- fuzzy edit matching, patch-language breadth, image reads, and arbitrary
  instruction filenames;
- Windows-specific publication behavior, which is not a current product
  priority;
- persistent observations across process restart: a resumed session must
  re-read before mutation.

## Decision and sequencing

Implement the safe file-mutation contract first. It closes a correctness hole
in an already-shipped write tool and provides the safe read/discovery primitive
needed by dynamic workspace instructions. Implement the system prompt and
append-only `AGENTS.md` contract second. The two designs are separate so the
first can ship without changing provider request composition.

Primary source pointers:

- [Codex apply-patch source](https://github.com/openai/codex/blob/89a4eec6dafce21486c5a56e6599095e7517c4b1/codex-rs/apply-patch/src/lib.rs)
- [Kimi agent customization](https://github.com/MoonshotAI/kimi-cli/blob/86f136422a0aae6b217ea49e7ea1d2e8a1defcd2/docs/en/customization/agents.md)
- [Grok Build shell README](https://github.com/xai-org/grok-build/blob/72a61251fcffb464bcc687aeb5a998e5a98ec0c9/crates/codegen/xai-grok-shell/README.md)
- [Pi edit tool](https://github.com/badlogic/pi-mono/blob/92d8e2d17d4f357788381c49ce2cdb3f4ed1f21c/packages/coding-agent/src/core/tools/edit.ts)
- [DeepSeek Harness filesystem provider](https://github.com/deepseek-ai/deepseek-harness/blob/b150a551b8d465e31e418e1b2eaf5e79bbb7d28e/packages/fs/fs-local/README.md)
- [DeepSeek Harness observation policy](https://github.com/deepseek-ai/deepseek-harness/blob/b150a551b8d465e31e418e1b2eaf5e79bbb7d28e/packages/fs/fs-observation-policy/README.md)
- [OpenCode edit tool](https://github.com/anomalyco/opencode/blob/dev/packages/opencode/src/tool/edit.ts)
- [Claude Code prompt caching](https://code.claude.com/docs/en/prompt-caching)
- [PostgreSQL transaction isolation](https://www.postgresql.org/docs/current/transaction-iso.html)
- [RFC 9110 `If-Match`](https://www.rfc-editor.org/rfc/rfc9110.html#name-if-match)
