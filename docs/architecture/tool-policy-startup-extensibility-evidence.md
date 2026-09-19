# Tool-policy startup extensibility evidence

**Status:** Complete on the implementation branch. **Date:** 2026-09-19.

This ledger covers the experimental startup-composed tool authorization policy
surface. The normative implemented contract is
[Startup extensibility](startup-extensibility.md); the pre-strategy security
boundary is documented separately in
[Tool authorization guard evidence](tool-authorization-guard-evidence.md).

## Scope and non-claims

The slice exposes trusted Go policy code through `sdk/toolpolicy` and
`sdk/och.Extensions.ToolPolicies`, selected once at startup. It does not expose
storage, execution, approval, events, or a general plugin kernel. It does not
provide dynamic loading, hot reload, hostile-code containment, or stable source
compatibility.

The project-owned [`examples/deny-tools`](../../examples/deny-tools/README.md)
module is an external-module compile and ACP integration gate. It is not a real
external adopter and does not satisfy the documented stability-promotion gate.
No paid provider was called; end-to-end tests use only loopback fixtures.

## Implementation commits

| Commit | Result |
| --- | --- |
| `92ebd54` | Experimental standard-library-only decision DTO/registration contract plus guarded core strategy boundary |
| `85d6d80` | Detached decision context propagated through the core guard without moving catalog authority |
| `146adb4` | Optional strict durable policy identity with legacy builtin byte preservation |
| `0f7d4be` | Application strategy injection, attributed fail-closed decisions, and cancellation behavior |
| `2d2af33` | Startup-local resolver, launcher flags, public extensions, and managed lifecycle |
| `4a508d3` | Frozen Eval Subject/ACP identity and collection/readback agreement checks |
| `ef350e3` | Independent `deny_tools` module, real ACP denial/audit proof, and synchronized contracts |

These hashes identify branch history, not a released API version. The final
evidence commit also adds the builtin Application-path omission regression found
by mutation; like every commit, it cannot cite its own hash inside its contents.

## Verified behavior

The following restored-tree commands passed during implementation:

```text
GOCACHE=/tmp/och-tool-policy-gocache go test ./internal/harness/eval ./internal/harness/architecture -count=1
ok github.com/SongYii/open-code-harness/internal/harness/eval 69.946s
ok github.com/SongYii/open-code-harness/internal/harness/architecture 0.966s

cd examples/deny-tools
GOWORK=off GOCACHE=/tmp/och-tool-policy-gocache go test ./... -count=1
ok example.com/deny-tools 0.007s

GOCACHE=/tmp/och-tool-policy-gocache go test ./sdk/och -run 'TestExternalToolPolicy' -count=1
ok github.com/SongYii/open-code-harness/sdk/och 3.436s
```

The external ACP test builds the separate module, has a fixture model offer
`exec`, and selects `deny_tools@1.0.0` with `{"names":["exec"]}`. It proves:

- the command does not create its marker file;
- the harness feeds the denial back through the normal tool loop and the model
  completes a second request;
- the cold, verified canonical audit contains `policy.decision.recorded` with
  the exact ID, version, config digest, effect, rule and reason;
- live ACP updates, `session/load` replay, and the session transcript do not
  expose the internal policy-decision event.

Eval tests additionally prove canonical Subject/argv identity, invalid identity
rejection, ACP-only execution, no publication on collection mismatch, rejection
of a manifest-backed readback mismatch, and compatibility with partial evidence
that contains no collected audit.

## Compatibility and rollback

Builtin selection returns no custom attribution, preserving prior event bytes.
Custom policy decisions add an optional nested identity that old strict readers
may reject. Before first custom-policy use, make and verify a backup. Roll back
with a reader that understands the field or restore that backup; never delete
the field or rewrite hashes/audit chains. Configuration is nonsecret because it
is visible in argv and Eval documents.

## Mutation evidence

Every mutation below was made temporarily with the production source restored
before the next mutation and before final verification.

| Removed or corrupted invariant | Observed falsification |
| --- | --- |
| Deleted the pre-strategy `coreDeny` branch | Seven hostile metadata cases accepted the permissive strategy's `allow`; Application's out-of-workspace custom-policy test also showed the strategy called and its allow persisted |
| Deleted post-strategy `validDecision` rejection | Unknown effect, empty rule, blank reason, and invalid UTF-8 passed; Application no longer produced the intended fail-closed terminal path |
| Returned the input pointer from `CloneToolPolicyIdentity` | `TestCloneToolPolicyIdentity` failed immediately because the clone aliased its source |
| Injected a synthetic custom identity into builtin Application recording | The plan-named Domain codec test unexpectedly stayed green because it only encoded a hand-built event and never traversed Application. A new real builtin tool-loop regression was added; the same mutation then failed on the injected identity. This is a repaired test gap, not counted as successful evidence from the original test. |
| Returned success before decoding/comparing Eval audit events | Five comparison negatives, collection-before-publication rejection, and manifest-backed readback rejection all failed because mismatches were accepted |

After restoration, the combined targeted run passed for policy, domain,
Application, and Eval. The mutation exercise found no production defect; it did
find and close one evidence defect in the default-attribution promise.

## Final restored-tree verification

The final tree passed:

```text
GOCACHE=/tmp/och-tool-policy-gocache go test ./... -count=1
# all packages passed; longest packages:
ok github.com/SongYii/open-code-harness/internal/harness/composition 93.885s
ok github.com/SongYii/open-code-harness/internal/harness/eval 72.452s
ok github.com/SongYii/open-code-harness/sdk/och 8.202s

GOCACHE=/tmp/och-tool-policy-gocache go test -race ./... -count=1
# all packages passed with no DATA RACE; longest packages:
ok github.com/SongYii/open-code-harness/internal/harness/composition 178.221s
ok github.com/SongYii/open-code-harness/internal/harness/eval 279.337s
ok github.com/SongYii/open-code-harness/internal/harness/runtime 41.999s
ok github.com/SongYii/open-code-harness/sdk/och 15.941s

GOCACHE=/tmp/och-tool-policy-gocache go vet ./...
# exit 0, no diagnostics
```

These integration runs used normal local socket, subprocess, and sandbox
permissions. They made no paid model request. The final documentation/diff/status
gate is run after this ledger update and recorded by the final commit result.
