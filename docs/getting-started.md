# Getting Started

This is a practical walkthrough for running Open Code Harness locally: build
the binaries, start an agent, and drive one turn through either the terminal
client or the browser UI. It complements, and does not replace, the
[documentation authority map](README.md) — that map is the source of truth
for what each subsystem actually guarantees. This page is a howto, not a
contract, and carries no authority level of its own.

The project is pre-v0. Nothing below is GA, and every binary logs that
plainly on its own. See the root [README](../README.md#current-status) for
which slices are implemented and verified versus still unimplemented.

## Prerequisites

- Go, matching the version in [go.mod](../go.mod).
- A C toolchain (cgo), required by `go test -race` and by the SQLite adapter.
- An OpenAI-compatible Chat Completions endpoint to talk to (a real provider,
  or a local server such as Ollama's or vLLM's OpenAI-compatible API) and an
  API key for it, even if the local server ignores the value.
- Node.js and npm, only if you want `cmd/acp-web-bridge`'s browser UI —
  see [Web UI](#web-ui-acp-web-bridge).

## Build

```bash
go build ./cmd/och
go build ./cmd/acp-client
```

`och` is the agent: it owns the workspace, the SQLite event store, the
provider connection, and (behind `-acp`) the ACP v1 wire protocol. It has no
interactive UI of its own — every example below drives it through a client
that spawns it as a subprocess over stdio.

## Prepare a workspace and database

```bash
mkdir -p /tmp/och-demo/workspace
mkdir -p /tmp/och-demo/db   # och.db's parent must already exist; the file itself is created on open
export OCH_API_KEY=sk-...   # or point -api-key-env at a different variable
```

`-workspace` jails every filesystem tool and is `exec`'s working directory;
it must already exist. `-database` is the canonical SQLite event store; its
parent directory must exist, but the database file itself is created on
first open.

## Run one turn from the terminal (`acp-client`)

`acp-client` spawns the agent binary itself — it does not attach to an
already-running `och` process. Pass `och`'s own flags after a literal `--`,
including `-acp` so it speaks ACP v1 instead of just idling:

```bash
./acp-client \
  -agent ./och \
  -cwd /tmp/och-demo/workspace \
  -- \
  -acp \
  -workspace /tmp/och-demo/workspace \
  -database /tmp/och-demo/db/och.db \
  -runtime-id local-dev \
  -provider-url https://api.example.com/v1 \
  -model gpt-4o-mini \
  -context-window 128000 \
  -max-output 4096
```

This prints `session <id> ready` and then a `>` prompt. Type a message and
press Enter to run one turn; the client renders the live trajectory
(assistant text, tool calls, tool results) to stdout as it streams in. If the
agent proposes a tool call that needs approval, `acp-client` prompts on its
own stdin before continuing. Ctrl-C during a turn sends `session/cancel`; a
second Ctrl-C before that settles exits immediately. EOF (Ctrl-D) on an idle
prompt ends the session cleanly.

To resume a session created in an earlier run, add `-resume <session-id>`
before the `--`.

If your provider endpoint is plain HTTP and resolves to loopback (a local
model server, for instance), also pass
`-provider-allow-insecure-loopback` to `och` after the `--` — HTTPS is
otherwise required.

### Policy modes

`-policy` controls which tool effects the built-in Policy engine allows
without an approval round-trip: `default` (the out-of-the-box behavior),
`read_only`, `allow_writes`, or `deny_all`. Omit it to get `default`.

### Exec sandboxing

`exec` calls are confined by bwrap + cgroup v2 on Linux or Seatbelt +
rlimits on macOS when available. If neither backend is present, `och`
refuses to start (`composition.Open` fails closed) unless you pass
`-allow-unsandboxed-exec`, which is a deliberate, logged trade-off — see
[SECURITY.md](../SECURITY.md) for the threat model this changes.

## Web UI (`acp-web-bridge`)

`acp-web-bridge` gives the same session a browser front end. It is a dumb
NDJSON-to-WebSocket relay — every ACP v1 semantic runs independently in the
browser's own TypeScript client — so it needs its frontend built once before
it embeds real assets:

```bash
cd cmd/acp-web-bridge/web && npm ci && npm run build && cd -
go build ./cmd/acp-web-bridge
```

Then start it exactly like `acp-client`, spawning `och -acp` after `--`:

```bash
./acp-web-bridge \
  -agent ./och \
  -cwd /tmp/och-demo/workspace \
  -- \
  -acp \
  -workspace /tmp/och-demo/workspace \
  -database /tmp/och-demo/db/och.db \
  -runtime-id local-dev \
  -provider-url https://api.example.com/v1 \
  -model gpt-4o-mini \
  -context-window 128000 \
  -max-output 4096
```

It prints a URL with a one-time token on stderr, e.g.
`acp-web-bridge: ready at http://127.0.0.1:54321/?token=...`. Open that exact
URL — the token is required on every WebSocket upgrade alongside an
Origin-allowlist check. The server only binds `127.0.0.1` (hardcoded, not
configurable) and has no TLS, so it is loopback-only by design; do not expose
it beyond your own machine. `-listen host:port` picks a fixed port instead of
an ephemeral one if you need one. `-resume <session-id>` works the same way
as in `acp-client`.

## Export a session transcript

Once a session has run at least one turn, export it as JSONL without going
through either client:

```bash
./och export-session -database /tmp/och-demo/db/och.db -session <session-id> -output transcript.jsonl
```

Omit `-output` to write JSONL to stdout instead.

## Where to go next

- [Implemented ACP v1 Adapter](architecture/acp-v1.md) documents the wire
  protocol both clients speak.
- [Implemented ACP-native Client](architecture/acp-native-client.md) and
  [Implemented Web Trajectory UI](architecture/web-trajectory-ui.md) document
  `acp-client` and `acp-web-bridge` themselves, including what each
  deliberately does not do yet (multi-viewer fan-out, non-loopback exposure,
  in-browser session list/resume/delete).
- [SECURITY.md](../SECURITY.md) states which boundaries are enforced today
  and which are not — read it before pointing any of this at anything other
  than a local, trusted workspace.
