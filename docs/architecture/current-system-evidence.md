# Current System Architecture Completion Evidence

- Status: Complete
- Date: 2026-09-09
- Contract: [Current system architecture](current-system.md)
- Design commit: `c6a97ab` (`docs: define current architecture boundary closure`)
- Implementation commit: `19c4532` (`test: make architecture ownership fail closed`)

## Delivered boundary changes

- `contextengine` and `redact` are explicit production owners.
- Every production Go directory below `internal/harness` must be classified;
  an unknown directory is rejected before import inspection.
- The internal dependency policy is an owner-keyed allowlist rather than an
  open-ended absence of forbidden entries.
- MCP is included in the exhaustive adapter ownership matrix.
- Client isolation covers all of `internal/client`, `cmd/acp-client`, and
  `cmd/acp-web-bridge` in the client-to-Harness direction, and all of
  `internal/harness` in the reverse direction.
- The as-built architecture contract records package ownership, request and
  recovery flow, process boundaries, and durable authorities.

## Mutation evidence

### Unknown production package

A temporary valid Go file was placed at
`internal/harness/architectureprobe/probe.go`. It imported only `time`, so no
forbidden dependency could mask the ownership result.

```text
go test ./internal/harness/architecture -run TestProductionDependencyBoundaries -count=1

internal/harness/architectureprobe: unclassified production package;
add it to the architecture ownership registry
FAIL
```

The probe was then removed. This proves classification itself fails closed;
the old unowned-import fallback could not have caught this mutation.

### Web client imports Harness

A temporary file under `internal/client/acpweb` blank-imported `domain`.

```text
go test ./internal/harness/architecture -run TestClientPackagesAreIsolatedFromInternalHarness -count=1

internal/client/acpweb/architecture_mutation_probe.go imports
github.com/SongYii/open-code-harness/internal/harness/domain
(forbidden: internal/harness boundary)
FAIL
```

The probe was then removed. The previous `internal/client/acp`-only walk would
not have observed this mutation.

## Verification

The final tree, with both mutation probes absent, passed:

```text
go test ./internal/harness/architecture -count=1
go test ./internal/docsguard -count=1
go vet ./...
PATH=/tmp/och-no-bwrap:$PATH go test -race ./... -count=1
go mod tidy -diff
git diff --check
```

The complete commands were run from the dedicated
`docs/current-architecture` worktree. The PATH prefix supplies a test wrapper
that reports `bwrap` unavailable, matching ordinary CI; this machine has a
working system `bwrap`, while two pre-existing localexec tests assert the
no-backend branch. No live provider credential or external model call is
involved in this architecture-only slice.

## Deliberate limits

Import gates prove compile-time dependency direction, not runtime authority.
Runtime authority remains proven by EventStore, recovery, tool, context, ACP,
and evaluation contract tests. The package registry covers production Harness
packages; independent clients receive a deliberately coarser process-boundary
gate because they do not share Harness layers.
