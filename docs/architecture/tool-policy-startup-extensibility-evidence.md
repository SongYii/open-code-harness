# Tool-policy startup extensibility evidence

**Status:** Implementation Tasks 1–7 complete in the working branch; final
mutation and full-tree verification pending. **Date:** 2026-09-19.

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

The independent example/documentation commit is recorded after Task 7 is
committed. These hashes identify branch history, not a released API version.

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

## Remaining final evidence

Task 8 will record the four required mutation groups after each mutation fails
for the intended reason and the production source is restored. It will also
replace this section with the complete `go test`, race, vet, documentation gate,
diff, and status outputs. Until then this ledger deliberately does not claim
final completion.
