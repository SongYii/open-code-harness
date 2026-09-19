# Custom launcher: deny configured tools

**API stability: experimental.** `sdk/och` and `sdk/toolpolicy` do not yet
promise source compatibility; pin a tested revision. This project-owned module
is an external-module compile and integration gate, not evidence of adoption by
an independent project.

This launcher imports only the public SDK. It registers `deny_tools` version
`1.0.0`, whose nonsecret JSON config contains up to 64 exact tool names. A
configured name is denied; other tools retain the safe default table (reads are
allowed and writes/exec require approval). Core denials still run before this
policy, so the extension can tighten authorization but cannot bypass workspace,
network, metadata, or risk invariants.

```sh
cd examples/deny-tools
go build -o /tmp/och-deny-tools .
/tmp/och-deny-tools -acp \
  -workspace /absolute/workspace -database /absolute/runtime.db \
  -runtime-id custom-runtime -provider-url https://provider.example/v1 \
  -model your-model -api-key-env OCH_API_KEY \
  -context-window 32768 -max-output 4096 \
  -tool-policy deny_tools -tool-policy-version 1.0.0 \
  -tool-policy-config '{"names":["exec"]}'
```

Registration alone does not select the policy. Empty `-tool-policy` uses the
builtin policy and emits legacy-compatible events without custom attribution.
Configuration is canonicalized and its SHA-256 digest is recorded with the
policy ID/version on durable policy decisions. The config is also visible in
argv and evaluation documents, so it must not contain credentials.

For evaluation, freeze this executable's binary hash and set the Subject's
optional `policy.toolPolicy` identity/config. Custom policies are ACP-only; the
stock in-process executor has no registration registry and rejects them.

The root `sdk/och` test builds this independent module, drives a real ACP turn
against a local fixture provider, proves a denied `exec` never runs, and checks
the attributed canonical audit. Policy-decision records deliberately remain
absent from user-facing ACP updates and transcript projection. See the
[startup extension contract](../../docs/architecture/startup-extensibility.md)
for trust, lifecycle, compatibility, and rollback limits.
