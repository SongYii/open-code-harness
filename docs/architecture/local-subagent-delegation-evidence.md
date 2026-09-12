# Local Subagent Delegation Completion Evidence

**Status:** complete

**Contract:** [local subagent delegation](local-subagent-delegation.md)

**Design:** [accepted design](../superpowers/specs/2026-09-12-local-subagent-delegation-design.md)

**Plan:** [implementation plan](../superpowers/plans/2026-09-12-local-subagent-delegation.md)

## Commit

| Commit | Content |
| --- | --- |
| `3bd08c8` | Research, accepted design and plan, durable lineage, opt-in tool/config, fresh read-only child execution, cancellation/failure bounds, Composition/CLI/trace wiring, tests, contracts, and evidence |

## What is proven

| Claim | Executable evidence |
| --- | --- |
| Lineage is durable and backward compatible | Domain decide/apply/codec tests cover the optional all-or-nothing parent tuple, defensive copies, self-parent rejection, and legacy JSON |
| Disabled catalog is unchanged | Catalog and Composition tests require `delegate_task` to be absent by default and present only under explicit configuration |
| Child context is fresh | Application request captures show the child receives the task without parent transcript history |
| Workspace rules still apply | Real SQLite/HTTP assembly proof finds ordinary system and `AGENTS.md` instruction messages in the child request |
| Child capability is read-only twice | Provider-request tests require exactly `read_file`/`list_dir`; a forged hidden write is rejected at dispatch and leaves the filesystem unchanged |
| Parent receives a bounded terminal result | Application end-to-end test returns child Session ID plus final answer through the normal Tool Result path |
| Failure text is safe | Provider-error canary maps to `subagent_failed` and is absent from parent durable events |
| Cancellation and timeout terminate work | Tests cover mid-child caller cancellation of both Turns and stable timeout failure |
| Independent parents remain isolated | Race-enabled concurrent test requires two distinct lineage tuples and matching results |
| Unknown parent result does not duplicate work | Fault-store test makes the parent Tool Result append unknown, retries the request, and observes one child only |
| Trace topology is preserved | Memory telemetry proof requires child `och.turn` to be parented by the delegation `och.tool.execute` |
| Real production composition works | Loopback OpenAI-compatible SSE plus real SQLite, workspace filesystem, instructions, Context Engine, and Composition complete a delegated read in four Provider requests |

## Verification record

Already passed during implementation:

```text
go test -race ./internal/harness/application -run 'TestDelegateTask' -count=1
go test -race ./internal/harness/application -run 'TestDelegateTask|TestTelemetryChildTurn' -count=1
go test ./internal/harness/tools ./internal/harness/application -count=1
go test ./internal/harness/composition -run 'TestValidate|TestOpenWithNoMCP|TestOpenAddsDelegate' -count=1
go test ./internal/harness/composition -run TestAssemblyRunsDelegated -count=1
go test ./internal/harness/domain ./internal/harness/application ./internal/docsguard -count=1
go vet ./...
go build ./...
git diff --check
```

The last command uses only loopback HTTP but requires network permission in the
execution sandbox. A full `go test ./... -count=1` passed every completed
package except the two previously documented `localexec` host-capability
tests; both also failed in an isolated rerun because this host reports an
available bwrap filesystem/network backend while the fixtures expect none,
and the cgroup fixture never observes its synthetic PID. The first full run
also started before `npm run build` regenerated the ignored embedded web
assets; after that build, the focused production set passed:

```text
go test ./cmd/acp-web-bridge ./cmd/och ./internal/harness/domain \
  ./internal/harness/tools ./internal/harness/application \
  ./internal/harness/composition ./internal/docsguard -count=1
```

No subagent code is on either failing `localexec` test path. This ledger does
not rewrite the known host-dependent failures into a clean full-suite claim.

## Mutation checks

Three temporary mutations were applied and restored:

| Mutation | Test that failed |
| --- | --- |
| Add `write_file` only to child schema projection | `TestDelegateTaskCreatesReadOnlyDurableChild` |
| Add `write_file` only to dispatch permission | `TestChildSessionRejectsHiddenWriteAtDispatch` |
| Drop the parent tuple from `session.created` | `TestCreateSessionCarriesDurableParentLineage` |

The first draft used one helper for both child allowlists. That made the two
claimed barriers one mechanism. The initial combined mutation exposed the
coupling; production now uses separate closed predicates, and the two focused
mutations above prove each test remains independently load-bearing.
