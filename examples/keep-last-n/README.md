# Custom launcher: keep the last N turns

**API stability: experimental.** Both SDK packages may change without source
compatibility guarantees; pin a tested revision. This project-owned example is
an external-module compilation/integration gate, not evidence of real external
adoption. Durable-event integrity and documented rollback requirements still apply.

This is an independent Go module importing only `sdk/och` and
`sdk/contextpolicy`. The local `replace` points at this checkout; a downstream
project should pin a tested released version or commit instead.

```sh
cd examples/keep-last-n
go build -o /tmp/och-keep-last-n .
/tmp/och-keep-last-n -acp \
  -workspace /absolute/workspace -database /absolute/runtime.db \
  -runtime-id custom-runtime -provider-url https://provider.example/v1 \
  -model your-model -api-key-env OCH_API_KEY \
  -context-window 32768 -max-output 4096 \
  -context-policy keep_last_n_turns \
  -context-policy-config '{"turns":3}'
```

Supply credentials through the named environment variable. Configure sandbox
availability according to the normal och contract; this example does not
disable it by default. The provider URL above is illustrative, not runnable.

Registration alone does not select the policy. Empty `-context-policy` retains
the builtin algorithm. Selected policy tries to compact whenever a candidate
preserves at least N recent turns; the core's protected-tail floor may retain
more. Forced recovery can override the N preference, but never the core floor.
The existing core still determines whether the resulting request fits.

For ACP evaluation, use this executable as the subject's launcher, freeze its
binary hash as usual, and include `context.policy` with `id`, `version`,
`configDigest`, and `config`. `contextpolicy.CanonicalConfig` computes the config
digest. Normalized argv supplies the version pin automatically. The stock
in-process executor intentionally rejects custom policies.

The root test `go test ./sdk/och` builds this module, drives actual ACP turns
against a local fixture provider, triggers compaction, closes the process and
exports policy-attributed evidence. See the [contract and rollback caveats](../../docs/architecture/startup-extensibility.md).
