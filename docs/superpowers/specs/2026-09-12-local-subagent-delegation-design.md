# Local Subagent Delegation Design

**Status:** Accepted normative design

**Date:** 2026-09-12

**Research:** [architecture gate](../../research/architecture-gates/2026-09-12-local-subagent-delegation.md)

## 1. Decision

Add one opt-in builtin tool, `delegate_task`, that runs a fresh, durable child
Session synchronously inside the same Harness process. The child receives only
the delegated task, the ordinary fixed system prompt, and workspace
instructions. It may use `read_file` and `list_dir`; it may not mutate files,
execute commands, call MCP tools, delegate again, or ask for approval.

The parent waits for the child and receives a bounded final answer containing
the child Session ID. Parent cancellation cancels the child Turn. No child
continues after the parent tool call settles.

This is local task decomposition, not A2A or distributed multi-agent work.

## 2. User-visible contract

The tool schema is static:

```json
{
  "name": "delegate_task",
  "arguments": {
    "task": "a self-contained task for a read-only child"
  }
}
```

`task` is required, trimmed only for validation, valid UTF-8, and at most
16,384 bytes. The exact submitted string becomes the child Turn input. Unknown
fields fail through the existing schema validator.

On success the parent tool result is:

```text
child session: <SessionID>
<child final answer>
```

The complete result uses the existing 64 KiB tool-result bound and redaction
boundary. A child application failure becomes a stable tool failure; raw
provider, store, path, credential, and error text is not copied into the
parent result.

## 3. Opt-in and cache behavior

Delegation is disabled by default. Composition exposes an explicit flag and
configuration field. When disabled, the catalog and every Provider request
remain byte-for-byte unchanged.

When enabled, one static schema is added to the parent's tool prefix. Nothing
about active children, IDs, status, timing, or previous results is rewritten
into that prefix. Each result is appended at the end of the current Turn in
the same way as every other tool result. This preserves prompt-cache reuse
after the one intentional configuration-level prefix change.

## 4. Durable identity and lineage

The child is an ordinary canonical Session. `session.created` gains one
optional `parent` value containing:

- parent Session ID;
- parent Turn ID;
- parent tool Item ID; and
- parent Call ID.

All four values are required together and validated as IDs. Existing events
without `parent` decode exactly as before. The projected Session retains the
relation, so replay and transcript readers can prove lineage without parsing
tool-result prose.

The child Session creation commit is the publication boundary. Before it
commits, no child is claimed. Once it commits, the child is real even if the
parent later crashes or its result append fails. The parent already has a
durable `tool.call.started` event at that point, so the two streams tell the
truth without pretending the EventStore offers a cross-stream transaction.

No reverse parent index or child-list command is added in this slice.

## 5. Execution and context

Application owns delegation because it already owns Session/Turn commands and
can recursively invoke those commands without depending on an adapter. It:

1. validates and authorizes the parent tool call through the existing catalog
   and Policy path;
2. creates a child Session in the parent's canonical workspace;
3. runs one child Turn with a deterministic request identity derived from the
   child Session ID;
4. waits synchronously for its terminal result; and
5. returns the bounded answer through the existing tool completion path.

The child does not inherit parent conversation messages, Context checkpoint,
file observations, pending approvals, or request identity history. It receives
the same configured Provider route and Context Engine behavior because it uses
the same Service. Workspace instructions are discovered and durably recorded
by the existing child admission path.

## 6. Capability narrowing

The parent catalog contains the normal tools plus `delegate_task`. For a child
Session, every request projects only the schemas for `read_file` and
`list_dir`. Application also enforces the same allowlist at dispatch, so a
model-authored hidden tool name cannot bypass schema filtering.

`delegate_task` is classified `RiskRead`, `Mutates=false`: the child has no
mutation or command capability. A later writable-child design must revisit
parent approval, child approval routing, concurrent filesystem observations,
and exec isolation; it may not silently widen this tool.

## 7. Bounds

| Resource | Bound |
| --- | --- |
| Task input | 16 KiB UTF-8 |
| Child count per call | exactly 1 |
| Delegation depth | exactly 1; child schema and dispatch both forbid recursion |
| Lifetime | caller context plus configured timeout |
| Child Steps/tool calls/output | existing Service limits |
| Parent-visible result | existing 64 KiB tool-result limit |
| Background work | none |

The default timeout is two minutes; composition accepts 5 seconds through 10
minutes. Timeout is reported as a stable tool failure and cancels the child.

## 8. Failure and cancellation

- Cancellation before child creation creates no child.
- Cancellation after publication cancels the child Turn through the same
  context and the parent follows its existing cancellation path.
- A timeout fails the delegation tool with `subagent_timeout`; it does not
  leave work running.
- Child validation, provider, context, persistence, or step-limit failures map
  to `subagent_failed`; raw error text is not surfaced.
- A successful child whose parent result commit has unknown outcome uses the
  existing append-intent resolution; the child is not run twice.
- The parent tool execution map still guarantees one invocation per durable
  tool Item inside a live execution.

Crash recovery does not restart an interrupted child or parent Turn in this
slice. Runtime reconciliation records the interruption exactly as it does for
ordinary running Turns.

## 9. Observability

The child `och.turn` begins under the parent's `och.tool.execute` context, so a
sampled trace shows the delegation relationship without a new span kind. IDs
remain metadata-only. The durable `session.created.parent` relation, not trace
sampling, is the authoritative lineage.

## 10. Composition

Composition adds:

- `Subagents.Enabled` (default false); and
- `Subagents.Timeout` (default two minutes).

Only Composition decides whether the schema joins the catalog. Application
fails construction if the schema/config relationship is inconsistent. No new
dependency or child process is introduced.

## 11. Required evidence

Completion requires:

- domain round-trip and malformed-lineage tests;
- disabled Provider-wire equality;
- enabled parent/child topology and exact child schema tests;
- hidden write/exec/MCP/delegation dispatch refusals;
- cancellation before publication and during a child Turn;
- timeout and child failure with raw-error canaries;
- parent crash/reconciliation evidence for both streams;
- race coverage for concurrent parent Sessions;
- Composition and real Provider fixture end-to-end tests;
- architecture/docsguard updates; and
- a plain-language English implementation contract plus Chinese reading copy.

## 12. Explicit exclusions

No background children, wait/list/send/interrupt tools, continuation, model or
reasoning override, parent-history fork, child writes/exec/MCP, recursive
delegation, child process, remote ACP child, cross-process ownership,
cross-stream transaction, roster UI, or automatic task selection is promised.
